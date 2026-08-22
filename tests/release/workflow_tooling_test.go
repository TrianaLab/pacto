package release

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// A release job runs shell, that shell runs scripts, and those scripts run
// external CLIs — oras, crane, cosign, syft, helm — that the GitHub runner image
// does not provide. Every job therefore has to install the ones it will reach,
// and nothing in the workflow declares that: the install step and the script
// that needs the tool sit hundreds of lines apart, in different files, connected
// only in a maintainer's head.
//
// That is how release run 32560058692 half-shipped. Commit 45abda33 moved the
// demo-compose unit to `docker compose publish` and deleted the job's ORAS
// install, reasoning that "ORAS stays where the ledger uses it" — while the job
// kept reading the ledger (`ledger.sh digest …`, to pin the dashboard image by
// digest) and kept publishing through publish-oci-unit.sh, which records into
// the ledger. ledger.sh IS the ORAS user. On the runner `oras` was not found,
// ledger.sh returned the empty string, the empty string is indistinguishable
// from "this transaction recorded no digest", the fail-closed guard fired and
// the unit died — after four irreversible publishes had already happened. Every
// existing gate was green, because not one of them runs a job with the runner's
// PATH.
//
// So this gate reads the dependency the way the runner experiences it: for each
// job it walks the shell that job runs — through `make` targets and through the
// transitive closure of the scripts those shells invoke — collects the gated
// CLIs that appear as commands, and requires the job to install every one of
// them. It is deliberately one-directional. A job that installs a tool it never
// uses wastes twenty seconds; a job that uses a tool it never installed loses a
// release. Only the second is an error here.
//
// Known ceilings, so nobody trusts this further than it reaches: a make target
// assembled from a matrix expression (`make test-acceptance-kind-${{ matrix.x }}`)
// does not resolve to a rule, so its tools are invisible; `if:` on an install
// step is not modelled; and a tool used only inside a container image is not a
// PATH dependency at all. Under-detection is the safe direction, and it is the
// price of having no second declaration to keep in sync.
//
// One consequence worth stating: a script that degrades when a tool is absent
// (build-cli.sh prints "syft not found — SBOM skipped") is still treated as
// needing it. A rehearsal that silently skips the SBOM the real release produces
// is not rehearsing the release, so the fix is to install the tool, not to
// exempt the script.

// toolInstaller maps each gated CLI to the single installer this repository uses
// for it. The key is the command name as it appears on a command line. The value
// is the installer's identity WITHOUT its pin — the pin is checked separately,
// and by consistency, so bumping it stays a one-line change.
var toolInstaller = map[string]string{
	"oras":   "oras-project/setup-oras",
	"crane":  "github.com/google/go-containerregistry/cmd/crane",
	"cosign": "sigstore/cosign-installer",
	"syft":   "anchore/sbom-action/download-syft",
	"helm":   "azure/setup-helm",
}

// toolCallRE matches the CLI being run, not the word: `oras.land/oras-go` is a Go
// import, and comments are already gone by the time commandLines is done.
func toolCallRE(tool string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[\s;|&(]|\$\()` + regexp.QuoteMeta(tool) + `\s`)
}

// shScriptRE finds, by basename, the shell scripts a command line runs.
var shScriptRE = regexp.MustCompile(`([A-Za-z0-9_.-]+)\.sh\b`)

// gateScripts is every shell script a workflow job can reach, basename -> path.
func gateScripts(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, pat := range [][]string{
		{"release", "orchestrator", "*.sh"},
		{"release", "scripts", "*.sh"},
		{"tests", "acceptance", "kind", "*.sh"},
		{"tests", "acceptance", "local", "*.sh"},
	} {
		matches, err := filepath.Glob(filepath.Join(append([]string{root}, pat...)...))
		if err != nil {
			t.Fatalf("glob %v: %v", pat, err)
		}
		for _, m := range matches {
			out[filepath.Base(m)] = m
		}
	}
	if len(out) == 0 {
		t.Fatal("no release scripts found — the closure below would silently pass")
	}
	return out
}

// toolsIn reduces any shell text to the gated CLIs it invokes and the scripts it
// runs, both by name.
func toolsIn(text string) (tools, scripts map[string]bool) {
	tools, scripts = map[string]bool{}, map[string]bool{}
	for _, l := range commandLines(text) {
		for tool := range toolInstaller {
			if toolCallRE(tool).MatchString(l) {
				tools[tool] = true
			}
		}
		for _, m := range shScriptRE.FindAllStringSubmatch(l, -1) {
			scripts[m[1]+".sh"] = true
		}
	}
	return tools, scripts
}

// scriptTools is the transitive tool closure of one script: tool -> the script in
// the chain that actually runs it, which is what the failure message needs to say.
func scriptTools(t *testing.T, name string, files map[string]string, seen map[string]bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	if seen[name] {
		return out
	}
	seen[name] = true
	path, ok := files[name]
	if !ok {
		return out // not one of ours: a vendored helper, or a name that only appears in prose.
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	tools, scripts := toolsIn(string(b))
	for tool := range tools {
		out[tool] = name
	}
	for _, s := range slices.Sorted(maps.Keys(scripts)) {
		for tool, via := range scriptTools(t, s, files, seen) {
			if _, dup := out[tool]; !dup {
				out[tool] = via
			}
		}
	}
	return out
}

// recipeClosure is all the recipe text running target can execute: its own, its
// prerequisites', and that of the targets it calls back into make for.
func recipeClosure(rules map[string]makeRule, target string, seen map[string]bool) string {
	if seen[target] {
		return ""
	}
	seen[target] = true
	r, ok := rules[target]
	if !ok {
		return ""
	}
	out := r.recipe
	next := slices.Clone(r.prereqs)
	for _, m := range subMakeRE.FindAllStringSubmatch(r.recipe, -1) {
		next = append(next, m[1])
	}
	for _, m := range makeCallRE.FindAllStringSubmatch(r.recipe, -1) {
		next = append(next, m[1])
	}
	for _, p := range next {
		out += recipeClosure(rules, p, seen)
	}
	return out
}

// jobNeeds is every gated CLI the job's shell can reach, mapped to the reason —
// the sentence a maintainer reads when the gate fires.
func jobNeeds(t *testing.T, job wfJob, files map[string]string, rules map[string]makeRule) map[string]string {
	t.Helper()
	need := map[string]string{}
	add := func(tool, why string) {
		if _, ok := need[tool]; !ok {
			need[tool] = why
		}
	}
	via := func(entry, runner string) string {
		if entry == runner {
			return entry + " runs it"
		}
		return entry + " -> " + runner + " runs it"
	}

	shell := job.runs()
	tools, scripts := toolsIn(shell)
	for tool := range tools {
		add(tool, "the job runs it directly")
	}
	for _, s := range slices.Sorted(maps.Keys(scripts)) {
		for tool, runner := range scriptTools(t, s, files, map[string]bool{}) {
			add(tool, via(s, runner))
		}
	}
	// The same walk again for anything reached through make, because a job that
	// runs one `make` line runs everything that target expands to.
	for _, l := range commandLines(shell) {
		for _, m := range makeCallRE.FindAllStringSubmatch(l, -1) {
			recipe := recipeClosure(rules, m[1], map[string]bool{})
			mTools, mScripts := toolsIn(recipe)
			for tool := range mTools {
				add(tool, "make "+m[1]+" runs it")
			}
			for _, s := range slices.Sorted(maps.Keys(mScripts)) {
				for tool, runner := range scriptTools(t, s, files, map[string]bool{}) {
					add(tool, "make "+m[1]+" -> "+via(s, runner))
				}
			}
		}
	}
	return need
}

// jobInstalls is the set of gated CLIs the job puts on the runner's PATH.
func jobInstalls(job wfJob) map[string]bool {
	out := map[string]bool{}
	for _, s := range job.Steps {
		for tool, installer := range toolInstaller {
			if strings.Contains(s.Uses, installer) || strings.Contains(s.Run, installer) {
				out[tool] = true
			}
		}
	}
	return out
}

// TestEveryJobInstallsTheCLIsItsScriptsRun is the gate: no job may reach a gated
// CLI it did not install.
func TestEveryJobInstallsTheCLIsItsScriptsRun(t *testing.T) {
	root := repoRoot(t)
	files := gateScripts(t, root)
	rules := makeRules(t, root)

	for _, w := range loadWorkflows(t, root) {
		jobs := workflowJobs(t, root, w.name)
		for _, name := range slices.Sorted(maps.Keys(jobs)) {
			job := jobs[name]
			need := jobNeeds(t, job, files, rules)
			installed := jobInstalls(job)
			for _, tool := range slices.Sorted(maps.Keys(need)) {
				if installed[tool] {
					continue
				}
				t.Errorf("%s job %q reaches %s (%s) but no step installs it — add the %s install step. "+
					"On a runner without %s the command is `%s: command not found`, and the release scripts read that as an empty answer rather than as a failure.",
					w.name, name, tool, need[tool], toolInstaller[tool], tool, tool)
			}
		}
	}
}

// installerPinRE finds every pinned reference to one installer, wherever a
// workflow spells it: an `uses:` line (whose trailing `# v2` comment is not part
// of the reference) or a `go install` line.
func installerPinRE(installer string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(installer) + `@(\S+)`)
}

var (
	commitPinRE = regexp.MustCompile(`^[0-9a-f]{40}$`)
	semverPinRE = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
)

// TestToolInstallersArePinnedAndIdentical: two workflows install these five CLIs
// from a couple of dozen steps between them. A bump that reaches all but one of
// them is a release where two jobs disagree about what "the tool" is, and the one
// that matters is whichever job publishes something irreversible. The expected pin
// is not written down here — it is whatever the workflows already agree on — so a
// genuine bump stays a find-and-replace and only a PARTIAL bump fails.
func TestToolInstallersArePinnedAndIdentical(t *testing.T) {
	root := repoRoot(t)
	workflows := loadWorkflows(t, root)

	for _, tool := range slices.Sorted(maps.Keys(toolInstaller)) {
		installer := toolInstaller[tool]
		seen := map[string][]string{} // pin -> where
		for _, w := range workflows {
			for i, line := range strings.Split(w.text, "\n") {
				for _, m := range installerPinRE(installer).FindAllStringSubmatch(line, -1) {
					pin := strings.TrimRight(m[1], `"'`)
					seen[pin] = append(seen[pin], w.name+":"+strconv.Itoa(i+1))
				}
			}
		}
		if len(seen) == 0 {
			t.Errorf("no workflow installs %s via %s — either the installer changed or the tool is gone; update toolInstaller so the closure gate keeps meaning something", tool, installer)
			continue
		}
		if len(seen) > 1 {
			var detail []string
			for _, pin := range slices.Sorted(maps.Keys(seen)) {
				detail = append(detail, pin+" ("+strings.Join(seen[pin], ", ")+")")
			}
			t.Errorf("%s is installed from %d different pins of %s — a partial bump: %s", tool, len(seen), installer, strings.Join(detail, " vs "))
		}
		shape, label := commitPinRE, "commit SHA"
		if strings.HasPrefix(installer, "github.com/") {
			shape, label = semverPinRE, "released version"
		}
		for _, pin := range slices.Sorted(maps.Keys(seen)) {
			if !shape.MatchString(pin) {
				t.Errorf("%s is installed from %s@%s (%s) — a release must not resolve its tooling through a movable reference; pin a %s", tool, installer, pin, strings.Join(seen[pin], ", "), label)
			}
		}
	}
}

// TestTheToolingGateBitesWhenAnInstallStepIsDeleted proves the rule above can
// actually fail, on the exact edit that caused the incident: delete demo-compose's
// ORAS install and the job still reads the ledger, so ORAS must still be reported
// missing. Without this, a closure that quietly resolved to nothing would leave
// the gate green forever.
func TestTheToolingGateBitesWhenAnInstallStepIsDeleted(t *testing.T) {
	root := repoRoot(t)
	files := gateScripts(t, root)
	rules := makeRules(t, root)

	job, ok := workflowJobs(t, root, "release.yml")["demo-compose"]
	if !ok {
		t.Fatal("release.yml has no demo-compose job — the unit was renamed; move this proof with it")
	}
	stripped := wfJob{}
	for _, s := range job.Steps {
		if strings.Contains(s.Uses, toolInstaller["oras"]) || strings.Contains(s.Run, toolInstaller["oras"]) {
			continue
		}
		stripped.Steps = append(stripped.Steps, s)
	}
	if jobInstalls(stripped)["oras"] {
		t.Fatal("stripping the ORAS install left one behind — the proof below would be vacuous")
	}
	why, needed := jobNeeds(t, stripped, files, rules)["oras"]
	if !needed {
		t.Error("demo-compose without an ORAS install is not reported as needing ORAS — but the job reads the release ledger, and the ledger is an OCI artifact ORAS pulls. This is commit 45abda33 and release run 32560058692, unnoticed a second time.")
	}
	if needed && !strings.Contains(why, "ledger.sh") {
		t.Errorf("the reason given for demo-compose needing ORAS is %q — it should name ledger.sh, which is the script that actually runs it", why)
	}
}
