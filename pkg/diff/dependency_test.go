package diff

import (
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestDiffDependencies_Added(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	changes := diffDependencies(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "dependencies" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected dependency Added change")
	}
}

func TestDiffDependencies_Removed(t *testing.T) {
	old := minimalContract()
	old.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	new := minimalContract()
	changes := diffDependencies(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "dependencies" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected dependency Removed change")
	}
}

func TestDiffDependencies_CompatibilityChanged(t *testing.T) {
	old := minimalContract()
	old.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	new := minimalContract()
	new.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^2.0.0"},
	}
	changes := diffDependencies(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "dependencies.compatibility" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected dependencies.compatibility Modified change")
	}
}

func TestDiffDependencies_RequiredChanged(t *testing.T) {
	old := minimalContract()
	old.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	new := minimalContract()
	new.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: false, Compatibility: "^1.0.0"},
	}
	changes := diffDependencies(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "dependencies.required" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected dependencies.required Modified change")
	}
}

func TestDiffDependencies_RefChanged(t *testing.T) {
	old := minimalContract()
	old.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	new := minimalContract()
	new.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:2.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	changes := diffDependencies(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "dependencies.ref" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected dependencies.ref Modified change")
	}
}

func TestDiffDependencies_NoChange(t *testing.T) {
	old := minimalContract()
	old.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	new := minimalContract()
	new.Dependencies = []contract.Dependency{
		{Name: "auth", Ref: "ghcr.io/acme/auth:1.0.0", Required: true, Compatibility: "^1.0.0"},
	}
	changes := diffDependencies(old, new)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d: %v", len(changes), changes)
	}
}
