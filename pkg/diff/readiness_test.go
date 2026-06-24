package diff

import (
	"testing"

	"github.com/trianalab/pacto/v2/pkg/contract"
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

func TestDiffReadiness_CheckStatusModified(t *testing.T) {
	old := &contract.Readiness{
		Expires: "2026-12-31",
		Checks:  []contract.ReadinessCheck{{ID: "dashboard", Type: "url", Status: "done", Evidence: "http://x", Weight: 10}},
	}
	new := &contract.Readiness{
		Expires: "2026-12-31",
		Checks:  []contract.ReadinessCheck{{ID: "dashboard", Type: "url", Status: "not-done", Evidence: "http://x", Weight: 10}},
	}
	changes := diffReadiness(old, new)
	c, ok := findChange(changes, "readiness.checks[dashboard]", Modified)
	if !ok {
		t.Fatalf("expected readiness.checks[dashboard] Modified, got %+v", changes)
	}
	if c.Classification != NonBreaking {
		t.Errorf("expected NonBreaking, got %s", c.Classification)
	}
}

func TestDiffReadiness_CheckAdded(t *testing.T) {
	old := &contract.Readiness{Expires: "2026-12-31"}
	new := &contract.Readiness{
		Expires: "2026-12-31",
		Checks:  []contract.ReadinessCheck{{ID: "runbook", Type: "url", Status: "done", Evidence: "http://x", Weight: 5}},
	}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.checks[runbook]", Added) {
		t.Errorf("expected readiness.checks[runbook] Added, got %+v", changes)
	}
}

func TestDiffReadiness_CheckRemoved(t *testing.T) {
	old := &contract.Readiness{
		Expires: "2026-12-31",
		Checks:  []contract.ReadinessCheck{{ID: "runbook", Type: "url", Status: "done", Evidence: "http://x", Weight: 5}},
	}
	new := &contract.Readiness{Expires: "2026-12-31"}
	changes := diffReadiness(old, new)
	if !hasChange(changes, "readiness.checks[runbook]", Removed) {
		t.Errorf("expected readiness.checks[runbook] Removed, got %+v", changes)
	}
}

func TestDiffReadiness_NoChange(t *testing.T) {
	r := &contract.Readiness{
		Expires: "2026-12-31",
		Checks:  []contract.ReadinessCheck{{ID: "dashboard", Type: "url", Status: "done", Evidence: "http://x", Weight: 10}},
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
	result := Compare(old, new, nil, nil)
	if !hasChange(result.Changes, "readiness", Added) {
		t.Errorf("expected Compare to surface readiness Added, got %+v", result.Changes)
	}
}

func hasChange(changes []Change, path string, ct ChangeType) bool {
	_, ok := findChange(changes, path, ct)
	return ok
}

func findChange(changes []Change, path string, ct ChangeType) (Change, bool) {
	for _, c := range changes {
		if c.Path == path && c.Type == ct {
			return c, true
		}
	}
	return Change{}, false
}
