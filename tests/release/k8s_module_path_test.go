package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Kubernetes integration is a nested Go module. Its module/import path carries
// the major suffix (…/integrations/kubernetes/v6), which is DISTINCT from the source
// directory (integrations/kubernetes/), the module tag (integrations/kubernetes/v6.0.0)
// and the OCI image/chart coordinates (ghcr.io/…, unchanged). This gate keeps every
// declared module coordinate in sync with go.mod and fails if any active file
// announces the bare (missing-/v6) module path as the Go module.

// k8sModulePath reads the authoritative module path from the operator go.mod.
func k8sModulePath(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "integrations", "kubernetes", "go.mod"))
	if err != nil {
		t.Fatalf("read operator go.mod: %v", err)
	}
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no module line in operator go.mod")
	}
	return m[1]
}

func TestKubernetesModulePathIsV6Everywhere(t *testing.T) {
	root := repoRoot(t)
	mod := k8sModulePath(t, root)
	if !strings.HasSuffix(mod, "/v6") {
		t.Fatalf("operator go.mod module %q must carry the /v6 major suffix", mod)
	}
	bare := strings.TrimSuffix(mod, "/v6") // github.com/…/integrations/kubernetes

	// Surfaces that DECLARE the module coordinate must use the exact /v6 path.
	for _, f := range []string{
		"release/release-manifest.json",
		"release/release-plan.json",
		"integrations/kubernetes/integration.yaml",
	} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Errorf("read %s: %v", f, err)
			continue
		}
		if !strings.Contains(string(b), mod) {
			t.Errorf("%s does not declare the Kubernetes module coordinate %q", f, mod)
		}
	}

	// No active, git-tracked text file may announce the BARE module path (the module
	// path without /v6) or a previous major's. History may name a previous major: the
	// CHANGELOGs, the v1-to-v2 migration guide and the last release's transaction record
	// what was true when they were written. Directory references use
	// github.com/<owner>/<repo>/tree/…, which does not contain the bare module path, so
	// they are unaffected.
	history := func(rel string) bool {
		return strings.HasSuffix(rel, "CHANGELOG.md") || rel == "MIGRATION.md" || rel == "release/release-transaction.json"
	}
	ref := regexp.MustCompile(regexp.QuoteMeta(bare) + `(/v\d+)?\b`)
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	textExt := map[string]bool{".md": true, ".yaml": true, ".yml": true, ".json": true, ".go": true, ".mod": true, ".txt": true}
	for _, rel := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if rel == "" || strings.HasPrefix(rel, "pkg/dashboard/ui/") || strings.HasSuffix(rel, "go.sum") {
			continue
		}
		if !textExt[filepath.Ext(rel)] {
			continue
		}
		b, e := os.ReadFile(filepath.Join(root, rel))
		if e != nil || !strings.Contains(string(b), bare) {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range ref.FindAllStringSubmatch(line, -1) {
				switch {
				case m[1] == "":
					t.Errorf("%s:%d announces the bare Kubernetes module path (missing /v6): %s", rel, i+1, strings.TrimSpace(line))
				case m[1] != "/v6" && !history(rel):
					t.Errorf("%s:%d announces a previous major of the Kubernetes module path (%s, not /v6): %s", rel, i+1, m[1], strings.TrimSpace(line))
				}
			}
		}
	}
}
