package diff

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestDiffInterfaces_TypeChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Interfaces[0].Type = "grpc"
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.type" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.type Modified change")
	}
}

func TestDiffInterfaces_PortChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	newPort := 9090
	new.Interfaces[0].Port = &newPort
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.port" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.port Modified change")
	}
}

func TestDiffInterfaces_PortAdded(t *testing.T) {
	old := minimalContract()
	old.Interfaces[0].Port = nil
	new := minimalContract()
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.port" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.port Added change")
	}
}

func TestDiffInterfaces_PortRemoved(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Interfaces[0].Port = nil
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.port" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.port Removed change")
	}
}

func TestDiffInterfaces_VisibilityChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Interfaces[0].Visibility = "public"
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.visibility" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.visibility Modified change")
	}
}

func TestDiffInterfaces_ContractPathChanged(t *testing.T) {
	old := minimalContract()
	old.Interfaces[0].Contract = "old.yaml"
	new := minimalContract()
	new.Interfaces[0].Contract = "new.yaml"
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.contract" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.contract Modified change")
	}
}

func TestDiffInterfaces_ContractOneEmpty(t *testing.T) {
	old := minimalContract()
	old.Interfaces[0].Contract = ""
	new := minimalContract()
	new.Interfaces[0].Contract = "openapi.yaml"
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.contract" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.contract Modified change when one is empty")
	}
}

func TestDiffConfiguration_BothNil(t *testing.T) {
	old := minimalContract()
	old.Configuration = nil
	new := minimalContract()
	new.Configuration = nil
	changes := diffConfiguration(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffConfiguration_OldNil(t *testing.T) {
	old := minimalContract()
	old.Configuration = nil
	new := minimalContract()
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration Added change")
	}
}

func TestDiffConfiguration_NewNil(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Configuration = nil
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration Removed change")
	}
}

func TestDiffConfiguration_SchemaChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Configuration.Schema = "config/new-schema.json"
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.schema" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.schema Modified change")
	}
}

func TestDiffConfiguration_EmptySchemaNoFileDiff(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{Schema: ""}
	new := minimalContract()
	new.Configuration = &contract.Configuration{Schema: ""}
	changes := diffConfiguration(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for empty schemas, got %d", len(changes))
	}
}

func TestDiffConfiguration_RefChanged(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{Ref: "oci://ghcr.io/acme/config:1.0.0"}
	new := minimalContract()
	new.Configuration = &contract.Configuration{Ref: "oci://ghcr.io/acme/config:2.0.0"}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.ref Modified change")
	}
}

func TestDiffConfiguration_RefAdded(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{Schema: "configuration/schema.json"}
	new := minimalContract()
	new.Configuration = &contract.Configuration{Schema: "configuration/schema.json", Ref: "oci://ghcr.io/acme/config:1.0.0"}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.ref" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.ref Added change")
	}
}

func TestDiffConfiguration_RefRemoved(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{Ref: "oci://ghcr.io/acme/config:1.0.0"}
	new := minimalContract()
	new.Configuration = &contract.Configuration{Schema: "configuration/schema.json"}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.ref" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.ref Removed change")
	}
}

func TestDiffConfiguration_OldNilWithRef(t *testing.T) {
	old := minimalContract()
	old.Configuration = nil
	new := minimalContract()
	new.Configuration = &contract.Configuration{Ref: "oci://ghcr.io/acme/config:1.0.0"}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration Added change")
	}
}

func TestDiffConfiguration_NewNilWithRef(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{Ref: "oci://ghcr.io/acme/config:1.0.0"}
	new := minimalContract()
	new.Configuration = nil
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration Removed change")
	}
}

func TestDiffPolicy_BothNil(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	changes := diffPolicy(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffPolicy_Added(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Schema: "policy/schema.json"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0]" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0] Added change")
	}
}

func TestDiffPolicy_AddedWithRef(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0]" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0] Added change")
	}
}

func TestDiffPolicy_Removed(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Schema: "policy/schema.json"}}
	new := minimalContract()
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0]" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0] Removed change")
	}
}

func TestDiffPolicy_RemovedWithRef(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0]" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0] Removed change")
	}
}

func TestDiffPolicy_SchemaChanged(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Schema: "policy/old.json"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Schema: "policy/new.json"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0].schema" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0].schema Modified change")
	}
}

func TestDiffPolicy_RefChanged(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Ref: "oci://ghcr.io/acme/policy:2.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0].ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0].ref Modified change")
	}
}

func TestDiffPolicy_NoChanges(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffPolicy_RefAdded(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Schema: "policy/schema.json"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0].ref" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0].ref Added change")
	}
}

func TestDiffPolicy_RefRemoved(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Schema: "policy/schema.json"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies[0].ref" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policies[0].ref Removed change")
	}
}

func TestDiffPolicy_SchemaFileChanged(t *testing.T) {
	// Policy-provider bundle: no policies in contract, but policy/schema.json exists.
	old := minimalContract()
	new := minimalContract()
	oldFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{
			"type": "object",
			"properties": {"service": {"type": "object"}}
		}`)},
	}
	newFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{
			"type": "object",
			"properties": {"service": {"type": "object"}, "runtime": {"type": "object"}}
		}`)},
	}
	changes := diffPolicy(old, new, oldFS, newFS)
	if len(changes) == 0 {
		t.Fatal("expected changes for modified policy/schema.json")
	}
	found := false
	for _, c := range changes {
		if c.Type == Added && strings.Contains(c.Path, "runtime") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a change involving runtime, got %+v", changes)
	}
}

func TestDiffPolicy_SchemaFileAdded(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	oldFS := fstest.MapFS{} // empty — no policy/schema.json
	newFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	}
	changes := diffPolicy(old, new, oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Added || changes[0].Path != "policy/schema.json" {
		t.Errorf("unexpected change: %+v", changes[0])
	}
}

func TestDiffPolicy_SchemaFileRemoved(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	oldFS := fstest.MapFS{
		"policy/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	}
	newFS := fstest.MapFS{} // empty — no policy/schema.json
	changes := diffPolicy(old, new, oldFS, newFS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Removed || changes[0].Path != "policy/schema.json" {
		t.Errorf("unexpected change: %+v", changes[0])
	}
}

func TestDiffPolicy_SchemaFileBothMissing(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	oldFS := fstest.MapFS{}
	newFS := fstest.MapFS{}
	changes := diffPolicy(old, new, oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffPolicy_SchemaFileNoChange(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	schema := []byte(`{"type":"object","properties":{"service":{"type":"object"}}}`)
	oldFS := fstest.MapFS{"policy/schema.json": &fstest.MapFile{Data: schema}}
	newFS := fstest.MapFS{"policy/schema.json": &fstest.MapFile{Data: schema}}
	changes := diffPolicy(old, new, oldFS, newFS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d: %+v", len(changes), changes)
	}
}

func TestDiffConfiguration_ConfigsArrayAdded(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{}
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.configs[0]" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.configs[0] Added change")
	}
}

func TestDiffConfiguration_ConfigsArrayRemoved(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	new := minimalContract()
	new.Configuration = &contract.Configuration{}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.configs[0]" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.configs[0] Removed change")
	}
}

func TestDiffConfiguration_ConfigsNameChanged(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "web", Schema: "config/app.json"},
		},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.configs[0].name" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.configs[0].name Modified change")
	}
}

func TestDiffConfiguration_ConfigsSchemaChanged(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/old.json"},
		},
	}
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/new.json"},
		},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.configs[0].schema" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.configs[0].schema Modified change")
	}
}

func TestDiffConfiguration_ConfigsRefChanged(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0"},
		},
	}
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Ref: "oci://ghcr.io/acme/config:2.0"},
		},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.configs[0].ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.configs[0].ref Modified change")
	}
}

func TestDiffConfiguration_ConfigsRefAdded(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json", Ref: "oci://ghcr.io/acme/config:1.0"},
		},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.configs[0].ref" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.configs[0].ref Added change")
	}
}

func TestDiffConfiguration_ConfigsRefRemoved(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0"},
		},
	}
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration.configs[0].ref" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration.configs[0].ref Removed change")
	}
}

func TestDiffConfiguration_ConfigsSchemaFileDiffed(t *testing.T) {
	oldFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`)},
	}
	newFS := fstest.MapFS{
		"config/app.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}}}`)},
	}
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	changes := diffConfiguration(old, new, oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "schema.properties.b" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected schema property diff in configs array")
	}
}

func TestConfigSummary_Nil(t *testing.T) {
	if got := configSummary(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestConfigSummary_Ref(t *testing.T) {
	cfg := &contract.Configuration{Ref: "oci://example.com/config:1.0"}
	if got := configSummary(cfg); got != "oci://example.com/config:1.0" {
		t.Errorf("expected ref, got %q", got)
	}
}

func TestConfigSummary_Configs(t *testing.T) {
	cfg := &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "a"},
			{Name: "b"},
		},
	}
	if got := configSummary(cfg); got != "2 configs" {
		t.Errorf("expected '2 configs', got %q", got)
	}
}

func TestConfigSummary_Empty(t *testing.T) {
	cfg := &contract.Configuration{}
	if got := configSummary(cfg); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestNamedConfigSummary_WithRef(t *testing.T) {
	cfg := &contract.NamedConfigSource{Name: "app", Ref: "oci://example.com/config:1.0"}
	if got := namedConfigSummary(cfg); got != "app: oci://example.com/config:1.0" {
		t.Errorf("expected 'app: oci://...', got %q", got)
	}
}

func TestNamedConfigSummary_WithSchema(t *testing.T) {
	cfg := &contract.NamedConfigSource{Name: "app", Schema: "config/app.json"}
	if got := namedConfigSummary(cfg); got != "app: config/app.json" {
		t.Errorf("expected 'app: config/app.json', got %q", got)
	}
}

func TestDiffConfiguration_OldNilWithConfigs(t *testing.T) {
	old := minimalContract()
	old.Configuration = nil
	new := minimalContract()
	new.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration Added change")
	}
}

func TestDiffConfiguration_NewNilWithConfigs(t *testing.T) {
	old := minimalContract()
	old.Configuration = &contract.Configuration{
		Configs: []contract.NamedConfigSource{
			{Name: "app", Schema: "config/app.json"},
		},
	}
	new := minimalContract()
	new.Configuration = nil
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configuration" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configuration Removed change")
	}
}

func TestDiffConfiguration_SchemaFilesDiffed(t *testing.T) {
	oldFS := fstest.MapFS{
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`)},
	}
	newFS := fstest.MapFS{
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"number"}}}`)},
	}
	old := minimalContract()
	new := minimalContract()
	changes := diffConfiguration(old, new, oldFS, newFS)
	found := false
	for _, c := range changes {
		if c.Path == "schema.properties.b" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected schema.properties.b Added change")
	}
}
