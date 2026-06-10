package contract

import (
	"testing"
)

func TestConfigurationSource_Fields(t *testing.T) {
	cs := ConfigurationSource{
		Name:   "app",
		Schema: "config/schema.json",
		Values: map[string]interface{}{"KEY": "val"},
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
			{Name: "db", Ref: "oci://example.com/db-config:1.0", Values: map[string]interface{}{"HOST": "localhost"}},
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

func TestReadinessCheck_Fields(t *testing.T) {
	rc := ReadinessCheck{
		ID:          "dashboard",
		Type:        EvidenceTypeURL,
		Evidence:    "https://grafana.company.com/payment-api",
		Weight:      20,
		Expires:     "2026-12-31",
		Description: "Main production dashboard",
	}
	if rc.ID != "dashboard" {
		t.Errorf("expected id dashboard, got %s", rc.ID)
	}
	if rc.Type != "url" {
		t.Errorf("expected type url, got %s", rc.Type)
	}
	if rc.Evidence != "https://grafana.company.com/payment-api" {
		t.Errorf("expected evidence, got %s", rc.Evidence)
	}
	if rc.Weight != 20 {
		t.Errorf("expected weight 20, got %d", rc.Weight)
	}
	if rc.Expires != "2026-12-31" {
		t.Errorf("expected expires 2026-12-31, got %s", rc.Expires)
	}
	if rc.Description != "Main production dashboard" {
		t.Errorf("expected description, got %s", rc.Description)
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
			Checks: []ReadinessCheck{
				{ID: "dashboard", Type: EvidenceTypeURL, Evidence: "https://x", Weight: 60, Expires: "2026-12-31"},
				{ID: "runbook", Type: EvidenceTypeDocument, Evidence: "docs/rb.md", Weight: 40, Expires: "2026-09-30"},
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
		Checks:   []ReadinessCheck{{ID: "dashboard", Type: EvidenceTypeURL, Evidence: "https://x", Weight: 100, Expires: "2026-12-31"}},
	}
	if r.MinScore == nil || *r.MinScore != 80 {
		t.Errorf("expected minScore 80, got %v", r.MinScore)
	}
}
