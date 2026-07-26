package release

// Gate: every OCI reference declared by a local demo bundle (examples/demo/bundles)
// must resolve to the monorepo-OWNED demo coordinate — the `demo-bundles` release
// unit's coordinate in release/release-manifest.json. This prevents the
// "local v2 bundle depends on an unowned/stale published coordinate" defect: if a
// bundle referenced a coordinate no monorepo publisher owns, live `pacto
// validate`/`lock` would resolve it to whatever stale artifact happens to sit
// there. Ownership + this static check keep the two in lockstep with no network.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/graph"
)

// ownedDemoCoordinate returns the coordinate of the `demo-bundles` release unit,
// i.e. the OCI namespace the monorepo publishes the demo bundles under.
func ownedDemoCoordinate(t *testing.T, root string) string {
	t.Helper()
	m := loadManifest(t, root)
	u, ok := m.Units["demo-bundles"]
	if !ok {
		t.Fatal("release manifest has no `demo-bundles` unit — the demo OCI bundles are unowned")
	}
	if u.Coordinate == "" {
		t.Fatal("`demo-bundles` unit has an empty coordinate")
	}
	return u.Coordinate
}

// demoRefViolations returns, for each OCI ref that does NOT resolve under
// ownedCoord, a human-readable violation string. Pure over its inputs so its
// teeth are unit-testable without touching the filesystem.
func demoRefViolations(ownedCoord string, refs []string) []string {
	var out []string
	for _, ref := range refs {
		parsed := graph.ParseDependencyRef(ref)
		if !parsed.IsOCI() {
			continue // local/file refs never hit a registry
		}
		if !strings.HasPrefix(parsed.Location, ownedCoord+"/") {
			out = append(out, ref+"  (not under owned coordinate "+ownedCoord+")")
		}
	}
	return out
}

// demoBundleOCIRefs walks examples/demo/bundles and returns every OCI ref a
// contract declares (dependencies + config/policy references), each labelled
// with its source file for a legible failure.
func demoBundleOCIRefs(t *testing.T, root string) (labelled []string, raw []string) {
	t.Helper()
	bundlesDir := filepath.Join(root, "examples", "demo", "bundles")
	err := filepath.Walk(bundlesDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || info.Name() != "pacto.yaml" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		c, err := contract.Parse(strings.NewReader(string(data)))
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		rel, _ := filepath.Rel(root, p)
		add := func(ref string) {
			if ref == "" {
				return
			}
			raw = append(raw, ref)
			labelled = append(labelled, rel+": "+ref)
		}
		for _, d := range c.Dependencies {
			add(d.Ref)
		}
		for _, r := range c.ReferenceRefs() {
			add(r.Ref)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk demo bundles: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("no OCI refs found under examples/demo/bundles — walk is broken")
	}
	return labelled, raw
}

// TestDemoBundlesReferenceOwnedCoordinate is the regression gate: no committed
// demo bundle may declare a ref outside the owned demo coordinate.
func TestDemoBundlesReferenceOwnedCoordinate(t *testing.T) {
	root := repoRoot(t)
	owned := ownedDemoCoordinate(t, root)
	labelled, raw := demoBundleOCIRefs(t, root)

	// Re-derive per-ref so the failure message keeps the source-file label.
	var bad []string
	for _, l := range labelled {
		ref := l[strings.LastIndex(l, ": ")+2:]
		if len(demoRefViolations(owned, []string{ref})) > 0 {
			bad = append(bad, l)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("demo bundles reference %d unowned/foreign coordinate(s) (must be under %q):\n  %s",
			len(bad), owned, strings.Join(bad, "\n  "))
	}
	t.Logf("checked %d OCI refs across examples/demo/bundles — all under owned coordinate %q", len(raw), owned)
}

// TestDemoRefGateHasTeeth proves the gate flags a foreign/unowned coordinate and
// accepts an owned one — so a real regression cannot slip past silently.
func TestDemoRefGateHasTeeth(t *testing.T) {
	const owned = "ghcr.io/trianalab/pacto-demo"

	// A ref under the owned namespace passes.
	if v := demoRefViolations(owned, []string{"oci://" + owned + "/redis"}); len(v) != 0 {
		t.Errorf("owned ref wrongly flagged: %v", v)
	}
	// Foreign registry, foreign namespace and an unowned sibling all fail.
	probes := []string{
		"oci://ghcr.io/someone-else/pacto-demo/redis",  // foreign namespace
		"oci://ghcr.io/trianalab/pacto-demo-old/redis", // unowned sibling coordinate
		"oci://registry.example.com/pacto-demo/redis",  // foreign registry
	}
	for _, p := range probes {
		if v := demoRefViolations(owned, []string{p}); len(v) != 1 {
			t.Errorf("bad ref %q was NOT flagged (gate has no teeth): %v", p, v)
		}
	}
}
