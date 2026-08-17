package release

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// This gate extracts the release.yml job DAG (needs + the unit each publisher
// gates on) and simulates which jobs run for a given changedUnits set, so a
// wiring mistake — like a Kubernetes publisher depending on the conditionally
// skipped core-tag instead of the always-run core-ready barrier — fails the
// build. It models GitHub's skip semantics:
//   - detect is the always-present root.
//   - a job with `if: always()` runs whenever the release fires, regardless of a
//     skipped dependency (that is how core-ready survives a skipped core-tag).
//   - any other job runs only if its unit gate matches changedUnits AND every
//     dependency ran successfully; a skipped/absent dependency skips it.

type relJob struct {
	needs   []string
	unit    string // the changedUnits entry this job gates on ("" = no unit gate)
	always  bool
	release bool // gated on release == 'true'
}

var unitGateRE = regexp.MustCompile(`contains\(fromJSON\(needs\.detect\.outputs\.units_json\),\s*'([a-z0-9-]+)'\)`)

func loadReleaseDAG(t *testing.T) map[string]relJob {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	var doc struct {
		Jobs map[string]struct {
			Needs any    `yaml:"needs"`
			If    string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse release.yml: %v", err)
	}
	out := map[string]relJob{}
	for name, j := range doc.Jobs {
		rj := relJob{
			always:  strings.Contains(j.If, "always()"),
			release: strings.Contains(j.If, "needs.detect.outputs.release == 'true'"),
		}
		switch n := j.Needs.(type) {
		case string:
			rj.needs = []string{n}
		case []any:
			for _, x := range n {
				rj.needs = append(rj.needs, x.(string))
			}
		}
		if m := unitGateRE.FindStringSubmatch(j.If); m != nil {
			rj.unit = m[1]
		}
		out[name] = rj
	}
	return out
}

// simulate returns the set of jobs that RUN for a release with the given
// changedUnits (detect + changesets excluded from the result).
func simulate(dag map[string]relJob, changedUnits []string) map[string]bool {
	inUnits := map[string]bool{}
	for _, u := range changedUnits {
		inUnits[u] = true
	}
	ran := map[string]bool{"detect": true} // detect always resolves for a release
	// Fixpoint: a job runs when its gate holds and its deps are satisfied.
	for i := 0; i < len(dag)+2; i++ {
		for name, j := range dag {
			if name == "detect" || name == "changesets" {
				continue
			}
			if ran[name] {
				continue
			}
			// unit gate
			if j.unit != "" && !inUnits[j.unit] {
				continue
			}
			// dependency satisfaction
			depsOK := true
			for _, d := range j.needs {
				if d == "detect" {
					continue
				}
				if !ran[d] {
					// an always() job survives a skipped dep; others do not.
					if !j.always {
						depsOK = false
						break
					}
				}
			}
			if !depsOK {
				continue
			}
			ran[name] = true
		}
	}
	delete(ran, "detect")
	return ran
}

func names(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestReleaseDAGSelection(t *testing.T) {
	dag := loadReleaseDAG(t)

	core := []string{"core", "cli", "dashboard-image", "dashboard-contract-bundle", "demo-bundles", "demo-compose"}
	k8s := []string{"k8s-module", "operator-image", "operator-chart", "k8s-docs"}
	k8sPublishers := []string{"k8s-module", "operator-image", "operator-chart"}

	t.Run("kubernetes-only runs every kubernetes publisher despite core-tag skip", func(t *testing.T) {
		ran := simulate(dag, k8s)
		if ran["core-tag"] {
			t.Error("core-tag must be skipped for a kubernetes-only release")
		}
		if !ran["core-ready"] {
			t.Error("core-ready barrier must run for any release")
		}
		for _, p := range k8sPublishers {
			if !ran[p] {
				t.Errorf("kubernetes-only: %q must run (got skipped) — it must depend on core-ready, not the skipped core-tag; ran=%v", p, names(ran))
			}
		}
	})

	t.Run("core-only runs no kubernetes publisher", func(t *testing.T) {
		ran := simulate(dag, core)
		if !ran["core-tag"] {
			t.Error("core-tag must run for a core release")
		}
		for _, p := range k8sPublishers {
			if ran[p] {
				t.Errorf("core-only: kubernetes publisher %q must not run", p)
			}
		}
	})

	t.Run("coordinated runs both groups with core-tag before kubernetes", func(t *testing.T) {
		ran := simulate(dag, append(append([]string{}, core...), k8s...))
		if !ran["core-tag"] {
			t.Error("core-tag must run in a coordinated release")
		}
		for _, p := range append(append([]string{}, k8sPublishers...), "core-ready") {
			if !ran[p] {
				t.Errorf("coordinated: %q must run", p)
			}
		}
		// Ordering: every kubernetes publisher transitively depends on core-ready,
		// which depends on core-tag.
		if cr, ok := dag["core-ready"]; !ok || !contains(cr.needs, "core-tag") {
			t.Error("core-ready must depend on core-tag so core is tagged before the kubernetes line")
		}
		for _, p := range []string{"k8s-module", "operator-image"} {
			if !contains(dag[p].needs, "core-ready") {
				t.Errorf("%q must depend on core-ready (the barrier), not core-tag directly", p)
			}
		}
	})

	t.Run("recovery subset runs only the requested unit's publisher", func(t *testing.T) {
		// A recovery of just operator-image: only operator-image (+ core-ready barrier).
		ran := simulate(dag, []string{"operator-image"})
		if !ran["operator-image"] {
			t.Error("recovery of operator-image must run it")
		}
		if ran["k8s-module"] || ran["operator-chart"] {
			t.Error("recovery subset must not run non-requested publishers")
		}
	})
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
