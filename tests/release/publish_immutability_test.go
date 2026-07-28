package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The demo-bundles byte-exact immutability gate in publish-demo-bundles.sh only runs
// when crane is on PATH. If crane is missing it must NOT silently publish over an
// existing immutable coordinate: fail closed in production mode, and the release job
// must actually install crane so the gate runs.
func TestDemoBundlesImmutabilityGateFailsClosedWithoutCrane(t *testing.T) {
	root := repoRoot(t)

	script, err := os.ReadFile(filepath.Join(root, "release", "scripts", "publish-demo-bundles.sh"))
	if err != nil {
		t.Fatalf("read publish-demo-bundles.sh: %v", err)
	}
	s := string(script)
	// A crane-missing branch must refuse the production publish rather than skip the gate.
	if !strings.Contains(s, `elif [ "$PROD" = "1" ]; then`) {
		t.Error("publish-demo-bundles.sh does not fail closed when crane is missing in production mode")
	}

	wf, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	demo, ok := jobsOf(t, string(wf))["demo-bundles"]
	if !ok {
		t.Fatal("release.yml has no demo-bundles job")
	}
	if !strings.Contains(demo, "cmd/crane") {
		t.Error("demo-bundles job does not install crane, so the byte-exact immutability gate never runs")
	}
}

// A resumed finalize must overwrite assets a prior interrupted run already uploaded
// (--clobber) instead of failing on the ones that already exist.
func TestFinalizeReleaseResumeUsesClobber(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "release", "orchestrator", "finalize-release.sh"))
	if err != nil {
		t.Fatalf("read finalize-release.sh: %v", err)
	}
	if !strings.Contains(string(b), "gh release upload") || !strings.Contains(string(b), "--clobber") {
		t.Error("finalize-release.sh resume upload does not use --clobber; a partial prior run makes the resume fail")
	}
}
