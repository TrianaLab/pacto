package fleetsrc

// Hubble Relay is read over gRPC with a byte codec and protowire, so no
// Cilium module is linked. The fields read, checked against
// github.com/cilium/cilium v1.18.17 (api/v1/observer/observer.proto and
// api/v1/flow/flow.proto):
//
//	GetFlowsRequest       number=1 follow=3 since=7 (Timestamp seconds=1 nanos=2)
//	GetFlowsResponse      flow=1 lost_events=3
//	Flow                  time=1 verdict=2 source=8 destination=9 node_name=11
//	                      destination_names=14
//	                      traffic_direction=22 is_reply=26 (BoolValue value=1)
//	Endpoint              identity=2 namespace=3 labels=4 workloads=6 cluster_name=7
//	Workload              name=1 kind=2
//	ServerStatusResponse  num_unavailable_nodes=6 (UInt32Value value=1)
//	LostEvent             num_events_lost=2
//	Verdict FORWARDED=1, TrafficDirection EGRESS=2

import (
	"cmp"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Bounds of a Hubble read. ponytail: each is fixed; a value only when a
// cluster proves it too small.
const (
	maxHubbleFlows    = 10000       // the newest flows of the window read
	hubbleWindowSlack = time.Minute // how late the earliest flow may start
)

// The two Relay methods Pacto calls, and the enum values an edge needs.
const (
	hubbleStatusMethod = "/observer.Observer/ServerStatus"
	hubbleFlowsMethod  = "/observer.Observer/GetFlows"
	verdictForwarded   = 1
	directionEgress    = 2
)

var (
	errRelayTLS         = errors.New("hubble relay serves TLS")
	errRelayUnreachable = errors.New("hubble relay unreachable")
	errFlowUnreadable   = errors.New("hubble flow unreadable")
)

// bytesCodec hands gRPC messages over as raw protobuf bytes.
type bytesCodec struct{}

func (bytesCodec) Marshal(v any) ([]byte, error) { return *v.(*[]byte), nil }

func (bytesCodec) Unmarshal(data []byte, v any) error {
	*v.(*[]byte) = slices.Clone(data)
	return nil
}

func (bytesCodec) Name() string { return "proto" }

// hubbleRelay is the Hubble Relay Service to read: labelled
// k8s-app=hubble-relay, else named hubble-relay, in kube-system or the one
// namespace Cilium's own Services sit in (soleNamespace, only when every
// Services list was read); never an ExternalName or a selectorless Service,
// whose address a tenant chooses.
func hubbleRelay(services []svcInfo, listComplete bool) (svcInfo, bool) {
	trusted := []string{"kube-system"}
	if ns, ok := soleNamespace(services, func(svc svcInfo) bool {
		return svc.Labels["k8s-app"] == "cilium" || svc.Labels["app.kubernetes.io/part-of"] == "cilium"
	}); ok && listComplete {
		trusted = append(trusted, ns)
	}
	sorted := slices.SortedFunc(slices.Values(services), func(a, b svcInfo) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	for _, match := range []func(svcInfo) bool{
		func(svc svcInfo) bool { return svc.Labels["k8s-app"] == "hubble-relay" },
		func(svc svcInfo) bool { return svc.Name == "hubble-relay" },
	} {
		for _, svc := range sorted {
			if match(svc) && svc.Type != "ExternalName" && svc.HasSelector && slices.Contains(trusted, svc.Namespace) {
				return svc, true
			}
		}
	}
	return svcInfo{}, false
}

// relayPort is the port of relay to dial: its first, else 80.
func relayPort(relay svcInfo) svcPort {
	if len(relay.Ports) == 0 {
		return svcPort{Port: 80}
	}
	return relay.Ports[0]
}

// hubbleRow is the Cilium Hubble row and the calls of the last hour Relay's
// flows show.
func (s *MeshSource) hubbleRow(ctx context.Context, cl meshCluster, asOf time.Time) (fleet.Provider, []fleet.ObservedEdge) {
	const name = "Cilium Hubble"
	relay, ok := hubbleRelay(cl.services, !cl.servicesErr)
	switch {
	case !ok && cl.servicesErr:
		return meshRow(fleet.ProviderHubble, name, "serves cilium.io; Services not read", asOf, nil, []fleet.Limitation{
			s.unread("Services could not be read in every namespace, so Pacto may miss Hubble Relay")}), nil
	case !ok:
		return meshRow(fleet.ProviderHubble, name, "serves cilium.io; no Hubble Relay found", asOf, nil, []fleet.Limitation{s.unread("no Hubble Relay found")}), nil
	}
	objects := []fleet.ProviderObject{serviceObject(relay)}
	read, err := s.hubbleFetch(ctx, relay, asOf)
	if err != nil {
		return meshRow(fleet.ProviderHubble, name, "serves cilium.io; Hubble Relay found, flows not read", asOf, objects,
			[]fleet.Limitation{s.unread(hubbleErrorText(err))}), nil
	}
	edges, readLims := read.edges(s.id, asOf, s.namespaces)
	return readsEgress(meshRow(fleet.ProviderHubble, name, "serves cilium.io; flows read from Hubble Relay", asOf, objects, readLims)), edges
}

// hubbleRead is what one Relay read returned.
type hubbleRead struct {
	flows       []flow
	unavailable uint64 // nodes Relay could not reach
	nodesKnown  bool   // Relay said how many nodes it could not reach
	lost        uint64 // flows lost before Relay served them
	full        bool   // maxHubbleFlows read, so older ones may exist
}

// hubbleFetch reads Relay's status and the newest flows since asOf minus
// the window. Relay is read only in the cluster and never over TLS: its
// client certificate sits in a Secret Pacto does not read.
func (s *MeshSource) hubbleFetch(ctx context.Context, relay svcInfo, asOf time.Time) (hubbleRead, error) {
	port := relayPort(relay)
	switch {
	case port.Port == 443 || strings.Contains(port.Name, "tls"):
		return hubbleRead{}, errRelayTLS
	case !s.inCluster:
		return hubbleRead{}, errRelayUnreachable
	}
	ctx, cancel := context.WithTimeout(ctx, meshTimeout)
	defer cancel()
	addr := relay.Name + "." + relay.Namespace + ".svc:" + strconv.Itoa(int(port.Port))
	// ponytail: in-cluster .svc names never go through an egress proxy.
	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(s.dial), grpc.WithNoProxy(), grpc.WithDefaultCallOptions(grpc.ForceCodec(bytesCodec{})))
	if err != nil {
		return hubbleRead{}, errRelayUnreachable
	}
	defer func() { _ = conn.Close() }()
	var read hubbleRead
	err = rpc(ctx, conn, hubbleStatusMethod, nil, func(frame []byte) (more bool, err error) {
		read.unavailable, read.nodesKnown, err = decodeStatus(frame)
		return false, err
	})
	if err != nil {
		return read, err
	}
	err = rpc(ctx, conn, hubbleFlowsMethod, encodeFlowsRequest(maxHubbleFlows, asOf.Add(-meshWindow)), read.add)
	read.full = len(read.flows) >= maxHubbleFlows
	return read, err
}

// rpc sends req to method, the unary ServerStatus included, and hands each
// answer to each until the stream ends or each wants no more.
func rpc(ctx context.Context, conn *grpc.ClientConn, method string, req []byte, each func([]byte) (bool, error)) error {
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, method)
	if err != nil {
		return err
	}
	// A failed send surfaces in RecvMsg with its real status.
	_ = stream.SendMsg(&req)
	_ = stream.CloseSend()
	for more := true; more; {
		var frame []byte
		if err := stream.RecvMsg(&frame); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if more, err = each(frame); err != nil {
			return err
		}
	}
	return nil
}

// add reads one GetFlows frame into r and wants more until maxHubbleFlows
// flows are read.
func (r *hubbleRead) add(frame []byte) (bool, error) {
	f, isFlow, lost, err := decodeFlowsResponse(frame)
	r.lost += lost
	if isFlow {
		r.flows = append(r.flows, f)
	}
	return len(r.flows) < maxHubbleFlows, err
}

// hubbleErrorText is the fixed text of a failed Relay read, never its raw
// error or the Service's name.
func hubbleErrorText(err error) string {
	switch {
	case errors.Is(err, errRelayTLS):
		return "Hubble Relay serves TLS; Pacto reads no Secret for a client certificate, so its flows are not read"
	case errors.Is(err, errFlowUnreadable):
		return "Hubble Relay answered with an unreadable flow"
	}
	switch status.Code(err) {
	case codes.DeadlineExceeded, codes.Canceled:
		return "Hubble Relay timed out"
	case codes.Unavailable, codes.Unknown:
		return "Hubble Relay is not reachable from here"
	default:
		return "Hubble Relay refused the request"
	}
}

// edges is the read's edges and the limitations of every gap: nodes Relay
// could not reach, lost flows, a full read, a ring buffer shorter than the
// window, no flow at all and flows left out.
func (r hubbleRead) edges(id string, asOf time.Time, namespaces []string) ([]fleet.ObservedEdge, []fleet.Limitation) {
	var lims []fleet.Limitation
	add := func(code string, ok bool, message string) {
		if ok {
			lims = append(lims, fleet.Limitation{Code: code, Source: id, Message: message})
		}
	}
	start := r.shortestSpan()
	kept := asOf.Sub(start)
	add(fleet.LimitationMeshCoveragePartial, r.unavailable > 0, countOf(int(r.unavailable), "node", "nodes")+" not reachable through Hubble Relay")
	add(fleet.LimitationMeshCoveragePartial, !r.nodesKnown, "Hubble Relay did not say how many nodes it reaches")
	add(fleet.LimitationMeshCoveragePartial, r.lost > 0, countOf(int(r.lost), "flow was", "flows were")+" lost before Hubble Relay could serve them")
	add(fleet.LimitationMeshTelemetryUnread, r.full, "only the newest "+strconv.Itoa(maxHubbleFlows)+" flows of the last hour were read")
	add(fleet.LimitationMeshTelemetryUnread, start.IsZero(), "Hubble Relay served no flow for the last hour")
	add(fleet.LimitationMeshCoveragePartial, !r.full && !start.IsZero() && kept < meshWindow-hubbleWindowSlack,
		"Hubble Relay kept only the last "+countOf(max(1, int(kept/time.Minute)), "minute", "minutes")+" of flows")
	edges, cross, out, clusters, undirected := flowEdges(r.flows, namespaces)
	egress, worldOut, unnamed, capped := worldEdges(r.flows, namespaces)
	edges, out = append(edges, egress...), out+worldOut
	add(fleet.LimitationMeshCallsUnattributed, undirected > 0, countOf(undirected, "flow", "flows")+" had no request direction")
	add(fleet.LimitationMeshCallsUnattributed, out > 0, countOf(out, "flow reaches", "flows reach")+" namespaces this installation does not read")
	add(fleet.LimitationMeshCallsUnattributed, unnamed > 0, countOf(unnamed, "flow", "flows")+" left the cluster with no name; enable DNS visibility for egress names")
	add(fleet.LimitationEgressSeriesCap, capped, egressCapText)
	add(fleet.LimitationMeshCallsUnattributed, cross > 0, countOf(cross, "cross-cluster flow was", "cross-cluster flows were")+" not attributed")
	// Build qualifies every end with this source's cluster, so the flows of
	// several clusters cannot be told apart: no edge, as for Prometheus.
	add(fleet.LimitationMeshTelemetryUnread, clusters > 1, "Hubble Relay holds flows of several clusters; Pacto cannot tell which is this one")
	if clusters > 1 {
		edges = nil
	}
	return edges, lims
}

// shortestSpan is the latest of each node's earliest timed flow, zero when
// no flow is timed. Each node keeps its own ring buffer, so the node whose
// flows start latest bounds what the read covers. ponytail: a quiet node
// whose first flow is recent reads as a short buffer too; Relay says nothing
// that tells the two apart.
func (r hubbleRead) shortestSpan() time.Time {
	first := map[string]time.Time{}
	for _, f := range r.flows {
		if t, ok := first[f.node]; !f.time.IsZero() && (!ok || f.time.Before(t)) {
			first[f.node] = f.time
		}
	}
	var start time.Time
	for _, t := range first {
		start = maxTime(start, t)
	}
	return start
}

// flow is the part of a Hubble flow an edge needs.
type flow struct {
	time               time.Time
	node               string
	verdict, direction uint64
	reply, replyKnown  bool
	src, dst           endpoint
	dstNames           []string
}

// worldCall reports whether f is an egress request from a named workload
// to the world, an address outside the cluster.
func (f flow) worldCall() bool {
	return f.verdict == verdictForwarded && f.direction == directionEgress && f.src.named() && f.dst.world && f.replyKnown && !f.reply
}

// worldEdges folds the world calls of flows into one edge per caller and
// first host its destination names give, the busiest maxEgressSeries kept.
// A world call from outside namespaces is counted in out; one with no host
// name in unnamed.
func worldEdges(flows []flow, namespaces []string) (edges []fleet.ObservedEdge, out, unnamed int, capped bool) {
	per := map[[2]string]*fleet.ObservedEdge{}
	for _, f := range flows {
		if !f.worldCall() {
			continue
		}
		host := firstHost(f.dstNames)
		switch {
		case !inScope(namespaces, f.src.namespace):
			out++
		case host == "":
			unnamed++
		default:
			countFlow(per, f.src.caller(), host, f.time)
		}
	}
	edges = sortedEdges(per)
	if len(edges) > maxEgressSeries {
		return edges[:maxEgressSeries], out, unnamed, true
	}
	return edges, out, unnamed, false
}

// firstHost is the first of names that is a host outside the cluster.
func firstHost(names []string) string {
	for _, n := range names {
		if h := egressHost(n); h != "" {
			return h
		}
	}
	return ""
}

// countFlow counts one flow at t on the edge from to in per.
func countFlow(per map[[2]string]*fleet.ObservedEdge, from, to string, t time.Time) {
	k := [2]string{from, to}
	e := per[k]
	if e == nil {
		e = &fleet.ObservedEdge{From: from, To: to, FirstSeen: t, LastSeen: t}
		per[k] = e
	}
	e.Count++
	e.FirstSeen, e.LastSeen = minTime(e.FirstSeen, t), maxTime(e.LastSeen, t)
}

// sortedEdges is the edges of per, busiest first.
func sortedEdges(per map[[2]string]*fleet.ObservedEdge) []fleet.ObservedEdge {
	var edges []fleet.ObservedEdge
	for _, e := range per {
		edges = append(edges, *e)
	}
	slices.SortFunc(edges, func(a, b fleet.ObservedEdge) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	return edges
}

// egress reports whether f is forwarded traffic leaving a named workload
// for another named workload.
func (f flow) egress() bool {
	return f.verdict == verdictForwarded && f.direction == directionEgress && f.src.named() && f.dst.named()
}

// call reports whether f is an egress request; an unknown is_reply is no
// call, since a reply would point the edge backwards.
func (f flow) call() bool { return f.egress() && f.replyKnown && !f.reply }

// cross reports whether f's two ends sit in different clusters.
func (f flow) cross() bool {
	return f.src.cluster != "" && f.dst.cluster != "" && f.src.cluster != f.dst.cluster
}

// endpoint is one end of a flow: its namespace, cluster and first workload,
// and whether Cilium marks it the world.
type endpoint struct {
	namespace, cluster, kind, name string
	world                          bool
}

// worldIdentities are Cilium's reserved world identities: world, world-ipv4
// and world-ipv6.
var worldIdentities = []uint64{2, 9, 10}

// named reports whether e is a workload in a namespace; a host, the world
// or a remote node is not.
func (e endpoint) named() bool { return e.namespace != "" && e.name != "" }

// caller is e's K8sCaller name; the cluster is qualified by Build.
func (e endpoint) caller() string { return fleet.K8sCaller("", e.namespace, e.kind, e.name) }

// flowEdges folds flows into one edge per pair of workloads: how many
// calls, the earliest and the latest. Flows that are no call are skipped;
// those with an end outside namespaces or crossing clusters are counted and
// left out. clusters is how many distinct clusters the kept flows name;
// undirected is how many egress flows did not say whether they reply.
func flowEdges(flows []flow, namespaces []string) (edges []fleet.ObservedEdge, crossCluster, outOfScope, clusters, undirected int) {
	per := map[[2]string]*fleet.ObservedEdge{}
	seen := map[string]bool{}
	for _, f := range flows {
		switch {
		case f.egress() && !f.replyKnown:
			undirected++
			continue
		case !f.call():
			continue
		case !inScope(namespaces, f.src.namespace) || !inScope(namespaces, f.dst.namespace):
			outOfScope++
			continue
		case f.cross():
			crossCluster++
			continue
		}
		seen[cmp.Or(f.src.cluster, f.dst.cluster)] = true
		if from, to := f.src.caller(), f.dst.caller(); from != to {
			countFlow(per, from, to, f.time)
		}
	}
	delete(seen, "")
	return sortedEdges(per), crossCluster, outOfScope, len(seen), undirected
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// encodeFlowsRequest is a GetFlowsRequest for the newest number flows since
// since, not following.
func encodeFlowsRequest(number uint64, since time.Time) []byte {
	var ts []byte
	ts = protowire.AppendTag(ts, 1, protowire.VarintType)
	ts = protowire.AppendVarint(ts, uint64(since.Unix()))
	ts = protowire.AppendTag(ts, 2, protowire.VarintType)
	ts = protowire.AppendVarint(ts, uint64(since.Nanosecond()))
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, number)
	b = protowire.AppendTag(b, 7, protowire.BytesType)
	return protowire.AppendBytes(b, ts)
}

// fields calls fn with each field of the message b: val for a
// length-delimited field, x for a varint. A field of another type is
// skipped; a truncated one is errFlowUnreadable.
func fields(b []byte, fn func(num protowire.Number, val []byte, x uint64) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return errFlowUnreadable
		}
		b = b[n:]
		var val []byte
		var x uint64
		switch typ {
		case protowire.VarintType:
			x, n = protowire.ConsumeVarint(b)
		case protowire.BytesType:
			val, n = protowire.ConsumeBytes(b)
		default:
			n = protowire.ConsumeFieldValue(num, typ, b)
		}
		if n < 0 {
			return errFlowUnreadable
		}
		b = b[n:]
		if err := fn(num, val, x); err != nil {
			return err
		}
	}
	return nil
}

// decodeFlowsResponse reads one GetFlows frame: a flow, or lost flows.
func decodeFlowsResponse(b []byte) (f flow, isFlow bool, lost uint64, err error) {
	err = fields(b, func(num protowire.Number, val []byte, _ uint64) (err error) {
		switch num {
		case 1:
			isFlow = true
			f, err = decodeFlow(val)
		case 3:
			var n uint64
			n, err = varintField(val, 2)
			lost += n
		}
		return err
	})
	return f, isFlow, lost, err
}

// decodeFlow reads the fields of a Flow an edge needs.
func decodeFlow(b []byte) (flow, error) {
	var f flow
	err := fields(b, func(num protowire.Number, val []byte, x uint64) (err error) {
		switch num {
		case 1:
			f.time, err = decodeTime(val)
		case 2:
			f.verdict = x
		case 8:
			f.src, err = decodeEndpoint(val)
		case 9:
			f.dst, err = decodeEndpoint(val)
		case 11:
			f.node = string(val)
		case 14:
			f.dstNames = append(f.dstNames, string(val))
		case 22:
			f.direction = x
		case 26:
			var v uint64
			v, err = varintField(val, 1)
			f.reply, f.replyKnown = v != 0, true
		}
		return err
	})
	return f, err
}

// decodeEndpoint reads an Endpoint's namespace, cluster and first named
// workload.
func decodeEndpoint(b []byte) (endpoint, error) {
	var e endpoint
	err := fields(b, func(num protowire.Number, val []byte, x uint64) (err error) {
		switch num {
		case 2:
			e.world = e.world || slices.Contains(worldIdentities, x)
		case 3:
			e.namespace = string(val)
		case 4:
			e.world = e.world || strings.HasPrefix(string(val), "reserved:world")
		case 6:
			if e.name == "" {
				e.name, e.kind, err = decodeWorkload(val)
			}
		case 7:
			e.cluster = string(val)
		}
		return err
	})
	return e, err
}

// decodeWorkload reads a Workload's name and kind.
func decodeWorkload(b []byte) (name, kind string, err error) {
	err = fields(b, func(num protowire.Number, val []byte, _ uint64) error {
		switch num {
		case 1:
			name = string(val)
		case 2:
			kind = string(val)
		}
		return nil
	})
	return name, kind, err
}

// decodeTime reads a google.protobuf.Timestamp.
func decodeTime(b []byte) (time.Time, error) {
	var sec, nanos uint64
	err := fields(b, func(num protowire.Number, _ []byte, x uint64) error {
		switch num {
		case 1:
			sec = x
		case 2:
			nanos = x
		}
		return nil
	})
	return time.Unix(int64(sec), int64(int32(nanos))).UTC(), err
}

// decodeStatus reads a ServerStatusResponse's num_unavailable_nodes; known
// is false when the answer leaves it out, which never reads as zero.
func decodeStatus(b []byte) (n uint64, known bool, err error) {
	err = fields(b, func(num protowire.Number, val []byte, _ uint64) (err error) {
		if num == 6 {
			known = true
			n, err = varintField(val, 1)
		}
		return err
	})
	return n, known, err
}

// varintField is the varint field num of the message b, 0 when absent: a
// wrapper's value or a LostEvent's count.
func varintField(b []byte, num protowire.Number) (uint64, error) {
	var v uint64
	err := fields(b, func(n protowire.Number, _ []byte, x uint64) error {
		if n == num {
			v = x
		}
		return nil
	})
	return v, err
}
