package ci

import (
	"encoding/json"
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
			{Service: "new", Changed: "yes", Next: "1.0.0", Impact: Impact{Text: "first publish, clear"}, Inferred: 1},
		},
	}
	var b strings.Builder
	Text(&b, p)
	for _, line := range []string{
		"CI: gitlab (from GITLAB_CI)\n", "1 field\n", "Registry: ghcr.io/acme (from --registry)\n",
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

// A consumer source the caller named but no row reached impact for reads
// "not read", never "none", and the plan carries no fabricated asOf.
func TestNamedSourceNotRead(t *testing.T) {
	p := &Plan{ConsumerSources: []string{"--target-state targets.yaml"}, Rows: []Row{{Service: "a", Changed: "no"}}}
	var b strings.Builder
	Text(&b, p)
	if !strings.Contains(b.String(), "Consumer source: --target-state targets.yaml (not read: no changed service with a baseline)\n") ||
		strings.Contains(b.String(), "Consumer source: none") {
		t.Fatalf("got:\n%s", b.String())
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "asOf") {
		t.Fatalf("unread snapshot carries an asOf: %s", raw)
	}
}

// publishPlan is a publish run with one row of each outcome.
func publishPlan() *Plan {
	return &Plan{Rows: []Row{
		{Service: "ledger", Baseline: "1.4.3", To: "1.4.4", Tool: "ko", Identity: "key /k/cosign.key", Published: "r.io/acme/ledger:1.4.4@sha256:ab", State: StatePublished, Attempts: 2},
		{Service: "web", To: "1.0.0", Published: "r.io/acme/web:1.0.0@sha256:cd", State: StatePublished, Attempts: 1},
		{Service: "reports", State: StateBlocked, Reason: "depends on ledger 1.4.4, which failed to publish"},
		{Service: "jobs", State: StateFailed, Reason: "superseded: jobs 2.0.0 was published from abc, which this checkout does not contain"},
		{Service: "api", State: StateFailed, Reason: "boom", Attempts: 3},
		{Service: "cron", State: StateRefused, Reason: "publish refused: version"},
		{Service: "payments", State: StateUnchanged, Notes: []string{"pacto.yaml is 2.3 (current 2.5), published as written"}},
	}}
}

func TestTextPublish(t *testing.T) {
	var b strings.Builder
	Text(&b, publishPlan())
	want := `CI: none
Registry: none (pass --registry)
Base: none

ledger    1.4.3 -> 1.4.4 (attempt 2/3)  built with ko  signed key /k/cosign.key  published r.io/acme/ledger:1.4.4@sha256:ab
web       - -> 1.0.0                    published r.io/acme/web:1.0.0@sha256:cd
reports   blocked: depends on ledger 1.4.4, which failed to publish
jobs      superseded: jobs 2.0.0 was published from abc, which this checkout does not contain
api       failed: boom (attempt 3/3)
cron      refused: publish refused: version
payments  unchanged
payments: pacto.yaml is 2.3 (current 2.5), published as written
`
	if b.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestMarkdownPublish(t *testing.T) {
	p := &Plan{Refused: "pull request pipelines do not publish; publish runs on the merged commit", Rows: []Row{{Service: "a", State: StateRefused, Reason: "x"}}}
	got := Markdown(p)
	want := "### pacto ci publish\n\n- `CI: none`\n- `Registry: none (pass --registry)`\n- `Base: none`\n- `refused: pull request pipelines do not publish; publish runs on the merged commit`\n\n- `a  refused: x`\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSettlePublish(t *testing.T) {
	p := publishPlan()
	p.SettlePublish(".")
	if p.Exit != 1 || p.Gate != StateFailed || p.Reason != "not published: 4 of 7 services" {
		t.Fatalf("%d %s %q", p.Exit, p.Gate, p.Reason)
	}
	p = &Plan{Rows: publishPlan().Rows[:2]}
	p.SettlePublish(".")
	if p.Exit != 0 || p.Gate != StatePublished || p.Reason != "" {
		t.Fatalf("%d %s %q", p.Exit, p.Gate, p.Reason)
	}
	p = &Plan{}
	p.SettlePublish("svc")
	if p.Exit != 1 || p.Reason != "not published: no pacto.yaml under svc" {
		t.Fatalf("no contracts is not published: %d %q", p.Exit, p.Reason)
	}
	p = &Plan{Rows: publishPlan().Rows[:2], Unreadable: 2}
	p.SettlePublish(".")
	if p.Exit != 1 || p.Reason != "not published: 2 folders could not be read" {
		t.Fatalf("an unread folder may hold a changed service: %d %q", p.Exit, p.Reason)
	}
	p = &Plan{Refused: "no"}
	p.SettlePublish(".")
	if p.Exit != 1 || p.Gate != StateRefused || p.Reason != "refused: no" {
		t.Fatalf("%d %s %q", p.Exit, p.Gate, p.Reason)
	}
}
