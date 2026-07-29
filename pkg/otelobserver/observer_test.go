package otelobserver

import (
	"reflect"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidence"
)

const twoServiceTrace = `{
  "resourceSpans": [
    {
      "resource": {"attributes": [{"key": "service.name", "value": {"stringValue": "frontend"}}]},
      "scopeSpans": [{"spans": [
        {"kind": 3, "attributes": [{"key": "peer.service", "value": {"stringValue": "payments"}}]},
        {"kind": "SPAN_KIND_CLIENT", "attributes": [{"key": "peer.service", "value": {"stringValue": "payments"}}]},
        {"kind": 4, "attributes": [{"key": "messaging.system", "value": {"stringValue": "kafka"}}]},
        {"kind": 2, "attributes": [{"key": "peer.service", "value": {"stringValue": "ignored-server-peer"}}]}
      ]}]
    }
  ]
}`

func TestParseTraces_Error(t *testing.T) {
	if _, err := ParseTraces([]byte("{not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseTraces_SpanKindVariants(t *testing.T) {
	td, err := ParseTraces([]byte(twoServiceTrace))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(td.ResourceSpans) != 1 {
		t.Fatalf("resourceSpans = %d", len(td.ResourceSpans))
	}
}

func TestParseTraces_UnknownAndBadKind(t *testing.T) {
	// Unknown string enum decodes to unspecified (0), not an error.
	if _, err := ParseTraces([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"kind":"SPAN_KIND_SERVER"}]}]}]}`)); err != nil {
		t.Fatalf("unknown kind should not error: %v", err)
	}
	// A non-int, non-string kind is a decode error.
	if _, err := ParseTraces([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"kind":true}]}]}]}`)); err == nil {
		t.Fatal("expected error for boolean kind")
	}
}

func TestDependencyEdges_ExtractionDedupeSort(t *testing.T) {
	td, _ := ParseTraces([]byte(twoServiceTrace))
	got := DependencyEdges(td)
	want := []Edge{
		{From: "frontend", To: "kafka", Count: 1},    // producer span
		{From: "frontend", To: "payments", Count: 2}, // two client spans deduped, counted
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
}

func TestDependencyEdges_SkipMissingCallerCalleeAndSelf(t *testing.T) {
	trace := `{"resourceSpans":[
	  {"resource":{"attributes":[]},"scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"x"}}]}]}]},
	  {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"self"}}]},"scopeSpans":[{"spans":[
	    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"self"}}]},
	    {"kind":3,"attributes":[]}
	  ]}]}
	]}`
	td, _ := ParseTraces([]byte(trace))
	if got := DependencyEdges(td); len(got) != 0 {
		t.Errorf("expected no edges (missing caller, self-edge, missing callee), got %+v", got)
	}
}

func TestDependencyEdges_CalleeKeyPrecedence(t *testing.T) {
	// db.system used only when higher-precedence keys are absent.
	trace := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"api"}}]},
	  "scopeSpans":[{"spans":[
	    {"kind":3,"attributes":[{"key":"db.system","value":{"stringValue":"postgresql"}}]},
	    {"kind":3,"attributes":[{"key":"net.peer.name","value":{"stringValue":"cache"}},{"key":"peer.service","value":{"stringValue":"cache-svc"}}]}
	  ]}]}]}`
	td, _ := ParseTraces([]byte(trace))
	got := DependencyEdges(td)
	want := []Edge{
		{From: "api", To: "cache-svc", Count: 1}, // peer.service beats net.peer.name
		{From: "api", To: "postgresql", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
}

func TestEvidenceSets_GroupingContentDefaults(t *testing.T) {
	edges := []Edge{
		{From: "frontend", To: "payments"},
		{From: "frontend", To: "kafka"},
		{From: "worker", To: "db"},
	}
	at := time.Unix(1000, 0).UTC()
	sets := EvidenceSets(edges, Options{ObservedAt: at}) // default collector, nil ContractRef
	if len(sets) != 2 {
		t.Fatalf("sets = %d, want 2", len(sets))
	}
	// Ordered by subject service.
	if sets[0].Subject.Name != "frontend" || sets[1].Subject.Name != "worker" {
		t.Fatalf("order wrong: %q, %q", sets[0].Subject.Name, sets[1].Subject.Name)
	}
	fe := sets[0]
	if fe.Subject.Kind != "service" || fe.Source != "otel" || fe.ContractRef != "" || !fe.ObservedAt.Equal(at) {
		t.Errorf("frontend set envelope wrong: %+v", fe)
	}
	if len(fe.Observations) != 2 {
		t.Fatalf("frontend observations = %d, want 2", len(fe.Observations))
	}
}

func TestEvidenceSets_ObservationContentAndValidity(t *testing.T) {
	edges := []Edge{{From: "frontend", To: "payments"}}
	at := time.Unix(1000, 0).UTC()
	o := EvidenceSets(edges, Options{ObservedAt: at})[0].Observations[0]
	if o.Kind != evidence.DependencyReachable || o.Subject.Kind != "dependency" ||
		o.Subject.Name != "payments" || o.Provenance.Collector != "otel" || !o.Provenance.DetectedAt.Equal(at) {
		t.Errorf("observation wrong: %+v", o)
	}
	dep, err := o.GetDependencyObservation()
	if err != nil || !dep.Reachable {
		t.Errorf("payload = %+v err=%v", dep, err)
	}
	// A set built with a contract ref is ingest-valid.
	withRef := EvidenceSets(edges, Options{ObservedAt: at, ContractRef: func(string) string { return "oci://x@sha256:a" }})[0]
	if errs := evidence.ValidateEvidenceSet(withRef); len(errs) > 0 {
		t.Errorf("set with a contract ref should validate, got %v", errs)
	}
}

func TestDependencyEdges_MultiCallerAndProducerStringKind(t *testing.T) {
	trace := `{"resourceSpans":[
	  {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
	   "scopeSpans":[{"spans":[{"kind":"SPAN_KIND_PRODUCER","attributes":[{"key":"messaging.system","value":{"stringValue":"kafka"}}]}]}]},
	  {"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"api"}}]},
	   "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"db"}}]}]}]}
	]}`
	td, err := ParseTraces([]byte(trace))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := DependencyEdges(td)
	// Sorted across distinct callers (api before web) exercises the From ordering.
	want := []Edge{
		{From: "api", To: "db", Count: 1},
		{From: "web", To: "kafka", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %+v, want %+v", got, want)
	}
}

func TestEvidenceSets_CustomCollectorAndContractRef(t *testing.T) {
	sets := EvidenceSets([]Edge{{From: "api", To: "db"}}, Options{
		Collector:   "mesh",
		ObservedAt:  time.Unix(2, 0).UTC(),
		ContractRef: func(s string) string { return "oci://reg/" + s + "@sha256:abc" },
	})
	if len(sets) != 1 {
		t.Fatalf("sets = %d", len(sets))
	}
	if sets[0].Source != "mesh" || sets[0].ContractRef != "oci://reg/api@sha256:abc" {
		t.Errorf("collector/ref wrong: %+v", sets[0])
	}
	if sets[0].Observations[0].Provenance.Collector != "mesh" {
		t.Errorf("provenance collector = %q", sets[0].Observations[0].Provenance.Collector)
	}
}

func TestObserve_EndToEndAndParseError(t *testing.T) {
	sets, err := Observe([]byte(twoServiceTrace), Options{
		ObservedAt:  time.Unix(1, 0).UTC(),
		ContractRef: func(string) string { return "oci://x@sha256:a" },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sets) != 1 || sets[0].Subject.Name != "frontend" {
		t.Fatalf("unexpected sets: %+v", sets)
	}
	if _, err := Observe([]byte("{bad"), Options{}); err == nil {
		t.Fatal("expected parse error")
	}
}
