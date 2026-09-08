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
// wiring mistake fails the build instead of half-shipping a release.
//
// The skip semantics below are measured, not read off the documentation. The
// documented rule — "if a job fails or is skipped, all jobs that need it are
// skipped" — reads as one hop, and this simulator used to model it that way. It is
// not one hop. A probe workflow on this repo showed a job with NO `if` at all, whose
// only dependency succeeded, skipped anyway because a job further up the chain had
// skipped. So:
//
//   - detect is the always-present root.
//   - a skipped job POISONS its entire transitive downstream closure, not only the
//     jobs that need it directly.
//   - `if: always()` rescues the job that carries it and nothing else: it runs, and
//     its descendants stay poisoned. An always() barrier does NOT unblock the jobs
//     below it.
//   - any other job runs only if its unit gate matches changedUnits AND nothing in
//     its transitive ancestry skipped.
//
// The practical consequence, and the reason this file was rewritten: the only
// reliable way to keep a conditional job from taking its dependents down with it is
// to stop the JOB from skipping — run it unconditionally and gate its STEPS.

type relJob struct {
	needs   []string
	unit    string // the changedUnits entry this job gates on ("" = no unit gate)
	always  bool
	release bool // gated on release == 'true'
	steps   []relStep
}

type relStep struct {
	name string
	unit string // the changedUnits entry this step gates on ("" = no unit gate)
}

var unitGateRE = regexp.MustCompile(`contains\(fromJSON\(needs\.detect\.outputs\.units_json\),\s*'([a-z0-9-]+)'\)`)

// publisherOf maps a release unit to the job that publishes it, read from the
// `# pacto-publishes: <unit>` marker that precedes each publisher job.
// TestExactlyOnePublisherPerUnit proves the markers are complete and unique; this
// only needs to pair each one with the job key that follows it.
var (
	markerRE = regexp.MustCompile(`^\s*# pacto-publishes:\s*([a-z0-9-]+)`)
	jobKeyRE = regexp.MustCompile(`^  ([a-z0-9-]+):\s*$`)
)

func publisherOf(src string) map[string]string {
	out := map[string]string{}
	unit := ""
	for _, line := range strings.Split(src, "\n") {
		if m := markerRE.FindStringSubmatch(line); m != nil {
			unit = m[1]
			continue
		}
		if unit == "" {
			continue
		}
		if m := jobKeyRE.FindStringSubmatch(line); m != nil {
			out[unit] = m[1]
			unit = ""
		}
	}
	return out
}

func loadReleaseDAG(t *testing.T) (map[string]relJob, map[string]string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	var doc struct {
		Jobs map[string]struct {
			Needs any    `yaml:"needs"`
			If    string `yaml:"if"`
			Steps []struct {
				Name string `yaml:"name"`
				Uses string `yaml:"uses"`
				If   string `yaml:"if"`
			} `yaml:"steps"`
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
		for _, s := range j.Steps {
			st := relStep{name: s.Name}
			if st.name == "" {
				st.name = s.Uses
			}
			if m := unitGateRE.FindStringSubmatch(s.If); m != nil {
				st.unit = m[1]
			}
			rj.steps = append(rj.steps, st)
		}
		out[name] = rj
	}
	return out, publisherOf(string(b))
}

// simulate returns the set of jobs that RUN for a release with the given
// changedUnits (detect + changesets excluded from the result).
func simulate(dag map[string]relJob, changedUnits []string) map[string]bool {
	inUnits := map[string]bool{}
	for _, u := range changedUnits {
		inUnits[u] = true
	}
	ran := map[string]bool{"detect": true} // detect always resolves for a release
	decided := map[string]bool{"detect": true}
	poisoned := map[string]bool{}
	// Fixpoint: decide a job once every dependency has been decided.
	for i := 0; i < len(dag)+2; i++ {
		for name, j := range dag {
			if name == "detect" || name == "changesets" || decided[name] {
				continue
			}
			ready := true
			poison := false
			for _, d := range j.needs {
				if d == "detect" {
					continue
				}
				if !decided[d] {
					ready = false
					break
				}
				if !ran[d] || poisoned[d] {
					poison = true
				}
			}
			if !ready {
				continue
			}
			gate := j.unit == "" || inUnits[j.unit]
			run := gate && (j.always || !poison)
			decided[name] = true
			ran[name] = run
			// always() rescues this job only — its descendants stay poisoned.
			poisoned[name] = poison || !run
		}
	}
	for name := range ran {
		if !ran[name] {
			delete(ran, name)
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
	dag, publisher := loadReleaseDAG(t)

	core := []string{"core", "cli", "dashboard-image", "dashboard-contract-bundle", "demo-bundles", "demo-compose"}
	k8s := []string{"k8s-module", "operator-image", "operator-chart", "k8s-docs"}
	k8sPublishers := []string{"k8s-module", "operator-image", "operator-chart"}

	// The invariant every scenario below is really testing: a release that fires for
	// a set of units must run the publisher of each one. Two releases shipped nothing
	// and reported success because no test asserted this directly — the old
	// kubernetes-only case enumerated three job names and the simulator's skip model
	// was wrong in exactly the way that mattered.
	requirePublished := func(t *testing.T, label string, units []string, ran map[string]bool) {
		t.Helper()
		for _, u := range units {
			job, ok := publisher[u]
			if !ok {
				t.Errorf("%s: unit %q has no `# pacto-publishes: %s` marker", label, u, u)
				continue
			}
			if !ran[job] {
				t.Errorf("%s: unit %q did not publish — job %q skipped. A skipped job poisons its whole downstream closure, so look for a skipped node anywhere in its ancestry, not just its direct needs; ran=%v", label, u, job, names(ran))
			}
		}
	}

	t.Run("kubernetes-only publishes the whole kubernetes line", func(t *testing.T) {
		ran := simulate(dag, k8s)
		requirePublished(t, "kubernetes-only", k8s, ran)
		// core-tag RUNS here and tags nothing. That is the fix, not a bug: while its
		// job gate could skip it, the skip travelled through the core-ready barrier
		// and took ledger-init and every kubernetes publisher with it. The "core is
		// not tagged" half of the invariant is asserted at the step level below.
		if !ran["core-tag"] {
			t.Error("core-tag must RUN for every release (unconditional job, unit-gated steps) — a skipped core-tag poisons the whole kubernetes line")
		}
		if !ran["core-ready"] {
			t.Error("core-ready barrier must run for any release")
		}
	})

	t.Run("core-tag gates its steps, not the job", func(t *testing.T) {
		ct, ok := dag["core-tag"]
		if !ok {
			t.Fatal("core-tag job is missing")
		}
		if ct.unit != "" {
			t.Errorf("core-tag must NOT carry a unit gate on the JOB (%q) — skipping it poisons every job downstream of core-ready", ct.unit)
		}
		if len(ct.steps) == 0 {
			t.Fatal("core-tag has no steps")
		}
		for _, s := range ct.steps {
			if s.unit != "core" {
				t.Errorf("core-tag step %q must be gated on the 'core' unit (got %q) — the job always runs, so the steps are what keep a non-core release from tagging core", s.name, s.unit)
			}
		}
	})

	t.Run("core-only runs no kubernetes publisher", func(t *testing.T) {
		ran := simulate(dag, core)
		requirePublished(t, "core-only", core, ran)
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
		all := append(append([]string{}, core...), k8s...)
		ran := simulate(dag, all)
		requirePublished(t, "coordinated", all, ran)
		if !ran["core-tag"] {
			t.Error("core-tag must run in a coordinated release")
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

	// A recovery dispatch narrows units_json to the incomplete units, so every unit
	// must be recoverable ALONE. This is where the poisoning bites hardest: the
	// narrower the dispatch, the more sibling publishers skip, and each skip reaches
	// everything below it.
	t.Run("every unit is recoverable on its own", func(t *testing.T) {
		units := append(append([]string{}, core...), k8s...)
		for _, u := range units {
			ran := simulate(dag, []string{u})
			requirePublished(t, "recovery of "+u, []string{u}, ran)
			for _, other := range units {
				if other == u {
					continue
				}
				job, ok := publisher[other]
				// A job with no JOB-level unit gate runs unconditionally on purpose
				// (core-tag) — its steps decide whether it publishes, and steps are
				// asserted separately. Only a job-gated publisher running is a leak.
				if !ok || dag[job].unit == "" {
					continue
				}
				if ran[job] {
					t.Errorf("recovery of %q must not also run %q (job %q)", u, other, job)
				}
			}
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
