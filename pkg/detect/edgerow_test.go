package detect

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var (
	edgeAsOf = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	edgeRead = edgeAsOf.Add(3 * time.Minute)
)

func jaegerResult(completeness string) (Result, Entry) {
	e := Entry{Kind: "jaeger", Value: "Jaeger", Evidence: "query API on port 16686", Source: "running pods and Services",
		Completeness: completeness, Namespaces: []string{"observability"},
		Objects:  []Object{{Kind: "Service", Namespace: "observability", Name: "jaeger-query"}},
		Endpoint: "http://jaeger-query.observability.svc:16686"}
	if completeness == Partial {
		e.Reason = "several candidates"
	}
	return Result{SchemaVersion: SchemaVersion, AsOf: edgeAsOf, Cluster: "c-1", ClusterName: "prod", Entries: []Entry{e}}, e
}

func TestEdgeSourceRowKeepsDetectedFacts(t *testing.T) {
	res, e := jaegerResult(Complete)
	p := EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourceAvailable, Edges: 2, ReadAt: edgeRead})
	want := []fleet.ProviderObject{{Kind: "Namespace", Namespace: "observability", Name: "observability"}, {Kind: "Service", Namespace: "observability", Name: "jaeger-query"}}
	switch {
	case p.Provenance != fleet.ProvenanceInferred, p.ID != "jaeger", p.Name != "Jaeger", p.Source != "",
		p.Cluster != "prod", p.ClusterID != "c-1", !reflect.DeepEqual(p.Objects, want),
		!reflect.DeepEqual(p.UsedFor, []string{fleet.UseObservedEdges}), p.Status != fleet.SourceAvailable, len(p.Limitations) != 0:
		t.Fatalf("row = %+v", p)
	}
	named := EdgeSourceRow(res, e, "x", LiveState{Status: fleet.SourceAvailable, Row: "service-graph", Name: "Service graph in Prometheus", ReadAt: edgeRead})
	if named.ID != "service-graph" || named.Name != "Service graph in Prometheus" {
		t.Fatalf("named row = %+v", named)
	}
}

func TestEdgeSourceRowAsOfIsTheLiveRead(t *testing.T) {
	res, e := jaegerResult(Complete)
	e.Details = []string{"1 more candidate; Pacto reads the first"}
	p := EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourceAvailable, Details: []string{"live fact"}, ReadAt: edgeRead})
	if p.AsOf == nil || !p.AsOf.Equal(edgeRead) {
		t.Fatalf("asOf = %v, want %v", p.AsOf, edgeRead)
	}
	want := []string{"1 more candidate; Pacto reads the first", "detected 2026-11-20T10:00:00Z", "live fact"}
	if !reflect.DeepEqual(p.Details, want) {
		t.Fatalf("details = %q, want %q", p.Details, want)
	}
	many := EdgeSourceRow(res, e, "x", LiveState{Status: fleet.SourceAvailable, Details: make([]string, MaxDetails), ReadAt: edgeRead})
	if len(many.Details) != MaxDetails {
		t.Fatalf("details = %d, want %d", len(many.Details), MaxDetails)
	}
}

func TestEdgeSourceRowTakesTheLowerStatus(t *testing.T) {
	res, e := jaegerResult(Complete)
	p := EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourcePartial, Reason: "no links", ReadAt: edgeRead})
	if p.Status != fleet.SourcePartial || len(p.Limitations) != 1 || p.Limitations[0].Code != fleet.LimitationEdgeSourcePartial ||
		p.Limitations[0].Message != "no links" || p.Limitations[0].Source != "detected-jaeger" {
		t.Fatalf("complete + partial = %+v", p)
	}
	res, e = jaegerResult(Partial)
	p = EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourceAvailable, ReadAt: edgeRead})
	if p.Status != fleet.SourcePartial {
		t.Fatalf("partial + available = %s", p.Status)
	}
	p = EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourceUnavailable, Reason: "down", ReadAt: edgeRead})
	if p.Status != fleet.SourceUnavailable || strings.Contains(p.Evidence, "edges") {
		t.Fatalf("partial + unavailable = %+v", p)
	}
	stale := EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourceAvailable, ReadAt: edgeAsOf.Add(StaleAfter + time.Minute)})
	if stale.Status != fleet.SourceStale {
		t.Fatalf("old detection = %s, want stale", stale.Status)
	}
}

func TestEdgeSourceRowSaysWhoWon(t *testing.T) {
	res, e := jaegerResult(Complete)
	p := EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourceAvailable, Edges: 1, Won: "the OTLP receiver is off",
		Hint: "a hint", ReadAt: edgeRead})
	if !strings.Contains(p.Evidence, "query API on port 16686; 1 edge in the last 15 minutes; won: the OTLP receiver is off (") || p.Hint != "a hint" {
		t.Fatalf("row = %+v", p)
	}
	e.Evidence = ""
	if p := EdgeSourceRow(res, e, "x", LiveState{Status: fleet.SourceUnavailable, Won: "w", ReadAt: edgeRead}); !strings.HasPrefix(p.Evidence, "Jaeger won: w (") {
		t.Fatalf("evidence = %q", p.Evidence)
	}
}

func TestEdgeSourceRowEvidenceNamesNoNamespace(t *testing.T) {
	res, e := jaegerResult(Complete)
	p := EdgeSourceRow(res, e, "detected-jaeger", LiveState{Status: fleet.SourceAvailable, Edges: 7, Won: "the OTLP receiver is off", ReadAt: edgeRead})
	if strings.Contains(p.Evidence, "observability") || strings.Contains(p.Evidence, "jaeger-query") {
		t.Fatalf("evidence names a namespace or object: %q", p.Evidence)
	}
}

func TestSplitEdgeKinds(t *testing.T) {
	res := Result{SchemaVersion: SchemaVersion, AsOf: edgeAsOf, Cluster: "c-1", Entries: []Entry{
		{Kind: "registry"}, {Kind: "jaeger"}, {Kind: "datadog"}, {Kind: "tempo"}, {Kind: "jaeger", Value: "second"},
	}, Installation: &Installation{}}
	rest, taken := SplitEdgeKinds(res, []string{"tempo", "jaeger"})
	if got := kinds(taken); !reflect.DeepEqual(got, []string{"jaeger", "tempo"}) || taken[0].Value != "" {
		t.Fatalf("taken = %v", got)
	}
	// A second jaeger entry stays a detected row.
	if got := kinds(rest.Entries); !reflect.DeepEqual(got, []string{"registry", "datadog", "jaeger"}) || rest.Cluster != "c-1" || rest.Installation == nil {
		t.Fatalf("rest = %+v", rest)
	}
	if len(res.Entries) != 5 {
		t.Fatal("SplitEdgeKinds changed its input")
	}
}

func kinds(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Kind)
	}
	return out
}

// A row with no detection behind it (the OTLP receiver with no Collector) is
// dated by its live read and claims no detection time.
func TestEdgeSourceRowWithNoDetection(t *testing.T) {
	at := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	p := EdgeSourceRow(Result{}, Entry{Kind: "otlp-receiver", Completeness: Complete}, "otlp-receiver",
		LiveState{Status: fleet.SourceAvailable, ReadAt: at, Name: "OTLP receiver"})
	if p.Status != fleet.SourceAvailable || !p.AsOf.Equal(at) || p.Name != "OTLP receiver" || strings.Contains(strings.Join(p.Details, " "), "detected ") {
		t.Fatalf("row = %+v", p)
	}
}
