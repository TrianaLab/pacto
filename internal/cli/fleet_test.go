package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/cli"
	"github.com/trianalab/pacto/v3/internal/testutil"
)

// writeFleetFixture builds a local bundle root plus an evidence file exercising
// every fleet renderer branch, returning their paths.
func writeFleetFixture(t *testing.T) (root, evidence string) {
	t.Helper()
	root = t.TempDir()

	// orders: rich — OpenAPI (tools), a skill, docs, a resolvable + an
	// unresolvable dependency, a capability and a passing readiness gate.
	ordersDir := filepath.Join(root, "orders")
	mustMkdir(t, filepath.Join(ordersDir, "skills"))
	mustMkdir(t, filepath.Join(ordersDir, "docs"))
	mustWrite(t, filepath.Join(ordersDir, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: orders
  version: "1.0.0"
  owner:
    team: commerce
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
    visibility: public
dependencies:
  - name: payments
    ref: oci://ghcr.io/acme/payments
    required: true
    compatibility: ^1.0.0
  - name: ghost
    ref: oci://ghcr.io/acme/ghost
    required: false
    compatibility: ^1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
capabilities:
  - type: health
readiness:
  minScore: 50
  expires: "2027-06-30"
  claims:
    - id: runbook
      type: url
      status: done
      evidence: https://example.com/runbook
      weight: 100
`)
	mustWrite(t, filepath.Join(ordersDir, "openapi.yaml"), string(testutil.TestOpenAPI()))
	mustWrite(t, filepath.Join(ordersDir, "skills", "checkout.md"), "# Checkout\n")
	mustWrite(t, filepath.Join(ordersDir, "docs", "overview.md"), "# Overview\n")

	// payments: minimal, no interface/skills — dependency target of orders.
	mustBundle(t, filepath.Join(root, "payments"), "payments", "2.0.0", "billing")

	// lonely: minimal, has a revision but NO operational target.
	mustBundle(t, filepath.Join(root, "lonely"), "lonely", "1.0.0", "")

	// brokensvc: parses but fails validation (interface missing type).
	mustWrite(t, filepath.Join(root, "brokensvc", "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: brokensvc
  version: "1.0.0"
interfaces:
  - name: api
    ref: openapi.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)

	evidence = filepath.Join(t.TempDir(), "evidence.yaml")
	mustWrite(t, evidence, `schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - scope: prod
    kind: k8s
    name: orders
    service: orders
    compliance: NonCompliant
    coverage:
      evaluated: 2
      required: 3
    findings:
      - code: STATELESS_PERSISTENT_CONFLICT
        severity: error
        category: StateMismatch
        subjectKind: service
        subjectName: orders
        message: drift detected
    evidenceAt: 2026-07-29T11:00:00Z
  - scope: prod
    kind: k8s
    name: payments
    service: payments
    compliance: Unknown
  - scope: prod
    kind: k8s
    name: dup
    service: orders
    compliance: Compliant
  - scope: eu
    kind: k8s
    name: dup
    service: orders
    compliance: Compliant
  - scope: prod
    kind: k8s
    name: extra
    service: extra-svc
    compliance: Unknown
  - scope: prod
    kind: k8s
    name: stale-one
    service: orders
    compliance: Compliant
    evidenceAt: 2020-01-01T00:00:00Z
`)
	return root, evidence
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustBundle(t *testing.T, dir, name, version, team string) {
	t.Helper()
	owner := ""
	if team != "" {
		owner = "  owner:\n    team: " + team + "\n"
	}
	mustWrite(t, filepath.Join(dir, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: `+name+`
  version: "`+version+`"
`+owner+`workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
}

// execFleet runs the pacto CLI with args and returns stdout, stderr and error.
func execFleet(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	svc := app.NewService(nil, nil)
	root := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	root.SetArgs(args)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	err := root.Execute()
	return out.String(), errb.String(), err
}

func TestFleetSearch(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"fleet", "search", "--local", root, "--target-state", ev}

	// Text output with a partial answer (extra-svc has a target but the local
	// source is complete; a service-only target keeps completeness=complete here,
	// so hit the plain path first).
	out, _, err := execFleet(t, append(append([]string{}, base...), "orders")...)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, "orders") || !strings.Contains(out, "service(s)") {
		t.Errorf("unexpected search output:\n%s", out)
	}

	// Every filter flag, to drive searchFilterFromCmd fully.
	filters := [][]string{
		{"--owner", "commerce"},
		{"--status", "NonCompliant"},
		{"--compliance", "NonCompliant"},
		{"--source", "local"},
		{"--workload", "service"},
		{"--label", "k=v"},
		{"--ready"},
		{"--not-ready"},
		{"--has-capability"},
		{"--has-dependency"},
		{"--limit", "2"},
		{"--offset", "1"},
	}
	for _, f := range filters {
		if _, _, err := execFleet(t, append(append([]string{}, base...), f...)...); err != nil {
			t.Errorf("search %v: %v", f, err)
		}
	}

	// JSON output.
	jout, _, err := execFleet(t, append(append([]string{}, base...), "--output-format", "json")...)
	if err != nil {
		t.Fatalf("search json: %v", err)
	}
	if !strings.Contains(jout, `"total"`) || !strings.Contains(jout, `"services"`) {
		t.Errorf("unexpected search json:\n%s", jout)
	}

	// An invalid filter value is a query error, not a silent default.
	if _, _, err := execFleet(t, append(append([]string{}, base...), "--status", "Bogus")...); err == nil {
		t.Error("expected error for an invalid --status filter")
	}
}

func TestFleetSearch_OrDashForEmpties(t *testing.T) {
	root, ev := writeFleetFixture(t)
	// extra-svc has a target but no owner → owner renders as "-".
	out, _, err := execFleet(t, "fleet", "search", "extra-svc", "--local", root, "--target-state", ev)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, "owner=-") {
		t.Errorf("expected orDash owner=- for ownerless service:\n%s", out)
	}
}

func TestFleetGetService(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	// orders: revisions + targets + dependencies + tools + skills present.
	out, _, err := execFleet(t, append([]string{"fleet", "get", "orders"}, base...)...)
	if err != nil {
		t.Fatalf("get orders: %v", err)
	}
	for _, want := range []string{"Service: orders", "Revisions", "Targets", "Dependencies", "tools=1 skills=1"} {
		if !strings.Contains(out, want) {
			t.Errorf("get orders missing %q:\n%s", want, out)
		}
	}

	// payments: dependents present, no deps/tools/skills.
	pout, _, err := execFleet(t, append([]string{"fleet", "get", "payments"}, base...)...)
	if err != nil {
		t.Fatalf("get payments: %v", err)
	}
	if !strings.Contains(pout, "Dependents") {
		t.Errorf("expected payments to list dependents:\n%s", pout)
	}

	// lonely: revision present but no targets.
	if _, _, err := execFleet(t, append([]string{"fleet", "get", "lonely"}, base...)...); err != nil {
		t.Fatalf("get lonely: %v", err)
	}

	// extra-svc: target-only service (no revisions).
	if _, _, err := execFleet(t, append([]string{"fleet", "get", "extra-svc"}, base...)...); err != nil {
		t.Fatalf("get extra-svc: %v", err)
	}

	// JSON.
	jout, _, err := execFleet(t, append([]string{"fleet", "get", "orders", "--output-format", "json"}, base...)...)
	if err != nil {
		t.Fatalf("get json: %v", err)
	}
	if !strings.Contains(jout, `"service"`) {
		t.Errorf("unexpected get json:\n%s", jout)
	}
}

func TestFleetGetTarget(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	// orders target: coverage + findings present.
	out, _, err := execFleet(t, append([]string{"fleet", "get", "--target", "prod/k8s/orders"}, base...)...)
	if err != nil {
		t.Fatalf("get target orders: %v", err)
	}
	for _, want := range []string{"Target: prod/k8s/orders", "Coverage:", "finding ["} {
		if !strings.Contains(out, want) {
			t.Errorf("target output missing %q:\n%s", want, out)
		}
	}

	// payments target: no coverage, a limitation (missing evidence).
	pout, _, err := execFleet(t, append([]string{"fleet", "get", "--target", "prod/k8s/payments"}, base...)...)
	if err != nil {
		t.Fatalf("get target payments: %v", err)
	}
	if !strings.Contains(pout, "limitation [") {
		t.Errorf("expected a limitation for payments target:\n%s", pout)
	}

	// stale target with a freshness window → stale limitation.
	sout, _, err := execFleet(t, append([]string{"fleet", "get", "--target", "prod/k8s/stale-one", "--freshness", "1h"}, base...)...)
	if err != nil {
		t.Fatalf("get target stale-one: %v", err)
	}
	if !strings.Contains(sout, "Stale: true") {
		t.Errorf("expected stale target:\n%s", sout)
	}

	// JSON.
	if _, _, err := execFleet(t, append([]string{"fleet", "get", "--target", "prod/k8s/orders", "--output-format", "json"}, base...)...); err != nil {
		t.Fatalf("get target json: %v", err)
	}
}

func TestFleetGet_Errors(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	// Neither service nor target.
	if _, _, err := execFleet(t, append([]string{"fleet", "get"}, base...)...); err == nil {
		t.Error("expected error when neither service nor --target is given")
	}
	// Unknown service.
	if _, _, err := execFleet(t, append([]string{"fleet", "get", "nope"}, base...)...); err == nil {
		t.Error("expected NotFound error for unknown service")
	}
	// Ambiguous target (two targets named "dup").
	if _, _, err := execFleet(t, append([]string{"fleet", "get", "--target", "dup"}, base...)...); err == nil {
		t.Error("expected ambiguous error for duplicate target name")
	}
	// Unknown target.
	if _, _, err := execFleet(t, append([]string{"fleet", "get", "--target", "no/such/target"}, base...)...); err == nil {
		t.Error("expected NotFound error for unknown target")
	}
}

func TestFleetGraph(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	// dependencies (default) + unresolved edge (orders → ghost).
	out, _, err := execFleet(t, append([]string{"fleet", "graph", "orders"}, base...)...)
	if err != nil {
		t.Fatalf("graph orders: %v", err)
	}
	if !strings.Contains(out, "dependencies of orders") || !strings.Contains(out, "unresolved: orders -> ghost") {
		t.Errorf("unexpected graph output:\n%s", out)
	}

	// dependents direction.
	dout, _, err := execFleet(t, append([]string{"fleet", "graph", "payments", "--direction", "dependents"}, base...)...)
	if err != nil {
		t.Fatalf("graph payments dependents: %v", err)
	}
	if !strings.Contains(dout, "dependents of payments") {
		t.Errorf("unexpected dependents output:\n%s", dout)
	}

	// transitive + max-depth.
	if _, _, err := execFleet(t, append([]string{"fleet", "graph", "orders", "--transitive", "--max-depth", "2"}, base...)...); err != nil {
		t.Fatalf("graph transitive: %v", err)
	}

	// JSON.
	if _, _, err := execFleet(t, append([]string{"fleet", "graph", "orders", "--output-format", "json"}, base...)...); err != nil {
		t.Fatalf("graph json: %v", err)
	}

	// Unknown root → error.
	if _, _, err := execFleet(t, append([]string{"fleet", "graph", "nope"}, base...)...); err == nil {
		t.Error("expected error for unknown graph root")
	}
}

func TestFleetGraph_Cycle(t *testing.T) {
	root := t.TempDir()
	mustCyclicBundle(t, filepath.Join(root, "a"), "a", "b")
	mustCyclicBundle(t, filepath.Join(root, "b"), "b", "a")

	out, _, err := execFleet(t, "fleet", "graph", "a", "--transitive", "--local", root)
	if err != nil {
		t.Fatalf("graph cycle: %v", err)
	}
	if !strings.Contains(out, "cycle:") {
		t.Errorf("expected a cycle to be rendered:\n%s", out)
	}
}

// TestFleetGraph_Aggregated proves a service-only graph over a multi-revision
// service reports the aggregated note (no "latest"/representative revision).
func TestFleetGraph_Aggregated(t *testing.T) {
	root := t.TempDir()
	mustBundle(t, filepath.Join(root, "v1"), "multi", "1.0.0", "team")
	mustBundle(t, filepath.Join(root, "v2"), "multi", "2.0.0", "team")

	out, _, err := execFleet(t, "fleet", "graph", "multi", "--local", root)
	if err != nil {
		t.Fatalf("graph multi: %v", err)
	}
	if !strings.Contains(out, "aggregated across revisions") {
		t.Errorf("expected aggregated note for a multi-revision service:\n%s", out)
	}
}

// TestFleetGraph_RevisionScoped proves --target roots the target's exact linked
// revision, so the output header names that revision (not the aggregated service).
func TestFleetGraph_RevisionScoped(t *testing.T) {
	root := t.TempDir()
	mustBundle(t, filepath.Join(root, "svc"), "svc", "1.0.0", "team")
	ev := filepath.Join(t.TempDir(), "ev.yaml")
	mustWrite(t, ev, `schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - scope: prod
    kind: k8s
    name: svc
    service: svc
    resolvedRef: oci://x/svc:1.0.0
    compliance: Compliant
`)
	out, _, err := execFleet(t, "fleet", "graph", "--target", "prod/k8s/svc", "--local", root, "--target-state", ev)
	if err != nil {
		t.Fatalf("graph --target: %v", err)
	}
	if !strings.Contains(out, "svc@") {
		t.Errorf("expected revision-scoped root in output:\n%s", out)
	}
}

func mustCyclicBundle(t *testing.T, dir, name, dep string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: `+name+`
  version: "1.0.0"
dependencies:
  - name: `+dep+`
    ref: oci://x/`+dep+`
    required: true
    compatibility: ^1.0.0
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
}

func TestFleetStatus(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	// Default union.
	out, _, err := execFleet(t, append([]string{"fleet", "status"}, base...)...)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "attention item(s)") {
		t.Errorf("unexpected status output:\n%s", out)
	}

	// Each individual category flag.
	for _, flag := range []string{"--non-compliant", "--unknown", "--invalid", "--missing-readiness", "--unresolved-deps", "--needs-attention"} {
		if _, _, err := execFleet(t, append(append([]string{"fleet", "status"}, base...), flag)...); err != nil {
			t.Errorf("status %s: %v", flag, err)
		}
	}

	// Stale needs a freshness window to classify.
	if _, _, err := execFleet(t, append(append([]string{"fleet", "status"}, base...), "--stale", "--freshness", "1h", "--limit", "50")...); err != nil {
		t.Errorf("status --stale: %v", err)
	}

	// JSON.
	if _, _, err := execFleet(t, append([]string{"fleet", "status", "--output-format", "json"}, base...)...); err != nil {
		t.Fatalf("status json: %v", err)
	}
}

func TestFleetSnapshot(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	out, _, err := execFleet(t, append([]string{"fleet", "snapshot"}, base...)...)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(out, "Fleet snapshot") || !strings.Contains(out, "Sources") {
		t.Errorf("unexpected snapshot output:\n%s", out)
	}

	if _, _, err := execFleet(t, append([]string{"fleet", "snapshot", "--output-format", "json"}, base...)...); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}

	// A missing evidence file makes a source unavailable → the snapshot carries a
	// limitation, exercising the limitation-print branch.
	lout, _, err := execFleet(t, "fleet", "snapshot", "--local", root, "--target-state", filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("snapshot partial: %v", err)
	}
	if !strings.Contains(lout, "limitation [SOURCE_UNAVAILABLE]") {
		t.Errorf("expected a snapshot limitation line:\n%s", lout)
	}
}

func TestFleetExplain(t *testing.T) {
	root, ev := writeFleetFixture(t)
	base := []string{"--local", root, "--target-state", ev}

	// orders: has reasons (findings).
	out, _, err := execFleet(t, append([]string{"fleet", "explain", "orders"}, base...)...)
	if err != nil {
		t.Fatalf("explain orders: %v", err)
	}
	if !strings.Contains(out, "service orders") {
		t.Errorf("unexpected explain output:\n%s", out)
	}

	// lonely: no reasons → "no reasons recorded".
	lout, _, err := execFleet(t, append([]string{"fleet", "explain", "lonely"}, base...)...)
	if err != nil {
		t.Fatalf("explain lonely: %v", err)
	}
	if !strings.Contains(lout, "no reasons recorded") {
		t.Errorf("expected 'no reasons recorded' for lonely:\n%s", lout)
	}

	// A target subject (explains via GetTarget).
	if _, _, err := execFleet(t, append([]string{"fleet", "explain", "prod/k8s/orders"}, base...)...); err != nil {
		t.Fatalf("explain target: %v", err)
	}

	// JSON.
	if _, _, err := execFleet(t, append([]string{"fleet", "explain", "orders", "--output-format", "json"}, base...)...); err != nil {
		t.Fatalf("explain json: %v", err)
	}

	// Unknown subject → error.
	if _, _, err := execFleet(t, append([]string{"fleet", "explain", "nope"}, base...)...); err == nil {
		t.Error("expected error for unknown explain subject")
	}
}

func TestFleetPartialWarning(t *testing.T) {
	root, _ := writeFleetFixture(t)
	// Point --target-state at a missing file → the target-state source is unavailable →
	// the answer is partial → warnPartial prints to stderr.
	_, stderr, err := execFleet(t, "fleet", "search", "--local", root, "--target-state", filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(stderr, "warning: answer is partial") {
		t.Errorf("expected partial warning on stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "SOURCE_UNAVAILABLE") {
		t.Errorf("expected SOURCE_UNAVAILABLE limitation on stderr:\n%s", stderr)
	}
}

// TestFleetEvidenceURL proves the --evidence-url flag reaches EvidenceURLs and
// its read-only HTTP contribution appears in the snapshot.
func TestFleetEvidenceURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"targets":[{"subject":"svc-a","producer":"prod-eu","compliance":"Compliant","coverage":{"evaluated":3,"required":5},"observedAt":"2026-07-29T11:00:00Z"}]}`))
	}))
	defer srv.Close()

	out, _, err := execFleet(t, "fleet", "snapshot", "--local", t.TempDir(), "--evidence-url", srv.URL, "--output-format", "json")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(out, "evidence-http") || !strings.Contains(out, "svc-a") {
		t.Errorf("expected the evidence-http source and svc-a target in snapshot:\n%s", out)
	}
}

func TestFleetBuildError(t *testing.T) {
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	root, ev := writeFleetFixture(t)

	// A cancelled context is fatal in fleet.Build, so every subcommand — those
	// going through buildQuery and snapshot (svc.Fleet directly) — must surface
	// the error rather than silently returning an empty answer.
	subcommands := [][]string{
		{"fleet", "search"},
		{"fleet", "get", "orders"},
		{"fleet", "graph", "orders"},
		{"fleet", "status"},
		{"fleet", "explain", "orders"},
		{"fleet", "snapshot"},
	}
	for _, sc := range subcommands {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		svc := app.NewService(nil, nil)
		root0 := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
		root0.SetArgs(append(append([]string{}, sc...), "--local", root, "--target-state", ev))
		var o, e bytes.Buffer
		root0.SetOut(&o)
		root0.SetErr(&e)
		if err := root0.ExecuteContext(ctx); err == nil {
			t.Errorf("expected error for %v when the build context is cancelled", sc)
		}
	}
}
