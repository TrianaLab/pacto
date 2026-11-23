package fleetsrc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// A tenant's lone Service of a class asked earlier never outranks the
// Prometheus in the mesh's control-plane namespace, nor backs it up.
func TestMeshTrustedPrometheusOutranksLoneTenant(t *testing.T) {
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("istio-system", "prometheus", "", "app=prometheus"),
		svcJSON("team-a", "prometheus-operated", "", "operated-prometheus=true")}, nil)
	tenant, real := newProm(t, answer(vector(series("source", "spy", "ledger", "9")))), newProm(t, answer(vector()))
	var asked []string
	col := collectMesh(t, meshSource(f, nil, map[string]*promStub{"team-a/prometheus-operated": tenant, "istio-system/prometheus": real}, &asked))
	if !slices.Equal(asked, []string{"istio-system/prometheus"}) || len(col.Observed) != 0 || tenant.count() != 0 {
		t.Errorf("asked %v observed %+v", asked, col.Observed)
	}
}

// A Service an earlier class took still counts toward a later class, so a
// tenant copying only the later label never looks like its only Service.
func TestMeshClassCountsServicesTakenEarlier(t *testing.T) {
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("monitoring", "prom", "", "operated-prometheus=true", "app.kubernetes.io/name=prometheus"),
		svcJSON("a-team", "fake", "", "app.kubernetes.io/name=prometheus")}, nil)
	real := newProm(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	fake := newProm(t, answer(vector(series("source", "spy", "ledger", "9"))))
	var asked []string
	col := collectMesh(t, meshSource(f, nil, map[string]*promStub{"monitoring/prom": real, "a-team/fake": fake}, &asked))
	if !slices.Equal(asked, []string{"monitoring/prom"}) || len(col.Observed) != 0 {
		t.Errorf("asked %v observed %+v", asked, col.Observed)
	}
}

// pageFailFake fails the Services page after the first.
type pageFailFake struct{ *meshFake }

func (f *pageFailFake) ListPage(ctx context.Context, gvr schema.GroupVersionResource, ns, token string) ([]byte, string, error) {
	if gvr == servicesGVR && token == "next" {
		return nil, "", errors.New("the server is currently unable to handle the request")
	}
	return f.meshFake.ListPage(ctx, gvr, ns, token)
}

// A Services list that failed after its first page trusts nothing for being
// alone in it: the tenant's copies read on page 1 are never asked or dialled.
func TestMeshPartialServicesListTrustsNone(t *testing.T) {
	m := meshClient(true, nil, []string{nsJSON("aaa-team")})
	m.versions = map[string]string{istioGroup: "v1", ciliumGroup: "v2"}
	m.pages["services||"] = [2]string{strings.Join([]string{svcJSON("aaa-team", "istiod", "", "app=istiod"),
		svcJSON("aaa-team", "prom", "", "app=prometheus"), svcJSON("aaa-team", "cilium", "", "k8s-app=cilium"),
		relayJSON("aaa-team", "hubble-relay", "grpc", 80, "k8s-app=hubble-relay")}, ","), "next"}
	fake := newProm(t, answer(vector(series("source", "spy", "ledger", "9"))))
	r := newRelay(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	var asked []string
	s := hubbleSource(m, nil, r)
	s.baseURL = meshSource(m, nil, map[string]*promStub{"aaa-team/prom": fake}, &asked).baseURL
	s.client = &pageFailFake{m}
	col := collectMesh(t, s)
	if len(asked) != 0 || len(r.dialled()) != 0 || len(col.Observed) != 0 || len(col.Providers) != 2 {
		t.Fatalf("asked %v dialled %v observed %+v providers %+v", asked, r.dialled(), col.Observed, col.Providers)
	}
	istio, hubble := col.Providers[0], col.Providers[1]
	if istio.Evidence != "serves networking.istio.io; Services not read" || len(istio.Objects) != 0 {
		t.Errorf("istio %+v", istio)
	}
	if hubble.Evidence != "serves cilium.io; Services not read" ||
		!slices.Equal(messages(hubble), []string{"Services could not be read in every namespace, so Pacto may miss Hubble Relay"}) {
		t.Errorf("hubble %+v", hubble)
	}
}

// One candidate answering empty and the next failing says the failure, never
// that the mesh's calls are not recorded, and names the one that failed.
func TestMeshMixedEmptyAndFailedReportsFailure(t *testing.T) {
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("istio-system", "prom-a", "", "operated-prometheus=true"), svcJSON("istio-system", "prom-b", "", "app=prometheus")}, nil)
	empty := newProm(t, answer(vector()))
	refused := newProm(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	row := istioRow(t, collectMesh(t, meshSource(f, nil, map[string]*promStub{"istio-system/prom-a": empty, "istio-system/prom-b": refused}, nil)))
	if !slices.Equal(messages(row), []string{"the Prometheus Pacto found refused the query (HTTP 503)"}) ||
		!slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Service", Namespace: "istio-system", Name: "prom-b"}) {
		t.Errorf("row %+v", row)
	}
}

// istio-injection wins over a revision label, as in Istio's webhook; ambient
// capture does not depend on it.
func TestMeshIstioInjectionDisabledWinsOverRevision(t *testing.T) {
	if row := coverageRow(t, "batch:istio-injection=disabled,istio.io/rev=1-22"); !hasMessage(row, fleet.LimitationMeshCoveragePartial, "1 namespace without sidecars") {
		t.Errorf("row %+v", row)
	}
	for _, labels := range []map[string]string{
		{"istio-injection": "disabled", "istio.io/dataplane-mode": "ambient"},
		{"istio-injection": "enabled", "istio.io/rev": "1-22"},
	} {
		if !istioCovers(nsInfo{Labels: labels}) {
			t.Errorf("%v not covered", labels)
		}
	}
	if istioCovers(nsInfo{Labels: map[string]string{"istio-injection": "disabled", "istio.io/rev": "1-22"}}) {
		t.Error("disabled with a revision covered")
	}
}

// A Prometheus holding another cluster's series under its external cluster
// label reads as several clusters, for Istio and for Linkerd.
func TestMeshExternalClusterLabelIsSeveralClusters(t *testing.T) {
	if !strings.Contains(istioQuery(nil), "reporter, cluster,") || !strings.Contains(linkerdQuery(nil), "target_cluster_name, cluster") {
		t.Errorf("queries keep no cluster label: %s / %s", istioQuery(nil), linkerdQuery(nil))
	}
	p := newProm(t, answer(vector(series("source", "cart", "ledger", "3", "source_cluster=Kubernetes", "destination_cluster=Kubernetes"),
		series("source", "cart", "ledger", "3", "source_cluster=Kubernetes", "destination_cluster=Kubernetes", "cluster=eu-2"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	if len(col.Observed) != 0 || !hasMessage(istioRow(t, col), fleet.LimitationMeshTelemetryUnread, "this Prometheus holds calls of several clusters; Pacto cannot tell which is this one") {
		t.Errorf("istio observed %+v row %+v", col.Observed, istioRow(t, col))
	}
	lp := newProm(t, answer(vector(lkSeries("2", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger"),
		lkSeries("2", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger", "cluster=eu-2"),
		lkSeries("2", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger", "cluster=eu-1"))))
	col = collectMesh(t, meshSource(linkerdMesh(nil), nil, map[string]*promStub{"linkerd-viz/prometheus": lp}, nil))
	if len(col.Observed) != 0 || !hasMessage(linkerdRow(t, col), fleet.LimitationMeshTelemetryUnread, "this Prometheus holds calls of several clusters; Pacto cannot tell which is this one") {
		t.Errorf("linkerd observed %+v row %+v", col.Observed, linkerdRow(t, col))
	}
}

// A control-plane label held in two namespaces lists neither Service as the
// mesh's control plane and says so.
func TestMeshAmbiguousControlPlaneListsNone(t *testing.T) {
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"), svcJSON("team-a", "istiod", "", "app=istiod")}, nil)
	row := istioRow(t, collectMesh(t, meshSource(f, nil, nil, nil)))
	for _, o := range row.Objects {
		if o.Name == "istiod" {
			t.Errorf("objects %+v name a control plane", row.Objects)
		}
	}
	if !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "the mesh's control plane could not be told apart from a copy in another namespace") {
		t.Errorf("row %+v", row)
	}
}

// Every mesh row carries the time of its real read, kept while it is served
// from memory.
func TestMeshRowsCarryReadTime(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	s := meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)
	now := meshNow
	s.now = func() time.Time { return now }
	collectMesh(t, s)
	now = now.Add(time.Minute)
	if row := istioRow(t, collectMesh(t, s)); row.AsOf == nil || !row.AsOf.Equal(meshNow) {
		t.Errorf("asOf %v", row.AsOf)
	}
	hubble, _, _ := readHubble(t, call(meshNow.Add(-59*time.Minute), cart(), ledger()))
	if hubble.AsOf == nil || !hubble.AsOf.Equal(meshNow) {
		t.Errorf("hubble asOf %v", hubble.AsOf)
	}
}

// Prometheus's own non-2xx answer relayed through the API server proxy
// reads as the direct path reads it; only the API server's RBAC denial says
// the user may not proxy.
func TestMeshProxyRelayedStatusIsPrometheusRefusal(t *testing.T) {
	for code, want := range map[int]string{422: "the Prometheus Pacto found refused the query (HTTP 422)", 403: "the Prometheus Pacto found refused the query (HTTP 403)"} {
		row := proxyRow(t, func(string, string, int32, string, url.Values) (io.ReadCloser, error) {
			return nil, apierrors.NewGenericServerResponse(code, "GET", schema.GroupResource{Resource: "services"}, "prom-main", `{"status":"error"}`, 0, true)
		})
		if !slices.Equal(messages(row), []string{want}) {
			t.Errorf("%d: %v", code, messages(row))
		}
	}
	if row := proxyRow(t, func(string, string, int32, string, url.Values) (io.ReadCloser, error) {
		return nil, &apierrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonUnknown}}
	}); !slices.Equal(messages(row), []string{"the Prometheus Pacto found is not reachable from here"}) {
		t.Errorf("no code: %v", messages(row))
	}
	if row := proxyRow(t, forbiddenProxy); !slices.Equal(messages(row), []string{"the Prometheus Pacto found is not reachable from here and you may not proxy to it"}) {
		t.Errorf("rbac: %v", messages(row))
	}
}

// Sources rebuilt for each build share one memo, so the mesh is read once
// per interval; a read its caller cancelled is never kept.
func TestMeshMemoSharedAcrossSources(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	memo := &MeshMemo{}
	now := meshNow
	build := func() *MeshSource {
		s := meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil).WithMemo(memo)
		s.now = func() time.Time { return now }
		return s
	}
	collectMesh(t, build())
	collectMesh(t, build())
	if p.count() != 1 {
		t.Errorf("queries %d; want one read for two builds", p.count())
	}
	now = now.Add(meshReadInterval)
	collectMesh(t, build())
	if p.count() != 2 {
		t.Errorf("queries %d after the interval", p.count())
	}
	cancelled := &MeshMemo{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := build().WithMemo(cancelled)
	if _, err := s.Collect(ctx); err != nil || cancelled.last != nil {
		t.Errorf("err %v kept %+v", err, cancelled.last)
	}
}

// nodeCall is a forwarded egress request at at seen on node.
func nodeCall(at time.Time, node string) []byte {
	return pb(1, pb(1, stamp(at), 2, uint64(verdictForwarded), 8, cart(), 9, ledger(), 11, node, 22, uint64(directionEgress), 26, pb(1, uint64(0))))
}

// A busy node whose buffer holds minutes is never hidden by a quiet node
// whose flows reach back an hour.
func TestHubbleShortestNodeBufferPartial(t *testing.T) {
	row, _, _ := readHubble(t, nodeCall(meshNow.Add(-59*time.Minute), "quiet"),
		nodeCall(meshNow.Add(-4*time.Minute), "busy"), nodeCall(meshNow.Add(-time.Minute), "busy"))
	if !hasMessage(row, fleet.LimitationMeshCoveragePartial, "Hubble Relay kept only the last 4 minutes of flows") {
		t.Errorf("row %+v", row)
	}
}

// A flow that does not say whether it replies is no call: counted, never an
// edge that may point backwards.
func TestHubbleUnknownReplyIsNoCall(t *testing.T) {
	at := meshNow.Add(-59 * time.Minute)
	row, col, _ := readHubble(t, pb(1, pb(1, stamp(at), 2, uint64(verdictForwarded), 8, ledger(), 9, cart(), 22, uint64(directionEgress))),
		call(at, cart(), ledger()))
	if len(col.Observed) != 1 || col.Observed[0].From != "k8s:shop/Deployment/cart" ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 flow had no request direction") {
		t.Errorf("observed %+v row %+v", col.Observed, row)
	}
}
