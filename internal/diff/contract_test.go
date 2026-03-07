package diff

import (
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
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
	old.Service.Owner = ""
	new := minimalContract()
	new.Service.Owner = "team/new"
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Error("expected service.owner Added change")
	}
}

func TestDiffContract_OwnerRemoved(t *testing.T) {
	old := minimalContract()
	old.Service.Owner = "team/old"
	new := minimalContract()
	new.Service.Owner = ""
	changes := diffContract(old, new)
	found := false
	for _, c := range changes {
		if c.Path == "service.owner" && c.Type == Removed {
			found = true
		}
	}
	if !found {
		t.Error("expected service.owner Removed change")
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
