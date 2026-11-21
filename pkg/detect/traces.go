package detect

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// EnvFact is one allowlisted environment variable of a workload's
// containers: Value for a literal the allowlist keeps, SecretName for a value
// that comes from a Secret (the name only; nothing reads the Secret).
type EnvFact struct {
	Key, Value, SecretName string
}

// EnvAllowlist are the only environment variables the operator copies into a
// Workload. DD_SITE keeps its literal value; DD_API_KEY keeps only the name
// of the Secret its value comes from, and a literal value is dropped.
var EnvAllowlist = map[string]bool{"DD_SITE": true, "DD_API_KEY": true}

// Trace-backend facts.
const (
	tracesSource = "running pods and Services"
	jaegerPort   = 16686
)

// Traces are the trace backends running in ws and svcs, in the order
// otel-collector, tempo, jaeger, datadog, only those found. ns is the
// namespace the operator runs in, for the Datadog hint. Nothing here reads a
// backend: the dashboard does, from the entry's Endpoint.
func Traces(ws []Workload, svcs []Service, ns string) []Entry {
	var out []Entry
	for _, e := range []Entry{collectorEntry(ws, svcs), tempoEntry(ws, svcs), jaegerEntry(ws, svcs), datadogEntry(ws, ns)} {
		if e.Kind != "" {
			out = append(out, e)
		}
	}
	return out
}

// TracesUnknown is the one entry written when the pods or Services a trace
// rule reads could not be read.
func TracesUnknown(reason string) Entry { return UnknownEntry("traces", reason) }

// repository is the repository path of image (otel/opentelemetry-collector
// for otel/opentelemetry-collector:0.98.0), empty when it does not parse.
func repository(image string) string {
	ref, err := name.ParseReference(image)
	if err != nil {
		return ""
	}
	return ref.Context().RepositoryStr()
}

// running are the workloads of ws running an image whose repository path
// match accepts, and their namespaces, sorted.
func running(ws []Workload, match func(repo string) bool) ([]Workload, []string) {
	var out []Workload
	nss := map[string]bool{}
	for _, w := range ws {
		if slices.ContainsFunc(w.Images, func(img string) bool { return match(repository(img)) }) {
			out = append(out, w)
			nss[w.Namespace] = true
		}
	}
	return out, slices.Sorted(maps.Keys(nss))
}

// workloadsFigure is "running in 1 workload" or "running in N workloads".
func workloadsFigure(n int) string {
	if n == 1 {
		return "running in 1 workload"
	}
	return fmt.Sprintf("running in %d workloads", n)
}

// prometheus is the Prometheus a service graph would be read from: the
// first trusted candidate's URL and Service object, or, when several
// Prometheus servers leave none, why there is none. Candidates in several
// namespaces (one per label class) are several servers too: the first is
// read and the reason says none is complete. Candidates in one namespace
// are taken as one server's Services (prometheus-operated next to
// prometheus).
func prometheus(svcs []Service) (endpoint string, objects []Object, reasons []string) {
	cands, several := PrometheusCandidates(svcs, nil, false)
	if len(cands) == 0 {
		if several {
			reasons = []string{"several Prometheus servers could hold the service graph; Pacto cannot tell which one does"}
		}
		return "", nil, reasons
	}
	c := cands[0]
	if nss := namespacesOf(cands); nss > 1 {
		reasons = []string{fmt.Sprintf("%d Prometheus servers could hold the service graph; Pacto read none of them as complete", nss)}
	}
	return fmt.Sprintf("http://%s.%s.svc:%d", c.Name, c.Namespace, PrometheusPort(c)), []Object{{Kind: "Service", Namespace: c.Namespace, Name: c.Name}}, reasons
}

// namespacesOf is how many namespaces svcs sit in.
func namespacesOf(svcs []Service) int {
	nss := map[string]bool{}
	for _, s := range svcs {
		nss[s.Namespace] = true
	}
	return len(nss)
}

// graphEntry is the entry of kind running in ws across namespaces, with the
// Prometheus its service graph would be read from.
func graphEntry(kind, value string, ws []Workload, namespaces []string, objects []Object, svcs []Service) Entry {
	endpoint, prom, reasons := prometheus(svcs)
	e := Entry{Kind: kind, Value: value, Evidence: workloadsFigure(len(ws)), Source: tracesSource, Completeness: Complete, Endpoint: endpoint}
	place(&e, namespaces, append(objects, prom...))
	return partial(e, reasons...)
}

// collectorEntry finds an OpenTelemetry Collector (official, contrib and
// releases images) and its OTLP Services (port 4317 or 4318).
func collectorEntry(ws []Workload, svcs []Service) Entry {
	found, nss := running(ws, func(repo string) bool { return strings.Contains(repo, "opentelemetry-collector") })
	if len(found) == 0 {
		return Entry{}
	}
	var objects []Object
	for _, s := range sortedServices(slices.Clone(svcs)) {
		if slices.Contains(nss, s.Namespace) && slices.ContainsFunc(s.Ports, func(p ServicePort) bool { return p.Port == 4317 || p.Port == 4318 }) {
			objects = append(objects, Object{Kind: "Service", Namespace: s.Namespace, Name: s.Name})
		}
	}
	return graphEntry("otel-collector", "OpenTelemetry Collector", found, nss, objects, svcs)
}

// tempoEntry finds Grafana Tempo.
func tempoEntry(ws []Workload, svcs []Service) Entry {
	found, nss := running(ws, func(repo string) bool { return strings.Contains(repo, "grafana/tempo") })
	if len(found) == 0 {
		return Entry{}
	}
	return graphEntry("tempo", "Tempo", found, nss, nil, svcs)
}

// jaegerEntry finds a Jaeger query API: a Service with port 16686 in a
// namespace running a jaegertracing/ image. Every candidate is an object;
// the first by namespace then name is the Endpoint. Several candidates make
// the entry partial here, where all of them are known: anyone who can create
// a Service may have made one, so none reads as complete.
func jaegerEntry(ws []Workload, svcs []Service) Entry {
	_, nss := running(ws, func(repo string) bool { return strings.HasPrefix(repo, "jaegertracing/") })
	var cands []Object
	for _, s := range sortedServices(slices.Clone(svcs)) {
		if slices.Contains(nss, s.Namespace) && slices.ContainsFunc(s.Ports, func(p ServicePort) bool { return p.Port == jaegerPort }) {
			cands = append(cands, Object{Kind: "Service", Namespace: s.Namespace, Name: s.Name})
		}
	}
	if len(cands) == 0 {
		return Entry{}
	}
	var reasons []string
	if len(cands) > 1 {
		reasons = []string{fmt.Sprintf("%d Jaeger query Services found; Pacto read none of them as complete", len(cands))}
	}
	cands = capped(cands, MaxNamed)
	e := Entry{Kind: "jaeger", Value: "Jaeger", Evidence: fmt.Sprintf("query API on port %d", jaegerPort), Source: tracesSource, Completeness: Complete,
		Endpoint: fmt.Sprintf("http://%s.%s.svc:%d", cands[0].Name, cands[0].Namespace, jaegerPort)}
	if more := len(cands) - 1; more == 1 {
		e.Details = []string{"1 more candidate; Pacto reads the first"}
	} else if more > 1 {
		e.Details = []string{fmt.Sprintf("%d more candidates; Pacto reads the first", more)}
	}
	place(&e, nil, cands)
	return partial(e, reasons...)
}

// datadogEntry finds a Datadog Agent and names its controller and the Secret
// its API key comes from, never a value. It is partial: Pacto reads Datadog
// edges only through a Collector.
func datadogEntry(ws []Workload, ns string) Entry {
	found, nss := running(ws, func(repo string) bool {
		return strings.HasSuffix(repo, "datadog/agent") || strings.HasSuffix(repo, "datadoghq/agent")
	})
	if len(found) == 0 {
		return Entry{}
	}
	var objects []Object
	site, keyFromSecret := "", false
	for _, w := range found {
		if w.Owner.Kind != "" && !slices.Contains(objects, w.Owner) {
			objects = append(objects, w.Owner)
		}
		for _, f := range w.Env {
			switch {
			case f.Key == "DD_SITE":
				site = cmp.Or(site, f.Value)
			case f.Key == "DD_API_KEY" && f.SecretName != "":
				keyFromSecret = true
				if o := (Object{Kind: "Secret", Namespace: w.Namespace, Name: f.SecretName}); !slices.Contains(objects, o) {
					objects = append(objects, o)
				}
			}
		}
	}
	e := Entry{Kind: "datadog", Value: "Datadog Agent", Evidence: "Agent found", Source: tracesSource,
		Hint: fitting(`exporters: {otlphttp/pacto: {endpoint: "http://pacto-dashboard.` + ns + `:4318", encoding: json}}`)}
	if site != "" {
		e.Evidence += "; DD_SITE " + site
	}
	if keyFromSecret {
		e.Details = []string{"DD_API_KEY comes from a Secret; Pacto did not read it"}
	}
	place(&e, nss, objects)
	return partial(e, "Pacto reads Datadog edges through an OTel Collector")
}
