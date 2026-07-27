package diff

import (
	"fmt"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// intPtrChanged returns true if two int pointers differ.
func intPtrChanged(a, b *int) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	return *a != *b
}

func intPtrVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func intPtrChangeType(old, new *int) ChangeType {
	if old == nil {
		return Added
	}
	if new == nil {
		return Removed
	}
	return Modified
}

// diffReadiness compares the optional readiness assessment. Readiness records
// operational maturity, not consumer-facing contract surface, so every change is
// NonBreaking — but changes (a check regressing, the assessment expiring, the
// gate moving) are still surfaced rather than silently dropped. The revision
// history is intentionally not diffed: it is an append-only changelog that
// changes on every release and would only add noise.
func diffReadiness(old, new *contract.Readiness) []Change {
	if old == nil && new == nil {
		return nil
	}
	if old == nil {
		return []Change{newChange("readiness", Added, nil, "readiness")}
	}
	if new == nil {
		return []Change{newChange("readiness", Removed, "readiness", nil)}
	}

	var changes []Change

	if intPtrChanged(old.MinScore, new.MinScore) {
		changes = append(changes, newChange("readiness.minScore",
			intPtrChangeType(old.MinScore, new.MinScore), intPtrVal(old.MinScore), intPtrVal(new.MinScore)))
	}
	if old.Expires != new.Expires {
		changes = append(changes, newChange("readiness.expires", strChangeType(old.Expires, new.Expires), old.Expires, new.Expires))
	}
	if floatPtrChanged(old.PartialCredit, new.PartialCredit) {
		changes = append(changes, newChange("readiness.partialCredit",
			floatPtrChangeType(old.PartialCredit, new.PartialCredit), floatPtrVal(old.PartialCredit), floatPtrVal(new.PartialCredit)))
	}

	changes = append(changes, diffReadinessClaims(old.Claims, new.Claims)...)

	return changes
}

// diffReadinessClaims compares readiness claims keyed by ID (the organizational
// requirement). A claim is a comparable struct, so any field difference makes it
// Modified, with a formatted summary showing the change.
func diffReadinessClaims(old, new []contract.ReadinessClaim) []Change {
	var changes []Change
	oldByID := indexClaims(old)
	newByID := indexClaims(new)

	for id, o := range oldByID {
		n, exists := newByID[id]
		if !exists {
			changes = append(changes, newChange(claimPath(id), Removed, formatClaim(o), nil))
			continue
		}
		if o != n {
			changes = append(changes, newChange(claimPath(id), Modified, formatClaim(o), formatClaim(n)))
		}
	}
	for id, n := range newByID {
		if _, exists := oldByID[id]; !exists {
			changes = append(changes, newChange(claimPath(id), Added, nil, formatClaim(n)))
		}
	}
	return changes
}

func indexClaims(claims []contract.ReadinessClaim) map[string]contract.ReadinessClaim {
	m := make(map[string]contract.ReadinessClaim, len(claims))
	for _, c := range claims {
		m[c.ID] = c
	}
	return m
}

func claimPath(id string) string {
	return "readiness.claims[" + id + "]"
}

func formatClaim(c contract.ReadinessClaim) string {
	return fmt.Sprintf("status=%s weight=%d evidence=%s", c.Status, c.Weight, c.Evidence)
}

func floatPtrChanged(a, b *float64) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	return *a != *b
}

func floatPtrVal(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// floatPtrChangeType returns the change type for an optional float pointer
// transition. The caller must ensure floatPtrChanged(old, new) is true.
func floatPtrChangeType(old, new *float64) ChangeType {
	if old == nil {
		return Added
	}
	if new == nil {
		return Removed
	}
	return Modified
}
