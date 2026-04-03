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

func TestDiffConfiguration_BothEmpty(t *testing.T) {
	old := minimalContract()
	old.Configurations = nil
	new := minimalContract()
	new.Configurations = nil
	changes := diffConfiguration(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffConfiguration_Added(t *testing.T) {
	old := minimalContract()
	old.Configurations = nil
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configurations" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configurations Added change")
	}
}

func TestDiffConfiguration_Removed(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
	}
	new := minimalContract()
	new.Configurations = nil
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configurations" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configurations Removed change")
	}
}

func TestDiffConfiguration_SchemaChanged(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/old.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/new.json"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configurations.schema" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected configurations.schema Modified change")
	}
}

func TestDiffConfiguration_RefChanged(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0.0"},
	}
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Ref: "oci://ghcr.io/acme/config:2.0.0"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configurations.ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected configurations.ref Modified change")
	}
}

func TestDiffConfiguration_RefAdded(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json", Ref: "oci://ghcr.io/acme/config:1.0.0"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configurations.ref" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected configurations.ref Added change")
	}
}

func TestDiffConfiguration_RefRemoved(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0.0"},
	}
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configurations.ref" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected configurations.ref Removed change")
	}
}

func TestDiffConfiguration_EmptySchemaNoFileDiff(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: ""},
	}
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: ""},
	}
	changes := diffConfiguration(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes for empty schemas, got %d", len(changes))
	}
}

func TestDiffConfiguration_SchemaFileDiffed(t *testing.T) {
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

func TestDiffConfiguration_MultipleConfigs(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
		{Name: "db", Schema: "config/db.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
		{Name: "cache", Schema: "config/cache.json"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	foundRemoved := false
	foundAdded := false
	for _, c := range changes {
		if c.Path == "configurations" && c.Type == Removed {
			foundRemoved = true
		}
		if c.Path == "configurations" && c.Type == Added {
			foundAdded = true
		}
	}
	if !foundRemoved {
		t.Error("expected configurations Removed change for 'db'")
	}
	if !foundAdded {
		t.Error("expected configurations Added change for 'cache'")
	}
}

func TestDiffConfiguration_NoChanges(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.ConfigurationSource{
		{Name: "app", Schema: "config/app.json"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d: %v", len(changes), changes)
	}
}

func TestConfigSummary_Nil(t *testing.T) {
	if got := configSummary(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestConfigSummary_Ref(t *testing.T) {
	cfg := &contract.ConfigurationSource{Name: "app", Ref: "oci://example.com/config:1.0"}
	if got := configSummary(cfg); got != "app: oci://example.com/config:1.0" {
		t.Errorf("expected 'app: oci://...', got %q", got)
	}
}

func TestConfigSummary_Schema(t *testing.T) {
	cfg := &contract.ConfigurationSource{Name: "app", Schema: "config/app.json"}
	if got := configSummary(cfg); got != "app: config/app.json" {
		t.Errorf("expected 'app: config/app.json', got %q", got)
	}
}

func TestConfigSummary_NameOnly(t *testing.T) {
	cfg := &contract.ConfigurationSource{Name: "app"}
	if got := configSummary(cfg); got != "app" {
		t.Errorf("expected 'app', got %q", got)
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
	new.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policies Added change")
	}
}

func TestDiffPolicy_AddedWithRef(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Name: "org", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policies Added change")
	}
}

func TestDiffPolicy_Removed(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json"}}
	new := minimalContract()
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policies Removed change")
	}
}

func TestDiffPolicy_RemovedWithRef(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Name: "org", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policies Removed change")
	}
}

func TestDiffPolicy_SchemaChanged(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/old.json"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/new.json"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies.schema" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected policies.schema Modified change")
	}
}

func TestDiffPolicy_RefChanged(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Name: "org", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Name: "org", Ref: "oci://ghcr.io/acme/policy:2.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies.ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected policies.ref Modified change")
	}
}

func TestDiffPolicy_NoChanges(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffPolicy_RefAdded(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies.ref" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected policies.ref Added change")
	}
}

func TestDiffPolicy_RefRemoved(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.PolicySource{{Name: "org", Schema: "policy/schema.json"}}
	changes := diffPolicy(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "policies.ref" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected policies.ref Removed change")
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

func TestDiffPolicy_MultipleByName(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.PolicySource{
		{Name: "org", Schema: "policy/org.json"},
		{Name: "team", Schema: "policy/team.json"},
	}
	new := minimalContract()
	new.Policies = []contract.PolicySource{
		{Name: "org", Schema: "policy/org.json"},
		{Name: "security", Schema: "policy/security.json"},
	}
	changes := diffPolicy(old, new, nil, nil)
	foundRemoved := false
	foundAdded := false
	for _, c := range changes {
		if c.Path == "policies" && c.Type == Removed {
			foundRemoved = true
		}
		if c.Path == "policies" && c.Type == Added {
			foundAdded = true
		}
	}
	if !foundRemoved {
		t.Error("expected policies Removed change for 'team'")
	}
	if !foundAdded {
		t.Error("expected policies Added change for 'security'")
	}
}

func TestPolicySummary_WithRef(t *testing.T) {
	p := &contract.PolicySource{Name: "org", Ref: "oci://example.com/policy:1.0"}
	if got := policySummary(p); got != "org: oci://example.com/policy:1.0" {
		t.Errorf("expected 'org: oci://...', got %q", got)
	}
}

func TestPolicySummary_WithSchema(t *testing.T) {
	p := &contract.PolicySource{Name: "org", Schema: "policy/schema.json"}
	if got := policySummary(p); got != "org: policy/schema.json" {
		t.Errorf("expected 'org: policy/schema.json', got %q", got)
	}
}

func TestPolicySummary_NameOnly(t *testing.T) {
	p := &contract.PolicySource{Name: "org"}
	if got := policySummary(p); got != "org" {
		t.Errorf("expected 'org', got %q", got)
	}
}
