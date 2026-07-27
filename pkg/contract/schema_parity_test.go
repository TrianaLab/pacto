package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// repoRootFromCaller resolves the monorepo root from this test file's location.
func repoRootFromCaller(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(self), "..", ".."))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestContractTopLevelFieldParity is the regression gate (contract-model item 1):
// the JSON Schema, the Go Contract model and the contract-reference documentation
// MUST expose exactly the same set of top-level contract fields. This is what keeps
// a removed field (e.g. `verification`) — or a newly added one — from lingering in
// one surface after being dropped/added in another.
func TestContractTopLevelFieldParity(t *testing.T) {
	root := repoRootFromCaller(t)

	// (1) schema top-level property names.
	b, err := os.ReadFile(filepath.Join(root, "schema", "pacto-v2.0.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	schemaFields := map[string]bool{}
	for k := range s.Properties {
		schemaFields[k] = true
	}

	// (2) Go Contract model yaml field names.
	modelFields := map[string]bool{}
	rt := reflect.TypeOf(Contract{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.Split(rt.Field(i).Tag.Get("yaml"), ",")[0]
		if name != "" && name != "-" {
			modelFields[name] = true
		}
	}

	// (3) documented top-level fields: `## \`field\`` H2 headings in the reference.
	doc, err := os.ReadFile(filepath.Join(root, "docs", "contract-reference", "sections.md"))
	if err != nil {
		t.Fatalf("read sections.md: %v", err)
	}
	docFields := map[string]bool{}
	h2 := regexp.MustCompile("(?m)^## `([a-zA-Z]+)`")
	for _, m := range h2.FindAllStringSubmatch(string(doc), -1) {
		docFields[m[1]] = true
	}

	// Schema and model must be exactly equal.
	if !reflect.DeepEqual(schemaFields, modelFields) {
		t.Errorf("schema vs Go model top-level fields differ:\n  schema: %v\n  model:  %v", sortedKeys(schemaFields), sortedKeys(modelFields))
	}
	// Every schema field must be documented (an H2 in the reference).
	for f := range schemaFields {
		if !docFields[f] {
			t.Errorf("top-level field %q is in the schema/model but has no `## %s` section in the contract reference", f, f)
		}
	}

	// Item 5: every H2 in the reference must be a real top-level field — no concept,
	// ownership or pattern heading masquerading as a YAML field.
	allH2 := regexp.MustCompile(`(?m)^## (.+)$`)
	for _, m := range allH2.FindAllStringSubmatch(string(doc), -1) {
		name := strings.Trim(strings.TrimSpace(m[1]), "`")
		if !schemaFields[name] {
			t.Errorf("contract-reference H2 %q is not a top-level contract field — demote to H3 or move to Patterns (item 5)", name)
		}
	}

	// Explicit: `verification` is gone from every surface (contract-model item 1).
	for name, set := range map[string]map[string]bool{"schema": schemaFields, "model": modelFields, "docs": docFields} {
		if set["verification"] {
			t.Errorf("`verification` must be removed but still appears in %s", name)
		}
	}
}
