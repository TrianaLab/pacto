package architecture

import (
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var updateV1API = flag.Bool("update", false, "rewrite tests/architecture/testdata/crd-v1 from the generated CRDs")

// crdBases lists the generated CRDs the operator ships.
func crdBases(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "integrations", "kubernetes", "config", "crd", "bases", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no generated CRDs found: %v", err)
	}
	return files
}

// TestCRDVersionsAreIdentical: every CRD serves v1 (storage) and the
// deprecated v1alpha1 with one schema and no conversion (D6).
func TestCRDVersionsAreIdentical(t *testing.T) {
	for _, path := range crdBases(t) {
		for _, f := range compareVersions(readCRD(t, path)) {
			t.Errorf("%s: %s", filepath.Base(path), f)
		}
	}
}

// compareVersions returns one finding per way a CRD departs from
// "v1 served and stored, v1alpha1 served, deprecated and identical".
func compareVersions(crd map[string]any) []string {
	spec, _ := crd["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)
	var out []string
	if c, ok := spec["conversion"].(map[string]any); ok && c["strategy"] != "None" {
		out = append(out, fmt.Sprintf("conversion strategy %v, want None", c["strategy"]))
	}
	if len(versions) != 2 {
		return append(out, fmt.Sprintf("%d versions, want v1 then v1alpha1", len(versions)))
	}
	v1, _ := versions[0].(map[string]any)
	old, _ := versions[1].(map[string]any)
	if v1["name"] != "v1" || old["name"] != "v1alpha1" {
		return append(out, fmt.Sprintf("versions %v, %v, want v1 then v1alpha1", v1["name"], old["name"]))
	}
	want := map[string]map[string]bool{
		"v1":       {"served": true, "storage": true},
		"v1alpha1": {"served": true, "storage": false, "deprecated": true},
	}
	for _, v := range []map[string]any{v1, old} {
		name := v["name"].(string)
		for _, flag := range []string{"served", "storage", "deprecated"} {
			w, ok := want[name][flag]
			if got, _ := v[flag].(bool); ok && got != w {
				out = append(out, fmt.Sprintf("%s: %s is %v", name, flag, got))
			}
		}
	}
	warning := fmt.Sprintf("%v/v1alpha1 is deprecated; use v1 (removed in chart 8.0)", spec["group"])
	if old["deprecationWarning"] != warning {
		out = append(out, fmt.Sprintf("v1alpha1: deprecationWarning %q, want %q", old["deprecationWarning"], warning))
	}
	for _, field := range []string{"schema", "subresources", "additionalPrinterColumns", "selectableFields"} {
		if !reflect.DeepEqual(v1[field], old[field]) {
			out = append(out, field+" differs between v1 and v1alpha1")
		}
	}
	return out
}

// TestV1APIOnlyGrows is D5's API-compatibility golden: the v1 schema of each
// CRD may gain properties and enum values, never lose or retype one, and never
// newly require one. Only a chart major refreshes the baseline (-update).
func TestV1APIOnlyGrows(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tests", "architecture", "testdata", "crd-v1")
	for _, path := range crdBases(t) {
		crd := readCRD(t, path)
		name, _ := crd["metadata"].(map[string]any)["name"].(string)
		cur := v1Schema(crd)
		if cur == nil {
			t.Errorf("%s: no v1 openAPIV3Schema", name)
			continue
		}
		golden := filepath.Join(dir, name+".json")
		if *updateV1API {
			raw, err := json.MarshalIndent(cur, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, append(raw, '\n'), 0o644); err != nil { //nolint:gosec // a committed golden file
				t.Fatal(err)
			}
			continue
		}
		if _, err := os.Stat(golden); err != nil {
			t.Errorf("%s: no baseline %s (run with -update in a chart major)", name, golden)
			continue
		}
		for _, f := range compareV1API(readJSONSchema(t, golden), cur) {
			t.Errorf("%s: %s", name, f)
		}
	}
}

func v1Schema(crd map[string]any) map[string]any {
	spec, _ := crd["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)
	for _, v := range versions {
		if m, _ := v.(map[string]any); m["name"] == "v1" {
			schema, _ := m["schema"].(map[string]any)
			root, _ := schema["openAPIV3Schema"].(map[string]any)
			return root
		}
	}
	return nil
}

// compareV1API walks the baseline schema against the current one and returns
// one finding per break, each naming its JSON path. Additions return nothing.
func compareV1API(base, cur map[string]any) []string {
	var out []string
	walkV1API("", base, cur, &out)
	return out
}

func walkV1API(path string, base, cur map[string]any, out *[]string) {
	if base["type"] != cur["type"] {
		*out = append(*out, fmt.Sprintf("%s: type %v -> %v", path, base["type"], cur["type"]))
	}
	if baseEnum, ok := base["enum"].([]any); ok {
		curEnum, _ := cur["enum"].([]any)
		for _, v := range baseEnum {
			if !slices.ContainsFunc(curEnum, func(c any) bool { return fmt.Sprint(c) == fmt.Sprint(v) }) {
				*out = append(*out, fmt.Sprintf("%s: enum value %q removed", path, fmt.Sprint(v)))
			}
		}
	}
	baseReq := stringSet(base["required"])
	for _, r := range sortedKeys(stringSet(cur["required"])) {
		if !baseReq[r] {
			*out = append(*out, joinPath(path, r)+": newly required")
		}
	}
	baseProps, _ := base["properties"].(map[string]any)
	curProps, _ := cur["properties"].(map[string]any)
	for _, k := range slices.Sorted(maps.Keys(baseProps)) {
		child := joinPath(path, k)
		c, ok := curProps[k].(map[string]any)
		if !ok {
			*out = append(*out, child+": property removed")
			continue
		}
		b, _ := baseProps[k].(map[string]any)
		walkV1API(child, b, c, out)
	}
	for key, suffix := range map[string]string{"items": "[]", "additionalProperties": "{}"} {
		b, ok := base[key].(map[string]any)
		if !ok {
			continue
		}
		c, ok := cur[key].(map[string]any)
		if !ok {
			*out = append(*out, path+suffix+": property removed")
			continue
		}
		walkV1API(path+suffix, b, c, out)
	}
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func stringSet(v any) map[string]bool {
	list, _ := v.([]any)
	set := make(map[string]bool, len(list))
	for _, s := range list {
		set[fmt.Sprint(s)] = true
	}
	return set
}

// TestV1APICompareDetectsDrift keeps the comparators honest: tests/ sits
// outside every coverage gate, so each drift kind has a committed fixture and
// the path the finding must name.
func TestV1APICompareDetectsDrift(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "tests", "architecture", "testdata", "crd-v1-drift")
	base := readJSONSchema(t, filepath.Join(dir, "base.json"))
	for _, tc := range []struct{ file, want string }{
		{"property-removed.json", "spec.items[].id: property removed"},
		{"type-changed.json", "spec.labels{}: type string -> integer"},
		{"enum-value-dropped.json", `spec.mode: enum value "b" removed`},
		{"newly-required.json", "spec.mode: newly required"},
		{"property-added.json", ""},
	} {
		got := compareV1API(base, readJSONSchema(t, filepath.Join(dir, tc.file)))
		assertFindings(t, tc.file, got, tc.want)
	}
	for _, tc := range []struct{ file, want string }{
		{"version-flag-flipped.yaml", "v1alpha1: served is false"},
		{"version-schema-diverged.yaml", "schema differs between v1 and v1alpha1"},
	} {
		assertFindings(t, tc.file, compareVersions(readCRD(t, filepath.Join(dir, tc.file))), tc.want)
	}
}

func assertFindings(t *testing.T, name string, got []string, want string) {
	t.Helper()
	if want == "" {
		if len(got) != 0 {
			t.Errorf("%s: want no finding, got %q", name, got)
		}
		return
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("%s: want exactly [%q], got %q", name, want, got)
	}
}

func readJSONSchema(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a fixture path under the repository root
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func readCRD(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a CRD path under the repository root
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

// TestDeliveryWireTypesAreStable: the delivery CRDs serve v1, so the wire
// types they embed are a semver-covered API (D5).
func TestDeliveryWireTypesAreStable(t *testing.T) {
	doc := readRepoFile(t, "pkg", "delivery", "model.go")
	if !strings.Contains(doc, "covered by semantic versioning") {
		t.Error("pkg/delivery's package doc must say its wire types are covered by semantic versioning")
	}
	if strings.Contains(doc, "Experimental: not covered") {
		t.Error("pkg/delivery's package doc still says it is Experimental")
	}
}
