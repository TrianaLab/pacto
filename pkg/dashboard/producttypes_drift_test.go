package dashboard

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestProductTypesMatchOpenAPI is the CI-blocking drift gate between the generated
// Huma OpenAPI (the Go source of truth) and the hand-written TypeScript product
// DTOs (frontend/src/lib/productTypes.ts). For every TS interface it asserts the
// field-name set equals the mapped OpenAPI schema's property set; it also asserts
// every bounded "*Preview" schema has the reusable Preview shape, and that every
// concrete OpenAPI Product* schema is modeled by a TS interface. If a Go product
// struct gains, loses or renames a field, this test fails until the TS contract is
// updated in lock-step, so the client can never silently drift from the server.
func TestProductTypesMatchOpenAPI(t *testing.T) {
	spec, err := ExportOpenAPI()
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	oaProps := func(name string) ([]string, bool) {
		s, ok := doc.Components.Schemas[name]
		if !ok {
			return nil, false
		}
		var out []string
		for p := range s.Properties {
			if p == "$schema" { // a Huma response-body artifact, not part of the contract
				continue
			}
			out = append(out, p)
		}
		return out, true
	}

	tsFields := parseTSInterfaces(t, "frontend/src/lib/productTypes.ts")

	// tsToOA maps each TS interface to the OpenAPI schema it must match. The generic
	// Preview<T>/Page<T> map to a representative concrete schema; all other
	// *Preview schemas are validated structurally below.
	tsToOA := map[string]string{
		"ProductRef":           "ProductRef",
		"ProductMeta":          "Fleet.ProductMeta",
		"SourceError":          "Fleet.SourceError",
		"SourceState":          "Fleet.SourceState",
		"Limitation":           "Fleet.Limitation",
		"Coverage":             "Fleet.Coverage",
		"RevisionIdentity":     "Fleet.RevisionIdentity",
		"ToolSummary":          "Fleet.ToolSummary",
		"DocRef":               "Fleet.DocRef",
		"DeclaredClaim":        "Fleet.DeclaredClaim",
		"ObservedSourceStat":   "Fleet.ObservedSourceStat",
		"OverviewSummary":      "Fleet.OverviewSummary",
		"Preview":              "ProductRefPreview",
		"Page":                 "ProductImpactConsumersPage",
		"Ownership":            "ProductOwnership",
		"AttributedFinding":    "ProductAttributedFinding",
		"AttributedLimitation": "ProductAttributedLimitation",
		"AttentionItem":        "ProductAttentionItem",
		"EvidenceItem":         "ProductEvidenceItem",
		"EntryPoint":           "ProductEntryPoint",
		"ProductOverview":      "ProductOverview",
		"ProductEntityList":    "ProductEntityList",
		"ProductAttentionList": "ProductAttentionList",
		"NeighborhoodNode":     "ProductNode",
		"NeighborhoodEdge":     "ProductEdge",
		"UnresolvedDependency": "ProductUnresolvedDependency",
		"ProductNeighborhood":  "ProductNeighborhood",
		"ServiceDetail":        "ProductServiceDetail",
		"RevisionDetail":       "ProductRevisionDetail",
		"TargetDetail":         "ProductTargetDetail",
		"OwnerDetail":          "ProductOwnerDetail",
		"SourceDetail":         "ProductSourceDetail",
		"ProductEntityDetail":  "ProductEntityDetail",
		"ImpactConsumer":       "ProductImpactConsumer",
		"ProductImpact":        "ProductImpact",
	}

	if len(tsFields) == 0 {
		t.Fatal("no TS interfaces parsed from productTypes.ts")
	}
	schemaNames := make([]string, 0, len(doc.Components.Schemas))
	for name := range doc.Components.Schemas {
		schemaNames = append(schemaNames, name)
	}
	checkTSMatchesOpenAPI(t, tsFields, tsToOA, oaProps)
	checkPreviewShapes(t, schemaNames, oaProps)
	checkEveryProductTypeModeled(t, schemaNames, tsToOA)
}

// checkTSMatchesOpenAPI asserts every TS interface is mapped and its field names
// equal the mapped OpenAPI schema's property names.
func checkTSMatchesOpenAPI(t *testing.T, tsFields map[string][]string, tsToOA map[string]string, oaProps func(string) ([]string, bool)) {
	for ts, fields := range tsFields {
		oa, ok := tsToOA[ts]
		if !ok {
			t.Errorf("TS interface %q has no OpenAPI mapping — add it to tsToOA and model it, or the client can drift", ts)
			continue
		}
		props, ok := oaProps(oa)
		if !ok {
			t.Errorf("TS interface %q maps to missing OpenAPI schema %q", ts, oa)
			continue
		}
		if a, b := sortedUnique(fields), sortedUnique(props); !equalStrs(a, b) {
			t.Errorf("drift: TS %q fields %v != OpenAPI %q properties %v", ts, a, oa, b)
		}
	}
}

// checkPreviewShapes asserts every "*Preview" schema has exactly the reusable
// Preview shape, so the TS Preview<T> generic faithfully models all of them.
func checkPreviewShapes(t *testing.T, schemaNames []string, oaProps func(string) ([]string, bool)) {
	wantPreview := []string{"count", "items", "total", "truncated"}
	for _, name := range schemaNames {
		if !strings.HasSuffix(name, "Preview") {
			continue
		}
		props, _ := oaProps(name)
		if !equalStrs(sortedUnique(props), wantPreview) {
			t.Errorf("preview schema %q has fields %v, want the reusable Preview shape %v", name, sortedUnique(props), wantPreview)
		}
	}
}

// checkEveryProductTypeModeled asserts every concrete (non-preview) Product*
// schema is modeled by a TS interface, so a new Go product type cannot ship
// without a TS DTO.
func checkEveryProductTypeModeled(t *testing.T, schemaNames []string, tsToOA map[string]string) {
	mapped := map[string]bool{}
	for _, oa := range tsToOA {
		mapped[oa] = true
	}
	for _, name := range schemaNames {
		if !strings.HasPrefix(name, "Product") || strings.HasSuffix(name, "Preview") {
			continue
		}
		if !mapped[name] {
			t.Errorf("OpenAPI schema %q is a product type with no TS model — add it to productTypes.ts and tsToOA", name)
		}
	}
}

var (
	tsInterfaceRe = regexp.MustCompile(`^export interface (\w+)`)
	tsFieldRe     = regexp.MustCompile(`^(\w+)\??:`)
)

// parseTSInterfaces extracts each `export interface Name { ... }` and its
// field names from a TypeScript file. It relies on productTypes.ts keeping one
// field per line and no inline object literals (a lone "}" closes an interface).
func parseTSInterfaces(t *testing.T, rel string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	out := map[string][]string{}
	var cur string
	for _, line := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(line)
		if cur == "" {
			if m := tsInterfaceRe.FindStringSubmatch(trim); m != nil {
				cur = m[1]
				out[cur] = nil
			}
			continue
		}
		if trim == "}" {
			cur = ""
			continue
		}
		if m := tsFieldRe.FindStringSubmatch(trim); m != nil {
			out[cur] = append(out[cur], m[1])
		}
	}
	return out
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
