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
