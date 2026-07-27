package diff

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

func TestDiffReadiness_BothNil(t *testing.T) {
	if changes := diffReadiness(nil, nil); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiffReadiness_Added(t *testing.T) {
	new := &contract.Readiness{Expires: "2026-12-31"}
	changes := diffReadiness(nil, new)
	if len(changes) != 1 || changes[0].Path != "readiness" || changes[0].Type != Added {
		t.Fatalf("expected single readiness Added, got %+v", changes)
	}
	if changes[0].Classification != NonBreaking {
		t.Errorf("expected NonBreaking, got %s", changes[0].Classification)
	}
}

func TestDiffReadiness_Removed(t *testing.T) {
	old := &contract.Readiness{Expires: "2026-12-31"}
	changes := diffReadiness(old, nil)
	if len(changes) != 1 || changes[0].Path != "readiness" || changes[0].Type != Removed {
		t.Fatalf("expected single readiness Removed, got %+v", changes)
	}
}

func TestDiffReadiness_MinScoreModified(t *testing.T) {
	o, n := 80, 90
	old := &contract.Readiness{MinScore: &o, Expires: "2026-12-31"}
	new := &contract.Readiness{MinScore: &n, Expires: "2026-12-31"}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.minScore", Modified) {
		t.Errorf("expected readiness.minScore Modified, got %+v", changes)
	}
}

func TestDiffReadiness_ExpiresModified(t *testing.T) {
	old := &contract.Readiness{Expires: "2026-01-01"}
	new := &contract.Readiness{Expires: "2026-12-31"}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.expires", Modified) {
		t.Errorf("expected readiness.expires Modified, got %+v", changes)
	}
}

func TestDiffReadiness_PartialCreditAdded(t *testing.T) {
	pc := 0.5
	old := &contract.Readiness{Expires: "2026-12-31"}
	new := &contract.Readiness{Expires: "2026-12-31", PartialCredit: &pc}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.partialCredit", Added) {
		t.Errorf("expected readiness.partialCredit Added, got %+v", changes)
	}
}

func TestDiffReadiness_PartialCreditRemoved(t *testing.T) {
	pc := 0.5
	old := &contract.Readiness{Expires: "2026-12-31", PartialCredit: &pc}
	new := &contract.Readiness{Expires: "2026-12-31"}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.partialCredit", Removed) {
		t.Errorf("expected readiness.partialCredit Removed, got %+v", changes)
	}
}

func TestDiffReadiness_PartialCreditModified(t *testing.T) {
	o, n := 0.5, 0.75
	old := &contract.Readiness{Expires: "2026-12-31", PartialCredit: &o}
	new := &contract.Readiness{Expires: "2026-12-31", PartialCredit: &n}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.partialCredit", Modified) {
		t.Errorf("expected readiness.partialCredit Modified, got %+v", changes)
	}
}

func TestDiffReadiness_ClaimStatusModified(t *testing.T) {
	old := &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "dashboard", Type: "url", Status: "done", Evidence: "http://x", Weight: 10}},
	}
	new := &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "dashboard", Type: "url", Status: "not-done", Evidence: "http://x", Weight: 10}},
	}
	changes := diffReadiness(old, new)
	c, ok := findChange(changes, "readiness.claims[dashboard]", Modified)
	if !ok {
		t.Fatalf("expected readiness.claims[dashboard] Modified, got %+v", changes)
	}
	if c.Classification != NonBreaking {
		t.Errorf("expected NonBreaking, got %s", c.Classification)
	}
}

func TestDiffReadiness_ClaimAdded(t *testing.T) {
	old := &contract.Readiness{Expires: "2026-12-31"}
	new := &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "runbook", Type: "url", Status: "done", Evidence: "http://x", Weight: 5}},
	}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.claims[runbook]", Added) {
		t.Errorf("expected readiness.claims[runbook] Added, got %+v", changes)
	}
}

func TestDiffReadiness_ClaimRemoved(t *testing.T) {
	old := &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "runbook", Type: "url", Status: "done", Evidence: "http://x", Weight: 5}},
	}
	new := &contract.Readiness{Expires: "2026-12-31"}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.claims[runbook]", Removed) {
		t.Errorf("expected readiness.claims[runbook] Removed, got %+v", changes)
	}
}

func TestDiffReadiness_NoChange(t *testing.T) {
	r := &contract.Readiness{
		Expires: "2026-12-31",
		Claims:  []contract.ReadinessClaim{{ID: "dashboard", Type: "url", Status: "done", Evidence: "http://x", Weight: 10}},
	}
	if changes := diffReadiness(r, r); len(changes) != 0 {
		t.Errorf("expected 0 changes, got %+v", changes)
	}
}

// Compare must include readiness changes end-to-end.
func TestCompare_IncludesReadiness(t *testing.T) {
	old := minimalContract()
	new := minimalContract()
	new.Readiness = &contract.Readiness{Expires: "2026-12-31"}
	result := Compare(context.Background(), old, new, nil, nil)
	found := false
	for _, c := range result.Changes {
		if c.Path == "readiness" && c.Type == Added {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Compare to surface readiness Added, got %+v", result.Changes)
	}
}

func TestIntPtrChanged_BothNil(t *testing.T) {
	if intPtrChanged(nil, nil) {
		t.Error("expected false for both nil")
	}
}

func TestIntPtrChanged_OneNil(t *testing.T) {
	v := 10
	if !intPtrChanged(nil, &v) {
		t.Error("expected true when old is nil")
	}
	if !intPtrChanged(&v, nil) {
		t.Error("expected true when new is nil")
	}
}

func TestIntPtrChanged_BothNonNil(t *testing.T) {
	a, b := 10, 20
	if !intPtrChanged(&a, &b) {
		t.Error("expected true for different values")
	}
	c := 10
	if intPtrChanged(&a, &c) {
		t.Error("expected false for same values")
	}
}

func TestIntPtrChangeType_Added(t *testing.T) {
	v := 10
	if intPtrChangeType(nil, &v) != Added {
		t.Error("expected Added")
	}
}

func TestIntPtrChangeType_Removed(t *testing.T) {
	v := 10
	if intPtrChangeType(&v, nil) != Removed {
		t.Error("expected Removed")
	}
}

func TestIntPtrChangeType_Modified(t *testing.T) {
	a, b := 10, 20
	if intPtrChangeType(&a, &b) != Modified {
		t.Error("expected Modified")
	}
}

func TestIntPtrVal_Nil(t *testing.T) {
	if intPtrVal(nil) != 0 {
		t.Error("expected 0 for nil")
	}
}

func TestIntPtrVal_NonNil(t *testing.T) {
	v := 42
	if intPtrVal(&v) != 42 {
		t.Error("expected 42")
	}
}
