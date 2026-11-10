package architecture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The gates on how Pacto runs its tests through Aval (CONTRIBUTING.md "Test
// Results Through Aval"), modelled on Aval's own tests/architecture/dogfood_test.go:
//
//   - main reuses only results its own CI signed, at runner level, on main or
//     on a same-repository pull request; a pull request also accepts
//     maintainers' local signatures, at developer level, and nothing else;
//   - only aval-publish and aval-record hold registry write or an OIDC token,
//     only on a push to main or a same-repository pull request the maintainer
//     opened, pushed and triggered, and they run no suite;
//   - the one workflow_run workflow that pushes to pull request branches
//     (ui-rebuild-commit.yml) pushes only Dependabot's own same-repository
//     rebuild to the commit it built;
//   - no workflow that runs aval can be started by pull_request_target,
//     workflow_run, issue_comment or another workflow's call into ci.yml;
//   - every suite job takes the policy and registry from main, installs the
//     pinned aval, reports and uploads what it recorded, and never swallows a
//     failure;
//   - every test a workflow runs goes through a make target into
//     .github/aval/run.sh and a contract; no workflow runs a test directly;
//   - every contract declares the platform it ran on and fingerprints cleanly;
//   - the registry is declared once and the aval release is pinned by sha256.

const (
	actionsIssuer = "https://token.actions.githubusercontent.com"
	// ownCIWorkflow and prCIWorkflow are the two identities main trusts at
	// runner level: ci.yml (job aval-publish) on main, and on a pull request
	// (refs/pull/<n>/merge, digits only). Forks get no OIDC token; the trust
	// condition is in .github/aval/main.yaml.
	ownCIWorkflow  = `^https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/heads/main$`
	prCIWorkflow   = `^https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/pull/[0-9]+/merge$`
	avalRegistry   = "ghcr.io/trianalab/pacto-attestations"
	avalPlatform   = "Linux x86_64"
	trustStepName  = "Aval trust policy and registry, from main"
	installStepRun = "make aval-install"
	summaryStepRun = "bash .github/aval/summary.sh"
	cosignStep     = "sigstore/cosign-installer@"
	uploadStep     = "actions/upload-artifact@"
)

// trustStepRun is the exact step every suite job runs before its suite: it
// executes main's trust-from-base.sh, never the checked-out ref's. A fetch
// that keeps failing leaves no policy (every suite runs) instead of failing
// the job: infrastructure trouble falls back to running the suite.
const trustStepRun = `fetched=
for i in 1 2 3; do
  /usr/bin/git fetch --no-tags --depth=1 origin +refs/heads/main:refs/remotes/origin/main && fetched=1 && break
  [ "$i" = 3 ] || sleep $((i * 10))
done
if [ -z "$fetched" ]; then
  echo "::warning::cannot fetch main: no trust policy, nothing is reused"
elif /usr/bin/git show origin/main:.github/aval/trust-from-base.sh > "$RUNNER_TEMP/trust-from-base.sh"; then
  /bin/bash "$RUNNER_TEMP/trust-from-base.sh"
else
  echo "::warning::main has no .github/aval/trust-from-base.sh: no trust policy, nothing is reused"
fi
`

// avalWorkflows are the workflows that run test suites.
var avalWorkflows = []string{"ci.yml", "delivery-demo.yml", "docs-check.yml"}

// --- policies ---------------------------------------------------------------

type avalPolicy struct {
	APIVersion          string         `yaml:"apiVersion"`
	Kind                string         `yaml:"kind"`
	TrustedRoot         string         `yaml:"trustedRoot"`
	MinTrustLevel       string         `yaml:"minTrustLevel"`
	Identities          []avalIdentity `yaml:"identities"`
	AllowedCacheability []string       `yaml:"allowedCacheability"`
}

type avalIdentity struct {
	Issuer        string `yaml:"issuer"`
	SubjectRegexp string `yaml:"subjectRegexp"`
	PublicKey     string `yaml:"publicKey"`
	TrustLevel    string `yaml:"trustLevel"`
}

// decodeStrict decodes one YAML document and rejects unknown fields, as Aval does.
func decodeStrict(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func loadAvalPolicy(t *testing.T, name string) avalPolicy {
	t.Helper()
	var p avalPolicy
	decodeStrict(t, filepath.Join(repoRoot(t), ".github", "aval", name), &p)
	return p
}

// literalRegexp reports whether s matches exactly one string.
func literalRegexp(s string) bool {
	re, err := regexp.Compile(s)
	if err != nil {
		return false
	}
	_, complete := re.LiteralPrefix()
	return complete
}

// mainPolicyProblems: main requires runner level and grants it only to
// ci.yml on main and ci.yml on a pull request, keyless, each written exactly
// as ownCIWorkflow and prCIWorkflow (anchored; a branch wildcard or an
// unanchored pattern fails). Any other identity is listed at developer level
// only, below the minimum, so its results are a miss on main (never reused,
// never a verification failure), and it must name one keyless subject: a
// catch-all would turn a forged signature from a verification failure into a
// miss on main and into a reused result on pull requests. Main reuses no
// external-dependencies result.
func mainPolicyProblems(p avalPolicy) []string {
	var problems []string
	if p.APIVersion != "aval.trianalab.io/v1" || p.Kind != "Policy" {
		problems = append(problems, "not an aval.trianalab.io/v1 Policy")
	}
	if p.MinTrustLevel != "runner" {
		problems = append(problems, "minTrustLevel must be runner")
	}
	if len(p.AllowedCacheability) != 0 {
		problems = append(problems, "main reuses no external-dependencies result: allowedCacheability must be empty")
	}
	runner := map[string]int{}
	for _, id := range p.Identities {
		switch {
		case id.PublicKey == "" && id.Issuer == actionsIssuer && (id.SubjectRegexp == ownCIWorkflow || id.SubjectRegexp == prCIWorkflow) && id.TrustLevel == "runner":
			runner[id.SubjectRegexp]++
		case id.TrustLevel == "developer" && id.PublicKey == "" && id.Issuer != "" && id.Issuer != actionsIssuer && literalRegexp(id.SubjectRegexp):
		default:
			problems = append(problems, fmt.Sprintf("untrusted identity %+v: only ci.yml on main or on a pull request at runner level, or one keyless maintainer subject at developer level", id))
		}
	}
	for _, re := range []string{ownCIWorkflow, prCIWorkflow} {
		if runner[re] != 1 {
			problems = append(problems, fmt.Sprintf("main must trust %s at runner level exactly once", re))
		}
	}
	return problems
}

// prPolicyProblems: a pull request accepts developer signatures too, so its
// minimum is developer level; it trusts exactly the identities main lists, at
// the same levels (a PR cannot raise a maintainer above developer), and may
// widen reuse at most to external-dependencies.
func prPolicyProblems(pr, main avalPolicy) []string {
	var problems []string
	if pr.APIVersion != main.APIVersion || pr.Kind != main.Kind || pr.TrustedRoot != main.TrustedRoot {
		problems = append(problems, "pr.yaml and main.yaml must share apiVersion, kind and trustedRoot")
	}
	if pr.MinTrustLevel != "developer" {
		problems = append(problems, "pr.yaml minTrustLevel must be developer")
	}
	byKey := func(ids []avalIdentity) []string {
		var out []string
		for _, id := range ids {
			out = append(out, fmt.Sprintf("%+v", id))
		}
		slices.Sort(out)
		return out
	}
	if !slices.Equal(byKey(pr.Identities), byKey(main.Identities)) {
		problems = append(problems, fmt.Sprintf("pr.yaml identities %v differ from main.yaml's %v: keep them in sync", byKey(pr.Identities), byKey(main.Identities)))
	}
	for _, c := range pr.AllowedCacheability {
		if c != "external-dependencies" {
			problems = append(problems, "pr.yaml allowedCacheability may list external-dependencies only, got "+c)
		}
	}
	return problems
}

func TestAvalPolicies(t *testing.T) {
	main, pr := loadAvalPolicy(t, "main.yaml"), loadAvalPolicy(t, "pr.yaml")
	for _, p := range append(mainPolicyProblems(main), prPolicyProblems(pr, main)...) {
		t.Error(p)
	}
}

func TestAvalPolicyChecksCatchLooseTrust(t *testing.T) {
	ci := avalIdentity{Issuer: actionsIssuer, SubjectRegexp: ownCIWorkflow, TrustLevel: "runner"}
	prCI := avalIdentity{Issuer: actionsIssuer, SubjectRegexp: prCIWorkflow, TrustLevel: "runner"}
	dev := avalIdentity{Issuer: "https://github.com/login/oauth", SubjectRegexp: `a@example\.com`, TrustLevel: "developer"}
	pol := func(min string, ids ...avalIdentity) avalPolicy {
		return avalPolicy{APIVersion: "aval.trianalab.io/v1", Kind: "Policy", MinTrustLevel: min, Identities: ids}
	}
	main := pol("runner", ci, prCI, dev)
	if p := mainPolicyProblems(main); len(p) != 0 {
		t.Fatalf("valid main policy reported %v", p)
	}
	if p := prPolicyProblems(pol("developer", prCI, ci, dev), main); len(p) != 0 {
		t.Fatalf("valid PR policy reported %v", p)
	}
	runnerDev := dev
	runnerDev.TrustLevel = "runner"
	wildDev := dev
	wildDev.SubjectRegexp = `.*@example\.com`
	widened := main
	widened.AllowedCacheability = []string{"external-dependencies"}
	runnerAt := func(re string) avalIdentity {
		return avalIdentity{Issuer: actionsIssuer, SubjectRegexp: re, TrustLevel: "runner"}
	}
	prDev := prCI
	prDev.TrustLevel = "developer"
	badMain := map[string]avalPolicy{
		"developer minimum":            pol("developer", ci, prCI, dev),
		"developer identity at runner": pol("runner", ci, prCI, runnerDev),
		"wildcard developer":           pol("runner", ci, prCI, wildDev),
		"key at runner":                pol("runner", ci, prCI, avalIdentity{PublicKey: "k.pub", TrustLevel: "runner"}),
		"key at developer":             pol("runner", ci, prCI, avalIdentity{PublicKey: "k.pub", TrustLevel: "developer"}),
		"other repository":             pol("runner", runnerAt(`^https://github\.com/evil/pacto/\.github/workflows/ci\.yml@refs/heads/main$`), prCI),
		"any branch":                   pol("runner", runnerAt(`^https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@.*$`), prCI),
		"any head branch":              pol("runner", ci, prCI, runnerAt(`^https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/heads/.*$`)),
		"unanchored pull request":      pol("runner", ci, runnerAt(`https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/pull/[0-9]+/merge`)),
		"unanchored main":              pol("runner", runnerAt(`https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/heads/main`), prCI),
		"any pull request ref":         pol("runner", ci, runnerAt(`^https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/pull/.*$`)),
		"fork-capable owner":           pol("runner", ci, runnerAt(`^https://github\.com/[^/]+/pacto/\.github/workflows/ci\.yml@refs/pull/[0-9]+/merge$`)),
		"pull request at developer":    pol("runner", ci, prDev, dev),
		"no pull request identity":     pol("runner", ci, dev),
		"pull request twice":           pol("runner", ci, prCI, prCI),
		"other workflow":               pol("runner", ci, prCI, runnerAt(`^https://github\.com/TrianaLab/pacto/\.github/workflows/release\.yml@refs/heads/main$`)),
		"other issuer":                 pol("runner", avalIdentity{Issuer: "https://evil", SubjectRegexp: ownCIWorkflow, TrustLevel: "runner"}, prCI),
		"isolated self-grant":          pol("runner", avalIdentity{Issuer: actionsIssuer, SubjectRegexp: ownCIWorkflow, TrustLevel: "isolated"}, prCI),
		"actions developer":            pol("runner", ci, prCI, avalIdentity{Issuer: actionsIssuer, SubjectRegexp: `https://github\.com/x`, TrustLevel: "developer"}),
		"no own CI":                    pol("runner", prCI, dev),
		"widened cacheability":         widened,
	}
	for name, p := range badMain {
		if len(mainPolicyProblems(p)) == 0 {
			t.Errorf("main policy with %s: no problem reported", name)
		}
	}
	nonDet := pol("developer", ci, prCI, dev)
	nonDet.AllowedCacheability = []string{"non-deterministic"}
	badPR := map[string]avalPolicy{
		"runner minimum":               pol("runner", ci, prCI, dev),
		"developer raised to runner":   pol("developer", ci, prCI, runnerDev),
		"extra developer":              pol("developer", ci, prCI, dev, avalIdentity{Issuer: "https://github.com/login/oauth", SubjectRegexp: `b@example\.com`, TrustLevel: "developer"}),
		"missing maintainer":           pol("developer", ci, prCI),
		"missing pull request CI":      pol("developer", ci, dev),
		"non-deterministic reuse":      nonDet,
		"wildcard developer":           pol("developer", ci, prCI, wildDev),
		"key at developer":             pol("developer", ci, prCI, dev, avalIdentity{PublicKey: "k.pub", TrustLevel: "developer"}),
		"isolated own CI":              pol("developer", avalIdentity{Issuer: actionsIssuer, SubjectRegexp: ownCIWorkflow, TrustLevel: "isolated"}, prCI, dev),
		"other trusted root":           func() avalPolicy { p := pol("developer", ci, prCI, dev); p.TrustedRoot = "root.json"; return p }(),
		"any pull request ref":         pol("developer", ci, runnerAt(`^https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/pull/.*$`), dev),
		"any head branch":              pol("developer", ci, prCI, runnerAt(`^https://github\.com/TrianaLab/pacto/\.github/workflows/ci\.yml@refs/heads/.*$`), dev),
		"maintainer identity replaced": pol("developer", ci, prCI, avalIdentity{Issuer: "https://github.com/login/oauth", SubjectRegexp: `evil@example\.com`, TrustLevel: "developer"}),
	}
	for name, p := range badPR {
		if len(prPolicyProblems(p, main)) == 0 {
			t.Errorf("PR policy with %s: no problem reported", name)
		}
	}
}

// --- workflows --------------------------------------------------------------

type avalStep struct {
	Name            string            `yaml:"name"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	If              string            `yaml:"if"`
	With            map[string]any    `yaml:"with"`
	Env             map[string]string `yaml:"env"`
	ContinueOnError any               `yaml:"continue-on-error"`
}

type avalJob struct {
	If              string            `yaml:"if"`
	Needs           any               `yaml:"needs"`
	Permissions     any               `yaml:"permissions"`
	Env             map[string]string `yaml:"env"`
	Uses            string            `yaml:"uses"`
	ContinueOnError any               `yaml:"continue-on-error"`
	Strategy        struct {
		Matrix map[string]any `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []avalStep `yaml:"steps"`
}

type avalWorkflow struct {
	On          any                `yaml:"on"`
	Env         map[string]string  `yaml:"env"`
	Permissions any                `yaml:"permissions"`
	Jobs        map[string]avalJob `yaml:"jobs"`
}

func loadAvalWorkflow(t *testing.T, name string) avalWorkflow {
	t.Helper()
	raw := readRepoFile(t, ".github", "workflows", name)
	var w avalWorkflow
	if err := yaml.Unmarshal([]byte(raw), &w); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(w.Jobs) == 0 {
		t.Fatalf("%s declares no jobs", name)
	}
	return w
}

// needsList is a job's needs, written as one name or a list.
func needsList(n any) []string {
	switch v := n.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

// writeScopes is the registry and OIDC write a permissions block grants.
func writeScopes(perms any) []string {
	switch v := perms.(type) {
	case string:
		if v == "write-all" {
			return []string{"write-all"}
		}
	case map[string]any:
		var out []string
		for _, scope := range []string{"packages", "id-token"} {
			if v[scope] == "write" {
				out = append(out, scope)
			}
		}
		return out
	}
	return nil
}

// suiteCommandRE matches a command that runs or reuses a test suite.
var suiteCommandRE = regexp.MustCompile(`\bgo test\b|\bnpm (?:run )?test\b|\bnpx playwright\b|\bplaywright test\b|\bnode --test\b|\bvitest\b|\bbash tests/|\baval run\b|\.github/aval/run\.sh|\bmake\s+(?:-\S+\s+)*(?:ci|test|e2e)-`)

// signingEvents is when aval-publish and aval-record run: a push to main, or a
// pull request from a branch of this repository (a fork gets no OIDC token
// anyway) that the maintainer opened, pushed and triggered. Where the head
// lives is not enough: anyone can open a pull request from an existing branch
// of this repository. Dependabot never matches. The trust condition behind
// signing pull requests at all is in .github/aval/main.yaml.
const signingEvents = "github.event_name == 'push' && github.ref == 'refs/heads/main' || " +
	"github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository && " +
	"github.event.pull_request.user.login == 'edu-diaz' && github.actor == 'edu-diaz' && github.triggering_actor == 'edu-diaz'"

// writeJobIf is the exact if: of each job that may hold a write scope.
// aval-publish signs nothing after a cancel; aval-record also publishes the
// records of failed runs.
var writeJobIf = map[string]string{
	"aval-publish": "${{ !cancelled() && (" + signingEvents + ") }}",
	"aval-record":  "${{ always() && (" + signingEvents + ") }}",
}

// writeJobProblems: registry write and the OIDC token belong to aval-publish
// (which signs) and aval-record (which publishes run records) only, with
// exactly the scopes they need, under exactly writeJobIf, and they run no
// suite: a suite runs code a pull request can change.
func writeJobProblems(name string, j avalJob) []string {
	scopes := writeScopes(j.Permissions)
	if len(scopes) == 0 {
		return nil
	}
	var problems []string
	want := map[string][]string{"aval-publish": {"packages", "id-token"}, "aval-record": {"packages"}}
	allowed, ok := want[name]
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("job %s holds %v: only aval-publish and aval-record may write to a registry or mint an OIDC token", name, scopes))
	case !slices.Equal(scopes, allowed):
		problems = append(problems, fmt.Sprintf("job %s holds %v, want exactly %v", name, scopes, allowed))
	}
	if m, isMap := j.Permissions.(map[string]any); !isMap || len(m) != len(allowed)+1 || m["contents"] != "read" {
		problems = append(problems, fmt.Sprintf("job %s: permissions must be contents: read plus %v, nothing else", name, allowed))
	}
	if ok && j.If != writeJobIf[name] {
		problems = append(problems, fmt.Sprintf("job %s holds %v, so its if: must be exactly %q, got %q", name, scopes, writeJobIf[name], j.If))
	}
	if j.Uses != "" {
		problems = append(problems, fmt.Sprintf("job %s holds %v and calls the workflow %s", name, scopes, j.Uses))
	}
	for _, s := range j.Steps {
		if suiteCommandRE.MatchString(s.Run) || strings.HasPrefix(s.Uses, "./.github/actions/ci") {
			problems = append(problems, fmt.Sprintf("job %s holds %v and runs a suite: %q", name, scopes, strings.TrimSpace(s.Run+s.Uses)))
		}
	}
	return problems
}

func TestAvalWriteScopedJobsRunOnlyOnPushAndRunNoSuite(t *testing.T) {
	for _, file := range avalWorkflows {
		w := loadAvalWorkflow(t, file)
		if s := writeScopes(w.Permissions); len(s) != 0 {
			t.Errorf("%s grants %v to every job", file, s)
		}
		for _, name := range slices.Sorted(maps.Keys(w.Jobs)) {
			for _, p := range writeJobProblems(name, w.Jobs[name]) {
				t.Errorf("%s: %s", file, p)
			}
		}
	}
	ci := loadAvalWorkflow(t, "ci.yml")
	for _, name := range []string{"aval-publish", "aval-record"} {
		if len(writeScopes(ci.Jobs[name].Permissions)) == 0 {
			t.Errorf("ci.yml has no %s job with its write scope", name)
		}
	}
}

func TestAvalWriteJobChecksCatchLooseJobs(t *testing.T) {
	perms := func(kv ...string) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	good := avalJob{If: writeJobIf["aval-publish"], Permissions: perms("contents", "read", "packages", "write", "id-token", "write"), Steps: []avalStep{{Run: "bash .github/aval/publish.sh .aval/results"}}}
	if p := writeJobProblems("aval-publish", good); len(p) != 0 {
		t.Fatalf("valid aval-publish reported %v", p)
	}
	record := avalJob{If: writeJobIf["aval-record"], Permissions: perms("contents", "read", "packages", "write")}
	if p := writeJobProblems("aval-record", record); len(p) != 0 {
		t.Fatalf("valid aval-record reported %v", p)
	}
	if p := writeJobProblems("ci-engine", avalJob{Permissions: perms("contents", "read", "packages", "read")}); len(p) != 0 {
		t.Fatalf("read-only job reported %v", p)
	}
	with := func(f func(*avalJob)) avalJob { j := good; j.Steps = slices.Clone(good.Steps); f(&j); return j }
	bad := map[string]struct {
		name string
		job  avalJob
	}{
		"suite job with id-token": {"ci-engine", with(func(j *avalJob) {})},
		"any event":               {"aval-publish", with(func(j *avalJob) { j.If = "always()" })},
		"any branch push":         {"aval-publish", with(func(j *avalJob) { j.If = "github.event_name == 'push'" })},
		"push to main only, loose": {"aval-publish", with(func(j *avalJob) {
			j.If = "${{ !cancelled() && github.event_name == 'push' && github.ref == 'refs/heads/main' }}"
		})},
		"fork pull request": {"aval-publish", with(func(j *avalJob) {
			j.If = strings.Replace(j.If, " && github.event.pull_request.head.repo.full_name == github.repository", "", 1)
		})},
		"outsider author on a same-repository head": {"aval-publish", with(func(j *avalJob) {
			j.If = strings.Replace(j.If, " && github.event.pull_request.user.login == 'edu-diaz'", "", 1)
		})},
		"outsider actor": {"aval-publish", with(func(j *avalJob) { j.If = strings.Replace(j.If, " && github.actor == 'edu-diaz'", "", 1) })},
		"outsider triggering actor": {"aval-publish", with(func(j *avalJob) {
			j.If = strings.Replace(j.If, " && github.triggering_actor == 'edu-diaz'", "", 1)
		})},
		"dependabot excluded only": {"aval-publish", with(func(j *avalJob) {
			j.If = strings.Replace(j.If, "github.event.pull_request.user.login == 'edu-diaz'", "github.event.pull_request.user.login != 'dependabot[bot]'", 1)
		})},
		"pull_request_target":       {"aval-publish", with(func(j *avalJob) { j.If = strings.Replace(j.If, "'pull_request'", "'pull_request_target'", 1) })},
		"record's condition":        {"aval-publish", with(func(j *avalJob) { j.If = writeJobIf["aval-record"] })},
		"runs a suite through make": {"aval-publish", with(func(j *avalJob) { j.Steps = append(j.Steps, avalStep{Run: "make ci-engine"}) })},
		"runs go test":              {"aval-publish", with(func(j *avalJob) { j.Steps = append(j.Steps, avalStep{Run: "go test ./..."}) })},
		"runs aval run":             {"aval-publish", with(func(j *avalJob) { j.Steps = append(j.Steps, avalStep{Run: "aval run -f aval.unit.yaml"}) })},
		"composite suite":           {"aval-publish", with(func(j *avalJob) { j.Steps = append(j.Steps, avalStep{Uses: "./.github/actions/ci"}) })},
		"record with id-token":      {"aval-record", with(func(j *avalJob) {})},
		"write-all":                 {"aval-publish", with(func(j *avalJob) { j.Permissions = "write-all" })},
		"extra contents write":      {"aval-publish", with(func(j *avalJob) { j.Permissions = perms("contents", "write", "packages", "write", "id-token", "write") })},
		"calls a workflow":          {"aval-publish", with(func(j *avalJob) { j.Uses = "./.github/workflows/delivery-demo.yml" })},
	}
	for name, c := range bad {
		if len(writeJobProblems(c.name, c.job)) == 0 {
			t.Errorf("%s: no problem reported", name)
		}
	}
}

// untrustedTriggers start a workflow with the base repository's token and
// OIDC on behalf of code or text from outside it (a fork's pull request, a
// comment, another workflow's run).
var untrustedTriggers = []string{"pull_request_target", "workflow_run", "issue_comment"}

// triggerProblems: a workflow that runs aval is started only by events whose
// OIDC subject is the workflow's own ref, never by untrustedTriggers. ci.yml,
// which signs, is not callable either: a caller's event decides the ref of the
// callee's token, so a pull_request_target caller would sign as main.
func triggerProblems(file string, on any) []string {
	events, ok := on.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: on: must be a map of events, got %T", file, on)}
	}
	var problems []string
	for _, e := range untrustedTriggers {
		if _, ok := events[e]; ok {
			problems = append(problems, fmt.Sprintf("%s runs aval and is started by %s", file, e))
		}
	}
	if _, ok := events["workflow_call"]; ok && file == "ci.yml" {
		problems = append(problems, "ci.yml signs results and must not be callable (workflow_call)")
	}
	return problems
}

func TestAvalWorkflowsHaveNoUntrustedTriggers(t *testing.T) {
	for _, file := range avalWorkflows {
		for _, p := range triggerProblems(file, loadAvalWorkflow(t, file).On) {
			t.Error(p)
		}
	}
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(readRepoFile(t, ".github", "workflows", filepath.Base(f)), ".github/workflows/ci.yml@") {
			t.Errorf("%s calls ci.yml, which signs results", filepath.Base(f))
		}
	}
	good := map[string]any{"push": nil, "pull_request": nil, "schedule": nil, "workflow_dispatch": nil}
	if p := triggerProblems("ci.yml", good); len(p) != 0 {
		t.Fatalf("valid triggers reported %v", p)
	}
	for _, e := range append(slices.Clone(untrustedTriggers), "workflow_call") {
		on := maps.Clone(good)
		on[e] = nil
		if len(triggerProblems("ci.yml", on)) == 0 {
			t.Errorf("ci.yml started by %s: no problem reported", e)
		}
	}
	if len(triggerProblems("ci.yml", "pull_request_target")) == 0 {
		t.Error("on: as a bare event: no problem reported")
	}
}

// pushingRunWorkflows pins every workflow started by an untrusted trigger:
// the exact if: of each job and strings its steps must contain. Such a
// workflow holds the base repository's token, and a push it makes to a
// same-repository branch lands in what main's policy trusts. A fork pull
// request can edit ui-rebuild.yml and still complete as "Rebuild dashboard
// UI" with its own artifact, so ui-rebuild-commit.yml acts only on
// Dependabot's run on a branch here, and only for Dependabot's pull request
// whose head is the commit that run built.
var pushingRunWorkflows = map[string]map[string]struct {
	If    string
	Steps []string
}{
	"ui-rebuild-commit.yml": {"commit": {
		If: "${{ github.event.workflow_run.event == 'pull_request' && github.event.workflow_run.conclusion == 'success' && " +
			"github.event.workflow_run.head_repository.full_name == github.repository && github.event.workflow_run.actor.login == 'dependabot[bot]' }}",
		Steps: []string{
			`RUN_HEAD_SHA: ${{ github.event.workflow_run.head_sha }}`,
			`[ "$author" != "dependabot[bot]" ]`,
			`[ "$head_sha" != "$RUN_HEAD_SHA" ]`,
			`[ "$(git rev-parse FETCH_HEAD)" != "$HEAD_SHA" ]`,
		},
	}},
}

// untrustedRunProblems: file (raw text src) is started by an untrusted
// trigger only if pushingRunWorkflows pins it, and then matches the pin.
func untrustedRunProblems(file, src string, w avalWorkflow) []string {
	events, _ := w.On.(map[string]any)
	untrusted := false
	for _, e := range untrustedTriggers {
		if _, ok := events[e]; ok {
			untrusted = true
		}
	}
	if !untrusted {
		return nil
	}
	pin, ok := pushingRunWorkflows[file]
	if !ok {
		return []string{fmt.Sprintf("%s is started by one of %v and is not pinned in pushingRunWorkflows", file, untrustedTriggers)}
	}
	var problems []string
	for name, j := range w.Jobs {
		p, ok := pin[name]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: job %s is not pinned", file, name))
		case j.If != p.If:
			problems = append(problems, fmt.Sprintf("%s: job %s if: must be exactly %q, got %q", file, name, p.If, j.If))
		}
		for _, want := range p.Steps {
			if !strings.Contains(src, want) {
				problems = append(problems, fmt.Sprintf("%s: job %s lost the check %q", file, name, want))
			}
		}
	}
	return problems
}

func TestUntrustedTriggerWorkflowsArePinned(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		for _, p := range untrustedRunProblems(name, readRepoFile(t, ".github", "workflows", name), loadAvalWorkflow(t, name)) {
			t.Error(p)
		}
	}
	const file = "ui-rebuild-commit.yml"
	src := readRepoFile(t, ".github", "workflows", file)
	w := loadAvalWorkflow(t, file)
	if p := untrustedRunProblems(file, src, w); len(p) != 0 {
		t.Fatalf("valid %s reported %v", file, p)
	}
	loose := func(f func(*avalWorkflow, *string)) (avalWorkflow, string) {
		c := w
		c.Jobs = maps.Clone(w.Jobs)
		s := src
		f(&c, &s)
		return c, s
	}
	bad := map[string]func(*avalWorkflow, *string){
		"any actor": func(c *avalWorkflow, _ *string) {
			j := c.Jobs["commit"]
			j.If = strings.Replace(j.If, " && github.event.workflow_run.actor.login == 'dependabot[bot]'", "", 1)
			c.Jobs["commit"] = j
		},
		"fork run": func(c *avalWorkflow, _ *string) {
			j := c.Jobs["commit"]
			j.If = strings.Replace(j.If, " && github.event.workflow_run.head_repository.full_name == github.repository", "", 1)
			c.Jobs["commit"] = j
		},
		"any pull request author": func(_ *avalWorkflow, s *string) {
			*s = strings.Replace(*s, `[ "$author" != "dependabot[bot]" ]`, "false", 1)
		},
		"any head": func(_ *avalWorkflow, s *string) {
			*s = strings.Replace(*s, `[ "$head_sha" != "$RUN_HEAD_SHA" ]`, "false", 1)
		},
		"extra job": func(c *avalWorkflow, _ *string) { c.Jobs["push"] = avalJob{} },
	}
	for name, f := range bad {
		c, s := loose(f)
		if len(untrustedRunProblems(file, s, c)) == 0 {
			t.Errorf("%s: no problem reported", name)
		}
	}
	if len(untrustedRunProblems("other.yml", "", avalWorkflow{On: map[string]any{"workflow_run": nil}})) == 0 {
		t.Error("unpinned workflow_run workflow: no problem reported")
	}
}

// TestCIIdentitiesMatchOnlyTheirSubjects: Aval matches subjectRegexp against
// the whole certificate subject; the two runner identities accept main and
// numbered pull request merge refs of ci.yml in this repository and nothing
// else.
func TestCIIdentitiesMatchOnlyTheirSubjects(t *testing.T) {
	base := "https://github.com/TrianaLab/pacto/.github/workflows/ci.yml@"
	match := func(subject string) bool {
		for _, re := range []string{ownCIWorkflow, prCIWorkflow} {
			if regexp.MustCompile(`^(?:` + re + `)$`).MatchString(subject) {
				return true
			}
		}
		return false
	}
	for _, s := range []string{base + "refs/heads/main", base + "refs/pull/1/merge", base + "refs/pull/4812/merge"} {
		if !match(s) {
			t.Errorf("%s is not trusted", s)
		}
	}
	for _, s := range []string{
		base + "refs/heads/feature", base + "refs/heads/main2", base + "refs/pull/1/head", base + "refs/pull/x/merge",
		base + "refs/pull//merge", base + "refs/pull/1/merge/x", base + "refs/tags/v1",
		"https://github.com/evil/pacto/.github/workflows/ci.yml@refs/pull/1/merge",
		"https://github.com/TrianaLab/pacto/.github/workflows/release.yml@refs/pull/1/merge",
		"x" + base + "refs/heads/main",
	} {
		if match(s) {
			t.Errorf("%s is trusted", s)
		}
	}
}

// matrixRE matches a matrix expression inside a step.
var matrixRE = regexp.MustCompile(`\$\{\{ *matrix\.([A-Za-z0-9_-]+) *\}\}`)

// expandMatrix is text once per value of the one matrix key it names (as
// GitHub runs it), or text itself when it names none.
func expandMatrix(t *testing.T, text string, matrix map[string]any) []string {
	t.Helper()
	m := matrixRE.FindStringSubmatch(text)
	if m == nil {
		return []string{text}
	}
	values, ok := matrix[m[1]].([]any)
	if !ok || len(values) == 0 {
		t.Fatalf("%q names matrix.%s, which is not a list in the job's matrix", text, m[1])
	}
	var out []string
	for _, v := range values {
		out = append(out, expandMatrix(t, strings.ReplaceAll(text, m[0], fmt.Sprint(v)), matrix)...)
	}
	return out
}

// makeTargetRE matches the targets of a `make` command line.
var makeTargetRE = regexp.MustCompile(`(?m)\bmake((?: +[A-Za-z0-9_./-]+)+)`)

// stepMakeTargets is every root make target a step runs, the matrix expanded:
// `make <targets>` in its shell, or the target of the ./.github/actions/ci
// composite, which runs `make <target>`.
func stepMakeTargets(t *testing.T, s avalStep, matrix map[string]any) []string {
	t.Helper()
	text := s.Run
	if strings.HasPrefix(s.Uses, "./.github/actions/ci") {
		text = "make " + fmt.Sprint(s.With["target"])
	}
	var out []string
	for _, line := range expandMatrix(t, text, matrix) {
		for _, m := range makeTargetRE.FindAllStringSubmatch(line, -1) {
			for _, f := range strings.Fields(m[1]) {
				if !strings.HasPrefix(f, "-") && !strings.Contains(f, "=") {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

// avalSuiteRE matches a suite in the dry run of a make target.
var avalSuiteRE = regexp.MustCompile(`(?m)^bash \.github/aval/run\.sh ([a-z0-9.-]+)$`)

// makeDryRun is what `make -n <args>` would run from the repository root,
// comment lines dropped, cached per argument list.
func makeDryRun(t *testing.T, cache map[string]string, args ...string) string {
	t.Helper()
	key := strings.Join(args, " ")
	if out, ok := cache[key]; ok {
		return out
	}
	cmd := exec.Command("make", append([]string{"-n", "--no-print-directory"}, args...)...) //nolint:gosec // fixed make targets
	cmd.Dir = repoRoot(t)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n %s: %v\n%s", key, err, raw)
	}
	var lines []string
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines = append(lines, line)
		}
	}
	cache[key] = strings.Join(lines, "\n")
	return cache[key]
}

// suiteJob is a workflow job that runs at least one suite, and the suites.
type suiteJob struct {
	file, name string
	job        avalJob
	suites     []string
	firstSuite int // index of the first step that runs a suite
}

// suiteJobs reads which jobs of the suite workflows run suites, through the
// make targets their steps run.
func suiteJobs(t *testing.T, cache map[string]string) []suiteJob {
	t.Helper()
	var out []suiteJob
	for _, file := range avalWorkflows {
		w := loadAvalWorkflow(t, file)
		for _, name := range slices.Sorted(maps.Keys(w.Jobs)) {
			j := w.Jobs[name]
			sj := suiteJob{file: file, name: name, job: j, firstSuite: -1}
			for i, s := range j.Steps {
				for _, target := range stepMakeTargets(t, s, j.Strategy.Matrix) {
					suites := avalSuiteRE.FindAllStringSubmatch(makeDryRun(t, cache, target), -1)
					for _, m := range suites {
						sj.suites = append(sj.suites, m[1])
					}
					if len(suites) > 0 && sj.firstSuite < 0 {
						sj.firstSuite = i
					}
				}
			}
			if len(sj.suites) > 0 {
				out = append(out, sj)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no workflow job runs a suite through aval: the checks below would pass vacuously")
	}
	return out
}

// contractEnv is the environment.env of aval.<suite>.yaml.
func contractEnv(t *testing.T, suite string) []string {
	t.Helper()
	return loadContract(t, suite).Environment.Env
}

// TestSuiteJobsWireAval: a suite job takes the trust policy and the registry
// from main, installs the pinned aval with cosign at hand, then runs its
// suites; afterwards, whatever happened, it reports and uploads what it
// recorded for aval-publish and aval-record. Nothing may swallow a failure (a
// verification failure is exit 5), and no workflow sets a variable a contract
// fingerprints, so the suite job and aval-publish compute the same fingerprint.
func TestSuiteJobsWireAval(t *testing.T) {
	cache := map[string]string{}
	ci := loadAvalWorkflow(t, "ci.yml")
	publishNeeds := needsList(ci.Jobs["aval-publish"].Needs)
	recordNeeds := needsList(ci.Jobs["aval-record"].Needs)
	callers := map[string]string{} // called workflow -> the ci.yml job calling it
	for name, j := range ci.Jobs {
		if j.Uses != "" {
			callers[strings.TrimPrefix(j.Uses, "./.github/workflows/")] = name
		}
	}
	for _, sj := range suiteJobs(t, cache) {
		where := sj.file + " job " + sj.name
		j := sj.job
		if j.ContinueOnError != nil {
			t.Errorf("%s sets continue-on-error: a failing suite or a verification failure must fail it", where)
		}
		cosign, trust, install := -1, -1, -1
		var summary, upload bool
		for i, s := range j.Steps {
			if s.ContinueOnError != nil {
				t.Errorf("%s step %d sets continue-on-error: a failing suite or a verification failure must fail the job", where, i)
			}
			switch {
			case strings.HasPrefix(s.Uses, cosignStep):
				cosign = i
			case s.Name == trustStepName:
				trust = i
				if s.Run != trustStepRun {
					t.Errorf("%s: the %q step must run exactly main's trust-from-base.sh:\n%s\ngot:\n%s", where, trustStepName, trustStepRun, s.Run)
				}
			case strings.TrimSpace(s.Run) == installStepRun:
				install = i
			case strings.TrimSpace(s.Run) == summaryStepRun:
				summary = s.If == "always()"
			case strings.HasPrefix(s.Uses, uploadStep) && s.If == "always()":
				name, _ := s.With["name"].(string)
				if strings.HasPrefix(name, "aval-") && s.With["path"] == ".aval/*.json" && s.With["include-hidden-files"] == true {
					upload = true
				}
			}
		}
		if cosign < 0 || trust < 0 || install < 0 || cosign > install || trust > install || install > sj.firstSuite {
			t.Errorf("%s runs %v but not after cosign-installer, the %q step and `%s`, in that order before its first suite (cosign %d, trust %d, install %d, suite %d)",
				where, sj.suites, trustStepName, installStepRun, cosign, trust, install, sj.firstSuite)
		}
		if !summary {
			t.Errorf("%s has no `if: always()` step running %s, which fails on a verification failure among the rejected candidates", where, summaryStepRun)
		}
		if !upload {
			t.Errorf("%s has no `if: always()` %s step named aval-* uploading .aval/*.json with include-hidden-files: true, so aval-publish and aval-record never see its results", where, uploadStep)
		}
		gate := sj.name
		if sj.file != "ci.yml" {
			gate = callers[sj.file]
		}
		if sj.file != "docs-check.yml" && (gate == "" || !slices.Contains(publishNeeds, gate) || !slices.Contains(recordNeeds, gate)) {
			t.Errorf("%s runs suites but %q is missing from the needs of ci.yml's aval-publish or aval-record, so main never signs or records them", where, gate)
		}
		// No variable a contract fingerprints may be set by the workflow: the
		// publish job, which recomputes the fingerprint, would not see it.
		w := loadAvalWorkflow(t, sj.file)
		for _, suite := range sj.suites {
			for _, name := range contractEnv(t, suite) {
				set := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `=`)
				if _, ok := w.Env[name]; ok {
					t.Errorf("%s sets %s for every job, which aval.%s.yaml fingerprints", sj.file, name, suite)
				}
				if _, ok := j.Env[name]; ok {
					t.Errorf("%s sets %s, which aval.%s.yaml fingerprints", where, name, suite)
				}
				for _, s := range j.Steps {
					if _, ok := s.Env[name]; ok || set.MatchString(s.Run) {
						t.Errorf("%s sets %s in a step, which aval.%s.yaml fingerprints: put it in the contract's runner.command", where, name, suite)
					}
				}
			}
		}
	}
	required := needsList(ci.Jobs["required"].Needs)
	for _, name := range []string{"aval-publish", "aval-record"} {
		if !slices.Contains(required, name) {
			t.Errorf("ci.yml's required job does not need %s", name)
		}
	}
}

// artifactSuites maps each aval-* artifact a job of the workflow file uploads,
// its matrix expanded, to the suites that job (or matrix leg) runs.
func artifactSuites(t *testing.T, cache map[string]string, file string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	w := loadAvalWorkflow(t, file)
	for _, name := range slices.Sorted(maps.Keys(w.Jobs)) {
		j := w.Jobs[name]
		for _, up := range j.Steps {
			art, _ := up.With["name"].(string)
			if !strings.HasPrefix(up.Uses, uploadStep) || !strings.HasPrefix(art, "aval-") {
				continue
			}
			legs := []map[string]any{j.Strategy.Matrix}
			if m := matrixRE.FindStringSubmatch(art); m != nil {
				values, _ := j.Strategy.Matrix[m[1]].([]any)
				legs = nil
				for _, v := range values {
					leg := maps.Clone(j.Strategy.Matrix)
					leg[m[1]] = []any{v}
					legs = append(legs, leg)
				}
			} else if len(j.Strategy.Matrix) > 0 {
				t.Errorf("%s job %s uploads %s from every matrix leg: name it after the matrix value", file, name, art)
			}
			for _, leg := range legs {
				var suites []string
				for _, s := range j.Steps {
					for _, target := range stepMakeTargets(t, s, leg) {
						for _, m := range avalSuiteRE.FindAllStringSubmatch(makeDryRun(t, cache, target), -1) {
							suites = append(suites, m[1])
						}
					}
				}
				out[expandMatrix(t, art, leg)[0]] = suites
			}
		}
	}
	return out
}

// publishTableRE is the artifact table in publish.sh: allowed='<lines>'.
var publishTableRE = regexp.MustCompile(`(?s)\nallowed='\n(.*?)\n'\n`)

// TestPublishTrustsEachArtifactOnlyForItsOwnSuites: aval-publish signs a
// result only from the artifact of the job that runs that suite. Any suite
// job runs code nobody verifies (a downloaded CLI, an upstream manifest); if
// publish.sh took a result from any artifact, that code could write a passing
// result for another suite and get it signed at runner level on main.
// publish.sh's table must be exactly the artifacts ci.yml's run uploads and
// the suites each runs, with no suite in two artifacts.
func TestPublishTrustsEachArtifactOnlyForItsOwnSuites(t *testing.T) {
	cache := map[string]string{}
	want := artifactSuites(t, cache, "ci.yml")
	maps.Copy(want, artifactSuites(t, cache, "delivery-demo.yml"))
	owner := map[string]string{}
	for _, art := range slices.Sorted(maps.Keys(want)) {
		for _, suite := range want[art] {
			if prev, ok := owner[suite]; ok {
				t.Errorf("suite %s runs in the jobs of both %s and %s: publish.sh could not tell which result to trust", suite, prev, art)
			}
			owner[suite] = art
		}
	}
	m := publishTableRE.FindStringSubmatch(readRepoFile(t, ".github", "aval", "publish.sh"))
	if m == nil {
		t.Fatal("publish.sh has no allowed='<artifact suite...>' table")
	}
	got := map[string][]string{}
	for line := range strings.SplitSeq(m[1], "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			if _, dup := got[f[0]]; dup {
				t.Errorf("publish.sh lists %s twice", f[0])
			}
			got[f[0]] = f[1:]
		}
	}
	for _, art := range slices.Sorted(maps.Keys(want)) {
		if !slices.Equal(got[art], want[art]) {
			t.Errorf("publish.sh: %s must carry exactly %v (what its job runs), lists %v", art, want[art], got[art])
		}
	}
	for art := range got {
		if _, ok := want[art]; !ok {
			t.Errorf("publish.sh lists %s, which no job of ci.yml's run uploads", art)
		}
	}
}

// directTestAllowed are workflow steps that run a test script for something
// other than its test: each builds or pulls the images the suites load.
var directTestAllowed = map[string]string{
	"ci.yml:delivery-images":   "DELIVERY_SAVE_IMAGES makes delivery.sh build and save the images, then stop; delivery-demo.sh save-images pulls pinned images",
	"delivery-demo.yml:images": "the same image build as ci.yml's delivery-images, for the nightly",
}

// suiteScriptRE is a runner.command word naming a repository script.
var suiteScriptRE = regexp.MustCompile(`^[A-Za-z0-9_./-]+\.(?:sh|mjs|js|py)$`)

// contractCommands is what only a contract may run: every runner.command,
// joined, and every repository script one names.
func contractCommands(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, suite := range contractSuites(t) {
		cmd := loadContract(t, suite).Runner.Command
		out = append(out, strings.Join(cmd, " "))
		for _, w := range cmd {
			if suiteScriptRE.MatchString(w) {
				if _, err := os.Stat(filepath.Join(repoRoot(t), w)); err == nil {
					out = append(out, w)
				}
			}
		}
	}
	return out
}

// TestNoWorkflowRunsATestDirectly: a workflow runs a test only through a make
// target that hands it to .github/aval/run.sh. A step that runs go test, npm
// test, Playwright, node --test, a tests/ script, aval itself, a contract's
// runner.command or a script one names bypasses the lookup, the record and
// the publication; so does a make target that does.
func TestNoWorkflowRunsATestDirectly(t *testing.T) {
	cache := map[string]string{}
	direct := regexp.MustCompile(`\bgo test\b|\bnpm (?:run )?test\b|\bnpx playwright\b|\bplaywright test\b|\bnode --test\b|\bvitest\b|\bbash tests/|\baval run\b`)
	suiteCmds := contractCommands(t)
	// runsSuite is what in line runs a contract's suite outside aval, or "".
	runsSuite := func(line string) string {
		if m := direct.FindString(line); m != "" {
			return m
		}
		for _, c := range suiteCmds {
			if strings.Contains(line, c) {
				return c
			}
		}
		return ""
	}
	for _, file := range avalWorkflows {
		w := loadAvalWorkflow(t, file)
		for _, name := range slices.Sorted(maps.Keys(w.Jobs)) {
			if _, ok := directTestAllowed[file+":"+name]; ok {
				continue
			}
			j := w.Jobs[name]
			for _, s := range j.Steps {
				if m := runsSuite(s.Run); m != "" {
					t.Errorf("%s job %s runs %q directly: run its make target, which goes through aval", file, name, m)
				}
				for _, target := range stepMakeTargets(t, s, j.Strategy.Matrix) {
					var bypass []string
					for line := range strings.SplitSeq(makeDryRun(t, cache, target), "\n") {
						if m := runsSuite(line); m != "" {
							bypass = append(bypass, strings.TrimSpace(line))
						}
					}
					if len(bypass) > 0 {
						t.Errorf("%s job %s runs `make %s`, which runs a test outside aval: %v", file, name, target, bypass)
					}
				}
			}
		}
	}
	for key := range directTestAllowed {
		file, job, _ := strings.Cut(key, ":")
		if _, ok := loadAvalWorkflow(t, file).Jobs[job]; !ok {
			t.Errorf("directTestAllowed names %s, which no longer exists: drop the entry", key)
		}
	}
}

// --- make targets and contracts ---------------------------------------------

type avalContract struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Runner struct {
		Command []string `yaml:"command"`
	} `yaml:"runner"`
	Scope struct {
		Source       []string         `yaml:"source"`
		Exclude      []string         `yaml:"exclude"`
		Dependencies []string         `yaml:"dependencies"`
		AllowMutable []map[string]any `yaml:"allowMutable"`
		Resolve      []map[string]any `yaml:"resolve"`
		Contracts    []string         `yaml:"contracts"`
	} `yaml:"scope"`
	Environment struct {
		Toolchain map[string]string   `yaml:"toolchain"`
		Probe     map[string][]string `yaml:"probe"`
		Env       []string            `yaml:"env"`
	} `yaml:"environment"`
	Cacheability string   `yaml:"cacheability"`
	Outputs      []string `yaml:"outputs"`
}

func loadContract(t *testing.T, suite string) avalContract {
	t.Helper()
	var c avalContract
	decodeStrict(t, filepath.Join(repoRoot(t), "aval."+suite+".yaml"), &c)
	return c
}

// contractSuites is the stem of every aval.<suite>.yaml at the repository root.
func contractSuites(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot(t), "aval.*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no aval.*.yaml at the repository root (%v)", err)
	}
	var out []string
	for _, p := range paths {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "aval."), ".yaml"))
	}
	return out
}

var (
	// literalRunRE is a recipe's `$(AVAL_RUN) <suite>`.
	literalRunRE = regexp.MustCompile(`(?m)^\t@?\$\(AVAL_RUN\) ([a-z0-9.-]+)$`)
	// patternRunRE is a pattern rule running one suite per target:
	// `$(LIST): <prefix>%:` then `$(AVAL_RUN) <suite prefix>$*`.
	patternRunRE = regexp.MustCompile(`(?m)^\$\(([A-Z_]+)\): ([a-z0-9.-]+)%:\n\t@?\$\(AVAL_RUN\) ([a-z0-9.-]+)\$\*$`)
	// avalAssignRE is a makefile assignment of what only the environment decides.
	avalAssignRE = regexp.MustCompile(`(?m)^\s*(?:export\s+|override\s+)?AVAL_(?:REGISTRY|POLICY|PUSH)\s*[:?+!]?=`)
)

// makeVariable is the target list a root makefile variable names, written
// `NAME := $(addprefix <prefix>,<words>)` (GNU make 3.81, macOS's, has no
// --eval to ask make itself).
func makeVariable(t *testing.T, name string) string {
	t.Helper()
	mk := readRepoFile(t, "Makefile") + "\n" + readRepoFile(t, "ci.mk")
	m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` :?= \$\(addprefix ([^,]+),([^)]+)\)$`).FindStringSubmatch(mk)
	if m == nil {
		t.Fatalf("%s is not written as NAME := $(addprefix <prefix>,<words>)", name)
	}
	var out []string
	for _, w := range strings.Fields(m[2]) {
		out = append(out, m[1]+w)
	}
	return strings.Join(out, " ")
}

// runSuites maps every suite a make target runs to that target: the literal
// `$(AVAL_RUN) <suite>` recipes, and each target of a pattern rule's list.
func runSuites(t *testing.T, cache map[string]string) map[string]string {
	t.Helper()
	mk := readRepoFile(t, "Makefile") + "\n" + readRepoFile(t, "ci.mk")
	out := map[string]string{}
	for _, m := range literalRunRE.FindAllStringSubmatch(mk, -1) {
		out[m[1]] = "a literal $(AVAL_RUN) " + m[1]
	}
	for _, m := range patternRunRE.FindAllStringSubmatch(mk, -1) {
		targets := strings.Fields(makeVariable(t, m[1]))
		if len(targets) == 0 {
			t.Fatalf("$(%s) lists no targets", m[1])
		}
		for _, target := range targets {
			suite := m[3] + strings.TrimPrefix(target, m[2])
			if !slices.ContainsFunc(avalSuiteRE.FindAllStringSubmatch(makeDryRun(t, cache, target), -1), func(m []string) bool { return m[1] == suite }) {
				t.Errorf("make %s does not run suite %s", target, suite)
			}
			out[suite] = target
		}
	}
	if len(out) == 0 {
		t.Fatal("no make target runs a suite through $(AVAL_RUN)")
	}
	return out
}

// shellMakeRE is a make call inside a `bash -c` string.
var shellMakeRE = regexp.MustCompile(`(?:^|[;&|(\s])make\b`)

// recursionProblem says how cmd reaches .github/aval/run.sh again, or "": a
// make target (after any `env` and KEY=VALUE words) whose dry run calls it,
// or a make call inside a shell string, which the dry run cannot follow.
func recursionProblem(t *testing.T, cache map[string]string, cmd []string) string {
	t.Helper()
	for len(cmd) > 0 && (cmd[0] == "env" || strings.Contains(cmd[0], "=")) {
		cmd = cmd[1:]
	}
	if len(cmd) > 1 && cmd[0] == "make" && strings.Contains(makeDryRun(t, cache, cmd[1:]...), ".github/aval/run.sh") {
		return fmt.Sprintf("%v runs a suite through aval again", cmd)
	}
	if i := slices.Index(cmd, "-c"); i >= 0 && i+1 < len(cmd) && shellMakeRE.MatchString(cmd[i+1]) {
		return fmt.Sprintf("%q calls make from a shell string", cmd[i+1])
	}
	return ""
}

func TestRecursionChecksCatchWrappers(t *testing.T) {
	cache := map[string]string{}
	for _, cmd := range [][]string{
		{"make", "ci-perf-envtest"},
		{"env", "PERF_QUICK=1", "make", "ci-perf-envtest"},
		{"bash", "-c", "make ci-ui && true"},
		{"sh", "-c", "cd x; make test"},
	} {
		if recursionProblem(t, cache, cmd) == "" {
			t.Errorf("%v: no problem reported", cmd)
		}
	}
	if p := recursionProblem(t, cache, []string{"env", "PERF_QUICK=1", "make", "perf-envtest"}); p != "" {
		t.Errorf("the inner perf-envtest target reported: %s", p)
	}
}

// TestEveryTestTargetGoesThroughAval: every suite a make target runs has its
// contract; every contract is either run by a target or shared by the
// contracts that name it in scope.contracts; a contract's runner.command never
// reaches .github/aval/run.sh again (no recursion through a wrapper target);
// and nothing in the makefiles decides the registry, the policy or pushing,
// which only the environment (CI's trust step, or the developer) may set.
func TestEveryTestTargetGoesThroughAval(t *testing.T) {
	cache := map[string]string{}
	suites := runSuites(t, cache)
	contracts := contractSuites(t)
	for _, suite := range slices.Sorted(maps.Keys(suites)) {
		if !slices.Contains(contracts, suite) {
			t.Errorf("%s runs suite %s, which has no aval.%s.yaml", suites[suite], suite, suite)
		}
	}
	shared := map[string]bool{}
	for _, suite := range contracts {
		for _, ref := range loadContract(t, suite).Scope.Contracts {
			shared[strings.TrimSuffix(strings.TrimPrefix(ref, "aval."), ".yaml")] = true
		}
	}
	for _, suite := range contracts {
		_, run := suites[suite]
		if !run && !shared[suite] {
			t.Errorf("aval.%s.yaml is run by no make target and named in no contract's scope.contracts", suite)
		}
		if p := recursionProblem(t, cache, loadContract(t, suite).Runner.Command); p != "" {
			t.Errorf("aval.%s.yaml: %s: runner.command must be the inner command, never a wrapper target (run.sh refuses it at run time too)", suite, p)
		}
	}
	for _, f := range []string{"Makefile", "ci.mk", filepath.Join("integrations", "kubernetes", "Makefile"), filepath.Join("integrations", "kubernetes", "ci.mk")} {
		if m := avalAssignRE.FindString(readRepoFile(t, f)); m != "" {
			t.Errorf("%s assigns %q: the registry, the policy and pushing come from the environment only", f, strings.TrimSpace(m))
		}
	}
}

// commandLineRE is the one-line runner.command .github/aval/run.sh parses.
var commandLineRE = regexp.MustCompile(`(?m)^  command: (\[.*\])$`)

// jobEnvNames are variables whose value differs between runs, jobs or
// machines for reasons that are not the suite's input: fingerprinting one
// makes every run a miss, or makes a suite job and aval-publish disagree.
var jobEnvNames = regexp.MustCompile(`^(?:AVAL_.*|SIGSTORE_.*|GITHUB_.*|RUNNER_.*|ACTIONS_.*|CI|HOME|PATH|PWD|TMPDIR|USER|XDG_.*|DOCKER_CONFIG|KUBEBUILDER_ASSETS)$`)

// TestContractsDeclarePlatformAndScope: every contract parses as Aval reads it,
// is named after its file, has a one-line runner.command run.sh can split, and
// fingerprints no job-specific variable. Every contract a make target runs
// declares the platform it ran on (environment.toolchain.platform, checked by
// `uname -sm`), so a macOS laptop's result never matches Linux CI, and every
// toolchain value it declares is checked by a probe.
func TestContractsDeclarePlatformAndScope(t *testing.T) {
	suites := runSuites(t, map[string]string{})
	classes := []string{"hermetic", "controlled-environment", "external-dependencies", "non-deterministic"}
	for _, suite := range contractSuites(t) {
		c := loadContract(t, suite)
		where := "aval." + suite + ".yaml"
		if c.APIVersion != "aval.trianalab.io/v1" || c.Kind != "Test" {
			t.Errorf("%s is not an aval.trianalab.io/v1 Test", where)
		}
		if c.Metadata.Name != "pacto-"+suite {
			t.Errorf("%s: metadata.name %q, want %q", where, c.Metadata.Name, "pacto-"+suite)
		}
		if !slices.Contains(classes, c.Cacheability) {
			t.Errorf("%s: cacheability %q is not one of %v", where, c.Cacheability, classes)
		}
		if len(c.Scope.Source) == 0 || len(c.Runner.Command) == 0 {
			t.Errorf("%s needs runner.command and scope.source", where)
		}
		m := commandLineRE.FindAllStringSubmatch(readRepoFile(t, "aval."+suite+".yaml"), -1)
		var argv []string
		if len(m) != 1 || json.Unmarshal([]byte(m[0][1]), &argv) != nil || !slices.Equal(argv, c.Runner.Command) {
			t.Errorf("%s: runner.command must be one line, `  command: [\"word\", ...]`, which .github/aval/run.sh splits without a YAML parser", where)
		}
		for _, w := range c.Runner.Command {
			if w == "" || strings.ContainsAny(w, `,"\`) {
				t.Errorf("%s: runner.command word %q is empty or holds a comma, quote or backslash, which run.sh cannot split", where, w)
			}
		}
		for _, name := range c.Environment.Env {
			if jobEnvNames.MatchString(name) {
				t.Errorf("%s fingerprints %s, which differs per job or machine for reasons outside the suite", where, name)
			}
		}
		for key := range c.Environment.Toolchain {
			if _, ok := c.Environment.Probe[key]; !ok {
				t.Errorf("%s declares toolchain %s=%s with no probe to check it", where, key, c.Environment.Toolchain[key])
			}
		}
		if _, run := suites[suite]; !run {
			continue // a shared block, fingerprinted inside the contracts naming it
		}
		if c.Environment.Toolchain["platform"] != avalPlatform || !slices.Equal(c.Environment.Probe["platform"], []string{"uname", "-sm"}) {
			t.Errorf("%s must declare toolchain platform %q with probe [uname, -sm]: a result from another platform must never match CI", where, avalPlatform)
		}
	}
}

// TestAvalRegistryDeclaredOnce: the registry is one value in one place, the
// env of ci.yml's aval-publish, which trust-from-base.sh reads (from main on
// pull requests). No workflow sets the policy or asks to push, and only the
// trust step writes AVAL_* into GITHUB_ENV.
func TestAvalRegistryDeclaredOnce(t *testing.T) {
	decl := regexp.MustCompile(`(?m)^ *AVAL_REGISTRY: *(.*)$`)
	count := 0
	files, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		text := readRepoFile(t, ".github", "workflows", filepath.Base(f))
		for _, m := range decl.FindAllStringSubmatch(text, -1) {
			count++
			if filepath.Base(f) != "ci.yml" || strings.TrimSpace(m[1]) != avalRegistry {
				t.Errorf("%s declares AVAL_REGISTRY: %s; the one declaration is %s in ci.yml's aval-publish", filepath.Base(f), m[1], avalRegistry)
			}
		}
		if m := regexp.MustCompile(`(?m)^ *AVAL_(?:POLICY|PUSH):|AVAL_[A-Z_]*=[^\n]*GITHUB_ENV|\baval\b[^\n]*--(?:policy|push)\b`).FindString(text); m != "" {
			t.Errorf("%s sets %q: the policy comes from main's trust-from-base.sh only, and CI pushes only in aval-publish", filepath.Base(f), m)
		}
	}
	if count != 1 {
		t.Errorf("AVAL_REGISTRY is declared %d times across the workflows, want exactly once", count)
	}
	if got := loadAvalWorkflow(t, "ci.yml").Jobs["aval-publish"].Env["AVAL_REGISTRY"]; got != avalRegistry {
		t.Errorf("ci.yml aval-publish env AVAL_REGISTRY = %q, want %q", got, avalRegistry)
	}
	trust := readRepoFile(t, ".github", "aval", "trust-from-base.sh")
	for _, want := range []string{`"$src:.github/workflows/ci.yml"`, "src=origin/main policy=pr.yaml", "src=HEAD policy=main.yaml", `[ "${GITHUB_REF:-}" = refs/heads/main ]`} {
		if !strings.Contains(trust, want) {
			t.Errorf("trust-from-base.sh no longer contains %q", want)
		}
	}
}

// TestAvalInstallPinned: the aval every target runs is one pinned release,
// checked before use: the archive's sha256 is pinned per platform, and
// checksums.txt must carry Aval's release workflow's keyless signature at that
// tag and list the same sha256. ci.mk and run.sh read the version from
// install.sh, so there is no second pin, and no workflow installs aval any
// other way.
func TestAvalInstallPinned(t *testing.T) {
	sh := readRepoFile(t, ".github", "aval", "install.sh")
	if !regexp.MustCompile(`(?m)^AVAL_VERSION=\d+\.\d+\.\d+$`).MatchString(sh) {
		t.Error("install.sh does not pin AVAL_VERSION to a release version")
	}
	for _, platform := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"} {
		if !regexp.MustCompile(`platform=` + platform + ` +sha=[0-9a-f]{64} ;;`).MatchString(sh) {
			t.Errorf("install.sh does not pin the sha256 of the %s archive", platform)
		}
	}
	for _, want := range []string{
		`echo "$sha  $archive" | "${sum[@]}" -c -`,
		"cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json",
		"--certificate-oidc-issuer https://token.actions.githubusercontent.com",
		`--certificate-identity "https://github.com/TrianaLab/aval/.github/workflows/release.yaml@refs/tags/$tag"`,
		`grep -qxF "$sha  $archive" checksums.txt`,
		"set -euo pipefail",
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("install.sh no longer verifies the release with %q", want)
		}
	}
	for file, want := range map[string]string{
		"ci.mk":                   `$(shell sed -n 's/^AVAL_VERSION=//p' .github/aval/install.sh)`,
		".github/aval/run.sh":     `version=$(sed -n 's/^AVAL_VERSION=//p' .github/aval/install.sh)`,
		".github/aval/publish.sh": `sed -n 's/^AVAL_VERSION=//p' .github/aval/install.sh`,
	} {
		if !strings.Contains(readRepoFile(t, filepath.FromSlash(file)), want) {
			t.Errorf("%s does not take the aval version from install.sh", file)
		}
	}
	files, _ := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	other := regexp.MustCompile(`TrianaLab/aval/releases|go install github\.com/TrianaLab/aval|get-aval\.sh|setup-aval`)
	for _, f := range files {
		if m := other.FindString(readRepoFile(t, ".github", "workflows", filepath.Base(f))); m != "" {
			t.Errorf("%s installs aval through %q; use make aval-install (.github/aval/install.sh)", filepath.Base(f), m)
		}
	}
}

// avalBinary is the pinned aval: $AVAL (ci.mk exports it to every suite), else
// aval on PATH at the pinned version.
func avalBinary(t *testing.T) string {
	t.Helper()
	version := regexp.MustCompile(`(?m)^AVAL_VERSION=(\S+)$`).FindStringSubmatch(readRepoFile(t, ".github", "aval", "install.sh"))[1]
	candidates := []string{os.Getenv("AVAL")}
	if p, err := exec.LookPath("aval"); err == nil {
		candidates = append(candidates, p)
	}
	for _, bin := range candidates {
		if bin == "" {
			continue
		}
		out, err := exec.Command(bin, "--version").Output() //nolint:gosec // the pinned aval binary
		if err == nil && strings.Contains(string(out), version) {
			return bin
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("no aval %s (make aval-install): CI must check every contract", version)
	}
	t.Skipf("no aval %s on PATH or in $AVAL (make aval-install)", version)
	return ""
}

// TestContractsFingerprintCleanly runs `aval explain` on every contract, as
// `aval run` would fingerprint it: a glob that matches nothing or reaches a
// symlink makes every run an error that records nothing, silently, so it fails
// here instead. A hermetic or controlled-environment contract must have no
// unaccepted mutable reference: with one, every run is a miss and aval-publish
// cannot push its result.
func TestContractsFingerprintCleanly(t *testing.T) {
	bin := avalBinary(t)
	for _, suite := range contractSuites(t) {
		cmd := exec.Command(bin, "explain", "-f", "aval."+suite+".yaml") //nolint:gosec // the pinned aval binary on a contract of this repository
		cmd.Dir = repoRoot(t)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("aval explain -f aval.%s.yaml: %v\n%s", suite, err, stderr.String())
			continue
		}
		if c := loadContract(t, suite).Cacheability; c != "hermetic" && c != "controlled-environment" {
			continue
		}
		for line := range strings.SplitSeq(string(out)+stderr.String(), "\n") {
			if strings.HasPrefix(line, "mutable: ") && !strings.Contains(line, "(allowed: ") {
				t.Errorf("aval.%s.yaml: unaccepted mutable reference, so no result can be reused or pushed: %s", suite, line)
			}
		}
	}
}
