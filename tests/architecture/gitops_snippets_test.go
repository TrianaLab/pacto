package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The GitOps promotion gates page ships two snippets a reader is meant to paste
// into their own cluster: a Flux healthCheckExprs block and an Argo CD health
// customization. Both hard-code the contract verdicts by name, and both fail
// SILENTLY when they drift — Flux falls through to in-progress and times out,
// Argo goes straight back to ignoring the object, which looks exactly like
// having configured nothing.
//
// The snippets therefore live in tests/acceptance/kind/fixtures/gitops and are
// included into the page verbatim, so the documented text is the tested text.
// What is left for this file is the drift the include cannot catch: a verdict
// added to the CRD that neither snippet knows about, a verdict misspelled, the
// one ConfigMap key that must be exact, and Lua that reaches for a library Argo
// does not load.

const (
	gitopsPage      = "gitops.md"
	fluxFixture     = "flux-kustomization.yaml"
	argocdFixture   = "argocd-cm-pacto-health.yaml"
	pactoAPIVersion = "pacto.trianalab.io/v1alpha1"
)

func repoDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

func gitopsFixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoDir(t), "tests", "acceptance", "kind", "fixtures", "gitops", name)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a path this test computed
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// pactoCRD is the slice of the generated CRD the snippets depend on. Parsing the
// CRD rather than the Go constants keeps the check on the artifact Flux and Argo
// actually read.
type pactoCRD struct {
	Spec struct {
		Group string `yaml:"group"`
		Names struct {
			Kind string `yaml:"kind"`
		} `yaml:"names"`
		Versions []struct {
			Schema struct {
				OpenAPIV3Schema struct {
					Properties struct {
						Status struct {
							Properties struct {
								ContractStatus struct {
									Enum []string `yaml:"enum"`
								} `yaml:"contractStatus"`
							} `yaml:"properties"`
						} `yaml:"status"`
					} `yaml:"properties"`
				} `yaml:"openAPIV3Schema"`
			} `yaml:"schema"`
		} `yaml:"versions"`
	} `yaml:"spec"`
}

func loadPactoCRD(t *testing.T) pactoCRD {
	t.Helper()
	path := filepath.Join(repoDir(t), "integrations", "kubernetes", "config", "crd",
		"bases", "pacto.trianalab.io_pactos.yaml")
	var crd pactoCRD
	if err := yaml.Unmarshal([]byte(readFile(t, path)), &crd); err != nil {
		t.Fatalf("cannot parse the Pacto CRD: %v", err)
	}
	if crd.Spec.Group == "" || crd.Spec.Names.Kind == "" || len(crd.Spec.Versions) == 0 {
		t.Fatal("the Pacto CRD did not parse into group/kind/versions; the shape moved")
	}
	return crd
}

func contractStatusEnum(t *testing.T, crd pactoCRD) []string {
	t.Helper()
	enum := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties.Status.Properties.ContractStatus.Enum
	if len(enum) == 0 {
		t.Fatal("status.contractStatus has no enum in the CRD; the snippets have nothing to be checked against")
	}
	return enum
}

// luaBlock returns the Lua script out of the Argo ConfigMap fixture, keyed by the
// customization key the test independently derives.
func luaBlock(t *testing.T, key string) string {
	t.Helper()
	var cm struct {
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, gitopsFixturePath(t, argocdFixture))), &cm); err != nil {
		t.Fatalf("cannot parse %s: %v", argocdFixture, err)
	}
	script, ok := cm.Data[key]
	if !ok {
		t.Fatalf("%s has no data key %q; Argo ignores a key it does not recognise, and an "+
			"ignored key is indistinguishable from no configuration at all (keys present: %v)",
			argocdFixture, key, mapKeys(cm.Data))
	}
	return script
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestArgoHealthKeyMatchesTheCRD pins the one string in the Argo snippet that has
// no forgiving failure mode. The key is resource.customizations.health.<group>_<kind>
// and Argo silently ignores anything else.
func TestArgoHealthKeyMatchesTheCRD(t *testing.T) {
	crd := loadPactoCRD(t)
	want := "resource.customizations.health." + crd.Spec.Group + "_" + crd.Spec.Names.Kind
	luaBlock(t, want) // fails with the present keys if the derived key is absent
}

// TestArgoLuaHandlesEveryVerdict fails when a contract status is added to the CRD
// and the Lua does not mention it — either as its own branch or in the comment
// naming what deliberately falls through to Progressing.
func TestArgoLuaHandlesEveryVerdict(t *testing.T) {
	crd := loadPactoCRD(t)
	script := luaBlock(t, "resource.customizations.health."+crd.Spec.Group+"_"+crd.Spec.Names.Kind)
	for _, verdict := range contractStatusEnum(t, crd) {
		if !strings.Contains(script, verdict) {
			t.Errorf("contractStatus %q is in the CRD but nowhere in the Argo Lua; give it a "+
				"branch, or name it in the fall-through comment if Progressing is correct", verdict)
		}
	}
}

var luaVerdictLiteral = regexp.MustCompile(`s == "([^"]*)"`)

// TestArgoLuaComparesRealVerdicts catches a misspelled verdict, which reads as a
// branch that can never be taken.
func TestArgoLuaComparesRealVerdicts(t *testing.T) {
	crd := loadPactoCRD(t)
	valid := make(map[string]bool)
	for _, v := range contractStatusEnum(t, crd) {
		valid[v] = true
	}
	script := luaBlock(t, "resource.customizations.health."+crd.Spec.Group+"_"+crd.Spec.Names.Kind)
	matches := luaVerdictLiteral.FindAllStringSubmatch(script, -1)
	if len(matches) == 0 {
		t.Fatal("the Argo Lua compares no verdict at all; the extractor or the script moved")
	}
	for _, m := range matches {
		if !valid[m[1]] {
			t.Errorf("the Argo Lua branches on %q, which is not a contractStatus value", m[1])
		}
	}
}

// luaStringLibrary lists what Argo's sandbox does not provide. gitops-engine runs
// the script in a gopher-lua VM with the string library left unloaded, so a call
// fails at runtime rather than at load — the object just stays unknown.
var luaStringLibrary = []string{
	"string.",
	":format(", ":gsub(", ":sub(", ":find(", ":len(", ":rep(",
	":upper(", ":lower(", ":match(", ":gmatch(", ":byte(", ":char(", ":reverse(",
}

func TestArgoLuaAvoidsTheStringLibrary(t *testing.T) {
	crd := loadPactoCRD(t)
	script := luaBlock(t, "resource.customizations.health."+crd.Spec.Group+"_"+crd.Spec.Names.Kind)
	for _, banned := range luaStringLibrary {
		if strings.Contains(script, banned) {
			t.Errorf("the Argo Lua uses %q; Argo runs it with the string library disabled, so "+
				"only concatenation, comparison, ipairs and tostring() are available", banned)
		}
	}
}

// healthCheckExpr is one entry of a Flux Kustomization's spec.healthCheckExprs.
type healthCheckExpr struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Failed     string `yaml:"failed"`
	Current    string `yaml:"current"`
	InProgress string `yaml:"inProgress"`
}

// fluxKustomization is the slice of the Flux fixture this file checks.
type fluxKustomization struct {
	Spec struct {
		HealthCheckExprs []healthCheckExpr `yaml:"healthCheckExprs"`
	} `yaml:"spec"`
}

func pactoHealthCheckExprs(t *testing.T) []healthCheckExpr {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(readFile(t, gitopsFixturePath(t, fluxFixture))))
	var found []healthCheckExpr
	for {
		var doc fluxKustomization
		err := dec.Decode(&doc)
		if err != nil {
			break
		}
		for _, expr := range doc.Spec.HealthCheckExprs {
			if expr.APIVersion == pactoAPIVersion && expr.Kind == "Pacto" {
				found = append(found, expr)
			}
		}
	}
	if len(found) == 0 {
		t.Fatalf("%s declares no healthCheckExprs for %s/Pacto, so it gates nothing",
			fluxFixture, pactoAPIVersion)
	}
	return found
}

var celVerdictLiteral = regexp.MustCompile(`'([^']*)'`)

// TestFluxExpressionsUseRealVerdicts checks both directions of the CEL sets: every
// verdict named is real, and no verdict is claimed by both failed and current.
// Flux matches on group and kind and throws the API version away, so these names
// are a promise to everyone who copies the snippet.
func TestFluxExpressionsUseRealVerdicts(t *testing.T) {
	crd := loadPactoCRD(t)
	valid := make(map[string]bool)
	for _, v := range contractStatusEnum(t, crd) {
		valid[v] = true
	}
	for _, expr := range pactoHealthCheckExprs(t) {
		failed := celLiterals(expr.Failed)
		current := celLiterals(expr.Current)
		if len(failed) == 0 || len(current) == 0 {
			t.Fatal("a Pacto healthCheckExprs entry is missing either failed or current; " +
				"without both, a violation reads as in-progress and only surfaces as a timeout")
		}
		for _, name := range append(append([]string{}, failed...), current...) {
			if !valid[name] {
				t.Errorf("the Flux expressions name %q, which is not a contractStatus value", name)
			}
		}
		for _, name := range failed {
			for _, other := range current {
				if name == other {
					t.Errorf("%q is in both failed and current; Flux evaluates failed first, so "+
						"current would never be reached for it", name)
				}
			}
		}
	}
}

// TestFluxLeavesUnrecognisedVerdictsToFallThrough is the fail-closed property.
// An inProgress expression, or a current set covering every verdict, would turn
// an unrecognised future value into a pass.
func TestFluxLeavesUnrecognisedVerdictsToFallThrough(t *testing.T) {
	crd := loadPactoCRD(t)
	enum := contractStatusEnum(t, crd)
	for _, expr := range pactoHealthCheckExprs(t) {
		if strings.TrimSpace(expr.InProgress) != "" {
			t.Error("the Pacto healthCheckExprs entry sets inProgress; leave it unset so a " +
				"verdict no expression matches falls through to in-progress on its own")
		}
		covered := len(celLiterals(expr.Failed)) + len(celLiterals(expr.Current))
		if covered >= len(enum) {
			t.Errorf("the Flux expressions enumerate all %d contract statuses, so a verdict "+
				"added later would be judged by neither and the snippet no longer says which "+
				"way it fails", len(enum))
		}
	}
}

func celLiterals(expr string) []string {
	var out []string
	for _, m := range celVerdictLiteral.FindAllStringSubmatch(expr, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestGitOpsPageIncludesTheTestedFixtures is what makes every check above mean
// something: the page must publish these exact files, not a copy of them.
func TestGitOpsPageIncludesTheTestedFixtures(t *testing.T) {
	page := readFile(t, filepath.Join(repoDir(t), "integrations", "kubernetes", "docs", gitopsPage))
	for _, fixture := range []string{fluxFixture, argocdFixture} {
		include := `--8<-- "tests/acceptance/kind/fixtures/gitops/` + fixture + `"`
		if !strings.Contains(page, include) {
			t.Errorf("%s does not include %s verbatim; expected the line %s so the published "+
				"snippet cannot drift from the checked one", gitopsPage, fixture, include)
		}
	}
}

// TestGitOpsPageNamesEveryVerdict is the tripwire for the whole page. A verdict
// added to the CRD that the prose never mentions leaves a reader with a gate that
// silently does not cover it.
func TestGitOpsPageNamesEveryVerdict(t *testing.T) {
	crd := loadPactoCRD(t)
	page := readFile(t, filepath.Join(repoDir(t), "integrations", "kubernetes", "docs", gitopsPage))
	fixtures := readFile(t, gitopsFixturePath(t, fluxFixture)) +
		readFile(t, gitopsFixturePath(t, argocdFixture))
	for _, verdict := range contractStatusEnum(t, crd) {
		if !strings.Contains(page, verdict) && !strings.Contains(fixtures, verdict) {
			t.Errorf("contractStatus %q appears nowhere on the GitOps page or in its snippets; "+
				"say which way the gate treats it", verdict)
		}
	}
}
