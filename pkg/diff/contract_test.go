package diff

import (
	"testing"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

func TestDiffScaling_BothNil(t *testing.T) {
	changes := diffScaling(nil, nil)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffScaling_OldNil(t *testing.T) {
	newS := &contract.Scaling{Min: 1, Max: 3}
	changes := diffScaling(nil, newS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Added {
		t.Errorf("expected Added, got %s", changes[0].Type)
	}
}

func TestDiffScaling_NewNil(t *testing.T) {
	oldS := &contract.Scaling{Min: 1, Max: 3}
	changes := diffScaling(oldS, nil)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Removed {
		t.Errorf("expected Removed, got %s", changes[0].Type)
	}
}

func TestDiffScaling_MinChanged(t *testing.T) {
	oldS := &contract.Scaling{Min: 1, Max: 3}
	newS := &contract.Scaling{Min: 2, Max: 3}
	changes := diffScaling(oldS, newS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Path != "scaling.min" {
		t.Errorf("expected path scaling.min, got %s", changes[0].Path)
	}
}

func TestDiffScaling_MaxChanged(t *testing.T) {
	oldS := &contract.Scaling{Min: 1, Max: 3}
	newS := &contract.Scaling{Min: 1, Max: 10}
	changes := diffScaling(oldS, newS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Path != "scaling.max" {
		t.Errorf("expected path scaling.max, got %s", changes[0].Path)
	}
}

func TestDiffScaling_ReplicasChanged(t *testing.T) {
	oldR, newR := 3, 5
	oldS := &contract.Scaling{Replicas: &oldR, Min: oldR, Max: oldR}
	newS := &contract.Scaling{Replicas: &newR, Min: newR, Max: newR}
	changes := diffScaling(oldS, newS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Path != "scaling.replicas" {
		t.Errorf("expected path scaling.replicas, got %s", changes[0].Path)
	}
}

func TestDiffScaling_ReplicasUnchanged(t *testing.T) {
	r := 3
	oldS := &contract.Scaling{Replicas: &r, Min: r, Max: r}
	newS := &contract.Scaling{Replicas: &r, Min: r, Max: r}
	changes := diffScaling(oldS, newS)
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffScaling_ReplicasToRange(t *testing.T) {
	r := 3
	oldS := &contract.Scaling{Replicas: &r, Min: r, Max: r}
	newS := &contract.Scaling{Min: 1, Max: 5}
	changes := diffScaling(oldS, newS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Path != "scaling" || changes[0].Type != Modified {
		t.Errorf("expected scaling Modified, got %s %s", changes[0].Path, changes[0].Type)
	}
}

func TestDiffScaling_RangeToReplicas(t *testing.T) {
	r := 3
	oldS := &contract.Scaling{Min: 1, Max: 5}
	newS := &contract.Scaling{Replicas: &r, Min: r, Max: r}
	changes := diffScaling(oldS, newS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Path != "scaling" || changes[0].Type != Modified {
		t.Errorf("expected scaling Modified, got %s %s", changes[0].Path, changes[0].Type)
	}
}

func TestDiffScaling_OldNilNewReplicas(t *testing.T) {
	r := 3
	newS := &contract.Scaling{Replicas: &r, Min: r, Max: r}
	changes := diffScaling(nil, newS)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != Added {
		t.Errorf("expected Added, got %s", changes[0].Type)
	}
	if changes[0].NewValue != "replicas=3" {
		t.Errorf("expected 'replicas=3', got %v", changes[0].NewValue)
	}
}

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

// Reproduces the reported bug: team unchanged but a contact added must surface
// as a granular contact change, not an opaque "team -> team" modification.
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

func TestDiffContract_ImageAdded(t *testing.T) {
	old := minimalContract()
	old.Service.Image = nil
	new := minimalContract()
	new.Service.Image = &contract.Image{Ref: "ghcr.io/acme/svc:1.0.0"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.image" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected service.image Added change")
	}
}

func TestDiffContract_ImageRemoved(t *testing.T) {
	old := minimalContract()
	old.Service.Image = &contract.Image{Ref: "ghcr.io/acme/svc:1.0.0"}
	new := minimalContract()
	new.Service.Image = nil
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.image" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected service.image Removed change")
	}
}

func TestDiffContract_ImageModified(t *testing.T) {
	old := minimalContract()
	old.Service.Image = &contract.Image{Ref: "ghcr.io/acme/svc:1.0.0"}
	new := minimalContract()
	new.Service.Image = &contract.Image{Ref: "ghcr.io/acme/svc:2.0.0"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.image" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected service.image Modified change")
	}
}

func TestDiffContract_ImagePrivateToggled(t *testing.T) {
	old := minimalContract()
	old.Service.Image = &contract.Image{Ref: "ghcr.io/acme/svc:1.0.0", Private: false}
	new := minimalContract()
	new.Service.Image = &contract.Image{Ref: "ghcr.io/acme/svc:1.0.0", Private: true}
	changes := diffContract(old, new)
	if !hasChange(changes, "service.image", Modified) {
		t.Errorf("expected service.image Modified for private toggle, got %+v", changes)
	}
}

func TestDiffContract_PactoVersionModified(t *testing.T) {
	old := minimalContract()
	old.PactoVersion = "1.1"
	new := minimalContract()
	new.PactoVersion = "1.2"
	changes := diffContract(old, new)
	c, ok := findChange(changes, "pactoVersion", Modified)
	if !ok {
		t.Fatalf("expected pactoVersion Modified, got %+v", changes)
	}
	if c.Classification != NonBreaking {
		t.Errorf("expected NonBreaking, got %s", c.Classification)
	}
}

func TestFormatImage_Nil(t *testing.T) {
	if got := formatImage(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFormatImage_NonNil(t *testing.T) {
	img := &contract.Image{Ref: "ghcr.io/acme/svc:1.0.0"}
	if got := formatImage(img); got != "ghcr.io/acme/svc:1.0.0" {
		t.Errorf("expected ghcr.io/acme/svc:1.0.0, got %q", got)
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

func TestDiffContract_ChartAdded(t *testing.T) {
	old := minimalContract()
	old.Service.Chart = nil
	new := minimalContract()
	new.Service.Chart = &contract.Chart{Ref: "oci://ghcr.io/acme/chart", Version: "1.0.0"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.chart" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected service.chart Added change")
	}
}

func TestDiffContract_ChartRemoved(t *testing.T) {
	old := minimalContract()
	old.Service.Chart = &contract.Chart{Ref: "oci://ghcr.io/acme/chart", Version: "1.0.0"}
	new := minimalContract()
	new.Service.Chart = nil
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.chart" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected service.chart Removed change")
	}
}

func TestDiffContract_ChartModified(t *testing.T) {
	old := minimalContract()
	old.Service.Chart = &contract.Chart{Ref: "oci://ghcr.io/acme/chart", Version: "1.0.0"}
	new := minimalContract()
	new.Service.Chart = &contract.Chart{Ref: "oci://ghcr.io/acme/chart", Version: "2.0.0"}
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.chart" && c.Type == Modified {
			found = true
		}
	}
	if !found {
		t.Error("expected service.chart Modified change")
	}
}

func TestFormatChart_Nil(t *testing.T) {
	if got := formatChart(nil); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestFormatChart_NonNil(t *testing.T) {
	ch := &contract.Chart{Ref: "oci://ghcr.io/acme/chart", Version: "1.0.0"}
	expected := "oci://ghcr.io/acme/chart:1.0.0"
	if got := formatChart(ch); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
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
