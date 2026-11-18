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
	want := `CI: none
Registry: none (pass --registry)
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
		Platform: "gitlab",
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
		"CI: gitlab (from GITLAB_CI)\n", "Registry: ghcr.io/acme (from --registry)\n",
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

func TestTextPlanLayout(t *testing.T) {
	p := &Plan{
		Platform: "github",
		Registry: Value{Value: "ghcr.io/acme", Source: "GITHUB_REPOSITORY_OWNER"},
		Base:     Value{Value: "4e1a9c0", Source: "GITHUB_BASE_REF, context only"},
		Rows: []Row{
			{Service: "payments", Changed: "yes", Baseline: "1.4.2", Next: "1.5.0", Classification: "NON_BREAKING", Impact: Impact{Text: "unverified (no consumer source)"}, Inferred: 6},
			{Service: "reports", Changed: "no", Impact: Impact{Text: "-"}},
		},
	}
	var b strings.Builder
	Text(&b, p)
	want := `CI: github (from GITHUB_ACTIONS)
Registry: ghcr.io/acme (from GITHUB_REPOSITORY_OWNER)
Base: 4e1a9c0 (from GITHUB_BASE_REF, context only)
Consumer source: none (IMPACT reads unverified)

SERVICE   CHANGED  VERSION         CLASSIFICATION  IMPACT                           INFERRED
payments  yes      1.4.2 -> 1.5.0  NON_BREAKING    unverified (no consumer source)  6 fields
reports   no       -               -               -                                -
`
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestMarkdownEscapesCells(t *testing.T) {
	p := &Plan{
		Registry: Value{Value: "ghcr.io/acme", Source: "--registry"},
		Notes:    []string{"images name `x`\n# heading"},
		Skipped:  []Skip{{Rel: "services/old", Rule: ".pactoignore:2"}},
		Rows: []Row{
			{Service: "pay`ments", Dir: "services/pay\nments", Changed: "yes", Next: "1.0.0", Impact: Impact{Text: "unverified (a | b `c`\r\n[x](https://evil) <img src=x>)"}, Errors: []string{"bad\n## yaml"}},
			{Service: "reports", Changed: "no", Impact: Impact{Text: "-"}},
		},
	}
	got := Markdown(p)
	want := "### pacto ci plan\n\n" +
		"- `CI: none`\n" +
		"- `Registry: ghcr.io/acme (from --registry)`\n" +
		"- `Base: none`\n" +
		"- `Consumer source: none (IMPACT reads unverified)`\n" +
		"- `images name 'x' # heading`\n\n" +
		"| SERVICE | CHANGED | VERSION | CLASSIFICATION | IMPACT | INFERRED |\n" +
		"|---|---|---|---|---|---|\n" +
		"| `pay'ments` | `yes` | `- -> 1.0.0` | - | `unverified (a \\| b 'c'  [x](https://evil) <img src=x>)` | - |\n" +
		"| `reports` | `no` | - | - | - | - |\n\n" +
		"- `skipped: services/old (from .pactoignore:2)`\n" +
		"- `error: services/pay ments: bad ## yaml`\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Count(got, "\n|---|") != 1 {
		t.Fatalf("more than one separator row:\n%s", got)
	}
}

func TestMarkdownNoFooter(t *testing.T) {
	if got := Markdown(&Plan{}); strings.Contains(got, "\n\n- `skipped") || !strings.HasSuffix(got, "|---|---|---|---|---|---|\n") {
		t.Fatalf("an empty plan ends after the table header:\n%s", got)
	}
}
