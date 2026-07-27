package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Release-safety item 4: production must publish OCI artifacts through the SAME
// shared adapter the staging dry-run uses (release/orchestrator/publish-oci-unit.sh),
// not reimplement the verify -> build/push -> record state transitions inline in
// YAML. This gate fails if an OCI publisher stops routing through the adapter or
// reintroduces an inline build-push-action, so staging and production cannot drift.
func TestOciPublishersUseSharedAdapter(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	jobs := jobsOf(t, string(b))

	for _, j := range []string{"dashboard-image", "operator-image", "dashboard-contract-bundle"} {
		block, ok := jobs[j]
		if !ok {
			t.Errorf("release.yml has no %q job", j)
			continue
		}
		if !strings.Contains(block, "publish-oci-unit.sh") {
			t.Errorf("job %q does not route through the shared adapter publish-oci-unit.sh (item 4: no inline state transitions)", j)
		}
		if strings.Contains(block, "build-push-action") {
			t.Errorf("job %q still uses build-push-action inline instead of the shared adapter (item 4)", j)
		}
	}

	// The shared adapter must genuinely be shared: the staging dry-run calls it too.
	dr, err := os.ReadFile(filepath.Join(root, "release", "orchestrator", "dry-run.sh"))
	if err != nil {
		t.Fatalf("read dry-run.sh: %v", err)
	}
	if !strings.Contains(string(dr), "publish-oci-unit.sh") {
		t.Error("the staging dry-run does not exercise the shared adapter publish-oci-unit.sh (item 4)")
	}
}
