package diff

import (
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func minimalContract() *contract.Contract {
	return &contract.Contract{
		PactoVersion: "2.0",
		Service: contract.Service{
			Name:    "my-svc",
			Version: "1.0.0",
			Owner:   contract.Owner{Team: "team/backend"},
		},
		Interfaces: []contract.Interface{
			{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "interfaces/openapi.yaml", Visibility: contract.VisibilityInternal},
		},
		Configurations: []contract.Configuration{
			{Name: "default", Schema: "configuration/schema.json"},
		},
		Workload: contract.WorkloadService,
		State: &contract.State{
			Type:            contract.StateStateless,
			Persistence:     contract.Persistence{Scope: contract.ScopeLocal, Durability: contract.DurabilityEphemeral},
			DataCriticality: contract.DataCriticalityLow,
		},
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
	new.State.Type = contract.StateStateful

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "state.type", Modified, Breaking)
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
	new.Interfaces = append(new.Interfaces, contract.Interface{
		Name: "grpc", Type: contract.InterfaceTypeGRPC, Ref: "interfaces/service.proto",
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
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
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
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}

	result := Compare(old, new, nil, nil)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "dependencies", Added, NonBreaking)
}

func TestCompare_CapabilityAdded_NonBreaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Capabilities = []contract.Capability{
		{Type: contract.CapabilityHealth},
	}

	result := Compare(old, new, nil, nil)

	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "capabilities", Added, NonBreaking)
}

func TestCompare_CapabilityRemoved_PotentialBreaking(t *testing.T) {
	old := minimalContract()
	old.Capabilities = []contract.Capability{
		{Type: contract.CapabilityMetrics},
	}
	new := minimalContract()

	result := Compare(old, new, nil, nil)

	if result.Classification != PotentialBreaking {
		t.Errorf("expected POTENTIAL_BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "capabilities", Removed, PotentialBreaking)
}

func TestCompare_PersistenceScopeChange_Breaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.State.Persistence.Scope = contract.ScopeShared

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "state.persistence.scope", Modified, Breaking)
}

func TestCompare_ConfigurationRemoved_Breaking(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Configurations = nil

	result := Compare(old, new, nil, nil)

	if result.Classification != Breaking {
		t.Errorf("expected BREAKING, got %s", result.Classification)
	}
	assertHasChange(t, result, "configurations", Removed, Breaking)
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

	assertHasChange(t, result, "schema.properties.debug", Added, PotentialBreaking)
}

func TestCompare_OverallClassification_MaxSeverity(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Service.Version = "2.0.0"           // NON_BREAKING
	new.State.Type = contract.StateStateful // BREAKING

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

func TestCompare_SBOMDiff_BothPresent(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	oldFS := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`{
			"spdxVersion": "SPDX-2.3",
			"packages": [{"name": "lib-a", "versionInfo": "1.0.0"}]
		}`)},
	}
	newFS := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`{
			"spdxVersion": "SPDX-2.3",
			"packages": [{"name": "lib-a", "versionInfo": "2.0.0"}]
		}`)},
	}

	result := Compare(old, new, oldFS, newFS)
	if result.SBOMDiff == nil {
		t.Fatal("expected SBOMDiff to be non-nil")
	}
	if len(result.SBOMDiff.Changes) != 1 {
		t.Fatalf("expected 1 SBOM change, got %d", len(result.SBOMDiff.Changes))
	}
	// SBOM changes should not affect classification
	if result.Classification != NonBreaking {
		t.Errorf("expected NON_BREAKING (SBOM changes are informational), got %s", result.Classification)
	}
}

func TestCompare_SBOMDiff_NeitherPresent(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	result := Compare(old, new, nil, nil)
	if result.SBOMDiff != nil {
		t.Error("expected nil SBOMDiff when no SBOMs present")
	}
}

func TestCompare_SBOMDiff_IdenticalSBOMs(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	sbomData := []byte(`{"spdxVersion":"SPDX-2.3","packages":[{"name":"lib-a","versionInfo":"1.0.0"}]}`)
	fs := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: sbomData},
	}

	result := Compare(old, new, fs, fs)
	if result.SBOMDiff != nil {
		t.Error("expected nil SBOMDiff when SBOMs are identical")
	}
}

func TestCompare_SBOMDiff_OnlyOldHasSBOM(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	oldFS := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`{
			"spdxVersion": "SPDX-2.3",
			"packages": [{"name": "lib-a", "versionInfo": "1.0.0"}]
		}`)},
	}

	result := Compare(old, new, oldFS, nil)
	if result.SBOMDiff == nil {
		t.Fatal("expected SBOMDiff when old has SBOM and new doesn't")
	}
	if len(result.SBOMDiff.Changes) != 1 {
		t.Errorf("expected 1 change (package removed), got %d", len(result.SBOMDiff.Changes))
	}
}

func TestCompare_SBOMDiff_OnlyNewHasSBOM(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	newFS := fstest.MapFS{
		"sbom/sbom.cdx.json": &fstest.MapFile{Data: []byte(`{
			"bomFormat": "CycloneDX",
			"components": [{"name": "lib-a", "version": "1.0.0"}]
		}`)},
	}

	result := Compare(old, new, nil, newFS)
	if result.SBOMDiff == nil {
		t.Fatal("expected SBOMDiff when new has SBOM and old doesn't")
	}
	if len(result.SBOMDiff.Changes) != 1 {
		t.Errorf("expected 1 change (package added), got %d", len(result.SBOMDiff.Changes))
	}
}

func TestCompare_SBOMDiff_InvalidOldSBOM(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	oldFS := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`{invalid json}`)},
	}
	newFS := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`{
			"spdxVersion": "SPDX-2.3",
			"packages": [{"name": "lib-a", "versionInfo": "1.0.0"}]
		}`)},
	}

	result := Compare(old, new, oldFS, newFS)
	if result.SBOMDiff != nil {
		t.Error("expected nil SBOMDiff when old SBOM is invalid")
	}
}

func TestCompare_SBOMDiff_InvalidNewSBOM(t *testing.T) {
	old := minimalContract()
	new := minimalContract()

	oldFS := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`{
			"spdxVersion": "SPDX-2.3",
			"packages": [{"name": "lib-a", "versionInfo": "1.0.0"}]
		}`)},
	}
	newFS := fstest.MapFS{
		"sbom/sbom.spdx.json": &fstest.MapFile{Data: []byte(`not valid json`)},
	}

	result := Compare(old, new, oldFS, newFS)
	if result.SBOMDiff != nil {
		t.Error("expected nil SBOMDiff when new SBOM is invalid")
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
