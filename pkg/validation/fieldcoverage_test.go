package validation

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The field coverage inventory (schema/field-coverage.yaml) states, for every path
// of the contract schema and every enum value, what the field means and how far
// runtime evidence can verify it. This test keeps it honest mechanically: the
// schema is walked, not listed, so a field added to the schema without an entry
// fails here, and so does an entry for a field the schema no longer has.

type fcTests struct {
	Unit        []string `yaml:"unit"`
	Integration []string `yaml:"integration"`
	E2E         []string `yaml:"e2e"`
}

type fcEntry struct {
	Meaning     string                `yaml:"meaning"`
	Runtime     string                `yaml:"runtime"`
	Evidence    string                `yaml:"evidence"`
	Rule        string                `yaml:"rule"`
	Outcomes    string                `yaml:"outcomes"`
	Limitations string                `yaml:"limitations"`
	Tests       fcTests               `yaml:"tests"`
	Platforms   map[string]fcPlatform `yaml:"platforms"`
	Values      map[string]fcEntry    `yaml:"values"`
}

// fcPlatform is how far one way of running runtime conformance verifies a field.
type fcPlatform struct {
	Evidence string   `yaml:"evidence"`
	Reason   string   `yaml:"reason"`
	Tests    []string `yaml:"tests"`
}

var fcRuntimeClasses = map[string]bool{"verified": true, "partial": true, "static-only": true, "not-applicable": true}

// fcPlatforms are the ways runtime conformance runs: the Kubernetes operator with its node agent, pacto agent
// on a host and pacto run around one command.
var fcPlatforms = []string{"kubernetes", "agent", "run"}

var fcPlatformEvidence = map[string]bool{"verified": true, "partial": true, "unverified": true}

// fcRuntimeEnums are the enums whose values change what runtime evidence can say,
// so each value carries a full entry. The other enums (contacts, readiness claims,
// pactoVersion) list their values with a meaning only.
var fcRuntimeEnums = map[string]bool{
	"interfaces[].type": true, "interfaces[].visibility": true, "workload": true, "state.type": true,
	"state.persistence.scope": true, "state.persistence.durability": true, "state.dataCriticality": true,
	"capabilities[].type": true, "capabilities[].binding.type": true, "policies[].target": true,
}

const fcRepoRoot = "../.."

func loadFieldCoverage(t *testing.T) map[string]fcEntry {
	t.Helper()
	data, err := os.ReadFile("schema/field-coverage.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fields map[string]fcEntry `yaml:"fields"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode field-coverage.yaml: %v", err)
	}
	return doc.Fields
}

// schemaFieldPaths walks the contract schema through properties and items only,
// returning every dotted path and the enum of each enum-valued path. Combinators
// (allOf, oneOf, if/then, not) only re-mention properties already declared, so
// descending into them would invent phantom paths; open objects with no properties
// are leaves.
func schemaFieldPaths(t *testing.T) (paths []string, enums map[string][]string) {
	t.Helper()
	data, err := os.ReadFile("schema/pacto-v2.2.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	enums = map[string][]string{}
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		if path != "" {
			paths = append(paths, path)
		}
		if raw, ok := node["enum"].([]any); ok {
			for _, v := range raw {
				enums[path] = append(enums[path], v.(string))
			}
		}
		if props, ok := node["properties"].(map[string]any); ok {
			for k, v := range props {
				child := k
				if path != "" {
					child = path + "." + k
				}
				walk(v.(map[string]any), child)
			}
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, path+"[]")
		}
	}
	walk(root, "")
	sort.Strings(paths)
	return paths, enums
}

func TestFieldCoverage_InventoryMatchesSchema(t *testing.T) {
	inv := loadFieldCoverage(t)
	paths, enums := schemaFieldPaths(t)
	if len(paths) < 50 {
		t.Fatalf("schema walk found only %d paths; the walker is broken", len(paths))
	}

	for _, p := range paths {
		if _, ok := inv[p]; !ok {
			t.Errorf("schema path %q has no inventory entry", p)
		}
	}
	for p := range inv {
		if !slices.Contains(paths, p) {
			t.Errorf("inventory entry %q names no schema path (stale)", p)
		}
	}

	for p, e := range inv {
		want := append([]string(nil), enums[p]...)
		got := make([]string, 0, len(e.Values))
		for v := range e.Values {
			got = append(got, v)
		}
		sort.Strings(want)
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: inventory values %v, schema enum %v", p, got, want)
		}
	}
	for p := range fcRuntimeEnums {
		if len(enums[p]) == 0 {
			t.Errorf("runtime-relevant enum %q is not an enum in the schema", p)
		}
	}
}

func TestFieldCoverage_EntriesAreComplete(t *testing.T) {
	for p, e := range loadFieldCoverage(t) {
		checkEntry(t, p, e, true)
		checkPlatforms(t, p, e)
		for v, ve := range e.Values {
			checkEntry(t, p+"="+v, ve, fcRuntimeEnums[p])
		}
	}
}

// checkEntry requires the keys a reader needs. A full entry states every key; a
// value of a static enum may state its meaning and class only. Every entry names
// an integration case, and anything runtime can see names an e2e scenario.
func checkEntry(t *testing.T, name string, e fcEntry, full bool) {
	t.Helper()
	if strings.TrimSpace(e.Meaning) == "" {
		t.Errorf("%s: empty meaning", name)
	}
	if !fcRuntimeClasses[e.Runtime] {
		t.Errorf("%s: runtime %q is not one of verified, partial, static-only, not-applicable", name, e.Runtime)
	}
	if full {
		for key, v := range map[string]string{"evidence": e.Evidence, "rule": e.Rule, "outcomes": e.Outcomes, "limitations": e.Limitations} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: empty %s", name, key)
			}
		}
	}
	if len(e.Tests.Integration) == 0 {
		t.Errorf("%s: no integration test", name)
	}
	if (e.Runtime == "verified" || e.Runtime == "partial") && len(e.Tests.E2E) == 0 {
		t.Errorf("%s: runtime %s needs an e2e scenario", name, e.Runtime)
	}
}

// checkPlatforms requires every runtime-relevant field (runtime verified or partial) to say what each
// platform verifies, and nothing else to: Kubernetes agrees with runtime (its tests are the e2e scenarios),
// and an agent or run claim beyond unverified names its tests.
func checkPlatforms(t *testing.T, name string, e fcEntry) {
	t.Helper()
	if e.Runtime != "verified" && e.Runtime != "partial" {
		if len(e.Platforms) != 0 {
			t.Errorf("%s: runtime %s has nothing to verify, so it lists no platforms", name, e.Runtime)
		}
		return
	}
	if len(e.Platforms) != len(fcPlatforms) {
		t.Errorf("%s: platforms %v, want exactly %v", name, slices.Sorted(maps.Keys(e.Platforms)), fcPlatforms)
	}
	for _, p := range fcPlatforms {
		pl, ok := e.Platforms[p]
		switch {
		case !ok:
			t.Errorf("%s: no %s platform", name, p)
			continue
		case !fcPlatformEvidence[pl.Evidence]:
			t.Errorf("%s: %s evidence %q is not one of verified, partial, unverified", name, p, pl.Evidence)
		case strings.TrimSpace(pl.Reason) == "":
			t.Errorf("%s: %s has no reason", name, p)
		case p == "kubernetes" && pl.Evidence != e.Runtime:
			t.Errorf("%s: kubernetes evidence %s disagrees with runtime %s", name, pl.Evidence, e.Runtime)
		case p != "kubernetes" && pl.Evidence != "unverified" && len(pl.Tests) == 0:
			t.Errorf("%s: %s evidence %s names no test", name, p, pl.Evidence)
		}
	}
}

var fcRefPattern = regexp.MustCompile(`^([\w./-]+\.(?:go|sh)):(.+)$`)

// fcRefChecker resolves test references against the files they name, reading each
// file once.
type fcRefChecker struct {
	t     *testing.T
	files map[string]string
}

func (c *fcRefChecker) read(path string) (string, bool) {
	if s, ok := c.files[path]; ok {
		return s, true
	}
	b, err := os.ReadFile(filepath.Join(fcRepoRoot, path))
	if err != nil {
		return "", false
	}
	c.files[path] = string(b)
	return c.files[path], true
}

func (c *fcRefChecker) entry(name string, e fcEntry) {
	refs := [][]string{e.Tests.Unit, e.Tests.Integration, e.Tests.E2E}
	for _, p := range e.Platforms {
		refs = append(refs, p.Tests)
	}
	for _, rs := range refs {
		for _, r := range rs {
			c.ref(name, r)
		}
	}
}

// ref checks one reference: a shell script must declare the scenario as a line
// starting `scenario "<id>"` or `scenario <id> `, a Go file must declare
// `func TestX(` and, for TestX/sub, contain the subtest name.
func (c *fcRefChecker) ref(name, ref string) {
	m := fcRefPattern.FindStringSubmatch(ref)
	if m == nil {
		c.t.Errorf("%s: malformed test reference %q", name, ref)
		return
	}
	file, target := m[1], m[2]
	src, ok := c.read(file)
	if !ok {
		c.t.Errorf("%s: test file %s does not exist", name, file)
		return
	}
	if strings.HasSuffix(file, ".sh") {
		id := regexp.QuoteMeta(target)
		if !regexp.MustCompile(`(?m)^scenario (?:"` + id + `"|` + id + `)\s`).MatchString(src) {
			c.t.Errorf("%s: %s has no scenario %q", name, file, target)
		}
		return
	}
	fn, sub, _ := strings.Cut(target, "/")
	if !regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(fn) + `\(`).MatchString(src) {
		c.t.Errorf("%s: %s declares no func %s", name, file, fn)
	}
	if sub != "" && !strings.Contains(src, `"`+sub) {
		c.t.Errorf("%s: %s has no subtest %q", name, file, sub)
	}
}

func TestFieldCoverage_ReferencedTestsExist(t *testing.T) {
	c := &fcRefChecker{t: t, files: map[string]string{}}
	for p, e := range loadFieldCoverage(t) {
		c.entry(p, e)
		for v, ve := range e.Values {
			c.entry(p+"="+v, ve)
		}
	}
}
