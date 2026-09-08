package architecture

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Every kind acceptance scenario must reach the node through ONE image-loading
// boundary — lib.sh's load_images, which delegates the platform decisions to
// tests/acceptance/kind/kindload. A scenario that calls `kind load` itself is
// back on the path that breaks under Docker Desktop's containerd image store,
// and it breaks alone: the shared boundary can be fixed once, a private copy
// cannot.

func kindDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "acceptance", "kind")
}

// code returns a script with its full-line comments removed, so a rule about
// what the harness DOES is not tripped by prose explaining what it must not do.
func code(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a path this test computed
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func scenarios(t *testing.T) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(kindDir(t), "*.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, path := range found {
		if filepath.Base(path) != "lib.sh" {
			out = append(out, path)
		}
	}
	if len(out) < 8 {
		t.Fatalf("expected at least eight kind scenarios, found %d: %v", len(out), out)
	}
	return out
}

// kindMatrixScenarios returns the scenario list the ci-e2e-kind job shards over.
func kindMatrixScenarios(t *testing.T) []string {
	t.Helper()
	wf := readFile(t, filepath.Join(repoDir(t), ".github", "workflows", "ci.yml"))
	_, after, ok := strings.Cut(wf, "scenario: [")
	if !ok {
		t.Fatal("no `scenario: [...]` matrix in .github/workflows/ci.yml; the shards are wired some other way now")
	}
	list, _, ok := strings.Cut(after, "]")
	if !ok {
		t.Fatal("the ci-e2e-kind scenario matrix has no closing bracket")
	}
	var out []string
	for name := range strings.SplitSeq(list, ",") {
		out = append(out, strings.TrimSpace(name))
	}
	return out
}

// TestEveryKindScenarioRuns is the tripwire for a scenario that exists and never
// executes. A script is only a gate if something invokes it: ci.mk needs a target
// whose recipe runs it, and that target's name has to appear in the ci-e2e-kind
// matrix. Miss either half and the pipeline stays green with one fewer thing
// proved — and nothing in the repository looks wrong, which is the whole problem.
func TestEveryKindScenarioRuns(t *testing.T) {
	mk := readFile(t, filepath.Join(repoDir(t), "ci.mk"))
	matrix := kindMatrixScenarios(t)
	const prefix = "test-acceptance-kind-"
	for _, path := range scenarios(t) {
		script := "bash tests/acceptance/kind/" + filepath.Base(path)
		before, _, ok := strings.Cut(mk, script)
		if !ok {
			t.Errorf("no ci.mk recipe runs %s, so the scenario runs nowhere", filepath.Base(path))
			continue
		}
		// The target owning that recipe is the nearest rule declared above it.
		target := ""
		for line := range strings.SplitSeq(before, "\n") {
			if name, _, isRule := strings.Cut(line, ":"); isRule && strings.HasPrefix(name, prefix) {
				target = strings.TrimPrefix(name, prefix)
			}
		}
		if target == "" {
			t.Errorf("%s is run by a ci.mk recipe with no %s* target above it", filepath.Base(path), prefix)
			continue
		}
		if !slices.Contains(matrix, target) {
			t.Errorf("%s runs under `make %s%s`, which the ci-e2e-kind matrix does not list, "+
				"so CI never runs it (matrix: %v)", filepath.Base(path), prefix, target, matrix)
		}
	}
}

func TestScenariosLoadImagesThroughTheSharedBoundary(t *testing.T) {
	for _, path := range scenarios(t) {
		if body := code(t, path); strings.Contains(body, "kind load") {
			t.Errorf("%s calls `kind load` directly; use load_images so the platform "+
				"handling stays in one place", filepath.Base(path))
		}
	}
}

func TestLoadImagesDelegatesToTheKindloadHelper(t *testing.T) {
	body := code(t, filepath.Join(kindDir(t), "lib.sh"))
	if !strings.Contains(body, "./tests/acceptance/kind/kindload") {
		t.Fatal("load_images must delegate to the kindload helper")
	}
	if strings.Contains(body, "kind load docker-image") {
		t.Fatal("lib.sh still loads via `kind load docker-image`, which cannot import a " +
			"partially-materialized multi-platform image")
	}
	if _, err := os.Stat(filepath.Join(kindDir(t), "kindload", "main.go")); err != nil {
		t.Fatalf("the helper lib.sh delegates to is missing: %v", err)
	}
}

// install_registry pulls a multi-platform registry tag whose local copy is the
// artifact that used to break the load. It must hand that pull to the shared
// boundary, and it must not carry a private flattening step whose result a later
// pull would silently overwrite. It must also pull, load and DEPLOY the same
// image: a pull narrowed for one reference and a Deployment naming another is a
// node that silently reaches for the internet.
func TestInstallRegistryLoadsThroughTheSharedBoundary(t *testing.T) {
	body := code(t, filepath.Join(kindDir(t), "lib.sh"))
	_, after, ok := strings.Cut(body, "install_registry() {")
	if !ok {
		t.Fatal("install_registry not found in lib.sh")
	}
	fn, _, ok := strings.Cut(after, "\n}")
	if !ok {
		t.Fatal("install_registry has no closing brace")
	}
	for _, want := range []string{
		`docker pull "$PACTO_REGISTRY_IMAGE"`,
		`load_images "$PACTO_REGISTRY_IMAGE"`,
		"image: $PACTO_REGISTRY_IMAGE",
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("install_registry must contain %q so one pin covers pull, load and deploy", want)
		}
	}
	for _, forbidden := range []string{"kind load", "docker tag", "docker save", "docker import"} {
		if strings.Contains(fn, forbidden) {
			t.Errorf("install_registry must not %q: image handling belongs to load_images", forbidden)
		}
	}
	// imagePullPolicy: Never is the reason any of this matters. Without it the
	// node would quietly pull the registry from the internet and the scenario
	// would pass while proving nothing about what was loaded.
	if !strings.Contains(fn, "imagePullPolicy: Never") {
		t.Error("the in-cluster registry must keep imagePullPolicy: Never")
	}
}

// The in-cluster registry must serve the native OCI 1.1 Referrers API, because
// accepted evidence is a referrer of a contract revision and Pacto refuses the
// legacy referrers-tag fallback. CNCF distribution (`registry:2`, `registry:3`)
// implements no referrers endpoint, and the ORAS CLI papers over that with a
// client-side tag fallback — so a harness on distribution would look healthy
// while the Evidence Server could never become ready.
func TestInClusterRegistrySupportsNativeReferrers(t *testing.T) {
	body := code(t, filepath.Join(kindDir(t), "lib.sh"))
	if strings.Contains(body, "PACTO_REGISTRY_IMAGE=\"registry:") {
		t.Error("the in-cluster registry is CNCF distribution, which serves no Referrers API")
	}
	if !strings.Contains(body, `PACTO_REGISTRY_IMAGE="ghcr.io/project-zot/zot-minimal:`) {
		t.Error("PACTO_REGISTRY_IMAGE must pin a registry with a native Referrers API")
	}
}
