package detect

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func traceEntry(t *testing.T, es []Entry, kind string) Entry {
	t.Helper()
	i := slices.IndexFunc(es, func(e Entry) bool { return e.Kind == kind })
	if i < 0 {
		t.Fatalf("no %s entry in %+v", kind, es)
	}
	return es[i]
}

func prom(ns, name string, labels map[string]string, ports ...ServicePort) Service {
	return Service{Namespace: ns, Name: name, Labels: labels, HasSelector: true, Ports: ports}
}

var operated = map[string]string{"operated-prometheus": "true"}

func TestTracesFindsCollectorFromImages(t *testing.T) {
	for _, img := range []string{
		"otel/opentelemetry-collector:0.98.0",
		"otel/opentelemetry-collector-contrib:0.98.0",
		"ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-k8s:0.98.0",
	} {
		es := Traces([]Workload{{ID: "c", Namespace: "obs", Images: []string{img}}},
			[]Service{{Namespace: "obs", Name: "otel", Ports: []ServicePort{{Port: 4318}}}, {Namespace: "obs", Name: "other", Ports: []ServicePort{{Port: 80}}}}, "pacto-system")
		e := traceEntry(t, es, "otel-collector")
		if e.Value != "OpenTelemetry Collector" || e.Completeness != Complete || e.Evidence != "running in 1 workload" ||
			!slices.Equal(e.Namespaces, []string{"obs"}) || !reflect.DeepEqual(e.Objects, []Object{{Kind: "Service", Namespace: "obs", Name: "otel"}}) {
			t.Errorf("%s: %+v", img, e)
		}
	}
}

func TestTracesFindsTempo(t *testing.T) {
	es := Traces([]Workload{{ID: "a", Namespace: "obs", Images: []string{"grafana/tempo:2.6"}}, {ID: "b", Namespace: "obs2", Images: []string{"docker.io/grafana/tempo:2.6"}}}, nil, "p")
	if e := traceEntry(t, es, "tempo"); e.Value != "Tempo" || e.Evidence != "running in 2 workloads" || e.Endpoint != "" || e.Completeness != Complete {
		t.Errorf("tempo = %+v", e)
	}
}

func TestTracesTempoCarriesPrometheusEndpoint(t *testing.T) {
	es := Traces([]Workload{{ID: "a", Namespace: "obs", Images: []string{"grafana/tempo:2.6"}}},
		[]Service{prom("monitoring", "prometheus-operated", operated, ServicePort{Name: "web", Port: 9091})}, "p")
	e := traceEntry(t, es, "tempo")
	if e.Endpoint != "http://prometheus-operated.monitoring.svc:9091" || CheckEndpoint(e.Endpoint) != nil ||
		!slices.Contains(e.Objects, Object{Kind: "Service", Namespace: "monitoring", Name: "prometheus-operated"}) {
		t.Errorf("tempo = %+v", e)
	}
}

func TestPrometheusCandidatesOrderAndTrust(t *testing.T) {
	app := map[string]string{"app": "prometheus"}
	name := map[string]string{"app.kubernetes.io/name": "prometheus"}
	viz := map[string]string{"linkerd.io/extension": "viz", "component": "prometheus"}
	names := func(cs []Service) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Namespace+"/"+c.Name)
		}
		return out
	}
	for _, tc := range []struct {
		name      string
		svcs      []Service
		trusted   []string
		viz       bool
		want      []string
		several   bool
		wantPorts []int32
	}{
		{name: "label order then namespace and name", svcs: []Service{prom("m", "b", app), prom("m", "a", name), prom("m", "c", operated)}, want: []string{"m/c", "m/a", "m/b"}},
		{name: "viz only when preferred", svcs: []Service{prom("linkerd-viz", "p", viz), prom("m", "x", app)}, trusted: []string{"linkerd-viz"}, viz: true, want: []string{"linkerd-viz/p"}},
		{name: "viz ignored otherwise", svcs: []Service{prom("linkerd-viz", "p", viz), prom("m", "x", app)}, want: []string{"m/x"}},
		{name: "ExternalName and selectorless never", svcs: []Service{{Namespace: "m", Name: "ext", Labels: app, Type: "ExternalName", HasSelector: true}, {Namespace: "m", Name: "nosel", Labels: app}}},
		{name: "trusted outranks a lone tenant", svcs: []Service{prom("tenant", "t", operated), prom("istio-system", "p", app)}, trusted: []string{"istio-system"}, want: []string{"istio-system/p"}},
		{name: "two untrusted of one class", svcs: []Service{prom("a", "p", app), prom("b", "p", app)}, several: true},
		{name: "refused stays refused", svcs: []Service{prom("a", "p", map[string]string{"app": "prometheus", "app.kubernetes.io/name": "prometheus"}), prom("b", "p", name)}, several: true},
	} {
		got, several := PrometheusCandidates(tc.svcs, tc.trusted, tc.viz)
		if !slices.Equal(names(got), tc.want) || several != tc.several {
			t.Errorf("%s: got %v several %v, want %v %v", tc.name, names(got), several, tc.want, tc.several)
		}
	}
	for _, tc := range []struct {
		ports []ServicePort
		want  int32
	}{
		{nil, 9090},
		{[]ServicePort{{Name: "x", Port: 1}, {Name: "web", Port: 2}}, 2},
		{[]ServicePort{{Name: "x", Port: 1}, {Name: "http-web", Port: 3}}, 3},
		{[]ServicePort{{Name: "http", Port: 4}}, 4},
		{[]ServicePort{{Port: 1}, {Port: 9090}}, 9090},
		{[]ServicePort{{Port: 7}}, 7},
	} {
		if got := PrometheusPort(Service{Ports: tc.ports}); got != tc.want {
			t.Errorf("port %+v = %d, want %d", tc.ports, got, tc.want)
		}
	}
	for _, c := range promClasses {
		for k := range c.labels {
			if !slices.Contains(PrometheusLabels, k) {
				t.Errorf("PrometheusLabels misses %s", k)
			}
		}
	}
}

func TestTracesSeveralPrometheusIsPartial(t *testing.T) {
	app := map[string]string{"app": "prometheus"}
	es := Traces([]Workload{{ID: "a", Namespace: "obs", Images: []string{"grafana/tempo:2.6"}}}, []Service{prom("a", "p", app), prom("b", "p", app)}, "p")
	if e := traceEntry(t, es, "tempo"); e.Completeness != Partial || e.Endpoint != "" || !strings.Contains(e.Reason, "several Prometheus servers") {
		t.Errorf("tempo = %+v", e)
	}
}

// One Prometheus per label class in different namespaces is several servers:
// the first is read and the entry is partial; two Services of one server in
// one namespace are not.
func TestTracesPrometheusAcrossClassesIsPartial(t *testing.T) {
	tempo := []Workload{{ID: "a", Namespace: "obs", Images: []string{"grafana/tempo:2.6"}}}
	app := map[string]string{"app.kubernetes.io/name": "prometheus"}
	e := traceEntry(t, Traces(tempo, []Service{prom("team-a", "prom", operated), prom("monitoring", "prometheus", app)}, "p"), "tempo")
	if e.Completeness != Partial || e.Endpoint == "" || e.Reason != "2 Prometheus servers could hold the service graph; Pacto read none of them as complete" {
		t.Errorf("tempo = %+v", e)
	}
	e = traceEntry(t, Traces(tempo, []Service{prom("monitoring", "prometheus-operated", operated), prom("monitoring", "prometheus", app)}, "p"), "tempo")
	if e.Completeness != Complete || e.Endpoint == "" {
		t.Errorf("one namespace = %+v", e)
	}
}

func TestTracesJaegerNeedsPortAndImage(t *testing.T) {
	q := Service{Namespace: "obs", Name: "jaeger-query", Ports: []ServicePort{{Port: 16686}}}
	if es := Traces([]Workload{{ID: "a", Namespace: "obs", Images: []string{"nginx"}}}, []Service{q}, "p"); len(es) != 0 {
		t.Errorf("port without image = %+v", es)
	}
	if es := Traces([]Workload{{ID: "a", Namespace: "other", Images: []string{"jaegertracing/all-in-one:1.60"}}}, []Service{q}, "p"); len(es) != 0 {
		t.Errorf("image in another namespace = %+v", es)
	}
	if es := Traces([]Workload{{ID: "a", Namespace: "obs", Images: []string{"jaegertracing/all-in-one:1.60"}}}, []Service{{Namespace: "obs", Name: "x", Ports: []ServicePort{{Port: 80}}}}, "p"); len(es) != 0 {
		t.Errorf("image without port = %+v", es)
	}
}

func TestTracesJaegerEndpoint(t *testing.T) {
	es := Traces([]Workload{{ID: "a", Namespace: "observability", Images: []string{"jaegertracing/jaeger:2.0"}}},
		[]Service{{Namespace: "observability", Name: "jaeger-query", Ports: []ServicePort{{Port: 16686}}}}, "p")
	e := traceEntry(t, es, "jaeger")
	if e.Endpoint != "http://jaeger-query.observability.svc:16686" || e.Evidence != "query API on port 16686" || e.Details != nil || e.Completeness != Complete {
		t.Errorf("jaeger = %+v", e)
	}
}

func datadogWorkload(env ...EnvFact) Workload {
	return Workload{ID: "d", Namespace: "datadog", Images: []string{"gcr.io/datadoghq/agent:7.50"},
		Owner: Object{Kind: "DaemonSet", Namespace: "datadog", Name: "datadog-agent"}, Env: env}
}

func TestTracesDatadogNamesSecretNotValue(t *testing.T) {
	es := Traces([]Workload{
		datadogWorkload(EnvFact{Key: "DD_SITE", Value: "datadoghq.eu"}, EnvFact{Key: "DD_API_KEY", SecretName: "datadog-secret"}),
		{ID: "e", Namespace: "datadog", Images: []string{"datadog/agent:7"}, Owner: Object{Kind: "DaemonSet", Namespace: "datadog", Name: "datadog-agent"},
			Env: []EnvFact{{Key: "DD_API_KEY", Value: "literal-key-value"}, {Key: "DD_API_KEY", SecretName: "datadog-secret"}}},
	}, nil, "pacto-system")
	e := traceEntry(t, es, "datadog")
	b, _ := json.Marshal(Result{Entries: es})
	if strings.Contains(string(b), "literal-key-value") {
		t.Errorf("a literal key reached the entry: %s", b)
	}
	if e.Evidence != "Agent found; DD_SITE datadoghq.eu" || !reflect.DeepEqual(e.Objects, []Object{
		{Kind: "DaemonSet", Namespace: "datadog", Name: "datadog-agent"}, {Kind: "Secret", Namespace: "datadog", Name: "datadog-secret"},
	}) || !slices.Equal(e.Details, []string{"DD_API_KEY comes from a Secret; Pacto did not read it"}) || strings.Contains(e.Evidence, "datadog-secret") {
		t.Errorf("datadog = %+v", e)
	}
}

func TestTracesDatadogIsPartialWithCollectorHint(t *testing.T) {
	e := traceEntry(t, Traces([]Workload{datadogWorkload()}, nil, "pacto-system"), "datadog")
	if e.Completeness != Partial || e.Reason != "Pacto reads Datadog edges through an OTel Collector" || e.Evidence != "Agent found" || e.Details != nil ||
		e.Hint != `exporters: {otlphttp/pacto: {endpoint: "http://pacto-dashboard.pacto-system:4318", encoding: json}}` || e.Endpoint != "" {
		t.Errorf("datadog = %+v", e)
	}
	if es := Traces([]Workload{{ID: "x", Namespace: "d", Images: []string{"datadog/cluster-agent:7"}}}, nil, "p"); len(es) != 0 {
		t.Errorf("the cluster agent is not the Agent: %+v", es)
	}
}

func TestTracesEmitsNothingWhenNoneFound(t *testing.T) {
	if es := Traces([]Workload{{ID: "a", Namespace: "a", Images: []string{"ghcr.io/acme/app:1", "::bad::"}}}, []Service{prom("m", "p", operated)}, "p"); es != nil {
		t.Errorf("entries = %+v", es)
	}
	if e := TracesUnknown("Forbidden"); e.Kind != "traces" || e.Completeness != Unknown || e.Reason != "Forbidden" {
		t.Errorf("unknown = %+v", e)
	}
}

func jaegerFleet(n int) []Entry {
	var svcs []Service
	var ws []Workload
	for i := range n {
		ns := fmt.Sprintf("obs-%c", 'a'+i)
		ws = append(ws, Workload{ID: ns, Namespace: ns, Images: []string{"jaegertracing/all-in-one:1"}})
		svcs = append(svcs, Service{Namespace: ns, Name: "jaeger-query", Ports: []ServicePort{{Port: 16686}}})
	}
	return Traces(ws, svcs, "p")
}

func TestTracesSeveralCandidatesAreObjects(t *testing.T) {
	e := traceEntry(t, jaegerFleet(3), "jaeger")
	if len(e.Objects) != 3 || e.Endpoint != "http://jaeger-query.obs-a.svc:16686" || !slices.Equal(e.Details, []string{"2 more candidates; Pacto reads the first"}) ||
		e.Completeness != Partial || e.Reason != "3 Jaeger query Services found; Pacto read none of them as complete" {
		t.Errorf("jaeger = %+v", e)
	}
	if e := traceEntry(t, jaegerFleet(2), "jaeger"); !slices.Equal(e.Details, []string{"1 more candidate; Pacto reads the first"}) {
		t.Errorf("jaeger = %+v", e)
	}
	for _, d := range e.Details {
		if strings.Contains(d, "obs-") {
			t.Errorf("details name a namespace: %q", d)
		}
	}
	// Too many to name as objects: the entry still says how many and is partial.
	if e := traceEntry(t, jaegerFleet(fleet.MaxProviderObjects+1), "jaeger"); e.Scope != fleet.ScopeCluster || e.Objects != nil ||
		e.Completeness != Partial || !strings.HasPrefix(e.Reason, "21 Jaeger query Services found") {
		t.Errorf("too many candidates to name = %+v", e)
	}
}

func TestTracesEvidenceNamesNoNamespace(t *testing.T) {
	ws := []Workload{
		{ID: "c", Namespace: "secret-ns", Images: []string{"otel/opentelemetry-collector:1"}},
		{ID: "t", Namespace: "secret-ns", Images: []string{"grafana/tempo:2"}},
		{ID: "j", Namespace: "secret-ns", Images: []string{"jaegertracing/all-in-one:1"}},
		datadogWorkload(),
	}
	ws[3].Namespace = "secret-ns"
	es := Traces(ws, []Service{{Namespace: "secret-ns", Name: "q", Ports: []ServicePort{{Port: 16686}}}}, "p")
	if len(es) != 4 {
		t.Fatalf("entries = %+v", es)
	}
	for _, e := range es {
		if strings.Contains(e.Evidence+e.Value+strings.Join(e.Details, " "), "secret-ns") {
			t.Errorf("%s names a namespace: %+v", e.Kind, e)
		}
	}
}

func TestTraceKindsRoundTrip(t *testing.T) {
	es := Traces([]Workload{{ID: "a", Namespace: "obs", Images: []string{"grafana/tempo:2.6"}}, datadogWorkload(EnvFact{Key: "DD_API_KEY", SecretName: "k"})},
		[]Service{prom("monitoring", "p", operated)}, "pacto-system")
	in := Result{AsOf: asOf, Entries: es}
	b, err := Encode(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(es, func(a, b Entry) int { return strings.Compare(a.Kind, b.Kind) })
	if !reflect.DeepEqual(out.Entries, es) {
		t.Errorf("round trip lost a field:\n got %+v\nwant %+v", out.Entries, es)
	}
	rows := ProviderRows(out, "detection", asOf)
	for _, r := range rows {
		if r.Provenance != fleet.ProvenanceInferred || !slices.Equal(r.UsedFor, []string{observedEdges}) {
			t.Errorf("row %+v", r)
		}
	}
	if rows[0].Name != "Datadog" || rows[1].Name != "Tempo" {
		t.Errorf("row names %s %s", rows[0].Name, rows[1].Name)
	}
}
