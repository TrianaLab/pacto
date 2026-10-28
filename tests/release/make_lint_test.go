package release

import (
	"os/exec"
	"strings"
	"testing"
)

// CONTRIBUTING.md and the PR template send contributors to `make lint`. It used
// to run a bare `gofmt -s -l`, which prints unformatted files and exits 0, so it
// passed on code ci-fmt rejects. lint now runs the CI gates themselves.
func TestMakeLintRunsTheCIGates(t *testing.T) {
	cmd := exec.Command("make", "-n", "lint")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n lint: %v\n%s", err, out)
	}
	for _, want := range []string{`test -z "$(gofmt -l .)"`, "go vet ./...", "check-section-sign.sh"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("make lint does not run %q:\n%s", want, out)
		}
	}
}
