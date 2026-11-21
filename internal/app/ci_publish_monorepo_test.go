package app

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/ci"
)

// TestCIPublishMonorepoThenUnchanged: the demo monorepo, whose contracts are
// empty or two lines, plans with every name inferred, so publish refuses
// both rows and writes nothing (an inferred name never reaches a bundle).
// Declaring each name, still within two lines, publishes both services at a
// derived 1.0.0 with no build (every image is pinned by digest), and the
// next plan reads both unchanged with exit 0.
func TestCIPublishMonorepoThenUnchanged(t *testing.T) {
	e := newCIPub(t)
	if err := os.CopyFS(e.root, os.DirFS("../../examples/demo/monorepo")); err != nil {
		t.Fatal(err)
	}
	opts := CIOptions{Root: e.root, Registry: e.host + "/acme", Getenv: mapEnv(nil)}
	publish := func(rev string) *ci.Plan {
		t.Helper()
		p, err := e.svc.CIPublish(t.Context(), CIPublishOptions{CI: opts, Revision: rev, SignKey: e.key, NoTransparencyLog: true})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	everyRow(t, "inferred names", publish("r1"), 1, func(r ci.Row) bool {
		return r.State == ci.StateFailed && strings.HasPrefix(r.Reason, "PUBLISH_NEEDS_SERVICE_NAME: service.name is inferred")
	})
	if n := e.reg.writes.Load(); n != 0 {
		t.Fatalf("inferred names wrote %d times", n)
	}
	e.write(t, map[string]string{
		"services/orders/pacto.yaml":  "service: {name: orders}\n",
		"services/billing/pacto.yaml": "service: {name: billing, owner: {team: billing}}\n",
	})
	everyRow(t, "declared names", publish("r2"), 0, func(r ci.Row) bool { return r.State == ci.StatePublished && r.To == "1.0.0" })
	if built := slices.DeleteFunc(slices.Clone(e.run.calls), func(c string) bool { return slices.Contains(preflights, c) || strings.HasPrefix(c, "git ") }); len(built) != 0 {
		t.Fatalf("ran a builder: %q", built)
	}
	again, _, err := e.svc.CIPlan(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	everyRow(t, "second plan", again, 0, func(r ci.Row) bool { return r.Changed == "no" })
}

// everyRow fails unless p exits exit with billing and orders rows that all
// satisfy ok.
func everyRow(t *testing.T, step string, p *ci.Plan, exit int, ok func(ci.Row) bool) {
	t.Helper()
	if p.Exit != exit || !slices.Equal(services(p), []string{"billing", "orders"}) || slices.ContainsFunc(p.Rows, func(r ci.Row) bool { return !ok(r) }) {
		t.Fatalf("%s: exit %d rows %+v", step, p.Exit, p.Rows)
	}
}
