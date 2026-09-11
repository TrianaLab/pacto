package contract

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
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
// This must be the copy pkg/validation embeds and nothing else: the root object is
// additionalProperties:false, so a field added to any other copy plus the Go model
// would turn this gate green while every contract using that field is rejected.
func schemaTopLevelFields(t *testing.T, root string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "pkg", "validation", "schema", "pacto-v2.0.schema.json"))
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

// vocabKey identifies a vocabulary by its value set rather than by the name of
// whatever declares it. Two fields constrained to the same values are the same
// vocabulary, however each side spells the constants.
func vocabKey(vals []string) string {
	s := append([]string(nil), vals...)
	sort.Strings(s)
	return strings.Join(s, ",")
}

// schemaEnums returns every string enum in the contract schema, keyed by value
// set and mapping to the JSON pointers that spell it.
func schemaEnums(t *testing.T, root string) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "pkg", "validation", "schema", "pacto-v2.0.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	out := map[string][]string{}
	var walk func(n any, ptr string)
	walk = func(n any, ptr string) {
		switch v := n.(type) {
		case map[string]any:
			if raw, ok := v["enum"].([]any); ok {
				vals, allStrings := make([]string, 0, len(raw)), true
				for _, x := range raw {
					s, ok := x.(string)
					if !ok {
						allStrings = false
						break
					}
					vals = append(vals, s)
				}
				if allStrings {
					k := vocabKey(vals)
					out[k] = append(out[k], ptr+"/enum")
				}
			}
			for k, c := range v {
				walk(c, ptr+"/"+k)
			}
		case []any:
			for i, c := range v {
				walk(c, ptr+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(doc, "")
	return out
}

// goVocabularies returns every parenthesized const block in pkg/contract whose
// members are all plain string literals, keyed by value set. Constants are
// invisible to reflection, so reading the source is the only way to enumerate
// them without keeping a third copy of each list here.
func goVocabularies(t *testing.T, root string) map[string][]string {
	t.Helper()
	dir := filepath.Join(root, "pkg", "contract")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read pkg/contract: %v", err)
	}
	fset := token.NewFileSet()
	out := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			// A one-value block still declares a vocabulary and can still
			// drift, so only an empty one is skipped. A bare unparenthesized
			// `const X = "y"` is not declaring a vocabulary and is not read.
			if !ok || g.Tok != token.CONST || !g.Lparen.IsValid() || len(g.Specs) == 0 {
				continue
			}
			if names, vals := constVocabulary(g); names != nil {
				k := vocabKey(vals)
				out[k] = append(out[k], names...)
			}
		}
	}
	return out
}

// constVocabulary reads one parenthesized const block as a vocabulary: its
// member names and the string values they spell. It returns nil the moment any
// member is not `Name = "literal"`, because a block mixing literals with
// anything else is not a vocabulary and must be skipped whole rather than
// half-read.
func constVocabulary(g *ast.GenDecl) (names, vals []string) {
	for _, s := range g.Specs {
		sp, ok := s.(*ast.ValueSpec)
		if !ok || len(sp.Names) != 1 || len(sp.Values) != 1 {
			return nil, nil
		}
		lit, ok := sp.Values[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return nil, nil
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return nil, nil
		}
		names = append(names, sp.Names[0].Name)
		vals = append(vals, v)
	}
	return names, vals
}

// vocabulariesWithoutSchemaEnum are Go const blocks that deliberately have no
// schema counterpart, keyed by value set. Both are discriminators Pacto computes
// while reading a bundle rather than values anyone writes in one, so the schema
// has nothing to constrain.
var vocabulariesWithoutSchemaEnum = map[string]string{
	"config,policy": "ReferenceKind*: which section a reference was found in",
	"dri,team":      "OwnerKind*: which of owner's mutually exclusive fields was set",
}

// TestVocabularyParityWithSchema is the DRY gate over contract vocabularies.
// Every enum a bundle author can write exists twice: as a Go const block in
// pkg/contract, and as an "enum" in the JSON Schema pkg/validation embeds. Only
// the schema copy is enforced, so a value added to the Go side alone compiles,
// reads as supported and is then rejected at validation with no hint that the
// constant promising it was never wired up.
//
// The reverse direction is deliberately not asserted: a schema enum is free to
// exist without Go constants (pactoVersion's sole legal value needs no name),
// and demanding one would manufacture constants nothing reads.
func TestVocabularyParityWithSchema(t *testing.T) {
	root := repoRootFromCaller(t)
	enums := schemaEnums(t, root)
	vocabs := goVocabularies(t, root)

	if len(vocabs) == 0 {
		t.Fatal("found no Go const vocabularies in pkg/contract; the source scan is broken, not the package")
	}

	for key, names := range vocabs {
		if _, ok := enums[key]; ok {
			continue
		}
		if vocabulariesWithoutSchemaEnum[key] != "" {
			continue
		}
		t.Errorf("const block %v spells the vocabulary [%s], which matches no enum in the contract schema.\n"+
			"A bundle is validated against the schema, so a value only the Go side knows is rejected at validation.\n"+
			"Add it to the schema enum, or record the block in vocabulariesWithoutSchemaEnum with the reason it has no counterpart.", names, key)
	}

	for key, reason := range vocabulariesWithoutSchemaEnum {
		if _, ok := vocabs[key]; !ok {
			t.Errorf("vocabulariesWithoutSchemaEnum exempts [%s] (%s), which no const block in pkg/contract declares; delete the entry", key, reason)
		}
		if ptrs, ok := enums[key]; ok {
			t.Errorf("vocabulariesWithoutSchemaEnum exempts [%s] (%s), but the schema now constrains it at %v; delete the entry", key, reason, ptrs)
		}
	}
}
