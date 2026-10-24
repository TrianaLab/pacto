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
	// The delivery chart is published the same way, so it carries the same wiring.
	for _, job := range []string{"operator-chart", "delivery-chart"} {
		chart, ok := jobsOf(t, string(wf))[job]
		if !ok {
			t.Errorf("release.yml has no %s job", job)
			continue
		}
		// 2. The verify call must pass the source revision + version so `adopt` can fire.
		if !strings.Contains(chart, "source_sha") {
			t.Errorf("%s verify does not pass source_sha provenance; a push-before-record crash stays unrecoverable", job)
		}
		// 3. The chart must be stamped with the revision, and an adopt step must handle
		//    the recovery.
		if !strings.Contains(chart, "org.opencontainers.image.revision") {
			t.Errorf("%s does not stamp the source revision onto the chart manifest", job)
		}
		if !strings.Contains(chart, "state == 'adopt'") {
			t.Errorf("%s has no adopt step to record an already-pushed chart on resume", job)
		}
	}
}
