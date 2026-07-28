package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A push-before-record crash in the operator-chart job must be recoverable: on
// resume verify-oci.sh has to be able to `adopt` the already-pushed chart by its OCI
// manifest provenance (org.opencontainers.image.revision/version). That requires
// three things wired together, asserted here so none regress.
func TestOperatorChartCrashRecoveryWiring(t *testing.T) {
	root := repoRoot(t)

	// 1. verify-oci.sh must read manifest annotations (helm charts carry provenance
	//    there, not in a docker-style config Labels block).
	vo, err := os.ReadFile(filepath.Join(root, "release", "orchestrator", "verify-oci.sh"))
	if err != nil {
		t.Fatalf("read verify-oci.sh: %v", err)
	}
	if !strings.Contains(string(vo), "crane manifest") || !strings.Contains(string(vo), ".annotations[") {
		t.Error("verify-oci.sh does not fall back to OCI manifest annotations; helm-chart provenance adoption cannot work")
	}

	wf, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	chart, ok := jobsOf(t, string(wf))["operator-chart"]
	if !ok {
		t.Fatal("release.yml has no operator-chart job")
	}
	// 2. The verify call must pass the source revision + version so `adopt` can fire.
	if !strings.Contains(chart, "source_sha") {
		t.Error("operator-chart verify does not pass source_sha provenance; a push-before-record crash stays unrecoverable")
	}
	// 3. The chart must be stamped with the revision, and an adopt step must handle
	//    the recovery.
	if !strings.Contains(chart, "org.opencontainers.image.revision") {
		t.Error("operator-chart does not stamp the source revision onto the chart manifest")
	}
	if !strings.Contains(chart, "state == 'adopt'") {
		t.Error("operator-chart has no adopt step to record an already-pushed chart on resume")
	}
}
