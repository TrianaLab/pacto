package contract

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigurationSource_Fields(t *testing.T) {
	cs := ConfigurationSource{
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

func TestConfigurationSource_RefOnly(t *testing.T) {
	cs := ConfigurationSource{
		Name: "ext",
		Ref:  "oci://example.com/config:1.0",
	}
	if cs.Ref != "oci://example.com/config:1.0" {
		t.Errorf("expected ref, got %s", cs.Ref)
	}
}

func TestContract_Configurations(t *testing.T) {
	c := &Contract{
		Configurations: []ConfigurationSource{
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

func TestPolicySource_NameField(t *testing.T) {
	ps := PolicySource{
		Name:   "scaling",
		Schema: "policy/scaling.json",
	}
	if ps.Name != "scaling" {
		t.Errorf("expected name scaling, got %s", ps.Name)
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

func TestReadiness_ParseV12Shape(t *testing.T) {
	src := []byte(`
minScore: 80
expires: 2026-12-31
partialCredit: 0.5
history:
  - date: 2026-06-21
    version: 2.1.0
    author: ed
    description: initial
checks:
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
	c := r.Checks[0]
	if c.Status != StatusDone || c.Category != CategorySecurity || c.Type != "ticket" {
		t.Fatalf("bad check: %+v", c)
	}
}

func TestReadinessCheck_Fields(t *testing.T) {
	rc := ReadinessCheck{
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
			Checks: []ReadinessCheck{
				{ID: "dashboard", Type: EvidenceTypeURL, Status: StatusDone, Evidence: "https://x", Weight: 60},
				{ID: "runbook", Type: EvidenceTypeDocument, Status: StatusDone, Evidence: "docs/rb.md", Weight: 40},
			},
		},
	}
	if c.Readiness == nil {
		t.Fatal("expected readiness to be present")
	}
	if len(c.Readiness.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(c.Readiness.Checks))
	}
	if c.Readiness.Checks[1].ID != "runbook" {
		t.Errorf("expected second check runbook, got %s", c.Readiness.Checks[1].ID)
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
		Checks:   []ReadinessCheck{{ID: "dashboard", Type: EvidenceTypeURL, Status: StatusDone, Evidence: "https://x", Weight: 100}},
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
		Configurations: []ConfigurationSource{
			{Name: "cfg-ref", Ref: "oci://r/cfg"},
			{Name: "cfg-inline", Schema: "config/schema.json"}, // no Ref -> skipped
		},
		Policies: []PolicySource{
			{Name: "pol-ref", Ref: "oci://r/pol"},
			{Name: "pol-inline", Schema: "policy/schema.json"}, // no Ref -> skipped
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
		Configurations: []ConfigurationSource{{Name: "cfg", Schema: "config/schema.json"}},
		Policies:       []PolicySource{{Name: "pol", Schema: "policy/schema.json"}},
	}
	if got := c.ReferenceRefs(); got != nil {
		t.Errorf("expected nil refs for inline-only contract, got %+v", got)
	}
}
