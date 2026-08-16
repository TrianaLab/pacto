package architecture

import (
	"os"
	"path/filepath"
	"runtime"
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
	if len(out) < 6 {
		t.Fatalf("expected the six kind scenarios, found %d: %v", len(out), out)
	}
	return out
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

// install_registry pulls registry:2 — a multi-platform tag whose local copy is
// the artifact that used to break the load. It must hand that pull to the shared
// boundary, and it must not carry a private flattening step whose result a later
// pull would silently overwrite.
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
	if !strings.Contains(fn, "load_images registry:2") {
		t.Error("install_registry must load registry:2 through load_images")
	}
	for _, forbidden := range []string{"kind load", "docker tag", "docker save", "docker import"} {
		if strings.Contains(fn, forbidden) {
			t.Errorf("install_registry must not %q: image handling belongs to load_images", forbidden)
		}
	}
	// imagePullPolicy: Never is the reason any of this matters. Without it the
	// node would quietly pull registry:2 from Docker Hub and the scenario would
	// pass while proving nothing about what was loaded.
	if !strings.Contains(fn, "imagePullPolicy: Never") {
		t.Error("the in-cluster registry must keep imagePullPolicy: Never")
	}
}
