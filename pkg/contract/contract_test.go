package contract

import (
	"testing"
)

func TestEffectiveConfigs_Nil(t *testing.T) {
	var c *Configuration
	if got := c.EffectiveConfigs(); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestEffectiveConfigs_Empty(t *testing.T) {
	c := &Configuration{}
	if got := c.EffectiveConfigs(); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestEffectiveConfigs_LegacySchemaOnly(t *testing.T) {
	c := &Configuration{Schema: "config/schema.json"}
	got := c.EffectiveConfigs()
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Schema != "config/schema.json" {
		t.Errorf("expected schema config/schema.json, got %s", got[0].Schema)
	}
}

func TestEffectiveConfigs_LegacyRefOnly(t *testing.T) {
	c := &Configuration{Ref: "oci://example.com/config:1.0"}
	got := c.EffectiveConfigs()
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Ref != "oci://example.com/config:1.0" {
		t.Errorf("expected ref, got %s", got[0].Ref)
	}
}

func TestEffectiveConfigs_LegacyValuesOnly(t *testing.T) {
	c := &Configuration{Values: map[string]interface{}{"KEY": "val"}}
	got := c.EffectiveConfigs()
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].Values["KEY"] != "val" {
		t.Error("expected KEY=val")
	}
}

func TestEffectiveConfigs_MultiConfigs(t *testing.T) {
	c := &Configuration{
		Configs: []NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
			{Name: "db", Ref: "oci://example.com/db-config:1.0", Values: map[string]interface{}{"HOST": "localhost"}},
		},
	}
	got := c.EffectiveConfigs()
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Name != "app" || got[0].Schema != "config/app.json" {
		t.Errorf("first config mismatch: %+v", got[0])
	}
	if got[1].Name != "db" || got[1].Ref != "oci://example.com/db-config:1.0" {
		t.Errorf("second config mismatch: %+v", got[1])
	}
	if got[1].Values["HOST"] != "localhost" {
		t.Error("expected HOST=localhost in second config")
	}
}

func TestEffectiveConfigs_ConfigsTakePrecedenceOverLegacy(t *testing.T) {
	c := &Configuration{
		Schema: "should-be-ignored.json",
		Configs: []NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	got := c.EffectiveConfigs()
	if len(got) != 1 {
		t.Fatalf("expected 1 (Configs takes precedence), got %d", len(got))
	}
	if got[0].Schema != "config/app.json" {
		t.Errorf("expected Configs entry, got %s", got[0].Schema)
	}
}
