package diff

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

func TestDiffInterfaces_TypeChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Interfaces[0].Type = contract.InterfaceTypeGRPC
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

func TestDiffInterfaces_VisibilityChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Interfaces[0].Visibility = contract.VisibilityPublic
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

func TestDiffInterfaces_RefChanged(t *testing.T) {
	old := minimalContract()
	old.Interfaces[0].Ref = "interfaces/old.yaml"
	new := minimalContract()
	new.Interfaces[0].Ref = "interfaces/new.yaml"
	changes := diffInterfaces(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "interfaces.ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected interfaces.ref Modified change")
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
	new.Configurations = []contract.Configuration{
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
	old.Configurations = []contract.Configuration{
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

func TestDiffConfiguration_ValuesChanged(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json", Values: map[string]any{"replicas": 1, "tier": "free"}},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json", Values: map[string]any{"replicas": 3, "tier": "free"}},
	}
	changes := diffConfiguration(old, new, nil, nil)
	found := false
	for _, c := range changes {
		if c.Path == "configurations[app].values.replicas" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Errorf("expected configurations[app].values.replicas Modified, got %+v", changes)
	}
}

func TestDiffConfiguration_ValuesUnchanged(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json", Values: map[string]any{"tier": "free"}},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json", Values: map[string]any{"tier": "free"}},
	}
	if changes := diffConfiguration(old, new, nil, nil); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %+v", changes)
	}
}

func TestDiffConfiguration_SchemaChanged(t *testing.T) {
	old := minimalContract()
	old.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/old.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
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
	old.Configurations = []contract.Configuration{
		{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0.0"},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
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
	old.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
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
	old.Configurations = []contract.Configuration{
		{Name: "app", Ref: "oci://ghcr.io/acme/config:1.0.0"},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
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
	old.Configurations = []contract.Configuration{
		{Name: "app", Schema: ""},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
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
	old.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json"},
		{Name: "db", Schema: "config/db.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
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
	old.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json"},
	}
	new := minimalContract()
	new.Configurations = []contract.Configuration{
		{Name: "app", Schema: "config/app.json"},
	}
	changes := diffConfiguration(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d: %v", len(changes), changes)
	}
}

func TestRefSourceSummary(t *testing.T) {
	tests := []struct {
		name string
		in   refSource
		want string
	}{
		{"ref takes precedence", refSource{name: "app", ref: "oci://example.com/config:1.0", schema: "x.json"}, "app: oci://example.com/config:1.0"},
		{"schema when no ref", refSource{name: "app", schema: "config/app.json"}, "app: config/app.json"},
		{"name only", refSource{name: "app"}, "app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refSourceSummary(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
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
	new.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json"}}
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
	new.Policies = []contract.Policy{{Name: "org", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
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
	old.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json"}}
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
	old.Policies = []contract.Policy{{Name: "org", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
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
	old.Policies = []contract.Policy{{Name: "org", Schema: "policy/old.json"}}
	new := minimalContract()
	new.Policies = []contract.Policy{{Name: "org", Schema: "policy/new.json"}}
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
	old.Policies = []contract.Policy{{Name: "org", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.Policy{{Name: "org", Ref: "oci://ghcr.io/acme/policy:2.0.0"}}
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
	old.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	changes := diffPolicy(old, new, nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffPolicy_RefAdded(t *testing.T) {
	old := minimalContract()
	old.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json"}}
	new := minimalContract()
	new.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
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
	old.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json", Ref: "oci://ghcr.io/acme/policy:1.0.0"}}
	new := minimalContract()
	new.Policies = []contract.Policy{{Name: "org", Schema: "policy/schema.json"}}
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
	oldFS := fstest.MapFS{}
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
	newFS := fstest.MapFS{}
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
	old.Policies = []contract.Policy{
		{Name: "org", Schema: "policy/org.json"},
		{Name: "team", Schema: "policy/team.json"},
	}
	new := minimalContract()
	new.Policies = []contract.Policy{
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

func TestDiffInterfaces_RefChangedWithBreakingSpecChange(t *testing.T) {
	old := minimalContract()
	old.Interfaces[0].Ref = "interfaces/openapi-v1.yaml"
	new := minimalContract()
	new.Interfaces[0].Ref = "interfaces/openapi-v2.yaml"

	oldFS := fstest.MapFS{
		"interfaces/openapi-v1.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /users:
    get:
      summary: List users
`)},
	}
	newFS := fstest.MapFS{
		"interfaces/openapi-v2.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.2.0
paths:
  /health:
    get:
      summary: Health
`)},
	}

	changes := diffInterfaces(old, new, oldFS, newFS)
	foundRefChange := false
	foundPathRemoved := false
	for _, c := range changes {
		if c.Path == "interfaces.ref" && c.Type == Modified {
			foundRefChange = true
		}
		if c.Path == "openapi.paths[/users]" && c.Type == Removed {
			foundPathRemoved = true
		}
	}
	if !foundRefChange {
		t.Errorf("expected interfaces.ref Modified, got %+v", changes)
	}
	if !foundPathRemoved {
		t.Errorf("expected openapi.paths[/users] Removed (spec-level breaking change), got %+v", changes)
	}
}
