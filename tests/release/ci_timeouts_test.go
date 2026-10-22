package release

import (
	"maps"
	"slices"
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
