package ci

import (
	"strings"
	"testing"
	"time"
)

func TestTextPlanSkeleton(t *testing.T) {
	p := &Plan{
		Base:    Value{Value: "main", Source: "GITHUB_BASE_REF, context only"},
		OIDC:    true,
		Notes:   []string{"GitHub Enterprise Server: no registry host to assume"},
		Skipped: []Skip{{Rel: "services/legacy", Rule: ".pactoignore:1"}},
		Rows: []Row{
			{Service: "payments", Dir: "services/payments", Impact: Impact{Text: "unverified (pending)"}, Notes: []string{"a note"}},
			{Dir: "services/broken", Errors: []string{"bad yaml"}},
		},
	}
	var b strings.Builder
	Text(&b, p)
	want := `Registry: none (pass --registry)
Base: main (from GITHUB_BASE_REF, context only)
OIDC: available, not requested in plan mode
Consumer source: none (IMPACT reads unverified)
GitHub Enterprise Server: no registry host to assume

SERVICE   CHANGED  VERSION  CLASSIFICATION  IMPACT                INFERRED
payments  -        -        -               unverified (pending)  -
-         -        -        -               -                     -
skipped: services/legacy (from .pactoignore:1)
payments: a note
error: services/broken: bad yaml
`
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestTextPlanFilledCells(t *testing.T) {
	asOf := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	p := &Plan{
		Registry: Value{Value: "ghcr.io/acme", Source: "--registry"},
		Snapshot: SnapshotInfo{AsOf: asOf, Sources: []SourceLine{{Name: "--target-state t.yaml", Status: "available"}}},
		Rows: []Row{
			{Service: "ledger", Changed: "yes", Baseline: "1.4.2", Next: "1.4.3", Derived: true, Classification: "NON_BREAKING", Impact: Impact{Text: "clear"}, Inferred: 3},
			{Service: "new", Changed: "yes", Next: "1.0.0", Impact: Impact{Text: "first publish, clear"}},
		},
	}
	var b strings.Builder
	Text(&b, p)
	for _, line := range []string{
		"Registry: ghcr.io/acme (from --registry)\n",
		"Consumer source: --target-state t.yaml (available, asOf 2026-11-17T10:00:00Z)\n",
		"1.4.2 -> 1.4.3 (derived)", "3 fields", "- -> 1.0.0",
	} {
		if !strings.Contains(b.String(), line) {
			t.Errorf("missing %q in:\n%s", line, b.String())
		}
	}
	if strings.Contains(b.String(), "OIDC") {
		t.Errorf("OIDC printed without a token URL:\n%s", b.String())
	}
}
