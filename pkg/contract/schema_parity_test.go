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

// schemaTopLevelFields returns the top-level property names of the JSON schema.
func schemaTopLevelFields(t *testing.T, root string) map[string]bool {
	t.Helper()
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
	out := map[string]bool{}
	for k := range s.Properties {
		out[k] = true
	}
	return out
}

// modelTopLevelFields returns the yaml field names of the Go Contract model.
func modelTopLevelFields() map[string]bool {
	out := map[string]bool{}
	rt := reflect.TypeOf(Contract{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.Split(rt.Field(i).Tag.Get("yaml"), ",")[0]
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// docH2Headings returns every `## <name>` heading (backticks stripped) in the
// contract-reference sections page.
func docH2Headings(t *testing.T, root string) []string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join(root, "docs", "contract-reference", "sections.md"))
	if err != nil {
		t.Fatalf("read sections.md: %v", err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^## (.+)$`).FindAllStringSubmatch(string(doc), -1) {
		out = append(out, strings.Trim(strings.TrimSpace(m[1]), "`"))
	}
	return out
}

// TestContractTopLevelFieldParity is the regression gate (contract-model item 1):
// the JSON Schema, the Go Contract model and the contract-reference documentation
// MUST expose exactly the same set of top-level contract fields. This is what keeps
// a removed field (e.g. `verification`) — or a newly added one — from lingering in
// one surface after being dropped/added in another (item 5: every reference H2 is a
// real field).
func TestContractTopLevelFieldParity(t *testing.T) {
	root := repoRootFromCaller(t)
	schemaFields := schemaTopLevelFields(t, root)
	modelFields := modelTopLevelFields()
	h2 := docH2Headings(t, root)

	docFields := map[string]bool{}
	for _, h := range h2 {
		docFields[h] = true
	}

	if !reflect.DeepEqual(schemaFields, modelFields) {
		t.Errorf("schema vs Go model top-level fields differ:\n  schema: %v\n  model:  %v", sortedKeys(schemaFields), sortedKeys(modelFields))
	}
	for f := range schemaFields {
		if !docFields[f] {
			t.Errorf("top-level field %q is in the schema/model but has no `## %s` section in the contract reference", f, f)
		}
	}
	// Every reference H2 must be a real top-level field — no concept/ownership/pattern
	// heading masquerading as a YAML field.
	for _, h := range h2 {
		if !schemaFields[h] {
			t.Errorf("contract-reference H2 %q is not a top-level contract field — demote to H3 or move to Patterns (item 5)", h)
		}
	}
	// Explicit: `verification` is gone from every surface (contract-model item 1).
	if schemaFields["verification"] || modelFields["verification"] || docFields["verification"] {
		t.Error("`verification` must be removed from the schema, Go model and docs")
	}
}
