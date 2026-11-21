package fleetsrc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// relayStub is an in-process Hubble Relay on bufconn: ServerStatus answers
// status (or statusErr), GetFlows sends frames then returns flowsErr.
type relayStub struct {
	lis       *bufconn.Listener
	mu        sync.Mutex
	methods   []string
	addrs     []string
	req       []byte
	status    []byte
	statusErr error
	frames    [][]byte
	flowsErr  error
	hang      bool
}

func newRelay(t *testing.T, frames ...[]byte) *relayStub {
	t.Helper()
	// A Relay always says how many nodes it cannot reach, zero included.
	r := &relayStub{lis: bufconn.Listen(1 << 20), frames: frames, status: pb(6, []byte{})}
	srv := grpc.NewServer(grpc.UnknownServiceHandler(r.handle), grpc.ForceServerCodec(bytesCodec{}))
	go func() { _ = srv.Serve(r.lis) }()
	t.Cleanup(srv.Stop)
	return r
}

func (r *relayStub) handle(_ any, stream grpc.ServerStream) error {
	m, _ := grpc.MethodFromServerStream(stream)
	r.mu.Lock()
	r.methods = append(r.methods, m)
	r.mu.Unlock()
	var in []byte
	if err := stream.RecvMsg(&in); err != nil {
		return err
	}
	if r.hang {
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	if m == hubbleStatusMethod {
		if r.statusErr != nil {
			return r.statusErr
		}
		return stream.SendMsg(&r.status)
	}
	r.mu.Lock()
	r.req = in
	r.mu.Unlock()
	for _, f := range r.frames {
		if err := stream.SendMsg(&f); err != nil {
			return err
		}
	}
	return r.flowsErr
}

func (r *relayStub) dial(ctx context.Context, addr string) (net.Conn, error) {
	r.mu.Lock()
	r.addrs = append(r.addrs, addr)
	r.mu.Unlock()
	return r.lis.DialContext(ctx)
}

func (r *relayStub) seen() ([]string, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.methods), slices.Clone(r.req)
}

func (r *relayStub) dialled() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.addrs)
}

// pb is a protobuf message of fields: a string or []byte is length-delimited,
// a uint64 a varint, keyed by the field number before it.
func pb(kv ...any) []byte {
	var b []byte
	for i := 0; i < len(kv); i += 2 {
		num := protowire.Number(kv[i].(int))
		switch v := kv[i+1].(type) {
		case uint64:
			b = protowire.AppendVarint(protowire.AppendTag(b, num, protowire.VarintType), v)
		case string:
			b = protowire.AppendBytes(protowire.AppendTag(b, num, protowire.BytesType), []byte(v))
		case []byte:
			b = protowire.AppendBytes(protowire.AppendTag(b, num, protowire.BytesType), v)
		}
	}
	return b
}

// ep is an Endpoint in ns with one workload, kind/name ("" for none), in
// cluster.
func ep(ns, workload, cluster string) []byte {
	fields := []any{3, ns, 7, cluster}
	if kind, name, ok := strings.Cut(workload, "/"); ok {
		fields = append(fields, 6, pb(2, kind, 1, name))
	}
	return pb(fields...)
}

func stamp(t time.Time) []byte { return pb(1, uint64(t.Unix()), 2, uint64(t.Nanosecond())) }

// call is a GetFlows frame of a forwarded egress request at at.
func call(at time.Time, src, dst []byte) []byte {
	return flowFrame(at, verdictForwarded, directionEgress, false, src, dst)
}

func flowFrame(at time.Time, verdict, dir uint64, reply bool, src, dst []byte) []byte {
	isReply := uint64(0)
	if reply {
		isReply = 1
	}
	return pb(1, pb(1, stamp(at), 2, verdict, 8, src, 9, dst, 22, dir, 26, pb(1, isReply)))
}

func lostFrame(n uint64) []byte { return pb(3, pb(1, uint64(1), 2, n)) }

func cart() []byte   { return ep("shop", "Deployment/cart", "") }
func ledger() []byte { return ep("shop", "StatefulSet/ledger", "") }

// relayJSON is a Service in ns named name with one port.
func relayJSON(ns, name, portName string, port int, labels ...string) string {
	var item map[string]any
	_ = json.Unmarshal([]byte(svcJSON(ns, name, "", labels...)), &item)
	item["spec"].(map[string]any)["ports"] = []map[string]any{{"name": portName, "port": port}}
	b, _ := json.Marshal(item)
	return string(b)
}

// hubbleMesh serves only Cilium with services; defaultRelay is Relay in
// kube-system on port 80.
func hubbleMesh(in bool, services ...string) *meshFake {
	f := meshClient(in, append([]string{svcJSON("shop", "cart", "")}, services...), []string{nsJSON("shop")})
	f.versions = map[string]string{ciliumGroup: "v2"}
	return f
}

var defaultRelay = relayJSON("kube-system", "hubble-relay", "grpc", 80, "k8s-app=hubble-relay")

func hubbleSource(f *meshFake, namespaces []string, r *relayStub) *MeshSource {
	s := meshSource(f, namespaces, nil, nil)
	if r != nil {
		s.dial = r.dial
	}
	return s
}

func hubbleRowOf(t *testing.T, col *fleet.Collection) fleet.Provider {
	t.Helper()
	if len(col.Providers) != 1 || col.Providers[0].ID != fleet.ProviderHubble || col.Providers[0].Name != "Cilium Hubble" {
		t.Fatalf("providers %+v", col.Providers)
	}
	return col.Providers[0]
}

// readHubble collects a default Relay sending frames.
func readHubble(t *testing.T, frames ...[]byte) (fleet.Provider, *fleet.Collection, *relayStub) {
	t.Helper()
	r := newRelay(t, frames...)
	col := collectMesh(t, hubbleSource(hubbleMesh(true, defaultRelay), nil, r))
	return hubbleRowOf(t, col), col, r
}

func TestHubbleEdgesFromFlows(t *testing.T) {
	t0 := meshNow.Add(-59 * time.Minute)
	row, col, r := readHubble(t,
		call(t0.Add(time.Minute), cart(), ledger()),
		call(t0, cart(), ledger()),
		call(meshNow.Add(-time.Minute), cart(), ledger()),
		call(meshNow.Add(-2*time.Minute), ep("shop", "/payments", ""), cart()),
		call(meshNow, cart(), cart()))
	want := []fleet.ObservedEdge{
		{From: "k8s:shop/Deployment/cart", To: "k8s:shop/StatefulSet/ledger", Count: 3, FirstSeen: t0, LastSeen: meshNow.Add(-time.Minute)},
		{From: "k8s:shop//payments", To: "k8s:shop/Deployment/cart", Count: 1, FirstSeen: meshNow.Add(-2 * time.Minute), LastSeen: meshNow.Add(-2 * time.Minute)},
	}
	if !slices.Equal(col.Observed, want) || !col.ObservedUndeclaredOnly || col.State.Status != fleet.SourceAvailable {
		t.Errorf("observed %+v state %+v", col.Observed, col.State)
	}
	if row.Status != fleet.SourceAvailable || row.Evidence != "serves cilium.io; flows read from Hubble Relay" || len(row.Limitations) != 0 ||
		!slices.Equal(row.Objects, []fleet.ProviderObject{{Kind: "Service", Namespace: "kube-system", Name: "hubble-relay"}}) ||
		!slices.Equal(row.UsedFor, []string{fleet.UseObservedEdges}) {
		t.Errorf("row %+v", row)
	}
	if got := r.dialled(); len(got) == 0 || got[0] != "hubble-relay.kube-system.svc:80" {
		t.Errorf("dialled %v", got)
	}
}

func TestHubbleSkipsRepliesDropsAndIngress(t *testing.T) {
	at := meshNow.Add(-59 * time.Minute)
	_, col, _ := readHubble(t,
		flowFrame(at, verdictForwarded, directionEgress, true, cart(), ledger()),
		flowFrame(at, 2, directionEgress, false, cart(), ledger()),
		flowFrame(at, verdictForwarded, 1, false, cart(), ledger()))
	if len(col.Observed) != 0 {
		t.Errorf("observed %+v", col.Observed)
	}
}

func TestHubbleNonPodEndpointIgnored(t *testing.T) {
	at := meshNow.Add(-59 * time.Minute)
	row, col, _ := readHubble(t, call(at, cart(), ep("", "", "")), call(at, ep("shop", "", ""), cart()))
	if len(col.Observed) != 0 || len(row.Limitations) != 0 {
		t.Errorf("observed %+v row %+v", col.Observed, row)
	}
}

func TestHubbleCrossClusterFlowsPartial(t *testing.T) {
	at := meshNow.Add(-59 * time.Minute)
	row, col, _ := readHubble(t, call(at, ep("shop", "Deployment/cart", "eu"), ep("shop", "Deployment/ledger", "us")),
		call(at, ep("shop", "Deployment/cart", "eu"), ep("shop", "Deployment/ledger", "eu")))
	if len(col.Observed) != 1 || row.Status != fleet.SourcePartial ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 cross-cluster flow was not attributed") {
		t.Errorf("observed %+v row %+v", col.Observed, row)
	}
}

// Flows of two clusters, each with both ends in one, cannot be told apart
// once Build names every end in this source's cluster: no edge, partial.
func TestHubbleSeveralClustersPartial(t *testing.T) {
	at := meshNow.Add(-59 * time.Minute)
	row, col, _ := readHubble(t, call(at, ep("shop", "Deployment/cart", "eu"), ep("shop", "Deployment/ledger", "eu")),
		call(at, ep("shop", "Deployment/cart", "us"), ep("shop", "Deployment/ledger", "")))
	if len(col.Observed) != 0 || row.Status != fleet.SourcePartial ||
		!hasMessage(row, fleet.LimitationMeshTelemetryUnread, "Hubble Relay holds flows of several clusters; Pacto cannot tell which is this one") {
		t.Errorf("observed %+v row %+v", col.Observed, row)
	}
}

func TestHubbleUnavailableNodesPartial(t *testing.T) {
	r := newRelay(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	r.status = pb(1, uint64(4000), 6, pb(1, uint64(2)))
	row := hubbleRowOf(t, collectMesh(t, hubbleSource(hubbleMesh(true, defaultRelay), nil, r)))
	if row.Status != fleet.SourcePartial || row.Evidence != "serves cilium.io; flows read from Hubble Relay" ||
		!hasMessage(row, fleet.LimitationMeshCoveragePartial, "2 nodes not reachable through Hubble Relay") {
		t.Errorf("row %+v", row)
	}
}

func TestHubbleNodeCountUnknownPartial(t *testing.T) {
	r := newRelay(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	r.status = nil
	row := hubbleRowOf(t, collectMesh(t, hubbleSource(hubbleMesh(true, defaultRelay), nil, r)))
	if row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshCoveragePartial, "Hubble Relay did not say how many nodes it reaches") {
		t.Errorf("row %+v", row)
	}
}

func TestHubbleLostEventsPartial(t *testing.T) {
	row, col, _ := readHubble(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()), lostFrame(3), lostFrame(4))
	if len(col.Observed) != 1 || !hasMessage(row, fleet.LimitationMeshCoveragePartial, "7 flows were lost before Hubble Relay could serve them") {
		t.Errorf("row %+v", row)
	}
}

func TestHubbleFlowCapPartial(t *testing.T) {
	frames := make([][]byte, maxHubbleFlows+5)
	for i := range frames {
		frames[i] = call(meshNow.Add(-time.Duration(i)*time.Second), cart(), ledger())
	}
	row, col, _ := readHubble(t, frames...)
	if len(col.Observed) != 1 || col.Observed[0].Count != maxHubbleFlows ||
		!hasMessage(row, fleet.LimitationMeshTelemetryUnread, "only the newest 10000 flows of the last hour were read") || len(row.Limitations) != 1 {
		t.Errorf("observed %+v row %+v", col.Observed, row)
	}
}

func TestHubbleNoFlowIsPartial(t *testing.T) {
	row, _, _ := readHubble(t, lostFrame(0))
	if !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "Hubble Relay served no flow for the last hour") {
		t.Errorf("row %+v", row)
	}
}

func TestHubbleNoRelayIsPartial(t *testing.T) {
	r := newRelay(t)
	col := collectMesh(t, hubbleSource(hubbleMesh(true), nil, r))
	row := hubbleRowOf(t, col)
	if row.Status != fleet.SourcePartial || row.Evidence != "serves cilium.io; no Hubble Relay found" || len(row.Objects) != 0 ||
		!hasMessage(row, fleet.LimitationMeshTelemetryUnread, "no Hubble Relay found") || len(r.dialled()) != 0 || col.State.Status != fleet.SourcePartial {
		t.Errorf("row %+v dialled %v", row, r.dialled())
	}
}

func TestHubbleTLSRelayIsPartial(t *testing.T) {
	for _, relay := range []string{
		relayJSON("kube-system", "hubble-relay", "grpc", 443, "k8s-app=hubble-relay"),
		relayJSON("kube-system", "hubble-relay", "grpc-tls", 4245, "k8s-app=hubble-relay"),
	} {
		r := newRelay(t)
		row := hubbleRowOf(t, collectMesh(t, hubbleSource(hubbleMesh(true, relay), nil, r)))
		if len(r.dialled()) != 0 || row.Evidence != "serves cilium.io; Hubble Relay found, flows not read" || !hasMessage(row, fleet.LimitationMeshTelemetryUnread,
			"Hubble Relay serves TLS; Pacto reads no Secret for a client certificate, so its flows are not read") {
			t.Errorf("row %+v dialled %v", row, r.dialled())
		}
	}
}

func TestHubbleUnreachableIsPartial(t *testing.T) {
	s := hubbleSource(hubbleMesh(true, defaultRelay), nil, nil)
	dialled := false
	s.dial = func(context.Context, string) (net.Conn, error) {
		dialled = true
		return nil, errors.New("dial tcp 10.0.0.9:80: refused")
	}
	row := hubbleRowOf(t, collectMesh(t, s))
	if !dialled || !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "Hubble Relay is not reachable from here") {
		t.Errorf("row %+v", row)
	}
	badName := relayJSON("kube-system", "bad%zz", "grpc", 80, "k8s-app=hubble-relay")
	if row := hubbleRowOf(t, collectMesh(t, hubbleSource(hubbleMesh(true, badName), nil, nil))); !hasMessage(row,
		fleet.LimitationMeshTelemetryUnread, "Hubble Relay is not reachable from here") {
		t.Errorf("row %+v", row)
	}
	// The default dialer reaches a closed local port, never the network.
	if c, err := NewMeshSource("x", nil, defaultMesh(true), nil).dial(context.Background(), "127.0.0.1:1"); err == nil {
		_ = c.Close()
		t.Error("dialled a closed port")
	}
}

func TestHubbleRequestIsBounded(t *testing.T) {
	_, _, r := readHubble(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	_, req := r.seen()
	var number uint64
	var follow bool
	var since time.Time
	err := fields(req, func(num protowire.Number, val []byte, x uint64) (err error) {
		switch num {
		case 1:
			number = x
		case 3:
			follow = x != 0
		case 7:
			since, err = decodeTime(val)
		}
		return err
	})
	if err != nil || number != 10000 || follow || !since.Equal(meshNow.Add(-time.Hour)) {
		t.Errorf("request number %d follow %v since %v (%v)", number, follow, since, err)
	}
}

func TestHubbleCallsOnlyStatusAndFlows(t *testing.T) {
	_, _, r := readHubble(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	if methods, _ := r.seen(); !slices.Equal(methods, []string{hubbleStatusMethod, hubbleFlowsMethod}) {
		t.Errorf("methods %v", methods)
	}
}

// hubbleFailures are the rows of every failure a Relay read can have, with
// the text each must read.
func hubbleFailures(t *testing.T) map[string]fleet.Provider {
	t.Helper()
	rows := map[string]fleet.Provider{}
	read := func(r *relayStub, ctx context.Context) fleet.Provider {
		col, _ := hubbleSource(hubbleMesh(true, defaultRelay), nil, r).Collect(ctx)
		return hubbleRowOf(t, col)
	}
	unreachable := hubbleSource(hubbleMesh(true, defaultRelay), nil, nil)
	unreachable.dial = func(context.Context, string) (net.Conn, error) { return nil, errors.New("dial tcp 10.0.0.9:80") }
	rows["Hubble Relay is not reachable from here"] = hubbleRowOf(t, collectMesh(t, unreachable))
	hang := newRelay(t)
	hang.hang = true
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	rows["Hubble Relay timed out"] = read(hang, ctx)
	refused := newRelay(t)
	refused.statusErr = status.Error(codes.PermissionDenied, "hubble-relay.kube-system denied")
	rows["Hubble Relay refused the request"] = read(refused, context.Background())
	flowsRefused := newRelay(t, call(meshNow, cart(), ledger()))
	flowsRefused.flowsErr = status.Error(codes.Unimplemented, "no")
	rows["Hubble Relay refused the request "] = read(flowsRefused, context.Background())
	rows["Hubble Relay answered with an unreadable flow"] = read(newRelay(t, []byte{0x0a, 0x05, 0x01}), context.Background())
	badStatus := newRelay(t)
	badStatus.status = []byte{0x32, 0x09}
	rows["Hubble Relay answered with an unreadable flow "] = read(badStatus, context.Background())
	return rows
}

func TestHubbleErrorsAreFixedTexts(t *testing.T) {
	for want, row := range hubbleFailures(t) {
		if !hasMessage(row, fleet.LimitationMeshTelemetryUnread, strings.TrimSpace(want)) || row.Status != fleet.SourcePartial {
			t.Errorf("want %q, row %+v", want, row)
		}
		for _, text := range append(messages(row), row.Evidence) {
			for _, raw := range []string{"hubble-relay", "kube-system", "shop", "10.0.0.9", ":", "dial", "denied", "deadline", ".svc", "rpc error"} {
				if strings.Contains(text, raw) {
					t.Errorf("%q holds %q", text, raw)
				}
			}
		}
	}
}

func TestHubbleDecodeRejectsTruncatedFrame(t *testing.T) {
	full := call(meshNow, cart(), ledger())
	for i := 1; i < len(full); i++ {
		if _, _, _, err := decodeFlowsResponse(full[:i]); !errors.Is(err, errFlowUnreadable) {
			t.Errorf("truncated at %d: %v", i, err)
		}
	}
	nested := map[string][]byte{
		"time":     pb(1, pb(1, []byte{0x08})),
		"endpoint": pb(1, pb(8, []byte{0x1a, 0x05})),
		"workload": pb(1, pb(8, pb(6, []byte{0x0a, 0x05}))),
		"is_reply": pb(1, pb(26, []byte{0x08})),
		"lost":     pb(3, []byte{0x10}),
		"tag":      {0x80},
	}
	for name, b := range nested {
		if _, _, _, err := decodeFlowsResponse(b); !errors.Is(err, errFlowUnreadable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Fields of other types are skipped.
	skip := protowire.AppendFixed32(protowire.AppendTag(nil, 4, protowire.Fixed32Type), 7)
	if f, isFlow, _, err := decodeFlowsResponse(append(skip, call(meshNow, cart(), ledger())...)); err != nil || !isFlow || f.src.name != "cart" {
		t.Errorf("flow %+v %v", f, err)
	}
}

// A tenant who copies Cilium's label onto a Service of its own gains no trust
// for its Relay; the Relay in kube-system is still read.
func TestHubbleCopiedCiliumLabelTrustsNone(t *testing.T) {
	r := newRelay(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	collectMesh(t, hubbleSource(hubbleMesh(true, svcJSON("kube-system", "hubble-peer", "", "k8s-app=cilium"),
		svcJSON("tenant", "fake-cilium", "", "k8s-app=cilium"), relayJSON("tenant", "relay", "grpc", 80, "k8s-app=hubble-relay"),
		relayJSON("kube-system", "hubble-relay", "grpc", 80)), nil, r))
	if got := r.dialled(); len(got) == 0 || got[0] != "hubble-relay.kube-system.svc:80" {
		t.Errorf("dialled %v", got)
	}
}

func TestHubbleRelayOutsideCiliumNamespaceIgnored(t *testing.T) {
	tenant := relayJSON("tenant", "hubble-relay", "grpc", 80, "k8s-app=hubble-relay")
	for name, svcs := range map[string][]string{
		"tenant":       {tenant},
		"externalName": {svcJSON("kube-system", "hubble-relay", "ExternalName", "k8s-app=hubble-relay")},
		"selectorless": {svcJSON("kube-system", "hubble-relay", "selectorless", "k8s-app=hubble-relay")},
	} {
		r := newRelay(t)
		row := hubbleRowOf(t, collectMesh(t, hubbleSource(hubbleMesh(true, svcs...), nil, r)))
		if len(r.dialled()) != 0 || row.Evidence != "serves cilium.io; no Hubble Relay found" {
			t.Errorf("%s: row %+v dialled %v", name, row, r.dialled())
		}
	}
	// In the namespace Cilium's own Services sit in, a Relay found by name
	// is read; the label class goes first.
	for want, svcs := range map[string][]string{
		"relay.cilium.svc:80": {svcJSON("cilium", "hubble-peer", "", "k8s-app=cilium"), relayJSON("cilium", "relay", "grpc", 80, "k8s-app=hubble-relay"),
			relayJSON("kube-system", "hubble-relay", "grpc", 80)},
		"hubble-relay.cilium.svc:9090": {svcJSON("cilium", "agent", "", "app.kubernetes.io/part-of=cilium"), svcJSON("cilium", "hubble-relay", ""), tenant},
	} {
		r := newRelay(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
		collectMesh(t, hubbleSource(hubbleMesh(true, svcs...), nil, r))
		if got := r.dialled(); len(got) == 0 || got[0] != want {
			t.Errorf("dialled %v, want %s", got, want)
		}
	}
	if p := relayPort(svcInfo{}); p.Port != 80 {
		t.Errorf("port %+v", p)
	}
}

func TestHubbleBufferShorterThanWindowPartial(t *testing.T) {
	row, col, _ := readHubble(t, call(meshNow.Add(-12*time.Minute), cart(), ledger()), call(meshNow, ep("shop", "Deployment/x", ""), cart()))
	if len(col.Observed) != 2 || row.Status != fleet.SourcePartial ||
		!hasMessage(row, fleet.LimitationMeshCoveragePartial, "Hubble Relay kept only the last 12 minutes of flows") {
		t.Errorf("row %+v", row)
	}
	row, _, _ = readHubble(t, call(meshNow.Add(10*time.Second), cart(), ledger()))
	if !hasMessage(row, fleet.LimitationMeshCoveragePartial, "Hubble Relay kept only the last 1 minute of flows") {
		t.Errorf("row %+v", row)
	}
}

func TestHubbleScopedDropsOutOfScopeFlows(t *testing.T) {
	at := meshNow.Add(-59 * time.Minute)
	r := newRelay(t, call(at, cart(), ledger()), call(at, cart(), ep("other", "Deployment/secret-svc", "")),
		call(at, ep("other", "Deployment/spy", ""), cart()))
	col := collectMesh(t, hubbleSource(hubbleMesh(true, defaultRelay), []string{"shop", "kube-system"}, r))
	row := hubbleRowOf(t, col)
	if len(col.Observed) != 1 || !hasMessage(row, fleet.LimitationMeshCallsUnattributed, "2 flows reach namespaces this installation does not read") {
		t.Errorf("observed %+v row %+v", col.Observed, row)
	}
	for _, text := range append(messages(row), col.Observed[0].From, col.Observed[0].To) {
		if strings.Contains(text, "other") || strings.Contains(text, "secret-svc") || strings.Contains(text, "spy") {
			t.Errorf("%q names an out-of-scope end", text)
		}
	}
	_, _, out, _ := flowEdges([]flow{{verdict: verdictForwarded, direction: directionEgress, src: endpoint{namespace: "shop", name: "a"},
		dst: endpoint{namespace: "other", name: "b"}}}, []string{"shop"})
	if out != 1 {
		t.Errorf("out of scope %d", out)
	}
}

func TestHubbleOutOfClusterNotDialled(t *testing.T) {
	r := newRelay(t)
	row := hubbleRowOf(t, collectMesh(t, hubbleSource(hubbleMesh(false, defaultRelay), nil, r)))
	if len(r.dialled()) != 0 || !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "Hubble Relay is not reachable from here") {
		t.Errorf("row %+v dialled %v", row, r.dialled())
	}
}

func TestHubbleAndIstioTwoRows(t *testing.T) {
	f := hubbleMesh(true, defaultRelay)
	f.versions[istioGroup] = "v1"
	r := newRelay(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	col := collectMesh(t, hubbleSource(f, nil, r))
	if len(col.Providers) != 2 || col.Providers[1].ID != fleet.ProviderHubble || len(col.Observed) != 1 {
		t.Errorf("providers %+v observed %+v", col.Providers, col.Observed)
	}
}

func TestBytesCodec(t *testing.T) {
	var c bytesCodec
	in := []byte{1, 2}
	out, _ := c.Marshal(&in)
	var got []byte
	if err := c.Unmarshal(out, &got); err != nil || !slices.Equal(got, in) || c.Name() != "proto" {
		t.Errorf("got %v", got)
	}
}
