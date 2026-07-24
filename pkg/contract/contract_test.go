package contract

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRequired_SerializationRoundTrip(t *testing.T) {
	// required is MANDATORY in the v2 schema, so a false value MUST survive marshaling
	// (no omitempty) or the re-emitted contract would fail the schema on reload. Spec section 5.2.
	c := Contract{
		PactoVersion:   "2.0",
		Service:        Service{Name: "orders", Version: "1.0.0"},
		Dependencies:   []Dependency{{Name: "dep", Ref: "oci://x", Required: false, Compatibility: "^1.0.0"}},
		Configurations: []Configuration{{Name: "cfg", Schema: "config/schema.json", Required: false}},
	}
	for _, tc := range []struct {
		val      bool
		yamlWant string
		jsonWant string
	}{
		{false, "required: false", `"required":false`},
		{true, "required: true", `"required":true`},
	} {
		c.Dependencies[0].Required = tc.val
		c.Configurations[0].Required = tc.val
		y, err := yaml.Marshal(c)
		if err != nil {
			t.Fatalf("yaml marshal: %v", err)
		}
		if got := strings.Count(string(y), tc.yamlWant); got != 2 {
			t.Errorf("yaml: want 2 %q (dependency+configuration), got %d\n%s", tc.yamlWant, got, y)
		}
		j, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("json marshal: %v", err)
		}
		if got := strings.Count(string(j), tc.jsonWant); got != 2 {
			t.Errorf("json: want 2 %q, got %d\n%s", tc.jsonWant, got, j)
		}
	}
}

func TestConfiguration_Fields(t *testing.T) {
	cs := Configuration{
		Name:   "app",
		Schema: "config/schema.json",
		Values: map[string]any{"KEY": "val"},
	}
	if cs.Name != "app" {
		t.Errorf("expected name app, got %s", cs.Name)
	}
	if cs.Schema != "config/schema.json" {
		t.Errorf("expected schema config/schema.json, got %s", cs.Schema)
	}
	if cs.Values["KEY"] != "val" {
		t.Error("expected KEY=val")
	}
}

func TestConfiguration_RefOnly(t *testing.T) {
	cs := Configuration{
		Name: "ext",
		Ref:  "oci://example.com/config:1.0",
	}
	if cs.Ref != "oci://example.com/config:1.0" {
		t.Errorf("expected ref, got %s", cs.Ref)
	}
}

func TestContract_Configurations(t *testing.T) {
	c := &Contract{
		Configurations: []Configuration{
			{Name: "app", Schema: "config/app.json"},
			{Name: "db", Ref: "oci://example.com/db-config:1.0", Values: map[string]any{"HOST": "localhost"}},
		},
	}
	if len(c.Configurations) != 2 {
		t.Fatalf("expected 2 configurations, got %d", len(c.Configurations))
	}
	if c.Configurations[0].Name != "app" || c.Configurations[0].Schema != "config/app.json" {
		t.Errorf("first config mismatch: %+v", c.Configurations[0])
	}
	if c.Configurations[1].Name != "db" || c.Configurations[1].Ref != "oci://example.com/db-config:1.0" {
		t.Errorf("second config mismatch: %+v", c.Configurations[1])
	}
	if c.Configurations[1].Values["HOST"] != "localhost" {
		t.Error("expected HOST=localhost in second config")
	}
}

func TestContract_EmptyConfigurations(t *testing.T) {
	c := &Contract{}
	if c.Configurations != nil {
		t.Errorf("expected nil configurations, got %v", c.Configurations)
	}
}

func TestPolicy_NameField(t *testing.T) {
	ps := Policy{
		Name:   "scaling",
		Schema: "policy/scaling.json",
	}
	if ps.Name != "scaling" {
		t.Errorf("expected name scaling, got %s", ps.Name)
	}
}

func TestPolicy_Target(t *testing.T) {
	ps := Policy{
		Name:   "compliance",
		Schema: "policy/compliance.json",
		Target: PolicyTargetContract,
	}
	if ps.Target != "contract" {
		t.Errorf("expected target contract, got %s", ps.Target)
	}
}

func TestDependency_NameField(t *testing.T) {
	d := Dependency{
		Name:          "auth-svc",
		Ref:           "oci://ghcr.io/acme/auth:1.0.0",
		Compatibility: "^1.0.0",
	}
	if d.Name != "auth-svc" {
		t.Errorf("expected name auth-svc, got %s", d.Name)
	}
}

func TestReadiness_ParseV2Shape(t *testing.T) {
	src := []byte(`
minScore: 80
expires: 2026-12-31
partialCredit: 0.5
history:
  - date: 2026-06-21
    version: 2.1.0
    author: ed
    description: initial
claims:
  - id: sec
    type: ticket
    category: security
    status: done
    evidence: SEC-1
    weight: 30
`)
	var r Readiness
	if err := yaml.Unmarshal(src, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Expires != "2026-12-31" || r.PartialCredit == nil || *r.PartialCredit != 0.5 {
		t.Fatalf("bad top-level: %+v", r)
	}
	if len(r.History) != 1 || r.History[0].Author != "ed" {
		t.Fatalf("bad history: %+v", r.History)
	}
	c := r.Claims[0]
	if c.Status != StatusDone || c.Category != CategorySecurity || c.Type != "ticket" {
		t.Fatalf("bad claim: %+v", c)
	}
}

func TestReadinessClaim_Fields(t *testing.T) {
	rc := ReadinessClaim{
		ID:          "dashboard",
		Type:        EvidenceTypeURL,
		Category:    CategoryObservability,
		Status:      StatusDone,
		Evidence:    "https://grafana.company.com/payment-api",
		Weight:      20,
		Description: "Main production dashboard",
	}
	if rc.ID != "dashboard" {
		t.Errorf("expected id dashboard, got %s", rc.ID)
	}
	if rc.Type != "url" {
		t.Errorf("expected type url, got %s", rc.Type)
	}
	if rc.Category != "observability" {
		t.Errorf("expected category observability, got %s", rc.Category)
	}
	if rc.Status != "done" {
		t.Errorf("expected status done, got %s", rc.Status)
	}
	if rc.Evidence != "https://grafana.company.com/payment-api" {
		t.Errorf("expected evidence, got %s", rc.Evidence)
	}
	if rc.Weight != 20 {
		t.Errorf("expected weight 20, got %d", rc.Weight)
	}
	if rc.Description != "Main production dashboard" {
		t.Errorf("expected description, got %s", rc.Description)
	}
}

func TestReadinessStatusConstants(t *testing.T) {
	want := []string{"done", "partial", "not-done", "deferred"}
	got := []string{StatusDone, StatusPartial, StatusNotDone, StatusDeferred}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("status %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestReadinessCategoryConstants(t *testing.T) {
	want := []string{
		"architecture", "testing", "code-quality", "observability", "security",
		"documentation", "infrastructure", "ci-cd", "deployment", "resilience",
		"backup-recovery", "incident-response", "compliance", "other",
	}
	got := []string{
		CategoryArchitecture, CategoryTesting, CategoryCodeQuality, CategoryObservability, CategorySecurity,
		CategoryDocumentation, CategoryInfrastructure, CategoryCICD, CategoryDeployment, CategoryResilience,
		CategoryBackupRecovery, CategoryIncidentResponse, CategoryCompliance, CategoryOther,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d categories, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("category %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestEvidenceTypeConstants(t *testing.T) {
	want := []string{"url", "document", "ticket", "report", "artifact", "identifier", "other"}
	got := []string{
		EvidenceTypeURL,
		EvidenceTypeDocument,
		EvidenceTypeTicket,
		EvidenceTypeReport,
		EvidenceTypeArtifact,
		EvidenceTypeIdentifier,
		EvidenceTypeOther,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d evidence types, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("evidence type %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestContract_Readiness(t *testing.T) {
	c := &Contract{
		Readiness: &Readiness{
			Expires: "2026-12-31",
			Claims: []ReadinessClaim{
				{ID: "dashboard", Type: EvidenceTypeURL, Status: StatusDone, Evidence: "https://x", Weight: 60},
				{ID: "runbook", Type: EvidenceTypeDocument, Status: StatusDone, Evidence: "docs/rb.md", Weight: 40},
			},
		},
	}
	if c.Readiness == nil {
		t.Fatal("expected readiness to be present")
	}
	if len(c.Readiness.Claims) != 2 {
		t.Fatalf("expected 2 claims, got %d", len(c.Readiness.Claims))
	}
	if c.Readiness.Claims[1].ID != "runbook" {
		t.Errorf("expected second claim runbook, got %s", c.Readiness.Claims[1].ID)
	}
}

func TestContract_EmptyReadiness(t *testing.T) {
	c := &Contract{}
	if c.Readiness != nil {
		t.Errorf("expected nil readiness, got %v", c.Readiness)
	}
}

func TestReadiness_MinScore(t *testing.T) {
	min := 80
	r := &Readiness{
		MinScore: &min,
		Expires:  "2026-12-31",
		Claims:   []ReadinessClaim{{ID: "dashboard", Type: EvidenceTypeURL, Status: StatusDone, Evidence: "https://x", Weight: 100}},
	}
	if r.MinScore == nil || *r.MinScore != 80 {
		t.Errorf("expected minScore 80, got %v", r.MinScore)
	}
}

// TestContract_ReferenceRefs asserts the order (policies first, then configs),
// the empty-Ref skip (inline schemas excluded) and the kind/name/ref mapping —
// the single source of truth both lock builders consume.
func TestContract_ReferenceRefs(t *testing.T) {
	c := &Contract{
		Configurations: []Configuration{
			{Name: "cfg-ref", Ref: "oci://r/cfg"},
			{Name: "cfg-inline", Schema: "config/schema.json"},
		},
		Policies: []Policy{
			{Name: "pol-ref", Ref: "oci://r/pol"},
			{Name: "pol-inline", Schema: "policy/schema.json"},
		},
	}
	got := c.ReferenceRefs()
	want := []ReferenceRef{
		{Kind: ReferenceKindPolicy, Name: "pol-ref", Ref: "oci://r/pol"},
		{Kind: ReferenceKindConfig, Name: "cfg-ref", Ref: "oci://r/cfg"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d refs, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ref[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestContract_ReferenceRefs_Empty covers a contract with no ref-bearing
// configs/policies (nil result).
func TestContract_ReferenceRefs_Empty(t *testing.T) {
	c := &Contract{
		Configurations: []Configuration{{Name: "cfg", Schema: "config/schema.json"}},
		Policies:       []Policy{{Name: "pol", Schema: "policy/schema.json"}},
	}
	if got := c.ReferenceRefs(); got != nil {
		t.Errorf("expected nil refs for inline-only contract, got %+v", got)
	}
}

func TestCapability_StandardTypes(t *testing.T) {
	h := Capability{Type: CapabilityHealth}
	m := Capability{Type: CapabilityMetrics}
	if h.Type != "health" {
		t.Errorf("expected health type, got %s", h.Type)
	}
	if m.Type != "metrics" {
		t.Errorf("expected metrics type, got %s", m.Type)
	}
	if h.Ref != "" || m.Ref != "" {
		t.Error("standard capabilities should have no ref")
	}
}

func TestCapability_Extension(t *testing.T) {
	ext := Capability{Type: CapabilityExtension, Ref: "example.com/custom"}
	if ext.Type != "extension" {
		t.Errorf("expected extension type, got %s", ext.Type)
	}
	if ext.Ref != "example.com/custom" {
		t.Errorf("expected namespaced ref, got %s", ext.Ref)
	}
}

func TestCapability_DiscriminatedBinding(t *testing.T) {
	src := `pactoVersion: "2.0"
service:
  name: orders
  version: 1.0.0
interfaces:
  - name: public-api
    type: openapi
    ref: interfaces/openapi.json
capabilities:
  - type: health
    binding:
      type: http
      interface: public-api
      path: /healthz
  - type: metrics
    binding:
      type: http
      interface: public-api
      path: /metrics
  - type: extension
    ref: example.com/custom
verification:
  conformance:
    - public-api
`
	c, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(c.Capabilities) != 3 {
		t.Fatalf("want 3 capabilities, got %d", len(c.Capabilities))
	}
	h := c.Capabilities[0]
	if h.Binding == nil || h.Binding.Type != "http" || h.Binding.Interface != "public-api" || h.Binding.Path != "/healthz" {
		t.Errorf("health binding not parsed: %+v", h.Binding)
	}
	if c.Capabilities[2].Binding != nil {
		t.Error("extension capability must not carry a binding")
	}
	if c.Capabilities[2].Ref != "example.com/custom" {
		t.Errorf("extension ref = %q", c.Capabilities[2].Ref)
	}
	if c.Verification == nil || len(c.Verification.Conformance) != 1 || c.Verification.Conformance[0] != "public-api" {
		t.Errorf("verification.conformance not parsed: %+v", c.Verification)
	}
}

func TestVerification_AbsentIsNil(t *testing.T) {
	src := `pactoVersion: "2.0"
service:
  name: orders
  version: 1.0.0
`
	c, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Verification != nil {
		t.Errorf("absent verification block must be nil, got %+v", c.Verification)
	}
}

func TestWorkloadConstants(t *testing.T) {
	want := []string{"service", "job", "scheduled"}
	got := []string{WorkloadService, WorkloadJob, WorkloadScheduled}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("workload %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}

func TestInterfaceTypeConstants(t *testing.T) {
	want := []string{"openapi", "asyncapi", "grpc"}
	got := []string{InterfaceTypeOpenAPI, InterfaceTypeAsyncAPI, InterfaceTypeGRPC}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("interface type %d: expected %s, got %s", i, want[i], got[i])
		}
	}
}
