package fleetsrc

import (
	"context"
	"path/filepath"
	"testing"
)

const observationTrace = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
  "scopeSpans":[{"spans":[
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]},
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]}
  ]}]}]}`

func TestObservationSource_IDAndKind(t *testing.T) {
	if got := NewObservationSource("", "/x").ID(); got != "observation" {
		t.Errorf("default id = %q, want observation", got)
	}
	if got := NewObservationSource("otel-eu", "/x").ID(); got != "otel-eu" {
		t.Errorf("custom id = %q, want otel-eu", got)
	}
	if got := NewObservationSource("", "/x").Kind(); got != "observation" {
		t.Errorf("kind = %q, want observation", got)
	}
}

func TestObservationSource_Collect(t *testing.T) {
	path := writeFixture(t, "traces.json", observationTrace)
	col, err := NewObservationSource("otel", path).Collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// Two spans to the same peer collapse to one edge with count 2.
	if len(col.Observed) != 1 {
		t.Fatalf("observed edges = %+v, want 1", col.Observed)
	}
	e := col.Observed[0]
	if e.From != "web" || e.To != "payments" || e.Count != 2 {
		t.Errorf("edge = %+v, want web->payments count 2", e)
	}
}

func TestObservationSource_MissingFile(t *testing.T) {
	if _, err := NewObservationSource("", filepath.Join(t.TempDir(), "missing.json")).Collect(context.Background()); err == nil {
		t.Fatal("expected an error for a missing trace file")
	}
}

func TestObservationSource_BadJSON(t *testing.T) {
	path := writeFixture(t, "bad.json", "{not json")
	if _, err := NewObservationSource("", path).Collect(context.Background()); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestObservationSource_ContextCancelled(t *testing.T) {
	path := writeFixture(t, "traces.json", observationTrace)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewObservationSource("", path).Collect(ctx); err == nil {
		t.Fatal("expected a context-cancelled error")
	}
}
