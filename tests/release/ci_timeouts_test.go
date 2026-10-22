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
