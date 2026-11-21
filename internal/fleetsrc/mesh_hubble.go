package fleetsrc

// Hubble Relay is read over gRPC with a byte codec and protowire, so no
// Cilium module is linked. The fields read, checked against
// github.com/cilium/cilium v1.18.17 (api/v1/observer/observer.proto and
// api/v1/flow/flow.proto):
//
//	GetFlowsRequest       number=1 follow=3 since=7 (Timestamp seconds=1 nanos=2)
//	GetFlowsResponse      flow=1 lost_events=3
//	Flow                  time=1 verdict=2 source=8 destination=9
//	                      traffic_direction=22 is_reply=26 (BoolValue value=1)
//	Endpoint              namespace=3 workloads=6 cluster_name=7
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
// namespace Cilium's own Services sit in (soleNamespace); never an
// ExternalName or a selectorless Service, whose address a tenant chooses.
func hubbleRelay(services []svcInfo) (svcInfo, bool) {
	trusted := []string{"kube-system"}
	if ns, ok := soleNamespace(services, func(svc svcInfo) bool {
		return svc.Labels["k8s-app"] == "cilium" || svc.Labels["app.kubernetes.io/part-of"] == "cilium"
	}); ok {
		trusted = append(trusted, ns)
	}
	sorted := sortedServices(slices.Clone(services))
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
	lims := slices.Clone(cl.servicesLim)
	relay, ok := hubbleRelay(cl.services)
	if !ok {
		lims = append(lims, s.unread("no Hubble Relay found"))
		return meshRow(fleet.ProviderHubble, name, "serves cilium.io; no Hubble Relay found", nil, lims), nil
	}
	objects := []fleet.ProviderObject{serviceObject(relay)}
	read, err := s.hubbleFetch(ctx, relay, asOf)
	if err != nil {
		lims = append(lims, s.unread(hubbleErrorText(err)))
		return meshRow(fleet.ProviderHubble, name, "serves cilium.io; Hubble Relay found, flows not read", objects, lims), nil
	}
	edges, readLims := read.edges(s.id, asOf, s.namespaces)
	return meshRow(fleet.ProviderHubble, name, "serves cilium.io; flows read from Hubble Relay", objects, append(lims, readLims...)), edges
}

// hubbleRead is what one Relay read returned.
type hubbleRead struct {
	flows       []flow
	unavailable uint64 // nodes Relay could not reach
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
		read.unavailable, err = decodeStatus(frame)
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
	earliest := r.earliest()
	kept := asOf.Sub(earliest)
	add(fleet.LimitationMeshCoveragePartial, r.unavailable > 0, countOf(int(r.unavailable), "node", "nodes")+" not reachable through Hubble Relay")
	add(fleet.LimitationMeshCoveragePartial, r.lost > 0, countOf(int(r.lost), "flow was", "flows were")+" lost before Hubble Relay could serve them")
	add(fleet.LimitationMeshTelemetryUnread, r.full, "only the newest "+strconv.Itoa(maxHubbleFlows)+" flows of the last hour were read")
	add(fleet.LimitationMeshTelemetryUnread, earliest.IsZero(), "Hubble Relay served no flow for the last hour")
	add(fleet.LimitationMeshCoveragePartial, !r.full && !earliest.IsZero() && kept < meshWindow-hubbleWindowSlack,
		"Hubble Relay kept only the last "+countOf(max(1, int(kept/time.Minute)), "minute", "minutes")+" of flows")
	edges, cross, out, clusters := flowEdges(r.flows, namespaces)
	add(fleet.LimitationMeshCallsUnattributed, out > 0, countOf(out, "flow reaches", "flows reach")+" namespaces this installation does not read")
	add(fleet.LimitationMeshCallsUnattributed, cross > 0, countOf(cross, "cross-cluster flow was", "cross-cluster flows were")+" not attributed")
	// Build qualifies every end with this source's cluster, so the flows of
	// several clusters cannot be told apart: no edge, as for Prometheus.
	add(fleet.LimitationMeshTelemetryUnread, clusters > 1, "Hubble Relay holds flows of several clusters; Pacto cannot tell which is this one")
	if clusters > 1 {
		edges = nil
	}
	return edges, lims
}

// earliest is the time of the read's earliest timed flow, zero when none.
func (r hubbleRead) earliest() time.Time {
	var t time.Time
	for _, f := range r.flows {
		if !f.time.IsZero() && (t.IsZero() || f.time.Before(t)) {
			t = f.time
		}
	}
	return t
}

// flow is the part of a Hubble flow an edge needs.
type flow struct {
	time               time.Time
	verdict, direction uint64
	reply              bool
	src, dst           endpoint
}

// call reports whether f is a forwarded request leaving a named workload
// for another named workload.
func (f flow) call() bool {
	return f.verdict == verdictForwarded && f.direction == directionEgress && !f.reply && f.src.named() && f.dst.named()
}

// cross reports whether f's two ends sit in different clusters.
func (f flow) cross() bool {
	return f.src.cluster != "" && f.dst.cluster != "" && f.src.cluster != f.dst.cluster
}

// endpoint is one end of a flow: its namespace, cluster and first workload.
type endpoint struct {
	namespace, cluster, kind, name string
}

// named reports whether e is a workload in a namespace; a host, the world
// or a remote node is not.
func (e endpoint) named() bool { return e.namespace != "" && e.name != "" }

// caller is e's K8sCaller name; the cluster is qualified by Build.
func (e endpoint) caller() string { return fleet.K8sCaller("", e.namespace, e.kind, e.name) }

// flowEdges folds flows into one edge per pair of workloads: how many
// calls, the earliest and the latest. Flows that are no call are skipped;
// those with an end outside namespaces or crossing clusters are counted and
// left out. clusters is how many distinct clusters the kept flows name.
func flowEdges(flows []flow, namespaces []string) (edges []fleet.ObservedEdge, crossCluster, outOfScope, clusters int) {
	type pair struct{ from, to string }
	per := map[pair]*fleet.ObservedEdge{}
	seen := map[string]bool{}
	for _, f := range flows {
		switch {
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
		k := pair{f.src.caller(), f.dst.caller()}
		if k.from == k.to {
			continue
		}
		e := per[k]
		if e == nil {
			e = &fleet.ObservedEdge{From: k.from, To: k.to, FirstSeen: f.time, LastSeen: f.time}
			per[k] = e
		}
		e.Count++
		e.FirstSeen, e.LastSeen = minTime(e.FirstSeen, f.time), maxTime(e.LastSeen, f.time)
	}
	for _, e := range per {
		edges = append(edges, *e)
	}
	slices.SortFunc(edges, func(a, b fleet.ObservedEdge) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To))
	})
	delete(seen, "")
	return edges, crossCluster, outOfScope, len(seen)
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
		case 22:
			f.direction = x
		case 26:
			var v uint64
			v, err = varintField(val, 1)
			f.reply = v != 0
		}
		return err
	})
	return f, err
}

// decodeEndpoint reads an Endpoint's namespace, cluster and first named
// workload.
func decodeEndpoint(b []byte) (endpoint, error) {
	var e endpoint
	err := fields(b, func(num protowire.Number, val []byte, _ uint64) (err error) {
		switch num {
		case 3:
			e.namespace = string(val)
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

// decodeStatus reads a ServerStatusResponse's num_unavailable_nodes.
func decodeStatus(b []byte) (uint64, error) {
	var n uint64
	err := fields(b, func(num protowire.Number, val []byte, _ uint64) (err error) {
		if num == 6 {
			n, err = varintField(val, 1)
		}
		return err
	})
	return n, err
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
