// Package release holds the machine-checkable "exactly one canonical publisher
// per artifact" gate for the Pacto monorepo.
//
// Every release unit in release/release-manifest.json MUST be published by
// exactly one GitHub Actions workflow, and no published registry coordinate may
// be pushed by more than one workflow. The consolidated .github/workflows/
// release.yml is the single publisher; this gate fails the build if a second
// workflow ever starts producing the same artifact.
package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoRoot resolves the monorepo root from this test file's location, so the
// gate is independent of the working directory `go test` runs in.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(self), "..", ".."))
}

type manifest struct {
	Units map[string]struct {
		ArtifactKind string `json:"artifactKind"`
		Coordinate   string `json:"coordinate"`
	} `json:"units"`
}

func loadManifest(t *testing.T, root string) manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "release", "release-manifest.json"))
	if err != nil {
		t.Fatalf("read release manifest: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse release manifest: %v", err)
	}
	if len(m.Units) == 0 {
		t.Fatal("release manifest has no units")
	}
	return m
}

type workflow struct {
	name string
	text string
}

func loadWorkflows(t *testing.T, root string) []workflow {
	t.Helper()
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows dir: %v", err)
	}
	var out []workflow
	for _, e := range entries {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		// Parsing every workflow doubles as a well-formedness gate.
		var doc any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("workflow %s is not valid YAML: %v", e.Name(), err)
		}
		out = append(out, workflow{name: e.Name(), text: string(b)})
	}
	return out
}

var publishesRE = regexp.MustCompile(`pacto-publishes:\s*([a-z0-9-]+)`)

// TestExactlyOnePublisherPerUnit: every manifest unit is declared by exactly one
// workflow via a `pacto-publishes: <unit>` marker (zero or duplicate fails).
func TestExactlyOnePublisherPerUnit(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	workflows := loadWorkflows(t, root)

	// unit -> set of workflows that declare it.
	declared := map[string]map[string]bool{}
	for _, w := range workflows {
		for _, match := range publishesRE.FindAllStringSubmatch(w.text, -1) {
			unit := match[1]
			if declared[unit] == nil {
				declared[unit] = map[string]bool{}
			}
			declared[unit][w.name] = true
		}
	}

	for unit := range m.Units {
		switch n := len(declared[unit]); {
		case n == 0:
			t.Errorf("release unit %q has NO publisher (no `pacto-publishes: %s` marker in any workflow)", unit, unit)
		case n > 1:
			t.Errorf("release unit %q has %d publishers %v — exactly one workflow must publish it", unit, n, keys(declared[unit]))
		}
	}
	// A declared publisher with no matching release unit is also a defect.
	for unit, wfs := range declared {
		if _, ok := m.Units[unit]; !ok {
			t.Errorf("workflow(s) %v declare `pacto-publishes: %s` but %q is not a release unit", keys(wfs), unit, unit)
		}
	}
}

// TestNoDuplicateRegistryCoordinate: each published registry coordinate (image
// repo / chart) appears in exactly the one workflow that declares its unit, and
// in no other workflow.
func TestNoDuplicateRegistryCoordinate(t *testing.T) {
	root := repoRoot(t)
	m := loadManifest(t, root)
	workflows := loadWorkflows(t, root)

	for unit, u := range m.Units {
		if u.ArtifactKind != "oci-image" && u.ArtifactKind != "helm-chart" {
			continue // non-registry coordinates are covered by the marker gate
		}
		var producing []string
		for _, w := range workflows {
			if strings.Contains(w.text, u.Coordinate) {
				producing = append(producing, w.name)
			}
		}
		switch {
		case len(producing) == 0:
			t.Errorf("unit %q coordinate %q is not pushed by any workflow", unit, u.Coordinate)
		case len(producing) > 1:
			t.Errorf("unit %q coordinate %q is pushed by more than one workflow %v — collapse to one canonical publisher", unit, u.Coordinate, producing)
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
