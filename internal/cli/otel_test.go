package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

const otelTrace = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
  "scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]}]}]}]}`

func TestOTelObserve_Edges(t *testing.T) {
	f := filepath.Join(t.TempDir(), "traces.json")
	mustWrite(t, f, otelTrace)

	out, _, err := execFleet(t, "otel", "observe", f)
	if err != nil {
		t.Fatalf("otel observe: %v", err)
	}
	if !strings.Contains(out, "web -> payments (count=1)") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestOTelObserve_EvidenceJSON(t *testing.T) {
	f := filepath.Join(t.TempDir(), "traces.json")
	mustWrite(t, f, otelTrace)

	out, _, err := execFleet(t, "otel", "observe", f, "--evidence", "--source", "mesh", "--output-format", "json")
	if err != nil {
		t.Fatalf("otel observe --evidence: %v", err)
	}
	for _, want := range []string{`"Subject"`, `"web"`, `"DependencyReachable"`, `"mesh"`} {
		if !strings.Contains(out, want) {
			t.Errorf("evidence json missing %q:\n%s", want, out)
		}
	}
}

func TestOTelObserve_EvidenceText(t *testing.T) {
	f := filepath.Join(t.TempDir(), "traces.json")
	mustWrite(t, f, otelTrace)

	out, _, err := execFleet(t, "otel", "observe", f, "--evidence")
	if err != nil {
		t.Fatalf("otel observe --evidence: %v", err)
	}
	if !strings.Contains(out, "EvidenceSets (1):") || !strings.Contains(out, "web: 1 observed dependencies") {
		t.Errorf("unexpected evidence text:\n%s", out)
	}
}

func TestOTelObserve_ReadError(t *testing.T) {
	if _, _, err := execFleet(t, "otel", "observe", filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestOTelObserve_ParseError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bad.json")
	mustWrite(t, f, "{not json")
	if _, _, err := execFleet(t, "otel", "observe", f); err == nil {
		t.Fatal("expected parse error")
	}
}
