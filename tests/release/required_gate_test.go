package release

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Merges to main are gated by CLASSIC branch protection (strict, enforce_admins)
// with exactly these required status contexts. GitHub Actions has no
// cross-workflow `needs:`, so a job that is neither one of these contexts nor in
// the transitive `needs:` of one reports a check that nothing enforces: it goes
// red and the pull request stays mergeable. That is not hypothetical — it is how
// dashboard-e2e, docs-check and the two vulnerability scans each came to be
// decorative, discovered one at a time after something merged broken.
//
// Keep this in step with the live settings; they are configuration, not code:
//
//	gh api repos/trianalab/pacto/branches/main/protection/required_status_checks
var requiredContexts = map[string]string{ // status context -> workflow that produces it
	"validate":          "pr-title.yml",
	"required":          "ci.yml",
	"docs-required":     "docs-check.yml",
	"security-required": "security.yml",
	"pacto-required":    "pacto.yml",
}

// advisoryJobs is every pull_request-triggered job that deliberately does NOT
// gate a merge, keyed "<workflow>:<job id>". An entry that matches nothing fails
// too, so renaming or deleting a job forces someone to re-read the reason rather
// than inherit it.
//
// Jobs in a workflow reached through `uses:` need no entry: the called
// workflow's result IS the calling job's result, so they already gate.
var advisoryJobs = map[string]string{
	"dependabot-auto-merge.yml:auto-merge": "acts ON the pull request (arms auto-merge) instead of judging it; failing can only leave auto-merge off",
	"repowise.yml:repowise":                "advisory by design, and says so in its header: change-risk and health scores are noisy as hard gates, so a legitimate large refactor must not block",
	"ui-rebuild.yml:build":                 "Dependabot-only convenience that rebuilds the committed UI bundle; the real gate on that bundle is ci-ui-drift inside the required ci-static leg",
	"security.yml:comment":                 "posts the scan summary as a pull request comment; the pass/fail lives in govulncheck and trivy-image, which security-required gates, so a flaky comment API call must not block a merge",
}

type gateJob struct {
	Name  string `yaml:"name"`
	Needs any    `yaml:"needs"`
	Uses  string `yaml:"uses"`
}

type gateWorkflow struct {
	// yaml.v3 keeps `on` a plain string key; only true/false resolve to bool, so
	// this does not need the YAML 1.1 `true:` workaround PyYAML would.
	On   any                `yaml:"on"`
	Jobs map[string]gateJob `yaml:"jobs"`
}

func loadGateWorkflows(t *testing.T) map[string]gateWorkflow {
	t.Helper()
	out := map[string]gateWorkflow{}
	for _, w := range loadWorkflows(t, repoRoot(t)) {
		var doc gateWorkflow
		if err := yaml.Unmarshal([]byte(w.text), &doc); err != nil {
			t.Fatalf("parse %s: %v", w.name, err)
		}
		out[w.name] = doc
	}
	return out
}

// pullRequestTrigger covers all the spellings in this repo: `on: pull_request`,
// a bare `pull_request:` and `pull_request:` with types/branches/paths.
func pullRequestTrigger(on any) (bool, map[string]any) {
	switch v := on.(type) {
	case string:
		return v == "pull_request", nil
	case []any:
		for _, x := range v {
			if fmt.Sprint(x) == "pull_request" {
				return true, nil
			}
		}
	case map[string]any:
		cfg, ok := v["pull_request"]
		if !ok {
			return false, nil
		}
		m, _ := cfg.(map[string]any)
		return true, m
	}
	return false, nil
}

func gateNeeds(j gateJob) []string {
	switch n := j.Needs.(type) {
	case string:
		return []string{n}
	case []any:
		out := make([]string, 0, len(n))
		for _, x := range n {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

// gatedJobs marks every job a required context depends on, following `needs:`
// within a workflow and `uses: ./.github/workflows/x.yml` across one.
func gatedJobs(t *testing.T, wfs map[string]gateWorkflow) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	var walk func(file, job string)
	walk = func(file, job string) {
		key := file + ":" + job
		if seen[key] {
			return
		}
		seen[key] = true
		j, ok := wfs[file].Jobs[job]
		if !ok {
			t.Fatalf("%s depends on job %q, which does not exist", file, job)
		}
		if called, isCall := strings.CutPrefix(j.Uses, "./.github/workflows/"); isCall {
			called = strings.TrimSpace(strings.SplitN(called, "@", 2)[0])
			if _, known := wfs[called]; !known {
				t.Fatalf("%s job %q calls unknown workflow %q", file, job, called)
			}
			for _, n := range slices.Sorted(maps.Keys(wfs[called].Jobs)) {
				walk(called, n)
			}
		}
		for _, n := range gateNeeds(j) {
			walk(file, n)
		}
	}
	for _, ctx := range slices.Sorted(maps.Keys(requiredContexts)) {
		file := requiredContexts[ctx]
		wf, ok := wfs[file]
		if !ok {
			t.Fatalf("required context %q is produced by %s, which does not exist", ctx, file)
		}
		id := ""
		for _, name := range slices.Sorted(maps.Keys(wf.Jobs)) {
			if name == ctx || wf.Jobs[name].Name == ctx {
				id = name
			}
		}
		if id == "" {
			t.Fatalf("no job in %s reports the required status context %q — branch protection is waiting for a check nothing produces", file, ctx)
		}
		walk(file, id)
	}
	return seen
}

// TestEveryPRJobGatesOrIsDeliberatelyAdvisory is the gate on the gating surface:
// adding a pull_request job without wiring it into an aggregate now fails here
// instead of merging broken months later.
func TestEveryPRJobGatesOrIsDeliberatelyAdvisory(t *testing.T) {
	wfs := loadGateWorkflows(t)
	gated := gatedJobs(t, wfs)

	matched := map[string]bool{}
	for _, file := range slices.Sorted(maps.Keys(wfs)) {
		onPR, _ := pullRequestTrigger(wfs[file].On)
		if !onPR {
			continue
		}
		for _, job := range slices.Sorted(maps.Keys(wfs[file].Jobs)) {
			key := file + ":" + job
			if _, listed := advisoryJobs[key]; listed {
				matched[key] = true
				if gated[key] {
					t.Errorf("%s is listed in advisoryJobs but IS reachable from a required context, so it does block merges; delete the allowlist entry", key)
				}
				continue
			}
			if gated[key] {
				continue
			}
			t.Errorf("%s runs on pull_request but no required status context depends on it, so it can fail while the pull request stays mergeable.\n"+
				"Branch protection requires only %v, and GitHub has no cross-workflow needs. Either add the job to the needs: of the aggregate in its own workflow, "+
				"or give its workflow a workflow_call: trigger and call it from a ci.yml job listed in required.needs, "+
				"or add %q to advisoryJobs here with the one-line reason it must never block.",
				key, slices.Sorted(maps.Keys(requiredContexts)), key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(advisoryJobs)) {
		if !matched[key] {
			t.Errorf("advisoryJobs entry %q matches no pull_request-triggered job — it was renamed or removed; re-read the reason and update or drop it", key)
		}
	}
}

// TestRequiredContextsCanAlwaysReport guards the failure mode that is worse than
// a red check: one that never starts. Branch protection then waits for a report
// that will never arrive, and strict cannot rescue a branch already up to date
// with main. A trigger-level paths: filter builds exactly that trap, which is
// why docs-check.yml filters inside a changes job instead.
func TestRequiredContextsCanAlwaysReport(t *testing.T) {
	wfs := loadGateWorkflows(t)
	for _, ctx := range slices.Sorted(maps.Keys(requiredContexts)) {
		file := requiredContexts[ctx]
		onPR, cfg := pullRequestTrigger(wfs[file].On)
		if !onPR {
			t.Errorf("%s produces the required context %q but does not run on pull_request", file, ctx)
			continue
		}
		for _, k := range []string{"paths", "paths-ignore"} {
			if _, bad := cfg[k]; bad {
				t.Errorf("%s filters pull_request by %s while producing the required context %q: when the filter excludes a pull request the check never starts, never reports, and the pull request waits forever. Filter inside the workflow with a changes job instead.", file, k, ctx)
			}
		}
	}
}
