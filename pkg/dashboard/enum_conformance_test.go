package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// This file proves end to end (requirement, item 5) that no invalid extension data
// escapes as an out-of-schema enum value: an adversarial custom Source emits a
// non-canonical compliance, finding severity and source-health status, and every
// enum-bearing field the product HTTP API then emits still conforms to the
// generated OpenAPI domain. It complements the fleet-level ingestion tests
// (pkg/fleet/enum_ingestion_test.go) by exercising the real query + transport +
// HTTP path, and the static openapi_enum_test.go which asserts the domains exist.

// schemaFieldEnum returns the enum value set declared for components.schemas.<schema>.<field>
// in the exported OpenAPI (following array item enums), or fails if there is none.
func schemaFieldEnum(t *testing.T, doc map[string]any, schema, field string) map[string]bool {
	t.Helper()
	s := mapAt(doc, "components", "schemas", schema)
	if s == nil {
		t.Fatalf("schema %q not found in OpenAPI", schema)
	}
	props, _ := s["properties"].(map[string]any)
	f, _ := props[field].(map[string]any)
	if f == nil {
		t.Fatalf("schema %q has no property %q", schema, field)
	}
	enum := enumSet(f)
	if enum == nil {
		if items, ok := f["items"].(map[string]any); ok {
			enum = enumSet(items)
		}
	}
	if enum == nil {
		t.Fatalf("%s.%s declares no enum", schema, field)
	}
	return enum
}

func enumSet(m map[string]any) map[string]bool {
	e, ok := m["enum"].([]any)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, v := range e {
		if s, ok := v.(string); ok {
			out[s] = true
		}
	}
	return out
}

func mapAt(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// TestProductHTTP_EmittedEnumsConform builds a snapshot from an adversarial custom
// Source and drives the real product HTTP endpoints, asserting every emitted enum
// value is a member of the field's generated OpenAPI domain.
func TestProductHTTP_EmittedEnumsConform(t *testing.T) {
	raw, err := ExportOpenAPI()
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal OpenAPI: %v", err)
	}

	src := fleet.NewMemorySource("ext", "custom", &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: newPaymentBundle(), Digest: "sha256:aaa", ResolvedRef: "reg/payment-service:1.0.0"}},
		Targets: []fleet.RawTarget{{
			Scope: "prod", Kind: "k8s", Name: "pay-1", Service: "payment-service",
			Compliance: "Banana", // out of the canonical compliance vocabulary
			Findings:   []finding.Finding{{Severity: finding.Severity("critical"), Message: "x"}},
		}},
		State: &fleet.SourceState{Status: fleet.SourceStatus("weird")}, // out of the source-health vocabulary
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	base, cancel := startFleetTestServer(t, func(context.Context) (*fleet.Query, error) { return q, nil }, nil)
	defer cancel()

	// Target detail: compliance, linkState and each finding severity must conform.
	targetKey := string(fleet.NewTargetKey("prod", "k8s", "pay-1"))
	var td map[string]any
	getJSON(t, base+"/api/fleet/entities/target?key="+url.QueryEscape(targetKey), http.StatusOK, &td)
	target, _ := td["target"].(map[string]any)
	if target == nil {
		t.Fatalf("target detail missing target payload: %v", td)
	}
	assertConforms(t, "ProductTargetDetail.compliance", target["compliance"], schemaFieldEnum(t, doc, "ProductTargetDetail", "compliance"))
	assertConforms(t, "ProductTargetDetail.linkState", target["linkState"], schemaFieldEnum(t, doc, "ProductTargetDetail", "linkState"))
	sevSet := schemaFieldEnum(t, doc, "Fleet.ProductFinding", "severity")
	for _, f := range itemsOf(target, "findings") {
		assertConforms(t, "Fleet.ProductFinding.severity", f["severity"], sevSet)
	}

	// Source detail: health must conform.
	var sd map[string]any
	getJSON(t, base+"/api/fleet/entities/source?key=ext", http.StatusOK, &sd)
	source, _ := sd["source"].(map[string]any)
	if source == nil {
		t.Fatalf("source detail missing source payload: %v", sd)
	}
	assertConforms(t, "ProductSourceDetail.health", source["health"], schemaFieldEnum(t, doc, "ProductSourceDetail", "health"))

	// Overview: meta source statuses and attention severities must conform.
	var ov map[string]any
	getJSON(t, base+"/api/fleet/overview", http.StatusOK, &ov)
	statusSet := schemaFieldEnum(t, doc, "Fleet.SourceState", "status")
	for _, s := range itemsSlice(mapAt(ov, "meta"), "sources") {
		assertConforms(t, "Fleet.SourceState.status", s["status"], statusSet)
	}
	attSevSet := schemaFieldEnum(t, doc, "ProductAttentionItem", "severity")
	for _, it := range itemsOf(ov, "attention") {
		assertConforms(t, "ProductAttentionItem.severity", it["severity"], attSevSet)
	}
}

func assertConforms(t *testing.T, label string, value any, allowed map[string]bool) {
	t.Helper()
	s, ok := value.(string)
	if !ok {
		t.Errorf("%s: expected a string enum value, got %T (%v)", label, value, value)
		return
	}
	if !allowed[s] {
		t.Errorf("%s emitted %q, which is NOT in the OpenAPI domain %v", label, s, keysOf(allowed))
	}
}

// itemsOf returns the []map items nested under obj[field].items (a bounded preview).
func itemsOf(obj map[string]any, field string) []map[string]any {
	return itemsSlice(mapAt(obj, field), "items")
}

func itemsSlice(obj map[string]any, field string) []map[string]any {
	if obj == nil {
		return nil
	}
	arr, _ := obj[field].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
