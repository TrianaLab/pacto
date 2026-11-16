package dashboard

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

// This file is the OpenAPI vocabulary spec: it proves the
// finite wire vocabularies the generated TypeScript SDK narrows on are actually
// emitted into the exported OpenAPI as enums, from Go-owned sources. In particular
// it proves finding severity carries "unknown" (the value the engine really emits
// for an unevaluable assertion, which a hand-written TS mirror had dropped), and
// that the narrower attention severity domain does NOT carry it.

// collectEnums recursively gathers every "enum" array in the OpenAPI document.
func collectEnums(v any, out *[][]string) {
	switch t := v.(type) {
	case map[string]any:
		if e, ok := t["enum"].([]any); ok {
			vals := make([]string, 0, len(e))
			for _, x := range e {
				if s, ok := x.(string); ok {
					vals = append(vals, s)
				}
			}
			sort.Strings(vals)
			*out = append(*out, vals)
		}
		for _, child := range t {
			collectEnums(child, out)
		}
	case []any:
		for _, child := range t {
			collectEnums(child, out)
		}
	}
}

func sortedSet(vals ...string) []string {
	cp := append([]string(nil), vals...)
	sort.Strings(cp)
	return cp
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

// hasEnumEqual reports whether some emitted enum is exactly the given set.
func hasEnumEqual(enums [][]string, want []string) bool {
	for _, e := range enums {
		if equalStrs(e, want) {
			return true
		}
	}
	return false
}

// hasEnumContaining reports whether some emitted enum contains every wanted value.
func hasEnumContaining(enums [][]string, want ...string) bool {
	for _, e := range enums {
		set := map[string]bool{}
		for _, v := range e {
			set[v] = true
		}
		all := true
		for _, w := range want {
			if !set[w] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func TestOpenAPI_FiniteEnums(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal OpenAPI: %v", err)
	}
	var enums [][]string
	collectEnums(doc, &enums)
	if len(enums) == 0 {
		t.Fatal("no enums emitted in the OpenAPI; the enum struct tags are not surfacing")
	}

	// The finding severity enum is Go-owned: it must equal the finding.Severity
	// constant set EXACTLY, which includes SeverityUnknown. This is the concrete bug
	// the hand-written TS mirror had (it dropped "unknown").
	findingSeverity := sortedSet(
		string(finding.SeverityError), string(finding.SeverityWarning),
		string(finding.SeverityInfo), string(finding.SeverityUnknown),
	)
	if !hasEnumEqual(enums, findingSeverity) {
		t.Errorf("no OpenAPI enum equals the finding severity set %v (SeverityUnknown must surface)", findingSeverity)
	}

	// The narrower attention severity domain must exist and must NOT carry "unknown".
	if !hasEnumEqual(enums, sortedSet("error", "warning", "info")) {
		t.Error("the narrower attention severity enum [error warning info] is not emitted")
	}

	cases := []struct {
		name string
		want []string
	}{
		{"entity kind", sortedSet("service", "revision", "target", "owner", "source", "domain", "offering", "external")},
		{"source health", sortedSet("available", "partial", "stale", "unavailable")},
		{"identity class", sortedSet("exact", "missing-digest", "mutable", "no-ref", "local", "malformed", "digest-mismatch")},
		{"link state", sortedSet("exact", "inferred", "ambiguous", "unresolved")},
		{"difference", sortedSet("matched", "expected-not-observed", "observed-not-expected", "insufficient")},
		{"direction", sortedSet("dependencies", "dependents", "both")},
		{"impact outcome", sortedSet("incompatible", "unknown", "compatible", "unclassified", "breaks", "may-degrade")},
		{"lifecycle filter", sortedSet("deprecated", "sunset")},
	}
	for _, c := range cases {
		if !hasEnumEqual(enums, c.want) {
			t.Errorf("no OpenAPI enum equals the %s vocabulary %v", c.name, c.want)
		}
	}

	// Fleet/product status: the full 7-value domain must be present as an enum
	// (compliance is the kind-locked field that carries it).
	if !hasEnumContaining(enums, "Compliant", "NonCompliant", "Unknown", "Warning", "Invalid", "Reference", "NotEvaluated") {
		t.Error("the fleet/product status vocabulary is not emitted as an enum")
	}
	// Knowledge views must be emitted (they are a slice-item enum).
	if !hasEnumContaining(enums, "expected", "observed", "differences") {
		t.Error("the knowledge-view vocabulary is not emitted as an enum")
	}
}

// TestOpenAPI_EnumsOnSpecificFields asserts each important finite vocabulary is the
// enum on the SPECIFIC schema field that carries it, not
// merely present somewhere in the document. A global search would pass even if a
// field lost its enum as long as some OTHER field happened to carry the same value
// set; pinning the field makes the drift gate real.
func TestOpenAPI_EnumsOnSpecificFields(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal OpenAPI: %v", err)
	}

	cases := []struct {
		schema, field string
		want          []string
	}{
		{"Fleet.ProductFinding", "severity", sortedSet(
			string(finding.SeverityError), string(finding.SeverityWarning),
			string(finding.SeverityInfo), string(finding.SeverityUnknown))},
		{"ProductAttentionItem", "severity", sortedSet("error", "warning", "info")},
		{"ProductTargetDetail", "compliance", sortedSet("Compliant", "NonCompliant", "Unknown", "Warning", "Invalid", "Reference", "NotEvaluated")},
		{"ProductTargetDetail", "linkState", sortedSet("exact", "inferred", "ambiguous", "unresolved")},
		{"Fleet.SourceState", "status", sortedSet("available", "partial", "stale", "unavailable")},
		{"ProductSourceDetail", "health", sortedSet("available", "partial", "stale", "unavailable")},
		{"ProductRef", "kind", sortedSet("service", "revision", "target", "owner", "source", "domain", "offering", "external")},
		{"ProductOfferingUse", "via", sortedSet("dependency", "configuration_reference", "policy_reference")},
		{"ProductEdge", "difference", sortedSet("matched", "expected-not-observed", "observed-not-expected", "insufficient")},
		// provenance is a merged-edge value: edgeProvenance can emit the combined
		// "declared+observed" when an edge is both declared and observed, so the OpenAPI
		// enum (and thus the generated SDK) must declare that value, never just the two
		// singletons. "inferred" names an edge to an external system only inferred
		// from configuration.
		{"ProductEdge", "provenance", sortedSet("declared", "observed", "declared+observed", "inferred")},
		{"ProductEdge", "observationScope", sortedSet("service", "target")},
		{"ProductEdge", "serviceCorroboration", sortedSet("matched", "expected-not-observed", "insufficient")},
		{"ProductNeighborhood", "direction", sortedSet("dependencies", "dependents", "both")},
		{"ProductNeighborhood", "views", sortedSet("expected", "observed", "differences")},
		{"StatsAnswer", "environmentOrder", sortedSet("stages", "name")},
	}
	for _, c := range cases {
		got := schemaFieldEnum(t, doc, c.schema, c.field)
		if !equalStrs(sortedKeys(got), c.want) {
			t.Errorf("%s.%s enum = %v, want %v", c.schema, c.field, sortedKeys(got), c.want)
		}
	}
}

// sortedKeys returns the sorted keys of a string set.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// queryParamEnum is the enum of a GET operation's query parameter, in order.
func queryParamEnum(t *testing.T, doc map[string]any, path, name string) []string {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	get, _ := paths[path].(map[string]any)["get"].(map[string]any)
	params, _ := get["parameters"].([]any)
	for _, p := range params {
		pm, _ := p.(map[string]any)
		if pm["name"] != name || pm["in"] != "query" {
			continue
		}
		schema, _ := pm["schema"].(map[string]any)
		if items, ok := schema["items"].(map[string]any); ok {
			schema = items // an array parameter enumerates its items
		}
		vals, _ := schema["enum"].([]any)
		out := []string{}
		for _, v := range vals {
			out = append(out, v.(string))
		}
		return out
	}
	t.Fatalf("%s has no query parameter %s", path, name)
	return nil
}

// TestStatsMetricEnumMatchesRunrecord pins the literal enum tags of the stats
// query parameters to the Go lists they repeat.
func TestStatsMetricEnumMatchesRunrecord(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if got := queryParamEnum(t, doc, "/api/stats/records", "metric"); !equalStrs(got, stats.MetricNames) {
		t.Errorf("metric enum = %v, want stats.MetricNames %v", got, stats.MetricNames)
	}
	for _, path := range []string{"/api/stats", "/api/stats/records"} {
		if got := queryParamEnum(t, doc, path, "window"); !equalStrs(got, []string{"7d", "30d", "90d"}) {
			t.Errorf("%s window enum = %v", path, got)
		}
	}
}

// TestEntitiesKindsEnumMatchesEntityKinds: the entities kinds parameter
// accepts exactly the kinds the fleet knows.
func TestEntitiesKindsEnumMatchesEntityKinds(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if got := queryParamEnum(t, doc, "/api/fleet/entities", "kinds"); !equalStrs(got, fleet.EntityKinds()) {
		t.Errorf("kinds enum = %v, want fleet.EntityKinds() %v", got, fleet.EntityKinds())
	}
	if got := queryParamEnum(t, doc, "/api/fleet/entities", "class"); !equalStrs(got, fleet.ExternalClasses()) {
		t.Errorf("class enum = %v, want fleet.ExternalClasses() %v", got, fleet.ExternalClasses())
	}
}
