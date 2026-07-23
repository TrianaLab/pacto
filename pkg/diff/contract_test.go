package diff

import (
	"testing"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

func TestDiffContract_OwnerAdded(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = contract.Owner{}
	new := minimalContract()
	new.Service.Owner = contract.Owner{Team: "team/new"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner.team" && c.Type == Added && c.Classification == NonBreaking {
			found = true
		}
	}
	if !found {
		t.Error("expected service.owner.team Added change")
	}
}

func TestDiffContract_OwnerRemoved(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = contract.Owner{Team: "team/old"}
	new := minimalContract()
	new.Service.Owner = contract.Owner{}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner.team" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected service.owner.team Removed change")
	}
}

func TestDiffContract_OwnerContactAdded(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = contract.Owner{Team: "foundations-team"}
	new := minimalContract()
	new.Service.Owner = contract.Owner{
		Team:     "foundations-team",
		Contacts: []contract.OwnerContact{{Type: "slack", Value: "#foundations"}},
	}
	changes := diffContract(old, new)
	for _, c := range changes {
		if c.Path == "service.owner" {
			t.Errorf("expected no opaque service.owner change, got %+v", c)
		}
	}
	found := false
	for _, c := range changes {
		if c.Type == Added && c.Classification == NonBreaking &&
			c.Path == "service.owner.contacts[slack:#foundations]" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected service.owner.contacts[slack:#foundations] Added, got %+v", changes)
	}
}

func TestDiffContract_OwnerTeamModified(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = contract.Owner{Team: "team/old"}
	new := minimalContract()
	new.Service.Owner = contract.Owner{Team: "team/new"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner.team" && c.Type == Modified &&
			c.OldValue == "team/old" && c.NewValue == "team/new" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected service.owner.team Modified, got %+v", changes)
	}
}

func TestDiffContract_OwnerDRIAdded(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = contract.Owner{Team: "t"}
	new := minimalContract()
	new.Service.Owner = contract.Owner{Team: "t", DRI: "alice"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner.dri" && c.Type == Added && c.NewValue == "alice" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected service.owner.dri Added, got %+v", changes)
	}
}

func TestDiffContract_OwnerContactRemoved(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = contract.Owner{
		Team:     "t",
		Contacts: []contract.OwnerContact{{Type: "email", Value: "a@b.c"}},
	}
	new := minimalContract()
	new.Service.Owner = contract.Owner{Team: "t"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner.contacts[email:a@b.c]" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Errorf("expected service.owner.contacts[email:a@b.c] Removed, got %+v", changes)
	}
}

func TestDiffContract_OwnerContactPurposeModified(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = contract.Owner{
		Team:     "t",
		Contacts: []contract.OwnerContact{{Type: "slack", Value: "#c", Purpose: "alerts"}},
	}
	new := minimalContract()
	new.Service.Owner = contract.Owner{
		Team:     "t",
		Contacts: []contract.OwnerContact{{Type: "slack", Value: "#c", Purpose: "escalation"}},
	}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner.contacts[slack:#c]" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Errorf("expected service.owner.contacts[slack:#c] Modified, got %+v", changes)
	}
}

func TestDiffContract_PactoVersionModified(t *testing.T) {
	old := minimalContract()
	old.PactoVersion = "2.0"
	new := minimalContract()
	new.PactoVersion = "2.1"
	changes := diffContract(old, new)
	c, ok := findChange(changes, "pactoVersion", Modified)
	if !ok {
		t.Fatalf("expected pactoVersion Modified, got %+v", changes)
	}
	if c.Classification != NonBreaking {
		t.Errorf("expected NonBreaking, got %s", c.Classification)
	}
}

func TestDiffContract_WorkloadChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Workload = contract.WorkloadJob
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "workload" && c.Type == Modified && c.Classification == Breaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected workload Modified Breaking, got %+v", changes)
	}
}

func TestDiffContract_StateTypeChanged(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.State.Type = contract.StateStateful
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "state.type" && c.Type == Modified && c.Classification == Breaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected state.type Modified Breaking, got %+v", changes)
	}
}

func TestDiffContract_CapabilityAdded(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Capabilities = []contract.Capability{{Type: contract.CapabilityHealth}}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "capabilities" && c.Type == Added && c.Classification == NonBreaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected capabilities Added NonBreaking, got %+v", changes)
	}
}

func TestDiffContract_CapabilityRemoved(t *testing.T) {
	old := minimalContract()
	old.Capabilities = []contract.Capability{{Type: contract.CapabilityMetrics}}
	new := minimalContract()
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "capabilities" && c.Type == Removed && c.Classification == PotentialBreaking {
			found = true
		}
	}
	if !found {
		t.Errorf("expected capabilities Removed PotentialBreaking, got %+v", changes)
	}
}

func TestDiffContract_ExtensionCapabilityAdded(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Capabilities = []contract.Capability{{Type: contract.CapabilityExtension, Ref: "acme.com/tracing"}}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "capabilities" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Errorf("expected extension capability Added, got %+v", changes)
	}
}

func TestDiffStringSet_AddedAndRemoved(t *testing.T) {
	oldSet := map[string]bool{"a": true, "b": true}
	newSet := map[string]bool{"b": true, "c": true}
	changes := diffStringSet(oldSet, newSet, "test.paths", "item")
	var foundAdded, foundRemoved bool
	for _, c := range changes {
		if c.Type == Added && c.NewValue == "c" {
			foundAdded = true
		}
		if c.Type == Removed && c.OldValue == "a" {
			foundRemoved = true
		}
	}
	if !foundAdded {
		t.Error("expected item 'c' Added")
	}
	if !foundRemoved {
		t.Error("expected item 'a' Removed")
	}
}

func TestNewChange(t *testing.T) {
	c := newChange("service.name", Modified, "old", "new")
	if c.Path != "service.name" {
		t.Errorf("expected path service.name, got %s", c.Path)
	}
	if c.Type != Modified {
		t.Errorf("expected Modified, got %s", c.Type)
	}
	if c.Classification != Breaking {
		t.Errorf("expected Breaking, got %s", c.Classification)
	}
}

func findChange(changes []Change, path string, ct ChangeType) (Change, bool) {
	for _, c := range changes {
		if c.Path == path && c.Type == ct {
			return c, true
		}
	}
	return Change{}, false
}

func hasChange(changes []Change, path string, ct ChangeType) bool {
	_, ok := findChange(changes, path, ct)
	return ok
}

func TestDiffContract_StateBothNil(t *testing.T) {
	old := minimalContract()
	old.State = nil
	new := minimalContract()
	new.State = nil
	changes := diffContract(old, new)
	for _, c := range changes {
		if c.Path == "state.type" || c.Path == "state.persistence.scope" {
			t.Errorf("expected no state changes for both nil, got %+v", c)
		}
	}
}

func TestDiffContract_StateOldNil(t *testing.T) {
	old := minimalContract()
	old.State = nil
	new := minimalContract()
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "state.type" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Errorf("expected state.type Added when old state is nil, got %+v", changes)
	}
}

func TestDiffContract_StateNewNil(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.State = nil
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "state.type" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Errorf("expected state.type Removed when new state is nil, got %+v", changes)
	}
}
