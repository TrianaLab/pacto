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
// the major suffix (…/integrations/kubernetes/v5), which is DISTINCT from the source
// directory (integrations/kubernetes/), the module tag (integrations/kubernetes/v5.0.0)
// and the OCI image/chart coordinates (ghcr.io/…, unchanged). This gate keeps every
// declared module coordinate in sync with go.mod and fails if any active file
// announces the bare (missing-/v5) module path as the Go module.

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

func TestKubernetesModulePathIsV5Everywhere(t *testing.T) {
	root := repoRoot(t)
	mod := k8sModulePath(t, root)
	if !strings.HasSuffix(mod, "/v5") {
		t.Fatalf("operator go.mod module %q must carry the /v5 major suffix", mod)
	}
	bare := strings.TrimSuffix(mod, "/v5") // github.com/…/integrations/kubernetes

	// Surfaces that DECLARE the module coordinate must use the exact /v5 path.
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
	// path without /v5). Directory references use github.com/<owner>/<repo>/tree/…,
	// which does not contain the bare module path, so they are unaffected.
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
		if e != nil {
			continue
		}
		// Remove every correct "<bare>/v5" occurrence; anything left is a bare path.
		stripped := strings.ReplaceAll(string(b), bare+"/v5", "")
		if strings.Contains(stripped, bare) {
			for i, line := range strings.Split(string(b), "\n") {
				if strings.Contains(strings.ReplaceAll(line, bare+"/v5", ""), bare) {
					t.Errorf("%s:%d announces the bare Kubernetes module path (missing /v5): %s", rel, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
