package diff

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
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

// TestDiffDependencies_External: a new free-text version is potentially
// breaking (nobody can check it); marking a dependency external is not.
func TestDiffDependencies_External(t *testing.T) {
	old, new := minimalContract(), minimalContract()
	old.Dependencies = []contract.Dependency{{Name: "model", External: true, Version: "v1"}, {Name: "db", Ref: "oci://r/db:1", Compatibility: "^1"}}
	new.Dependencies = []contract.Dependency{{Name: "model", External: true, Version: "v2"}, {Name: "db", Ref: "oci://r/db:1", Compatibility: "^1", External: true}}
	var got []string
	for _, c := range diffDependencies(old, new) {
		got = append(got, c.Path+" "+c.Type.String()+" "+c.Classification.String())
	}
	want := []string{"dependencies.external modified NON_BREAKING", "dependencies.version modified POTENTIAL_BREAKING"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestDiffDependencyNameFromRefMatchesDeclared(t *testing.T) {
	parse := func(doc string) *contract.Contract {
		t.Helper()
		c, err := contract.Parse(strings.NewReader(doc))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	old := parse("dependencies:\n  - name: ledger\n    ref: oci://ghcr.io/acme/ledger\n    required: true\n    compatibility: ^1.0.0\n")
	new := parse("dependencies:\n  - ref: oci://ghcr.io/acme/ledger\n    compatibility: ^1.0.0\n")
	if changes := diffDependencies(old, new); len(changes) != 0 {
		t.Errorf("a name derived from the ref equal to the declared one is no change, got %+v", changes)
	}
}

// compatibilityChange diffs one dependency's compatibility from old to new and
// returns the single change.
func compatibilityChange(t *testing.T, old, new string) Change {
	t.Helper()
	o, n := minimalContract(), minimalContract()
	o.Dependencies = []contract.Dependency{{Name: "auth", Ref: "ghcr.io/acme/auth", Required: true, Compatibility: old}}
	n.Dependencies = []contract.Dependency{{Name: "auth", Ref: "ghcr.io/acme/auth", Required: true, Compatibility: new}}
	changes := diffDependencies(o, n)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	return changes[0]
}

func TestDiffCompatibilityUnknownToRangeNonBreaking(t *testing.T) {
	c := compatibilityChange(t, contract.CompatibilityUnknown, "^1.0.0")
	if c.Path != "dependencies.compatibility.declared" || c.Classification != NonBreaking {
		t.Errorf("change = %+v, want dependencies.compatibility.declared NON_BREAKING", c)
	}
}

func TestDiffCompatibilityRangeToUnknownPotentialBreaking(t *testing.T) {
	c := compatibilityChange(t, "^1.0.0", contract.CompatibilityUnknown)
	if c.Path != "dependencies.compatibility.unknown" || c.Classification != PotentialBreaking {
		t.Errorf("change = %+v, want dependencies.compatibility.unknown POTENTIAL_BREAKING", c)
	}
}

func TestDiffCompatibilityRangeToRangeUnchanged(t *testing.T) {
	c := compatibilityChange(t, "^1.0.0", "^2.0.0")
	if c.Path != "dependencies.compatibility" || c.Classification != PotentialBreaking {
		t.Errorf("change = %+v, want dependencies.compatibility POTENTIAL_BREAKING", c)
	}
}
