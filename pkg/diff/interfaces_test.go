package diff

import (
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
	changes := diffPolicy(old, new)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffPolicy_Added(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Policy = &contract.Policy{Schema: "policy/schema.json"}
	changes := diffPolicy(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "policy" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policy Added change")
	}
}

func TestDiffPolicy_AddedWithRef(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Policy = &contract.Policy{Ref: "oci://ghcr.io/acme/policy:1.0.0"}
	changes := diffPolicy(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "policy" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policy Added change")
	}
}

func TestDiffPolicy_Removed(t *testing.T) {
	old := minimalContract()
	old.Policy = &contract.Policy{Schema: "policy/schema.json"}
	new := minimalContract()
	changes := diffPolicy(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "policy" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policy Removed change")
	}
}

func TestDiffPolicy_RemovedWithRef(t *testing.T) {
	old := minimalContract()
	old.Policy = &contract.Policy{Ref: "oci://ghcr.io/acme/policy:1.0.0"}
	new := minimalContract()
	changes := diffPolicy(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "policy" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policy Removed change")
	}
}

func TestDiffPolicy_SchemaChanged(t *testing.T) {
	old := minimalContract()
	old.Policy = &contract.Policy{Schema: "policy/old.json"}
	new := minimalContract()
	new.Policy = &contract.Policy{Schema: "policy/new.json"}
	changes := diffPolicy(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "policy.schema" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected policy.schema Modified change")
	}
}

func TestDiffPolicy_RefChanged(t *testing.T) {
	old := minimalContract()
	old.Policy = &contract.Policy{Ref: "oci://ghcr.io/acme/policy:1.0.0"}
	new := minimalContract()
	new.Policy = &contract.Policy{Ref: "oci://ghcr.io/acme/policy:2.0.0"}
	changes := diffPolicy(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "policy.ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected policy.ref Modified change")
	}
}

func TestDiffPolicy_NoChanges(t *testing.T) {
	old := minimalContract()
	old.Policy = &contract.Policy{Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}
	new := minimalContract()
	new.Policy = &contract.Policy{Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}
	changes := diffPolicy(old, new)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
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
		if c.Path == "schema.properties[b]" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected schema.properties[b] Added change")
	}
}
