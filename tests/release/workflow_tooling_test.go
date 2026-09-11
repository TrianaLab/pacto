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
//
// Every value here names something that hands over a PREBUILT binary. A Go
// module path would not: see
// TestTheReleasePathNeverCompilesThirdPartyToolingFromSource for the release
// that cost.
var toolInstaller = map[string]string{
	"oras":   "oras-project/setup-oras",
	"crane":  "./.github/actions/setup-crane",
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
		if strings.HasPrefix(installer, "./") {
			checkInTreeInstaller(t, root, workflows, tool, installer)
			continue
		}
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

// inTreeVersionRE finds the version an in-tree installer pins, wherever the
// action spells it: `CRANE_VERSION: v0.20.2` and anything shaped like it.
var inTreeVersionRE = regexp.MustCompile(`_VERSION:\s*"?(v?\d+\.\d+\.\d+)"?`)

// checkInTreeInstaller is the pin rule for an installer that lives in this
// repository rather than in someone else's. Such an action needs no `@pin`: it
// is read out of the same checkout as the workflow that calls it, so the two
// move together by construction and cannot partially bump. What it does need is
// exactly one version inside it — the pin did not disappear when it moved out of
// the workflow, and a second one in the action file would be the same partial
// bump in a new place.
func checkInTreeInstaller(t *testing.T, root string, workflows []workflow, tool, installer string) {
	t.Helper()

	var used []string
	for _, w := range workflows {
		for i, line := range strings.Split(w.text, "\n") {
			if strings.Contains(line, "uses:") && strings.Contains(line, installer) {
				used = append(used, w.name+":"+strconv.Itoa(i+1))
			}
		}
	}
	if len(used) == 0 {
		t.Errorf("no workflow installs %s via %s — either the installer changed or the tool is gone; update toolInstaller so the closure gate keeps meaning something", tool, installer)
		return
	}

	path := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(installer, "./")), "action.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%d workflow steps use %s but %s is unreadable (%v) — a `uses: ./…` that resolves to nothing fails at run time, in a publish job", len(used), installer, path, err)
		return
	}
	pins := map[string]bool{}
	for _, m := range inTreeVersionRE.FindAllStringSubmatch(string(b), -1) {
		pins[m[1]] = true
	}
	switch len(pins) {
	case 1: // the one thing we want
	case 0:
		t.Errorf("%s pins no version of %s — the pin moved out of the workflows and has to land somewhere; a release must not resolve its tooling through a movable reference", path, tool)
	default:
		t.Errorf("%s pins %d different versions of %s (%s) — a partial bump, in the one file that was supposed to make bumping atomic", path, len(pins), tool, strings.Join(slices.Sorted(maps.Keys(pins)), " vs "))
	}
}

// goInstallRE finds a `go install` and the package it compiles.
var goInstallRE = regexp.MustCompile(`\bgo install\s+([^\s;&|]+)`)

// TestTheReleasePathNeverCompilesThirdPartyToolingFromSource is the rule release
// run 34613593980 bought.
//
// crane was the only tool in the release path built from source at publish time.
// oras, cosign, syft and helm all arrive as prebuilt binaries from pinned
// installers; crane alone ran `go install …/cmd/crane@v0.20.2`, which fetches
// roughly twenty third-party module zips, in seven publish jobs, every release.
// One of those fetches — docker/distribution, from proxy.golang.org — returned
// an HTTP/2 INTERNAL_ERROR, and the release half-shipped: operator-image failed,
// operator-chart and the GitHub Release job skipped behind it, and 3.3.0/5.4.0
// existed as Go tags with no binaries and no operator artifacts.
//
// A publish job is a transaction. Compiling a tool inside one turns every
// transitive dependency of that tool into a way for the transaction to die
// halfway, in exchange for a binary its upstream already publishes. So: nothing
// in release.yml compiles third-party source.
//
// Scoped to release.yml deliberately. `go install` elsewhere — govulncheck in
// security.yml, helm-docs and kind in ci.yml — fails a check rather than
// stranding a release, and a red check is not an irreversible half-publish.
func TestTheReleasePathNeverCompilesThirdPartyToolingFromSource(t *testing.T) {
	root := repoRoot(t)

	for _, tool := range slices.Sorted(maps.Keys(toolInstaller)) {
		if strings.HasPrefix(toolInstaller[tool], "github.com/") {
			t.Errorf("toolInstaller[%q] is the Go module path %q — that is a source build; point it at an installer that hands over a prebuilt binary", tool, toolInstaller[tool])
		}
	}

	jobs := workflowJobs(t, root, "release.yml")
	if len(jobs) == 0 {
		t.Fatal("release.yml parsed to no jobs — this gate would pass vacuously")
	}
	for _, name := range slices.Sorted(maps.Keys(jobs)) {
		for _, l := range commandLines(jobs[name].runs()) {
			for _, m := range goInstallRE.FindAllStringSubmatch(l, -1) {
				pkg := strings.Trim(m[1], `"'`)
				if strings.HasPrefix(pkg, "./") || strings.HasPrefix(pkg, "../") {
					continue // this repository's own binary — there is no prebuilt one to fetch instead.
				}
				t.Errorf("release.yml job %q runs `go install %s` — a publish job must not compile third-party source. "+
					"Every module that build downloads is one more way for the transaction to die after an irreversible push; "+
					"install a prebuilt binary the way oras, cosign, syft, helm and crane are installed.", name, pkg)
			}
		}
	}
}

// goFetchRE is a Go command whose work is network round trips against
// proxy.golang.org — one per module in the graph it resolves.
var goFetchRE = regexp.MustCompile(`\bgo (?:mod download|install)\b`)

// quotedShellStringRE is a shell string literal. `go install …` inside one is a
// hint printed at a human ("crane is required. Install it: go install …"), not a
// command anything runs.
var quotedShellStringRE = regexp.MustCompile(`"[^"]*"|'[^']*'`)

// retryWrapperRE is the two shapes a wrapped fetch takes: release/scripts/retry.sh
// where the script is reachable, and the inline `retry` shell function the
// Dockerfiles define because it is not.
var retryWrapperRE = regexp.MustCompile(`(?:^|[\s;&|(/])retry(?:\.sh)?\s`)

// retryLoopRE is the open-coded loop used where there is neither a script nor a
// function to call — the root Dockerfile's RUN and the operator Makefile's
// go-install-tool define.
var retryLoopRE = regexp.MustCompile(`for attempt in 1 2 3 4 5`)

// classifyGoFetch reads one command list and reports whether it fetches Go
// modules over the network, and if so whether the fetch is retried.
func classifyGoFetch(cmd string) (fetches, retried bool) {
	bare := quotedShellStringRE.ReplaceAllString(cmd, "")
	if !goFetchRE.MatchString(bare) {
		return false, false
	}
	return true, retryWrapperRE.MatchString(bare) || retryLoopRE.MatchString(bare)
}

// ciFetchSurfaces is every file whose shell CI runs and that could reach the
// module proxy. Globbed rather than listed so a new workflow, action or release
// script is covered the day it lands.
func ciFetchSurfaces(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, pat := range [][]string{
		{".github", "workflows", "*.yml"},
		{".github", "actions", "*", "action.yml"},
		{"release", "scripts", "*.sh"},
		{"release", "orchestrator", "*.sh"},
		{"tests", "acceptance", "*", "*.sh"},
		{"Dockerfile"},
		{"Makefile"},
		{"ci.mk"},
		{"integrations", "kubernetes", "Dockerfile"},
		{"integrations", "kubernetes", "Makefile"},
		{"integrations", "kubernetes", "ci.mk"},
	} {
		matches, err := filepath.Glob(filepath.Join(append([]string{root}, pat...)...))
		if err != nil {
			t.Fatalf("glob %v: %v", pat, err)
		}
		out = append(out, matches...)
	}
	slices.Sort(out)
	return out
}

// TestEveryGoNetworkFetchInCIRetries is the rule 2026-09-11 bought.
//
// The go command retries nothing. `go mod download` and `go install` are one
// network round trip per module against proxy.golang.org, and a single 5xx, a
// dropped HTTP/2 stream or a stalled connection fails the whole step. In one
// afternoon that took out three separate things on a tree nobody had touched:
// release run 34613593980 died fetching a module zip for crane AFTER the tags
// were pushed, half-shipping 3.3.0/5.4.0; `ci-e2e-kind (observation)` died in
// the operator image build; and `ci-e2e-compose` died at the root Dockerfile's
// `RUN go mod download`. Three failures, one cause, zero retries anywhere.
//
// The npm half of the same problem was solved long ago — ci.mk passes
// --fetch-retries=5 to `npm ci`. This is the Go half, and it is a gate rather
// than a habit because the sites are spread across two Dockerfiles, three
// makefiles, four workflows and a release script, and the next one gets added by
// someone who never read this file.
//
// Retrying is the right shape for CI specifically. A publish job is different:
// there the rule is stronger — do not compile third-party source at all, see
// TestTheReleasePathNeverCompilesThirdPartyToolingFromSource — because a retry
// that eventually gives up still leaves an irreversible half-publish behind.
//
// One ceiling, stated so nobody trusts this further than it reaches. Continued
// lines are joined, so the unit is one shell command list, not one source line:
// a RUN that retried one fetch and not a second would pass.
//
// `go build`, `go run` and `go test` fetch modules too, and this gate never sees
// them. That used to be written here as a second ceiling that excused itself —
// "out of scope because every CI job that runs them runs a retried `go mod
// download` first, which leaves the cache warm". It was not true. Four
// release.yml jobs compiled Go with no retried fetch anywhere, three of them
// after core-tag had already pushed the git tag, which is the same half-ship
// window this whole rule exists to close. A ceiling that claims something nobody
// checks is worse than no ceiling, so the claim is now its own gate:
// TestEveryReleaseJobThatCompilesGoWarmsTheCacheFirst.
func TestEveryGoNetworkFetchInCIRetries(t *testing.T) {
	root := repoRoot(t)

	found := 0
	for _, path := range ciFetchSurfaces(t, root) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("rel %s: %v", path, err)
		}
		for _, cmd := range commandLines(string(b)) {
			// `@#` is a make recipe comment; commandLines only knows the bare form.
			if strings.HasPrefix(strings.TrimSpace(cmd), "@#") {
				continue
			}
			fetches, retried := classifyGoFetch(cmd)
			if !fetches {
				continue
			}
			found++
			if retried {
				continue
			}
			t.Errorf("%s fetches Go modules without a retry: %s\n"+
				"Wrap it in release/scripts/retry.sh, or — where that path is not reachable, as in the two Dockerfiles and the standalone operator module — use the same 5-attempt loop inline. "+
				"Unretried, one dropped connection to proxy.golang.org fails the step, and on the release path it half-ships.", rel, elide(cmd))
		}
	}
	// Every known site plus a little slack. A refactor that quietly stops
	// matching would otherwise leave this green while checking nothing.
	if found < 10 {
		t.Errorf("only %d Go module fetches found across the CI surfaces — there are at least 10; the scan stopped matching and this gate is now vacuous", found)
	}
}

// elide keeps a failure message readable when the offending command is a joined
// multi-line RUN.
func elide(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// goCompileRE is a Go command that compiles, and therefore downloads whatever
// the module cache is missing — the same unretried round trips as `go mod
// download`, only implicit, which is why they went unnoticed.
//
// `go mod download` and `go install` are deliberately absent: they are
// TestEveryGoNetworkFetchInCIRetries' subject, and a job whose only Go command
// is one of those has nothing left to warm.
var goCompileRE = regexp.MustCompile(`\bgo (?:build|run|test|vet|generate)\b`)

// reachableShell is every line of shell a workflow job can execute: its own
// `run` bodies, the scripts those invoke transitively, the recipes of the make
// targets they call, and the scripts those recipes invoke. It is the same walk
// jobNeeds does for gated CLIs, kept generic because a gate that says "this job
// compiles Go" has to be able to say where it saw it. Keyed by that origin.
func reachableShell(t *testing.T, job wfJob, files map[string]string, rules map[string]makeRule) map[string]string {
	t.Helper()
	out := map[string]string{}
	seen := map[string]bool{}
	var walk func(name string)
	walk = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		path, ok := files[name]
		if !ok {
			return // not one of ours: a vendored helper, or a name that only appears in prose.
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		out[name] = string(b)
		_, scripts := toolsIn(string(b))
		for _, s := range slices.Sorted(maps.Keys(scripts)) {
			walk(s)
		}
	}

	shell := job.runs()
	out["the job's own shell"] = shell
	_, scripts := toolsIn(shell)
	for _, s := range slices.Sorted(maps.Keys(scripts)) {
		walk(s)
	}
	for _, l := range commandLines(shell) {
		for _, m := range makeCallRE.FindAllStringSubmatch(l, -1) {
			recipe := recipeClosure(rules, m[1], map[string]bool{})
			out["make "+m[1]] = recipe
			_, mScripts := toolsIn(recipe)
			for _, s := range slices.Sorted(maps.Keys(mScripts)) {
				walk(s)
			}
		}
	}
	return out
}

// goCacheState reads one job's reachable shell and reports where it first
// compiles Go (origin "" if it never does) and whether anything it reaches warms
// the module cache through a retry.
func goCacheState(t *testing.T, job wfJob, files map[string]string, rules map[string]makeRule) (origin, cmd string, warmed bool) {
	t.Helper()
	reach := reachableShell(t, job, files, rules)
	for _, src := range slices.Sorted(maps.Keys(reach)) {
		for _, l := range commandLines(reach[src]) {
			bare := quotedShellStringRE.ReplaceAllString(l, "")
			if origin == "" && goCompileRE.MatchString(bare) {
				origin, cmd = src, l
			}
			if fetches, retried := classifyGoFetch(l); fetches && retried {
				warmed = true
			}
		}
	}
	return origin, cmd, warmed
}

// TestEveryReleaseJobThatCompilesGoWarmsTheCacheFirst closes the hole the gate
// above used to wave away in a comment.
//
// setup-go restores a module cache keyed on go.sum, and while that cache hits,
// a `go build` in a release job touches the network for nothing. On a miss —
// evicted after seven days, or a go.sum that moved, which is exactly what a
// release commit does — the same `go build` becomes one bare fetch per module
// in the graph, none of them retried. The `release` job's build-cli.sh alone
// resolves ~97.
//
// Three of the four jobs that were in this state run AFTER core-tag has pushed
// the git tag. A dropped connection there does not fail a release; it
// half-ships one — tags on GitHub, no binaries on the Release, no demo bundles,
// no published Compose application. That is run 34613593980, and it is why the
// fix is a gate and not a habit: the next release job someone adds will compile
// Go, and nothing about `go build` looks like network access.
//
// The rule is uniform across release.yml rather than carved to the post-tag
// jobs. A pre-tag failure is cheap, but "cheap enough to leave unretried" is an
// exception whose boundary moves every time a job is reordered, and the fix
// costs one idempotent step that is free on a cache hit.
//
// Ceilings: ordering is not modelled, so a script that compiled before its own
// retried fetch would pass (none does — every warm step here is a job-level
// step that runs before any compile); a Go command inside a Dockerfile is not
// on this runner's network path and is covered by the Dockerfiles' own inline
// retry loops; and a make target assembled from a matrix expression does not
// resolve to a recipe, so its compiles are invisible.
func TestEveryReleaseJobThatCompilesGoWarmsTheCacheFirst(t *testing.T) {
	root := repoRoot(t)
	files := gateScripts(t, root)
	rules := makeRules(t, root)
	jobs := workflowJobs(t, root, "release.yml")

	compiling := 0
	for _, name := range slices.Sorted(maps.Keys(jobs)) {
		origin, cmd, warmed := goCacheState(t, jobs[name], files, rules)
		if origin == "" {
			continue
		}
		compiling++
		if warmed {
			continue
		}
		t.Errorf("release.yml job %q compiles Go (%s: %s) but never warms the module cache through a retry.\n"+
			"Add `- name: Warm the module cache (retried)` running `bash release/scripts/retry.sh go mod download` after setup-go. "+
			"On a cache miss that `go build` is one unretried fetch per module, and in a post-tag job one dropped connection half-ships the release.",
			name, origin, elide(cmd))
	}
	// The four fixed here plus the ones that already had a fetch. A refactor that
	// stopped resolving the closure would otherwise leave this green.
	if compiling < 4 {
		t.Errorf("only %d release.yml jobs found compiling Go — there are at least 4; the closure stopped resolving and this gate is now vacuous", compiling)
	}
}

// TestTheRetryGateBitesOnABareFetch proves the classifier above can fail, and
// fails on the exact three lines that broke on 2026-09-11 rather than on a
// strawman. Without it, a regex that quietly stopped matching would leave the
// gate green forever.
func TestTheRetryGateBitesOnABareFetch(t *testing.T) {
	for _, tc := range []struct {
		name             string
		cmd              string
		fetches, retried bool
	}{
		{"the root Dockerfile as it failed", "RUN go mod download", true, false},
		{"the operator Dockerfile as it failed", "go work sync && go mod download all", true, false},
		{"the release path as it failed", "go install github.com/google/go-containerregistry/cmd/crane@v0.20.2", true, false},
		{"wrapped in the shared script", "bash release/scripts/retry.sh go mod download", true, true},
		{"wrapped in the inline function", "retry go mod download all", true, true},
		{"wrapped in the inline function with env", "retry env GOWORK=off go mod download", true, true},
		{"wrapped in the inline loop", "for attempt in 1 2 3 4 5; do if go mod download; then break; fi; done", true, true},
		{"a hint printed at a human", `echo "crane is required. Install it: go install github.com/google/go-containerregistry/cmd/crane@latest"`, false, false},
		{"not a fetch at all", "go build ./...", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fetches, retried := classifyGoFetch(tc.cmd)
			if fetches != tc.fetches {
				t.Errorf("classifyGoFetch(%q) fetches = %v, want %v", tc.cmd, fetches, tc.fetches)
			}
			if retried != tc.retried {
				t.Errorf("classifyGoFetch(%q) retried = %v, want %v", tc.cmd, retried, tc.retried)
			}
		})
	}
}

// TestTheWarmCacheGateBitesWhenTheWarmStepIsDeleted proves that gate can fail,
// on the exact state release.yml was in until this branch: the `release` job with
// no retried fetch, still reaching build-cli.sh's `go build`. Without this, a
// closure that quietly stopped resolving scripts would leave the gate green while
// checking nothing.
func TestTheWarmCacheGateBitesWhenTheWarmStepIsDeleted(t *testing.T) {
	root := repoRoot(t)
	files := gateScripts(t, root)
	rules := makeRules(t, root)

	job, ok := workflowJobs(t, root, "release.yml")["release"]
	if !ok {
		t.Fatal("release.yml has no release job — the binary publisher was renamed; move this proof with it")
	}
	stripped := wfJob{}
	for _, s := range job.Steps {
		if fetches, retried := classifyGoFetch(s.Run); fetches && retried {
			continue
		}
		stripped.Steps = append(stripped.Steps, s)
	}
	origin, _, warmed := goCacheState(t, stripped, files, rules)
	if warmed {
		t.Fatal("stripping the warm step left a retried fetch behind — the proof below would be vacuous")
	}
	if origin == "" {
		t.Error("the release job without its warm step is not reported as compiling Go — but it runs build-cli.sh, which runs `go build`. The closure stopped resolving, and the gate now passes every job for free.")
	}
	if origin != "" && origin != "build-cli.sh" {
		t.Errorf("the release job's Go compile is attributed to %q — it should name build-cli.sh, the script that actually runs it", origin)
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
