package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v3/tests/acceptance/scenario"
)

// Docker Compose owns the demo application's OCI artifact type: `docker compose
// publish` writes it and `docker compose -f oci://…@sha256:… -p <name> up` runs
// it. That is a property of the release path, the acceptance harness, CI and the
// documentation at once, and every one of them can regress on its own — a
// convenience `oras pull`, a materialized run directory, a tag instead of a
// digest, one project name for two versions, a runner whose Compose is too old to
// have the verb. This file is the permanent gate on all of it.
//
// What the projection itself owes (no bind mount, no tag-only image, no top-level
// `name:`, every fixture inline, the declared version floor) is gated where it is
// produced, in tests/acceptance/scenario/compose_test.go. Nothing here restates it.

// composeFloor is the version the demo declares it needs, from the one place it
// is written.
func composeFloor(t *testing.T) *semver.Version {
	t.Helper()
	v, err := semver.NewVersion(scenario.ComposeMinVersion)
	if err != nil {
		t.Fatalf("scenario.ComposeMinVersion %q is not a version: %v", scenario.ComposeMinVersion, err)
	}
	return v
}

type wfStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
	Run  string         `yaml:"run"`
}

type wfJob struct {
	Steps []wfStep `yaml:"steps"`
}

// runs is every shell body the job executes, joined.
func (j wfJob) runs() string {
	var b []string
	for _, s := range j.Steps {
		b = append(b, s.Run)
	}
	return strings.Join(b, "\n")
}

// env reports whether any step sets key.
func (j wfJob) env(key string) bool {
	for _, s := range j.Steps {
		if _, ok := s.Env[key]; ok {
			return true
		}
	}
	return false
}

func workflowJobs(t *testing.T, root, file string) map[string]wfJob {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	var doc struct {
		Jobs map[string]wfJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	if len(doc.Jobs) == 0 {
		t.Fatalf("%s declares no jobs", file)
	}
	return doc.Jobs
}

func readFile(t *testing.T, root string, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(parts...), err)
	}
	return string(b)
}

// orasRE matches the tool, not the word: `oras.land/oras-go` is a Go library and
// not a second transport for this artifact.
var orasRE = regexp.MustCompile(`(?:^|[\s"'(/])oras(?:-project)?[\s"'/]`)

// adapterRE matches the shared publisher being *run*, not merely named.
var adapterRE = regexp.MustCompile(`^(?:bash|sh)\s+\S*publish-oci-unit\.sh\s+demo-compose\b|^\./\S*publish-oci-unit\.sh\s+demo-compose\b`)

// TestDemoComposeIsPublishedByComposeItself: the release unit's push command is
// `docker compose publish`, run through the one shared adapter, keyed by the
// content identity Compose's own manifest can be checked against. A unit that
// went back to `oras push` would publish an artifact Compose cannot run, and one
// that called Compose directly would leave the ledger, the crash window and the
// conflict check behind.
func TestDemoComposeIsPublishedByComposeItself(t *testing.T) {
	root := repoRoot(t)
	job, ok := workflowJobs(t, root, "release.yml")["demo-compose"]
	if !ok {
		t.Fatal("release.yml has no demo-compose job — the demo application's publisher was renamed; move this gate with it")
	}
	runs := job.runs()

	var published, adapted bool
	for _, inv := range composeInvocations("release.yml job demo-compose", runs) {
		published = published || inv.has("publish")
	}
	for _, l := range commandLines(runs) {
		adapted = adapted || adapterRE.MatchString(l)
	}
	if !published {
		t.Error("the demo-compose job never runs `docker compose … publish` — Docker Compose owns this artifact type, and anything else publishes something Compose cannot run with -f oci://")
	}
	if !adapted {
		t.Error("the demo-compose job does not publish through publish-oci-unit.sh — bypassing the shared adapter loses the ledger record, the crash-window adoption and the fail-closed conflict check")
	}
	if !job.env("PACTO_EXPECT_CONTENT") {
		t.Error("the demo-compose job sets no PACTO_EXPECT_CONTENT — Compose stamps a moving `created` timestamp and no provenance, so the one-layer content digest is the ONLY identity a crashed publish can be adopted by")
	}
}

// TestNothingOnTheComposeDemoPathTouchesOras: the publication path, the execution
// path, the acceptance harness and the documentation are all free of the generic
// OCI tool. ORAS stays where this repository genuinely manipulates generic
// artifacts (the ledger, the bundles, the chart's artifacthub metadata) — this is
// not a repository-wide ban, it is a ban on the Compose application's path.
func TestNothingOnTheComposeDemoPathTouchesOras(t *testing.T) {
	root := repoRoot(t)
	subjects := map[string]string{
		"tests/acceptance/local/compose-demo.sh": readFile(t, root, "tests", "acceptance", "local", "compose-demo.sh"),
		"docs/examples/compose-demo.md":          readFile(t, root, "docs", "examples", "compose-demo.md"),
	}
	// The two jobs that run that harness / that publish the unit, in full: an
	// `uses:` line installing ORAS is as much a regression as a call to it.
	for file, jobs := range map[string]map[string]wfJob{
		"ci.yml":      workflowJobs(t, root, "ci.yml"),
		"release.yml": workflowJobs(t, root, "release.yml"),
	} {
		for _, name := range []string{"ci-e2e-compose", "demo-compose"} {
			job, ok := jobs[name]
			if !ok {
				continue
			}
			b, err := yaml.Marshal(job)
			if err != nil {
				t.Fatalf("re-encode %s job %s: %v", file, name, err)
			}
			subjects[file+" job "+name] = string(b)
		}
	}
	for name, text := range subjects {
		for i, line := range strings.Split(text, "\n") {
			if orasRE.MatchString(line) {
				t.Errorf("%s:%d invokes ORAS on the Compose demo path: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// composeInvocation is one logical `docker compose` command line.
type composeInvocation struct {
	file, text string
	fileArgs   []string // every -f value
	project    string   // the -p value, "" when absent
	verbs      []string
}

var (
	fileArgRE   = regexp.MustCompile(`-f\s+"?([^"\s]+)"?`)
	projRE      = regexp.MustCompile(`-p\s+"?([^"\s]+)"?`)
	envAssignRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*\s+`)
)

// commandLines reduces a script or a fenced documentation block to the commands
// it actually runs: continuations joined, comments dropped, and the shell noise a
// command can hide behind (`if`, a subshell, leading environment assignments)
// removed — so that prose *about* a command, and a comment mentioning a script,
// are not mistaken for running one.
func commandLines(text string) []string {
	var out []string
	for _, line := range joinContinuations(text) {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			continue
		}
		for _, p := range []string{"if ", "then ", "! ", "(", "$ "} {
			l = strings.TrimSpace(strings.TrimPrefix(l, p))
		}
		for envAssignRE.MatchString(l) {
			l = envAssignRE.ReplaceAllString(l, "")
		}
		out = append(out, l)
	}
	return out
}

// shellText is the runnable part of a subject: a script whole, a document reduced
// to its fenced blocks. Prose that talks about a command is not a command.
func shellText(name, text string) string {
	if !strings.HasSuffix(name, ".md") {
		return text
	}
	var out []string
	var in bool
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			in = !in
			continue
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// composeStart is where a command line actually invokes compose — as the command
// itself or as arguments handed to a wrapper — and -1 when the only mention is
// inside a quoted string, which is a diagnostic message, not a call.
func composeStart(l string) int {
	for _, tok := range []string{"docker compose", "dc "} {
		for i := 0; i < len(l); {
			j := strings.Index(l[i:], tok)
			if j < 0 {
				break
			}
			at := i + j
			if (at == 0 || strings.ContainsAny(l[at-1:at], " \t(")) && strings.Count(l[:at], `"`)%2 == 0 {
				return at
			}
			i = at + len(tok)
		}
	}
	return -1
}

// composeInvocations finds every compose command in text, including calls through
// the harness's `dc` wrapper (which is `docker compose` plus the throwaway
// registry's plain-http flag) and its `up_or_dump` runner.
func composeInvocations(file, text string) []composeInvocation {
	var out []composeInvocation
	for _, l := range commandLines(shellText(file, text)) {
		at := composeStart(l)
		if at < 0 {
			continue
		}
		l = l[at:]
		if !strings.Contains(l, "-f ") {
			continue
		}
		inv := composeInvocation{file: file, text: l}
		for _, m := range fileArgRE.FindAllStringSubmatch(l, -1) {
			inv.fileArgs = append(inv.fileArgs, m[1])
		}
		if m := projRE.FindStringSubmatch(l); m != nil {
			inv.project = m[1]
		}
		for _, v := range []string{"publish", "up", "create", "run", "start", "restart", "stop", "down", "config", "ps", "logs"} {
			if strings.Contains(l, " "+v) {
				inv.verbs = append(inv.verbs, v)
			}
		}
		out = append(out, inv)
	}
	return out
}

func (c composeInvocation) has(verb string) bool {
	for _, v := range c.verbs {
		if v == verb {
			return true
		}
	}
	return false
}

// TestTheDemoIsExecutedFromTheArtifactByDigest: every compose command that RUNS
// the demo takes the application straight out of the registry, by digest. A local
// `compose.yaml` on the execution path means the user needed a checkout or an
// extracted directory after all; a tag means the artifact they pinned can start
// executing different bytes. Publication is the one place a local file is
// correct — it is the file being published.
func TestTheDemoIsExecutedFromTheArtifactByDigest(t *testing.T) {
	root := repoRoot(t)
	subjects := map[string]string{
		"tests/acceptance/local/compose-demo.sh": readFile(t, root, "tests", "acceptance", "local", "compose-demo.sh"),
		"docs/examples/compose-demo.md":          readFile(t, root, "docs", "examples", "compose-demo.md"),
		"release/orchestrator/dry-run.sh":        readFile(t, root, "release", "orchestrator", "dry-run.sh"),
	}
	var executions int
	for name, text := range subjects {
		for _, inv := range composeInvocations(name, text) {
			if inv.has("publish") {
				for _, f := range inv.fileArgs {
					if strings.HasPrefix(f, "oci://") {
						t.Errorf("%s publishes FROM an oci:// reference, which is not a compose file: %s", inv.file, inv.text)
					}
				}
				continue
			}
			executions++
			for _, f := range inv.fileArgs {
				if !strings.HasPrefix(f, "oci://") {
					t.Errorf("%s executes the demo from the local file %q — the journey must need no file on disk: %s", inv.file, f, inv.text)
					continue
				}
				if !strings.Contains(f, "@") {
					t.Errorf("%s executes the demo by tag %q — a tag can be moved, and the artifact's whole claim is that it cannot: %s", inv.file, f, inv.text)
				}
			}
		}
	}
	if executions == 0 {
		t.Error("no compose invocation anywhere executes the demo from an oci:// reference — this gate has nothing to gate")
	}
}

// TestEveryDemoProjectIsNamedExplicitly: the project name is the demo's identity
// on the user's machine — its containers, its network, its volumes and the only
// handle it has once the compose file is gone. Two versions that fell back to one
// implicit name would share all of that, and cleaning up one would take the other
// with it.
func TestEveryDemoProjectIsNamedExplicitly(t *testing.T) {
	root := repoRoot(t)
	for name, text := range map[string]string{
		"tests/acceptance/local/compose-demo.sh": readFile(t, root, "tests", "acceptance", "local", "compose-demo.sh"),
		"docs/examples/compose-demo.md":          readFile(t, root, "docs", "examples", "compose-demo.md"),
	} {
		projects := map[string]bool{}
		for _, inv := range composeInvocations(name, text) {
			if inv.has("publish") || !inv.has("up") {
				continue
			}
			if inv.project == "" {
				t.Errorf("%s brings the demo up with no -p, so its identity is whatever directory it ran in: %s", inv.file, inv.text)
				continue
			}
			projects[inv.project] = true
		}
		if len(projects) < 2 {
			t.Errorf("%s brings the demo up under %d project name(s) %v — two versions side by side is the case that needs two, and it is the case that breaks", name, len(projects), projects)
		}
	}
}

// --- the CI version floor -------------------------------------------------

type makeRule struct {
	prereqs []string
	recipe  string
}

var (
	makeRuleRE = regexp.MustCompile(`^([A-Za-z0-9_./-]+(?:\s+[A-Za-z0-9_./-]+)*)\s*::?\s*([^=].*)?$`)
	subMakeRE  = regexp.MustCompile(`\$\(MAKE\)\s+([A-Za-z0-9_./-]+)`)
	makeCallRE = regexp.MustCompile(`\bmake\s+([A-Za-z0-9_./-]+)`)

	composeScriptRE = regexp.MustCompile(`(?:bash|sh)\s+\S*(?:compose-demo|dry-run)\.sh|\./\S*(?:compose-demo|dry-run)\.sh`)
)

// makeRules reads every root-level makefile fragment into target -> rule.
func makeRules(t *testing.T, root string) map[string]makeRule {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "*.mk"))
	if err != nil {
		t.Fatalf("glob *.mk: %v", err)
	}
	files = append(files, filepath.Join(root, "Makefile"))
	rules := map[string]makeRule{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		var current []string
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "\t") {
				for _, tgt := range current {
					r := rules[tgt]
					r.recipe += line + "\n"
					rules[tgt] = r
				}
				continue
			}
			current = nil
			m := makeRuleRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			for _, tgt := range strings.Fields(m[1]) {
				r := rules[tgt]
				r.prereqs = append(r.prereqs, strings.Fields(m[2])...)
				rules[tgt] = r
				current = append(current, tgt)
			}
		}
	}
	if len(rules) == 0 {
		t.Fatal("no make rules found — the resolver below would silently pass")
	}
	return rules
}

// reachesScript reports whether running target eventually runs a script whose
// path contains needle, following prerequisites and same-directory $(MAKE) calls.
func reachesScript(rules map[string]makeRule, target, needle string, seen map[string]bool) bool {
	if seen[target] {
		return false
	}
	seen[target] = true
	r, ok := rules[target]
	if !ok {
		return false
	}
	if strings.Contains(r.recipe, needle) {
		return true
	}
	next := append([]string{}, r.prereqs...)
	for _, m := range subMakeRE.FindAllStringSubmatch(r.recipe, -1) {
		next = append(next, m[1])
	}
	for _, p := range next {
		if reachesScript(rules, p, needle, seen) {
			return true
		}
	}
	return false
}

// TestCIRunsAComposeThatOwnsThisArtifact: any job that publishes or executes the
// demo application has to be given a Compose new enough to have the verbs. The
// GitHub runner's own version is not something this repository controls, so the
// jobs pin one — and the pin is checked against the floor the demo itself
// declares, not against a number written twice.
func TestCIRunsAComposeThatOwnsThisArtifact(t *testing.T) {
	root := repoRoot(t)
	floor := composeFloor(t)
	rules := makeRules(t, root)

	// A job is on this path if it runs compose directly, runs one of the scripts,
	// or runs a make target that eventually does.
	onPath := func(job wfJob) bool {
		for _, l := range commandLines(job.runs()) {
			if strings.HasPrefix(l, "docker compose") || composeScriptRE.MatchString(l) {
				return true
			}
			for _, m := range makeCallRE.FindAllStringSubmatch(l, -1) {
				for _, needle := range []string{"compose-demo.sh", "dry-run.sh"} {
					if reachesScript(rules, m[1], needle, map[string]bool{}) {
						return true
					}
				}
			}
		}
		return false
	}

	var checked int
	for _, file := range []string{"ci.yml", "release.yml"} {
		for name, job := range workflowJobs(t, root, file) {
			if !onPath(job) {
				continue
			}
			checked++
			var pinned string
			for _, s := range job.Steps {
				if !strings.HasPrefix(s.Uses, "docker/setup-compose-action") {
					continue
				}
				pinned = fmt.Sprint(s.With["version"])
			}
			if pinned == "" || pinned == "<nil>" {
				t.Errorf("%s job %q runs the Compose demo path but pins no Compose version — it would publish or run the application on whatever the runner happens to ship, which may not have `publish` or `-f oci://` at all", file, name)
				continue
			}
			got, err := semver.NewVersion(strings.TrimPrefix(pinned, "v"))
			if err != nil {
				t.Errorf("%s job %q pins Compose %q, which is not a version", file, name, pinned)
				continue
			}
			if got.LessThan(floor) {
				t.Errorf("%s job %q pins Compose %s, older than the %s the demo declares it needs", file, name, got, floor)
			}
		}
	}
	if checked == 0 {
		t.Error("no CI job was found on the Compose demo path — either the harness stopped running in CI or this gate stopped finding it")
	}
}

// TestTheDemoDocumentationTeachesTheNativeJourney: the page a user actually
// follows. It has to teach the commands that exist — the digest-addressed
// `-f oci://`, the explicit project name, the version floor — and must not still
// teach a download-and-enter-a-directory install that no longer exists.
func TestTheDemoDocumentationTeachesTheNativeJourney(t *testing.T) {
	root := repoRoot(t)
	doc := readFile(t, root, "docs", "examples", "compose-demo.md")
	floor := composeFloor(t)

	for _, want := range []struct{ needle, why string }{
		{"-f oci://", "the command that runs the application straight out of the registry"},
		{"-p pacto-demo", "the explicit project name the rest of the page depends on"},
		{"-p pacto-demo-next", "the second project name, which is what makes two versions independent"},
		{"--pull never", "the offline run"},
		{"down -v", "cleanup, including the volumes"},
		{fmt.Sprintf("%d.%d", floor.Major(), floor.Minor()), "the minimum Compose version"},
	} {
		if !strings.Contains(doc, want.needle) {
			t.Errorf("docs/examples/compose-demo.md never mentions %q — %s", want.needle, want.why)
		}
	}
	for _, gone := range []string{"oras pull", "mkdir pacto-demo", "cd pacto-demo"} {
		if strings.Contains(doc, gone) {
			t.Errorf("docs/examples/compose-demo.md still teaches %q, which is not how this artifact is consumed", gone)
		}
	}
}
