package release

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Without timeout-minutes a GitHub job may run for six hours. That happened: on
// 2026-08-19 two kind and Compose jobs froze for 97 minutes in a make step,
// holding the PR with no failure to read. Every CI job therefore declares its
// own bound, so a wedged runner fails in minutes and says where.
func TestEveryCIJobHasATimeout(t *testing.T) {
	root := repoRoot(t)
	for _, file := range []string{"ci.yml", "delivery-demo.yml"} {
		jobs := workflowJobs(t, root, file)
		for _, name := range slices.Sorted(maps.Keys(jobs)) {
			// A job that calls a reusable workflow cannot set a timeout; the
			// called workflow's jobs carry theirs, and this test reads them.
			if u := jobs[name].Uses; u != "" {
				if !slices.Contains([]string{"ci.yml", "delivery-demo.yml"}, strings.TrimPrefix(u, "./.github/workflows/")) {
					t.Errorf("%s job %q calls %s, whose job timeouts this test does not read", file, name, u)
				}
				continue
			}
			if jobs[name].TimeoutMinutes == nil {
				t.Errorf("%s job %q has no timeout-minutes, so a wedged runner holds it for six hours", file, name)
			}
		}
	}
}

// A job timeout cancels the job, and `if: failure()` steps do not run on a
// cancellation. So when a step hangs to its own timeout, the diagnostics and
// upload steps after it only run if the job timeout leaves room for every step
// timeout plus setup. Otherwise a hang, the case the timeouts exist for, loses
// its failure artifacts.
func TestJobTimeoutsCoverTheirStepTimeouts(t *testing.T) {
	const setup = 5 // checkout, toolchain setup and other steps without a bound
	root := repoRoot(t)
	for _, file := range []string{"ci.yml", "delivery-demo.yml"} {
		jobs := workflowJobs(t, root, file)
		for _, name := range slices.Sorted(maps.Keys(jobs)) {
			job, ok := jobs[name].TimeoutMinutes.(int)
			if !ok {
				continue // missing: TestEveryCIJobHasATimeout reports it
			}
			sum := 0
			for _, s := range jobs[name].Steps {
				sum += s.TimeoutMinutes
			}
			if sum > 0 && job < sum+setup {
				t.Errorf("%s job %q allows %d minutes but its steps allow %d plus %d for setup, so a hung step can cancel the job before its failure steps run",
					file, name, job, sum, setup)
			}
		}
	}
}

// The release jobs this delivery work added, and the terminal verify, carry
// their own bound too; the older release jobs predate the rule.
// ponytail: only these jobs are listed; extend to every release job when the
// older ones get their timeouts.
func TestNewReleaseJobsHaveATimeout(t *testing.T) {
	jobs := workflowJobs(t, repoRoot(t), "release.yml")
	for _, name := range []string{"delivery-image-build", "delivery-image", "verify-transaction"} {
		if j, ok := jobs[name]; !ok || j.TimeoutMinutes == nil {
			t.Errorf("release.yml job %q has no timeout-minutes", name)
		}
	}
}

// verify-public.sh probes the registry anonymously at the end of a release; a
// stalled endpoint must fail it in seconds, not hold the run for six hours.
func TestVerifyPublicCurlsHaveATimeout(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "release", "orchestrator", "verify-public.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "curl ") && !strings.Contains(line, "--max-time") {
			t.Errorf("verify-public.sh:%d calls curl with no --max-time: %s", i+1, strings.TrimSpace(line))
		}
	}
}
