package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var meshNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// meshFake is a workload fake that may say it runs in the cluster.
type meshFake struct {
	workloadFake
	in bool
}

func (f *meshFake) InCluster() bool { return f.in }

// svcJSON is a Service list item; labels are k=v pairs. A selector is set
// unless kind is ExternalName or "selectorless".
func svcJSON(ns, name, kind string, labels ...string) string {
	l := map[string]string{}
	for _, kv := range labels {
		k, v, _ := strings.Cut(kv, "=")
		l[k] = v
	}
	spec := map[string]any{"type": "ClusterIP", "selector": map[string]string{"app": name}, "ports": []map[string]any{{"name": "web", "port": 9090}}}
	switch kind {
	case "ExternalName":
		spec = map[string]any{"type": "ExternalName"}
	case "selectorless":
		delete(spec, "selector")
	}
	b, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": name, "namespace": ns, "labels": l}, "spec": spec})
	return string(b)
}

// nsJSON is a Namespace list item; labels are k=v pairs.
func nsJSON(name string, labels ...string) string {
	return svcJSON("", name, "", labels...)
}

// meshCluster is a fake serving Istio discovery, the Services (each also
// listed in its own namespace) and the Namespaces given.
func meshClient(in bool, services []string, namespaces []string) *meshFake {
	f := &meshFake{in: in, workloadFake: workloadFake{versions: map[string]string{istioGroup: "v1"}, pages: map[string][2]string{}}}
	byNS := map[string][]string{}
	for _, s := range services {
		var item struct {
			Metadata struct{ Namespace string } `json:"metadata"`
		}
		_ = json.Unmarshal([]byte(s), &item)
		byNS[item.Metadata.Namespace] = append(byNS[item.Metadata.Namespace], s)
	}
	f.pages["services||"] = [2]string{strings.Join(services, ",")}
	for ns, items := range byNS {
		f.pages["services|"+ns+"|"] = [2]string{strings.Join(items, ",")}
	}
	f.pages["namespaces||"] = [2]string{strings.Join(namespaces, ",")}
	return f
}

// defaultMesh is Istio with its Prometheus in istio-system and one injected
// namespace, shop.
func defaultMesh(in bool) *meshFake {
	return meshClient(in,
		[]string{svcJSON("istio-system", "istiod", "", "app=istiod"), svcJSON("istio-system", "prom-main", "", "app.kubernetes.io/name=prometheus"),
			svcJSON("shop", "cart", ""), svcJSON("kube-system", "kube-dns", "")},
		[]string{nsJSON("istio-system"), nsJSON("shop", "istio-injection=enabled"), nsJSON("kube-system"), nsJSON("idle")})
}

// promStub is a Prometheus answering every query with handler, recording
// each request.
type promStub struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newProm(t *testing.T, handler http.HandlerFunc) *promStub {
	t.Helper()
	p := &promStub{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.reqs = append(p.reqs, r)
		p.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *promStub) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.reqs)
}

// answer serves body with status 200.
func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

// series is one Istio series from src to dst in shop, of reporter, valued v.
func series(reporter, src, dst, v string, extra ...string) string {
	m := map[string]string{"reporter": reporter, "source_workload": src, "source_workload_namespace": "shop",
		"destination_workload": dst, "destination_workload_namespace": "shop"}
	for _, kv := range extra {
		k, val, _ := strings.Cut(kv, "=")
		m[k] = val
	}
	b, _ := json.Marshal(map[string]any{"metric": m, "value": []any{float64(meshNow.Unix()), v}})
	return string(b)
}

func vector(items ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(items, ",") + `]}}`
}

// meshSource is a source over f whose Prometheus Services are the stubs by
// "namespace/name"; asked records the Services asked, in order.
func meshSource(f *meshFake, namespaces []string, stubs map[string]*promStub, asked *[]string) *MeshSource {
	s := NewMeshSource("c1-mesh", &fleet.Cluster{ID: "c1", Name: "eu"}, f, namespaces)
	s.now = func() time.Time { return meshNow }
	s.baseURL = func(ns, name string, port int32) string {
		if asked != nil {
			*asked = append(*asked, ns+"/"+name)
		}
		if p, ok := stubs[ns+"/"+name]; ok {
			return p.srv.URL
		}
		return "http://127.0.0.1:1"
	}
	return s
}

func collectMesh(t *testing.T, s *MeshSource) *fleet.Collection {
	t.Helper()
	col, err := s.Collect(context.Background())
	if err != nil || col == nil || !col.Advisory {
		t.Fatalf("collect = %+v, %v", col, err)
	}
	return col
}

// istioRow is the one row of col.
func istioRow(t *testing.T, col *fleet.Collection) fleet.Provider {
	t.Helper()
	if len(col.Providers) != 1 || col.Providers[0].ID != fleet.ProviderIstio {
		t.Fatalf("providers %+v", col.Providers)
	}
	return col.Providers[0]
}

func messages(p fleet.Provider) []string {
	var out []string
	for _, l := range p.Limitations {
		out = append(out, l.Message)
	}
	return out
}

func hasMessage(p fleet.Provider, code, msg string) bool {
	return slices.ContainsFunc(p.Limitations, func(l fleet.Limitation) bool { return l.Code == code && l.Message == msg && l.Source == "c1-mesh" })
}

func TestMeshNothingServedIsAvailableEmpty(t *testing.T) {
	f := defaultMesh(true)
	f.versions = map[string]string{}
	s := meshSource(f, nil, nil, nil)
	col := collectMesh(t, s)
	if col.State.Status != fleet.SourceAvailable || len(col.Providers) != 0 || len(col.Observed) != 0 || len(f.asked) != 0 || col.Cluster.ID != "c1" {
		t.Errorf("collection %+v asked %v", col, f.asked)
	}
	if s.ID() != "c1-mesh" || s.Kind() != fleet.SourceKindMesh || s.Cluster().ID != "c1" {
		t.Errorf("id %q kind %q", s.ID(), s.Kind())
	}
}

func TestMeshDiscoveryFailedIsUnavailable(t *testing.T) {
	f := defaultMesh(true)
	f.discErr = fmt.Errorf("dial tcp 10.0.0.1:443: connection refused")
	col := collectMesh(t, meshSource(f, nil, nil, nil))
	if col.State.Status != fleet.SourceUnavailable || col.State.Error.Code != "DISCOVERY_FAILED" || strings.Contains(col.State.Error.Message, "10.0.0.1") || len(col.Providers) != 0 {
		t.Errorf("collection %+v", col.State)
	}
}

func istioAnswer() string {
	return vector(
		series("source", "cart", "ledger", "10.4"),
		series("destination", "cart", "ledger", "12.6"),
		series("source", "cart", "payments", "0.3"),
		series("source", "ledger", "cart", "0"),
		series("source", "cart", "cart", "5"),
	)
}

func TestMeshIstioEdgesFromPrometheus(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	want := []fleet.ObservedEdge{
		{From: "k8s:shop//cart", To: "k8s:shop//ledger", Count: 13, FirstSeen: meshNow.Add(-time.Hour), LastSeen: meshNow},
		{From: "k8s:shop//cart", To: "k8s:shop//payments", Count: 1, FirstSeen: meshNow.Add(-time.Hour), LastSeen: meshNow},
	}
	if !slices.Equal(col.Observed, want) || !col.ObservedUndeclaredOnly {
		t.Errorf("observed %+v", col.Observed)
	}
	row := istioRow(t, col)
	if row.Status != fleet.SourceAvailable || row.Evidence != "serves networking.istio.io; calls read from Prometheus" || len(row.Limitations) != 0 ||
		!slices.Equal(row.UsedFor, []string{fleet.UseObservedEdges}) || row.Name != "Istio" {
		t.Errorf("row %+v", row)
	}
	if !slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Service", Namespace: "istio-system", Name: "prom-main"}) ||
		!slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Service", Namespace: "istio-system", Name: "istiod"}) {
		t.Errorf("objects %+v", row.Objects)
	}
	if col.State.Status != fleet.SourceAvailable || !col.State.ObservedAt.Equal(meshNow) || !col.State.LastSuccessfulSync.Equal(meshNow) {
		t.Errorf("state %+v", col.State)
	}
}

func TestMeshIstioQueryIsUnscopedAtAsOf(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	q, _ := url.ParseQuery(p.reqs[0].URL.RawQuery)
	if q.Get("time") != fmt.Sprint(meshNow.Unix()) || !strings.Contains(q.Get("query"), "increase(istio_requests_total[1h])") || strings.Contains(q.Get("query"), "=~") {
		t.Errorf("query %v", q)
	}
}

func TestMeshObservedUndeclaredOnly(t *testing.T) {
	for name, f := range map[string]*meshFake{"read": defaultMesh(true), "partial": defaultMesh(false)} {
		p := newProm(t, answer(istioAnswer()))
		if col := collectMesh(t, meshSource(f, nil, map[string]*promStub{"istio-system/prom-main": p}, nil)); !col.ObservedUndeclaredOnly {
			t.Errorf("%s: the mesh's silence would count against a declared call", name)
		}
	}
}

func TestMeshIstioNoPrometheusIsPartial(t *testing.T) {
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod")}, []string{nsJSON("istio-system")})
	col := collectMesh(t, meshSource(f, nil, nil, nil))
	row := istioRow(t, col)
	if row.Status != fleet.SourcePartial || row.Evidence != "serves networking.istio.io; no Prometheus found" || col.State.Status != fleet.SourcePartial ||
		!hasMessage(row, fleet.LimitationMeshTelemetryUnread, "no Prometheus found in the namespaces this installation reads") {
		t.Errorf("row %+v state %+v", row, col.State)
	}
}

func TestMeshIstioNoSeriesIsPartial(t *testing.T) {
	p := newProm(t, answer(vector()))
	row := istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)))
	if row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshTelemetryUnread,
		"the Prometheus Pacto found holds no istio_requests_total series for the last hour, so it does not record this mesh's calls") ||
		!slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Service", Namespace: "istio-system", Name: "prom-main"}) {
		t.Errorf("row %+v", row)
	}
}

func TestMeshIstioZeroIncreaseIsAvailableWithoutEdges(t *testing.T) {
	p := newProm(t, answer(vector(series("source", "cart", "ledger", "0"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	if row := istioRow(t, col); row.Status != fleet.SourceAvailable || len(col.Observed) != 0 || col.State.Status != fleet.SourceAvailable {
		t.Errorf("row %+v observed %+v", row, col.Observed)
	}
}

func TestMeshIstioPrometheusRefusedIsPartial(t *testing.T) {
	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"401":     {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }, "the Prometheus Pacto found refused the query (HTTP 401)"},
		"503":     {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }, "the Prometheus Pacto found refused the query (HTTP 503)"},
		"garbage": {answer("<html>"), "the Prometheus Pacto found answered with an unreadable body"},
		"matrix":  {answer(`{"status":"success","data":{"resultType":"matrix","result":[]}}`), "the Prometheus Pacto found answered with an unreadable body"},
	} {
		p := newProm(t, tc.handler)
		row := istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)))
		if row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshTelemetryUnread, tc.want) ||
			row.Evidence != "serves networking.istio.io; Prometheus found, calls not read" {
			t.Errorf("%s: row %+v", name, row)
		}
	}
	unreachable := istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, nil, nil)))
	if !hasMessage(unreachable, fleet.LimitationMeshTelemetryUnread, "the Prometheus Pacto found is not reachable from here") {
		t.Errorf("unreachable %+v", unreachable)
	}
	hang := newProm(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	s := meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": hang}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	col, _ := s.Collect(ctx)
	if row := istioRow(t, col); !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "the Prometheus Pacto found timed out") {
		t.Errorf("timeout %+v", row)
	}
}

// coverageRow is the row of a mesh whose extra namespaces each hold a
// Service.
func coverageRow(t *testing.T, extra ...string) fleet.Provider {
	t.Helper()
	svcs := []string{svcJSON("istio-system", "istiod", "", "app=istiod"), svcJSON("istio-system", "prom-main", "", "app.kubernetes.io/name=prometheus"),
		svcJSON("kube-system", "kube-dns", "")}
	nss := []string{nsJSON("istio-system"), nsJSON("kube-system"), nsJSON("idle")}
	for _, ns := range extra {
		name, labels, _ := strings.Cut(ns, ":")
		svcs = append(svcs, svcJSON(name, "app", ""))
		if labels == "" {
			nss = append(nss, nsJSON(name))
			continue
		}
		nss = append(nss, nsJSON(name, labels))
	}
	p := newProm(t, answer(istioAnswer()))
	return istioRow(t, collectMesh(t, meshSource(meshClient(true, svcs, nss), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)))
}

func TestMeshIstioUncoveredNamespacesPartial(t *testing.T) {
	row := coverageRow(t, "a", "b", "c", "shop:istio-injection=enabled")
	if row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshCoveragePartial, "3 namespaces without sidecars") {
		t.Errorf("row %+v", row)
	}
	var nss []string
	for _, o := range row.Objects {
		if o.Kind == "Namespace" {
			nss = append(nss, o.Name)
		}
	}
	if !slices.Equal(nss, []string{"a", "b", "c"}) {
		t.Errorf("uncovered %v", nss)
	}
	if one := coverageRow(t, "a"); !hasMessage(one, fleet.LimitationMeshCoveragePartial, "1 namespace without sidecars") {
		t.Errorf("one %+v", one)
	}
}

func TestMeshIstioAmbientNamespaceCovered(t *testing.T) {
	if row := coverageRow(t, "a:istio.io/dataplane-mode=ambient"); row.Status != fleet.SourceAvailable {
		t.Errorf("row %+v", row)
	}
}

func TestMeshIstioRevisionLabelCovered(t *testing.T) {
	if row := coverageRow(t, "a:istio.io/rev=1-22"); row.Status != fleet.SourceAvailable {
		t.Errorf("row %+v", row)
	}
}

func TestMeshNamespacesForbiddenCoverageUnknown(t *testing.T) {
	f := defaultMesh(true)
	f.errs = map[string]error{"namespaces|": apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", nil)}
	p := newProm(t, answer(istioAnswer()))
	row := istioRow(t, collectMesh(t, meshSource(f, nil, map[string]*promStub{"istio-system/prom-main": p}, nil)))
	if row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshCoveragePartial, "sidecar coverage is unknown because namespaces could not be read") {
		t.Errorf("row %+v", row)
	}
}

func TestMeshServicesForbiddenIsPartial(t *testing.T) {
	f := defaultMesh(true)
	f.errs = map[string]error{"services|": apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", nil)}
	row := istioRow(t, collectMesh(t, meshSource(f, nil, nil, nil)))
	if row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshTelemetryUnread,
		"Services could not be read in every namespace, so Pacto may miss the Prometheus that records this mesh") {
		t.Errorf("row %+v", row)
	}
}

func TestMeshIstioUnknownWorkloadUnattributed(t *testing.T) {
	p := newProm(t, answer(vector(series("source", "unknown", "ledger", "4"), series("source", "cart", "", "4"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	row := istioRow(t, col)
	if len(col.Observed) != 0 || row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshCallsUnattributed, "2 call series had an end Pacto could not name") {
		t.Errorf("row %+v observed %+v", row, col.Observed)
	}
}

func TestMeshPrometheusCandidateOrder(t *testing.T) {
	empty, full := newProm(t, answer(vector())), newProm(t, answer(istioAnswer()))
	svcs := []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("istio-system", "prom-app", "", "app=prometheus"),
		svcJSON("monitoring", "prometheus-operated", "", "operated-prometheus=true"),
		svcJSON("istio-system", "prom-name", "", "app.kubernetes.io/name=prometheus"),
		svcJSON("istio-system", "prom-app2", "", "app=prometheus")}
	f := meshClient(true, svcs, nil)
	var asked []string
	col := collectMesh(t, meshSource(f, nil, map[string]*promStub{"monitoring/prometheus-operated": empty, "istio-system/prom-name": full}, &asked))
	if !slices.Equal(asked, []string{"monitoring/prometheus-operated", "istio-system/prom-name"}) || len(col.Observed) != 2 {
		t.Errorf("asked %v observed %+v", asked, col.Observed)
	}
	if !slices.Contains(istioRow(t, col).Objects, fleet.ProviderObject{Kind: "Service", Namespace: "istio-system", Name: "prom-name"}) {
		t.Errorf("objects %+v", istioRow(t, col).Objects)
	}
	asked = nil
	collectMesh(t, meshSource(f, nil, map[string]*promStub{"monitoring/prometheus-operated": empty, "istio-system/prom-name": empty, "istio-system/prom-app": empty, "istio-system/prom-app2": empty}, &asked))
	if len(asked) != maxPromCandidates {
		t.Errorf("asked %v; never more than %d", asked, maxPromCandidates)
	}
}

func TestServicePort(t *testing.T) {
	for want, ports := range map[int32][]svcPort{
		8080: {{Name: "grpc", Port: 9091}, {Name: "http", Port: 8080}},
		9093: {{Name: "http", Port: 9092}, {Name: "web", Port: 9093}},
		9090: {{Name: "a", Port: 1}, {Name: "b", Port: 9090}},
		7000: {{Name: "a", Port: 7000}},
	} {
		if got := servicePort(svcInfo{Ports: ports}); got != want {
			t.Errorf("port of %v = %d, want %d", ports, got, want)
		}
	}
	if servicePort(svcInfo{}) != 9090 {
		t.Error("no port")
	}
}

func TestMeshPrometheusBodyBound(t *testing.T) {
	big := strings.Repeat(" ", maxPromBody+1)
	p := newProm(t, answer(big))
	row := istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)))
	if !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "the Prometheus Pacto found answered more than 16 MiB") {
		t.Errorf("row %+v", messages(row))
	}
	if _, err := readPromBody(failingReader{}); err == nil {
		t.Error("a failing body read")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, fmt.Errorf("reset") }

func TestMeshSeriesCapIsPartial(t *testing.T) {
	items := make([]string, 0, maxMeshSeries+1)
	for i := range maxMeshSeries + 1 {
		items = append(items, series("source", fmt.Sprintf("w%d", i), "ledger", fmt.Sprint(i+1)))
	}
	p := newProm(t, answer(vector(items...)))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	row := istioRow(t, col)
	if len(col.Observed) != maxMeshSeries || col.Observed[0].From != fmt.Sprintf("k8s:shop//w%d", maxMeshSeries) ||
		!hasMessage(row, fleet.LimitationMeshTelemetryUnread, "only the 5000 busiest call pairs were read") {
		t.Errorf("observed %d first %+v lims %v", len(col.Observed), col.Observed[0], messages(row))
	}
}

func TestMeshScopedNamespacesOnly(t *testing.T) {
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"), svcJSON("monitoring", "prom-main", "", "app.kubernetes.io/name=prometheus"),
		svcJSON("shop", "cart", "")}, []string{nsJSON("istio-system"), nsJSON("shop", "istio-injection=enabled"), nsJSON("monitoring")})
	row := istioRow(t, collectMesh(t, meshSource(f, []string{"shop", "istio-system", ""}, nil, nil)))
	if !slices.Contains(f.asked, "services|shop|") || slices.Contains(f.asked, "services||") || slices.Contains(f.asked, "services|monitoring|") {
		t.Errorf("asked %v", f.asked)
	}
	if !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "no Prometheus found in the namespaces this installation reads") ||
		slices.ContainsFunc(row.Objects, func(o fleet.ProviderObject) bool { return o.Namespace == "monitoring" }) {
		t.Errorf("row %+v", row)
	}
}

func TestMeshScopedDropsOutOfScopeEdges(t *testing.T) {
	p := newProm(t, answer(vector(series("source", "cart", "ledger", "3"),
		series("source", "cart", "secret-svc", "3", "destination_workload_namespace=hidden-team"))))
	col := collectMesh(t, meshSource(defaultMesh(true), []string{"shop", "istio-system"}, map[string]*promStub{"istio-system/prom-main": p}, nil))
	q := p.reqs[0].URL.Query().Get("query")
	if !strings.Contains(q, `source_workload_namespace=~"istio-system|shop"`) || !strings.Contains(q, `destination_workload_namespace=~"istio-system|shop"`) {
		t.Errorf("query %s", q)
	}
	row := istioRow(t, col)
	if len(col.Observed) != 1 || !hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 call series reach namespaces this installation does not read") {
		t.Errorf("observed %+v lims %v", col.Observed, messages(row))
	}
	b, _ := json.Marshal(col)
	if strings.Contains(string(b), "hidden-team") || strings.Contains(string(b), "secret-svc") {
		t.Errorf("an out-of-scope name reached the collection: %s", b)
	}
}

func TestMeshHostilePrometheusCandidateIgnored(t *testing.T) {
	hostile := []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("a-team", "prom-a", "", "app.kubernetes.io/name=prometheus", "app=prometheus"), svcJSON("b-team", "prom-b", "", "app.kubernetes.io/name=prometheus")}
	var asked []string
	row := istioRow(t, collectMesh(t, meshSource(meshClient(true, hostile, nil), nil, nil, &asked)))
	if len(asked) != 0 || !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "several Prometheus servers could record this mesh; Pacto cannot tell which one does") ||
		row.Evidence != "serves networking.istio.io; no Prometheus found" {
		t.Errorf("asked %v row %+v", asked, row)
	}
	p := newProm(t, answer(istioAnswer()))
	row = istioRow(t, collectMesh(t, meshSource(meshClient(true, append(hostile, svcJSON("istio-system", "prom-main", "", "app.kubernetes.io/name=prometheus")), nil),
		nil, map[string]*promStub{"istio-system/prom-main": p}, &asked)))
	if !slices.Equal(asked, []string{"istio-system/prom-main"}) || slices.Contains(messages(row), "several Prometheus servers could record this mesh; Pacto cannot tell which one does") {
		t.Errorf("asked %v row %+v", asked, row)
	}
}

func TestMeshExternalNameAndSelectorlessIgnored(t *testing.T) {
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("monitoring", "prom-ext", "ExternalName", "app=prometheus"), svcJSON("istio-system", "prom-manual", "selectorless", "operated-prometheus=true")}, nil)
	var asked []string
	row := istioRow(t, collectMesh(t, meshSource(f, nil, nil, &asked)))
	if len(asked) != 0 || !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "no Prometheus found in the namespaces this installation reads") {
		t.Errorf("asked %v row %+v", asked, row)
	}
}

func TestMeshIstioCrossClusterSeriesNotAttributed(t *testing.T) {
	p := newProm(t, answer(vector(series("source", "cart", "ledger", "3", "source_cluster=c1", "destination_cluster=c1"),
		series("source", "cart", "remote", "3", "source_cluster=c1", "destination_cluster=c2"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	row := istioRow(t, col)
	if len(col.Observed) != 1 || col.Observed[0].To != "k8s:shop//ledger" || !hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 cross-cluster call was not attributed") {
		t.Errorf("observed %+v lims %v", col.Observed, messages(row))
	}
}

func TestMeshIstioMultiClusterPrometheusPartial(t *testing.T) {
	p := newProm(t, answer(vector(series("source", "cart", "ledger", "3", "source_cluster=c1", "destination_cluster=c1"),
		series("source", "cart", "ledger", "3", "source_cluster=c2", "destination_cluster=c2"))))
	col := collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	row := istioRow(t, col)
	if len(col.Observed) != 0 || !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "this Prometheus holds calls of several clusters; Pacto cannot tell which is this one") {
		t.Errorf("observed %+v lims %v", col.Observed, messages(row))
	}
}

func TestMeshCollectHasOneDeadline(t *testing.T) {
	hang := func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	stubs := map[string]*promStub{"monitoring/prom-1": newProm(t, hang), "monitoring/prom-2": newProm(t, hang), "monitoring/prom-3": newProm(t, hang)}
	f := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("monitoring", "prom-1", "", "operated-prometheus=true"), svcJSON("monitoring", "prom-2", "", "app.kubernetes.io/name=prometheus"),
		svcJSON("monitoring", "prom-3", "", "app=prometheus")}, nil)
	var asked []string
	s := meshSource(f, nil, stubs, &asked)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	col, _ := s.Collect(ctx)
	if time.Since(start) > 5*time.Second || !slices.Equal(asked, []string{"monitoring/prom-1"}) {
		t.Errorf("took %v asking %v", time.Since(start), asked)
	}
	if row := istioRow(t, col); !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "the Prometheus Pacto found timed out") {
		t.Errorf("row %+v", row)
	}
	if meshBudget != 15*time.Second || meshTimeout != 10*time.Second || s.http.Timeout != meshTimeout {
		t.Errorf("budget %v timeout %v client %v", meshBudget, meshTimeout, s.http.Timeout)
	}
}

func TestMeshReadsAtMostOncePerInterval(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	s := meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)
	now := meshNow
	s.now = func() time.Time { return now }
	first := collectMesh(t, s)
	first.Providers[0].Objects[0].Name = "mutated"
	now = now.Add(meshReadInterval - time.Second)
	second := collectMesh(t, s)
	if p.count() != 1 || !second.State.ObservedAt.Equal(meshNow) || second.Providers[0].Objects[0].Name == "mutated" {
		t.Errorf("queries %d observedAt %v objects %+v", p.count(), second.State.ObservedAt, second.Providers[0].Objects)
	}
	now = meshNow.Add(meshReadInterval)
	if third := collectMesh(t, s); p.count() != 2 || !third.State.ObservedAt.Equal(now) {
		t.Errorf("queries %d observedAt %v", p.count(), third.State.ObservedAt)
	}
}

func TestMeshOutOfClusterUsesProxyOnly(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	outside := NewMeshSource("c1-mesh", nil, &defaultMesh(true).workloadFake, nil)
	for name, s := range map[string]*MeshSource{
		"false":     meshSource(defaultMesh(false), nil, map[string]*promStub{"istio-system/prom-main": p}, nil),
		"no method": outside,
	} {
		if name == "no method" {
			s.baseURL = func(string, string, int32) string { return p.srv.URL }
		}
		row := istioRow(t, collectMesh(t, s))
		if p.count() != 0 || !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "the Prometheus Pacto found is not reachable from here") {
			t.Errorf("%s: queries %d row %+v", name, p.count(), row)
		}
	}
}

func TestMeshDirectIgnoresEnvProxy(t *testing.T) {
	proxy := newProm(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) })
	t.Setenv("HTTP_PROXY", proxy.srv.URL)
	t.Setenv("http_proxy", proxy.srv.URL)
	p := newProm(t, answer(istioAnswer()))
	s := meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil)
	if col := collectMesh(t, s); len(col.Observed) != 2 || proxy.count() != 0 || p.count() != 1 {
		t.Errorf("observed %+v proxy %d prometheus %d", col.Observed, proxy.count(), p.count())
	}
	if tr, ok := s.http.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Error("the direct client may use an environment proxy")
	}
	if got := NewMeshSource("x", nil, defaultMesh(true), nil).baseURL("monitoring", "prom", 9090); got != "http://prom.monitoring.svc:9090" {
		t.Errorf("base URL %s", got)
	}
}

// failureRows are the rows of every failure a Prometheus read can have.
func failureRows(t *testing.T) []fleet.Provider {
	t.Helper()
	var rows []fleet.Provider
	for _, h := range []http.HandlerFunc{answer(vector()), func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }, answer("{")} {
		p := newProm(t, h)
		rows = append(rows, istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))))
	}
	rows = append(rows, istioRow(t, collectMesh(t, meshSource(defaultMesh(true), nil, nil, nil))))
	hang := newProm(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	col, _ := meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": hang}, nil).Collect(ctx)
	rows = append(rows, istioRow(t, col))
	hostile := meshClient(true, []string{svcJSON("istio-system", "istiod", "", "app=istiod"),
		svcJSON("a-team", "prom-a", "", "app=prometheus"), svcJSON("b-team", "prom-b", "", "app=prometheus")}, []string{nsJSON("a-team"), nsJSON("b-team")})
	return append(rows, istioRow(t, collectMesh(t, meshSource(hostile, nil, nil, nil))))
}

func TestMeshRowNamesNoNamespace(t *testing.T) {
	for _, row := range failureRows(t) {
		for _, text := range append(messages(row), row.Evidence) {
			for _, name := range []string{"istio-system", "prom-main", "prom-a", "prom-b", "a-team", "b-team", "shop", "istiod", "idle"} {
				if strings.Contains(text, name) {
					t.Errorf("%q names %q", text, name)
				}
			}
		}
	}
}

func TestMeshErrorsAreFixedTexts(t *testing.T) {
	for _, row := range failureRows(t) {
		for _, text := range messages(row) {
			for _, raw := range []string{"http", "127.0.0.1", ":", "dial", "refused the connection", "connection", "deadline", "EOF", ".svc"} {
				if strings.Contains(strings.ToLower(text), raw) && !strings.HasPrefix(text, "the Prometheus Pacto found refused the query (HTTP") {
					t.Errorf("%q holds %q", text, raw)
				}
			}
		}
	}
}

func TestMeshListsEveryPage(t *testing.T) {
	f := defaultMesh(true)
	f.pages["services||"] = [2]string{svcJSON("istio-system", "istiod", "", "app=istiod"), "next"}
	f.pages["services||next"] = [2]string{svcJSON("istio-system", "prom-main", "", "app.kubernetes.io/name=prometheus")}
	p := newProm(t, answer(istioAnswer()))
	if col := collectMesh(t, meshSource(f, nil, map[string]*promStub{"istio-system/prom-main": p}, nil)); len(col.Observed) != 2 {
		t.Errorf("observed %+v asked %v", col.Observed, f.asked)
	}
}

func TestMeshReadsOnlyDiscoveryServicesNamespaces(t *testing.T) {
	f := defaultMesh(true)
	collectMesh(t, meshSource(f, nil, nil, nil))
	for gvr := range f.gvrs {
		if gvr != servicesGVR && gvr != namespacesGVR {
			t.Errorf("listed %v", gvr)
		}
	}
	if len(f.gvrs) != 2 || !slices.Equal(f.discovered, []string{istioGroup, "linkerd.io", "policy.linkerd.io", "cilium.io"}) {
		t.Errorf("listed %v discovered %v", f.gvrs, f.discovered)
	}
}

func TestMeshPrometheusOnlyQueries(t *testing.T) {
	p := newProm(t, answer(istioAnswer()))
	collectMesh(t, meshSource(defaultMesh(true), nil, map[string]*promStub{"istio-system/prom-main": p}, nil))
	for _, r := range p.reqs {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/query" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
	}
}

func TestMeshBadBaseURLIsUnreachable(t *testing.T) {
	s := meshSource(defaultMesh(true), nil, nil, nil)
	s.baseURL = func(string, string, int32) string { return "http://bad host" }
	if row := istioRow(t, collectMesh(t, s)); !hasMessage(row, fleet.LimitationMeshTelemetryUnread, "the Prometheus Pacto found is not reachable from here") {
		t.Errorf("row %+v", row)
	}
}

func TestPromSeriesValue(t *testing.T) {
	for in, want := range map[string]float64{"2.5": 2.5, "NaN": 0, "x": 0} {
		if got := (promSeries{Value: [2]any{0.0, in}}).value(); got != want {
			t.Errorf("value(%q) = %v", in, got)
		}
	}
	if (promStatusError{503}).Error() != "prometheus answered 503" {
		t.Error("status error text")
	}
	if edges, _ := foldCalls([]meshCall{{from: "a", to: "b", count: 1e300}}, meshNow); edges[0].Count != 1e15 {
		t.Errorf("an infinite count %+v", edges)
	}
}

// nsAnnotated is a Namespace list item carrying one k=v annotation.
func nsAnnotated(name, annotation string) string {
	k, v, _ := strings.Cut(annotation, "=")
	b, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": name, "annotations": map[string]string{k: v}}})
	return string(b)
}

// lkSeries is one Linkerd outbound series; labels are k=v pairs.
func lkSeries(v string, labels ...string) string {
	m := map[string]string{}
	for _, kv := range labels {
		k, val, _ := strings.Cut(kv, "=")
		m[k] = val
	}
	b, _ := json.Marshal(map[string]any{"metric": m, "value": []any{float64(meshNow.Unix()), v}})
	return string(b)
}

// linkerdMesh serves only Linkerd: its control plane in linkerd, viz with its
// Prometheus in linkerd-viz and one injected namespace, shop.
func linkerdMesh(extraSvcs []string, extraNS ...string) *meshFake {
	f := meshClient(true, append([]string{
		svcJSON("linkerd", "linkerd-destination", "", "linkerd.io/control-plane-component=destination"),
		svcJSON("linkerd-viz", "web", "", "linkerd.io/extension=viz"),
		svcJSON("linkerd-viz", "prometheus", "", "linkerd.io/extension=viz", "component=prometheus"),
		svcJSON("shop", "cart", "")}, extraSvcs...),
		append([]string{nsJSON("linkerd"), nsJSON("linkerd-viz"), nsAnnotated("shop", "linkerd.io/inject=enabled")}, extraNS...))
	f.versions = map[string]string{"policy.linkerd.io": "v1beta3", "linkerd.io": "v1alpha2"}
	return f
}

// linkerdRow is the one row of col, the Linkerd one.
func linkerdRow(t *testing.T, col *fleet.Collection) fleet.Provider {
	t.Helper()
	if len(col.Providers) != 1 || col.Providers[0].ID != fleet.ProviderLinkerd {
		t.Fatalf("providers %+v", col.Providers)
	}
	return col.Providers[0]
}

func TestMeshLinkerdEdgesByKind(t *testing.T) {
	p := newProm(t, answer(vector(
		lkSeries("7.6", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_statefulset=ledger"),
		lkSeries("2", "namespace=shop", "daemonset=agent", "dst_namespace=shop", "dst_deployment=cart"))))
	col := collectMesh(t, meshSource(linkerdMesh(nil), nil, map[string]*promStub{"linkerd-viz/prometheus": p}, nil))
	want := []fleet.ObservedEdge{
		{From: "k8s:shop/Deployment/cart", To: "k8s:shop/StatefulSet/ledger", Count: 8, FirstSeen: meshNow.Add(-time.Hour), LastSeen: meshNow},
		{From: "k8s:shop/DaemonSet/agent", To: "k8s:shop/Deployment/cart", Count: 2, FirstSeen: meshNow.Add(-time.Hour), LastSeen: meshNow},
	}
	if !slices.Equal(col.Observed, want) || !col.ObservedUndeclaredOnly {
		t.Errorf("observed %+v", col.Observed)
	}
	for _, e := range col.Observed {
		for _, end := range []string{e.From, e.To} {
			if _, _, kind, _, ok := fleet.ParseK8sCaller(end); !ok || kind == "" {
				t.Errorf("end %q has no kind", end)
			}
		}
	}
	checkLinkerdRead(t, linkerdRow(t, col), p)
}

// checkLinkerdRead checks the row and the query of a full Linkerd read.
func checkLinkerdRead(t *testing.T, row fleet.Provider, p *promStub) {
	t.Helper()
	if row.Status != fleet.SourceAvailable || row.Name != "Linkerd" || row.Evidence != "serves policy.linkerd.io; calls read from Prometheus" ||
		!slices.Equal(row.UsedFor, []string{fleet.UseObservedEdges}) || len(row.Limitations) != 0 {
		t.Errorf("row %+v", row)
	}
	if !slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Service", Namespace: "linkerd", Name: "linkerd-destination"}) ||
		!slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Service", Namespace: "linkerd-viz", Name: "prometheus"}) ||
		slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Service", Namespace: "linkerd-viz", Name: "web"}) {
		t.Errorf("objects %+v", row.Objects)
	}
	q, _ := url.ParseQuery(p.reqs[0].URL.RawQuery)
	if !strings.Contains(q.Get("query"), `increase(response_total{direction="outbound"}[1h])`) || !strings.Contains(q.Get("query"), "target_cluster_name") {
		t.Errorf("query %q", q.Get("query"))
	}
}

func TestMeshLinkerdOnlyLinkerdGroupServed(t *testing.T) {
	f := linkerdMesh(nil)
	f.versions = map[string]string{"linkerd.io": "v1alpha2"}
	if row := linkerdRow(t, collectMesh(t, meshSource(f, nil, nil, nil))); row.Evidence != "serves linkerd.io; Prometheus found, calls not read" {
		t.Errorf("row %+v", row)
	}
}

func TestMeshLinkerdJobCallerUnattributed(t *testing.T) {
	p := newProm(t, answer(vector(
		lkSeries("4", "namespace=shop", "k8s_job=migrate", "dst_namespace=shop", "dst_deployment=cart"),
		lkSeries("4", "namespace=shop", "deployment=cart", "dst_namespace=shop"))))
	col := collectMesh(t, meshSource(linkerdMesh(nil), nil, map[string]*promStub{"linkerd-viz/prometheus": p}, nil))
	row := linkerdRow(t, col)
	if len(col.Observed) != 0 || row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshCallsUnattributed, "2 call series had an end Pacto could not name") {
		t.Errorf("row %+v observed %+v", row, col.Observed)
	}
}

func TestMeshLinkerdInjectAnnotationCovers(t *testing.T) {
	p := newProm(t, answer(vector(lkSeries("1", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger"))))
	f := linkerdMesh([]string{svcJSON("a", "app", ""), svcJSON("b", "app", "")},
		nsAnnotated("a", "linkerd.io/inject=enabled"), nsJSON("b", "istio-injection=enabled"))
	row := linkerdRow(t, collectMesh(t, meshSource(f, nil, map[string]*promStub{"linkerd-viz/prometheus": p}, nil)))
	if row.Status != fleet.SourcePartial || !hasMessage(row, fleet.LimitationMeshCoveragePartial, "1 namespace without sidecars") ||
		!slices.Contains(row.Objects, fleet.ProviderObject{Kind: "Namespace", Namespace: "b", Name: "b"}) || len(row.Limitations) != 1 {
		t.Errorf("row %+v", row)
	}
}

func TestMeshLinkerdPrefersVizPrometheus(t *testing.T) {
	viz := newProm(t, answer(vector(lkSeries("1", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger"))))
	operated := newProm(t, answer(vector(lkSeries("1", "namespace=shop", "deployment=x", "dst_namespace=shop", "dst_deployment=y"))))
	f := linkerdMesh([]string{svcJSON("linkerd", "prometheus-operated", "", "operated-prometheus=true")})
	var asked []string
	col := collectMesh(t, meshSource(f, nil, map[string]*promStub{"linkerd-viz/prometheus": viz, "linkerd/prometheus-operated": operated}, &asked))
	if !slices.Equal(asked, []string{"linkerd-viz/prometheus"}) || len(col.Observed) != 1 || col.Observed[0].To != "k8s:shop/Deployment/ledger" {
		t.Errorf("asked %v observed %+v", asked, col.Observed)
	}
}

func TestMeshIstioAndLinkerdTwoRows(t *testing.T) {
	f := linkerdMesh([]string{svcJSON("istio-system", "istiod", "", "app=istiod"), svcJSON("istio-system", "prom-main", "", "app.kubernetes.io/name=prometheus")},
		nsJSON("istio-system"))
	f.versions[istioGroup] = "v1"
	byQuery := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "istio_requests_total") {
			_, _ = w.Write([]byte(istioAnswer()))
			return
		}
		_, _ = w.Write([]byte(vector(lkSeries("3", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger"))))
	}
	istio, lk := newProm(t, byQuery), newProm(t, byQuery)
	col := collectMesh(t, meshSource(f, nil, map[string]*promStub{"istio-system/prom-main": istio, "linkerd-viz/prometheus": lk}, nil))
	if len(col.Providers) != 2 || col.Providers[0].ID != fleet.ProviderIstio || col.Providers[1].ID != fleet.ProviderLinkerd {
		t.Fatalf("providers %+v", col.Providers)
	}
	// shop is injected by Linkerd only, so Istio reads it uncovered.
	if col.Providers[0].Status != fleet.SourcePartial || col.Providers[1].Status != fleet.SourceAvailable || col.State.Status != fleet.SourcePartial {
		t.Errorf("rows %+v state %+v", col.Providers, col.State)
	}
	if len(col.Observed) != 3 || !slices.ContainsFunc(col.Observed, func(e fleet.ObservedEdge) bool { return e.From == "k8s:shop/Deployment/cart" }) ||
		!slices.ContainsFunc(col.Observed, func(e fleet.ObservedEdge) bool { return e.From == "k8s:shop//cart" }) {
		t.Errorf("observed %+v", col.Observed)
	}
	if !slices.Equal(f.discovered, []string{istioGroup, "linkerd.io", "policy.linkerd.io", "cilium.io"}) {
		t.Errorf("discovered %v", f.discovered)
	}
}

func TestMeshLinkerdScopedAndCrossCluster(t *testing.T) {
	p := newProm(t, answer(vector(
		lkSeries("3", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger"),
		lkSeries("3", "namespace=shop", "deployment=cart", "dst_namespace=shop", "dst_deployment=ledger-west", "target_cluster_name=west"),
		lkSeries("3", "namespace=shop", "deployment=cart", "dst_namespace=hidden", "dst_deployment=secret"))))
	col := collectMesh(t, meshSource(linkerdMesh(nil), []string{"shop", "linkerd-viz"}, map[string]*promStub{"linkerd-viz/prometheus": p}, nil))
	row := linkerdRow(t, col)
	if len(col.Observed) != 1 || col.Observed[0].To != "k8s:shop/Deployment/ledger" ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 cross-cluster call was not attributed") ||
		!hasMessage(row, fleet.LimitationMeshCallsUnattributed, "1 call series reach namespaces this installation does not read") {
		t.Errorf("observed %+v lims %v", col.Observed, messages(row))
	}
	for _, text := range append(messages(row), row.Evidence) {
		if strings.Contains(text, "hidden") || strings.Contains(text, "west") {
			t.Errorf("%q names what is out of scope", text)
		}
	}
	q := p.reqs[0].URL.Query().Get("query")
	if !strings.Contains(q, `{direction="outbound",namespace=~"linkerd-viz|shop",dst_namespace=~"linkerd-viz|shop"}`) {
		t.Errorf("query %q", q)
	}
}
