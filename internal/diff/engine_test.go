package diff

import (
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
)

func minimalContract() *contract.Contract {
	port := 8080
	return &contract.Contract{
		PactoVersion: "1.0",
		Service: contract.ServiceIdentity{
			Name:    "my-svc",
			Version: "1.0.0",
			Owner:   "team/backend",
		},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: &port, Visibility: "internal", Contract: "interfaces/openapi.yaml"},
		},
		Configuration: &contract.Configuration{Schema: "configuration/schema.json"},
		Runtime: &contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:            "stateless",
				Persistence:     contract.Persistence{Scope: "local", Durability: "ephemeral"},
				DataCriticality: "low",
			},
			Health: &contract.Health{Interface: "api", Path: "/health"},
		},
		Scaling: &contract.Scaling{Min: 1, Max: 3},
	}
}

func TestCompare_NoChanges(t *testing.T) {
	c := minimalContract()
	result := Compare(c, c, nil, nil)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(result.Changes))
	}
}

func TestCompare_ServiceNameChange_Breaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Service.Name = "renamed-svc"

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "service.name", Modified, Breaking)
}

func TestCompare_VersionChange_NonBreaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Service.Version = "2.0.0"

	result := Compare(old, new, nil, nil)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "service.version", Modified, NonBreaking)
}

func TestCompare_StateTypeChange_Breaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.State.Type = "stateful"

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "runtime.state.type", Modified, Breaking)
}

func TestCompare_InterfaceRemoved_Breaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Interfaces = nil

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "interfaces", Removed, Breaking)
}

func TestCompare_InterfaceAdded_NonBreaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	grpcPort := 9090
	new.Interfaces = append(new.Interfaces, contract.Interface{
		Name: "grpc", Type: "grpc", Port: &grpcPort,
	})

	result := Compare(old, new, nil, nil)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "interfaces", Added, NonBreaking)
}

func TestCompare_DependencyRemoved_Breaking(t *testing.T) {
	old := minimalContract()
	old.Dependencies = []contract.Dependency{
		{Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	new := minimalContract()

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "dependencies", Removed, Breaking)
}

func TestCompare_DependencyAdded_NonBreaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Dependencies = []contract.Dependency{
		{Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}

	result := Compare(old, new, nil, nil)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "dependencies", Added, NonBreaking)
}

func TestCompare_ScalingMaxChange_NonBreaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Scaling.Max = 10

	result := Compare(old, new, nil, nil)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "scaling.max", Modified, NonBreaking)
}

func TestCompare_ScalingMinChange_PotentialBreaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Scaling.Min = 3

	result := Compare(old, new, nil, nil)

	if result.Classification != PotentialBreaking {
		t.Errorf("expected POTENTIAL_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "scaling.min", Modified, PotentialBreaking)
}

func TestCompare_HealthPathChange_PotentialBreaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.Health.Path = "/healthz"

	result := Compare(old, new, nil, nil)

	if result.Classification != PotentialBreaking {
		t.Errorf("expected POTENTIAL_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "runtime.health.path", Modified, PotentialBreaking)
}

func TestCompare_PersistenceScopeChange_Breaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Runtime.State.Persistence.Scope = "shared"

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "runtime.state.persistence.scope", Modified, Breaking)
}

func TestCompare_ConfigurationRemoved_Breaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Configuration = nil

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "configuration", Removed, Breaking)
}

func TestCompare_OpenAPIPathRemoved_Breaking(t *testing.T) {
	oldFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
  /users:
    get:
      summary: List users
`)},
	}
	newFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
`)},
	}

	old := minimalContract()
	new := minimalContract()

	result := Compare(old, new, oldFS, newFS)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "openapi.paths[/users]", Removed, Breaking)
}

func TestCompare_SchemaPropertyAdded_NonBreaking(t *testing.T) {
	oldFS := fstest.MapFS{
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{}}`)},
	}
	newFS := fstest.MapFS{
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{"debug":{"type":"boolean"}}}`)},
	}

	old := minimalContract()
	new := minimalContract()

	result := Compare(old, new, oldFS, newFS)

	assertHasChange(t, result, "schema.properties[debug]", Added, NonBreaking)
}

func TestCompare_OverallClassification_MaxSeverity(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Service.Version = "2.0.0"       // NON_BREAKING
	new.Runtime.State.Type = "stateful" // BREAKING

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected overall BREAKING (max severity), got %s", result.Classification)
	}
}

func TestCompare_DocsDirectoryChangesIgnored(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	// Old bundle has docs/ with some content, new bundle has different docs/ content.
	// The diff engine should produce zero changes because docs/ is not part of the
	// contract schema — only explicitly referenced files (OpenAPI specs, JSON Schemas)
	// are compared.
	oldFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
`)},
		"docs":                 &fstest.MapFile{Mode: 0755 | 0040000},
		"docs/README.md":       &fstest.MapFile{Data: []byte("# Old README")},
		"docs/architecture.md": &fstest.MapFile{Data: []byte("# Old Architecture")},
	}
	newFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
`)},
		"docs":            &fstest.MapFile{Mode: 0755 | 0040000},
		"docs/README.md":  &fstest.MapFile{Data: []byte("# New README — completely rewritten")},
		"docs/runbook.md": &fstest.MapFile{Data: []byte("# Runbook — brand new file")},
	}

	result := Compare(old, new, oldFS, newFS)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes when only docs/ differs, got %d: %v", len(result.Changes), result.Changes)
	}
}

func TestCompare_DocsAddedToNewBundle(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	// Old bundle has no docs/, new bundle adds docs/. No changes expected.
	oldFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
`)},
	}
	newFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
`)},
		"docs":            &fstest.MapFile{Mode: 0755 | 0040000},
		"docs/README.md":  &fstest.MapFile{Data: []byte("# Service Docs")},
		"docs/runbook.md": &fstest.MapFile{Data: []byte("# Runbook")},
	}

	result := Compare(old, new, oldFS, newFS)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes when docs/ is added, got %d: %v", len(result.Changes), result.Changes)
	}
}

func TestCompare_DocsRemovedFromBundle(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	// Old bundle has docs/, new bundle removes it. No changes expected.
	oldFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
`)},
		"docs":           &fstest.MapFile{Mode: 0755 | 0040000},
		"docs/README.md": &fstest.MapFile{Data: []byte("# Service Docs")},
	}
	newFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: test
  version: 0.1.0
paths:
  /health:
    get:
      summary: Health
`)},
	}

	result := Compare(old, new, oldFS, newFS)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	if len(result.Changes) != 0 {
		t.Errorf("expected 0 changes when docs/ is removed, got %d: %v", len(result.Changes), result.Changes)
	}
}

// assertHasChange checks that a change with the given path, type, and classification exists.
func assertHasChange(t *testing.T, result *Result, path string, ct ChangeType, cls Classification) {
	t.Helper()
	for _, c := range result.Changes {
		if c.Path == path && c.Type == ct && c.Classification == cls {
			return
		}
	}
	t.Errorf("expected change {path=%s, type=%s, classification=%s} not found in %v", path, ct, cls, result.Changes)
}
