package diff

import (
	"fmt"
	"maps"
	"slices"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// ptrChanged reports whether two optional values differ, treating "absent" as
// distinct from the zero value: an omitted minScore is not a minScore of 0.
func ptrChanged[T comparable](a, b *T) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	return *a != *b
}

// ptrVal renders an optional value for the change summary. Absent reads as the
// zero value here; the change type already carries the appear/disappear fact.
func ptrVal[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// ptrChangeType returns the change type for an optional value's transition. The
// caller must ensure ptrChanged(old, new) is true.
func ptrChangeType[T any](old, new *T) ChangeType {
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

	if ptrChanged(old.MinScore, new.MinScore) {
		changes = append(changes, newChange("readiness.minScore",
			ptrChangeType(old.MinScore, new.MinScore), ptrVal(old.MinScore), ptrVal(new.MinScore)))
	}
	if old.Expires != new.Expires {
		changes = append(changes, newChange("readiness.expires", strChangeType(old.Expires, new.Expires), old.Expires, new.Expires))
	}
	if ptrChanged(old.PartialCredit, new.PartialCredit) {
		changes = append(changes, newChange("readiness.partialCredit",
			ptrChangeType(old.PartialCredit, new.PartialCredit), ptrVal(old.PartialCredit), ptrVal(new.PartialCredit)))
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

	for _, id := range slices.Sorted(maps.Keys(oldByID)) {
		o := oldByID[id]
		n, exists := newByID[id]
		if !exists {
			changes = append(changes, newChange(claimPath(id), Removed, formatClaim(o), nil))
			continue
		}
		if o != n {
			changes = append(changes, newChange(claimPath(id), Modified, formatClaim(o), formatClaim(n)))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(newByID)) {
		if _, exists := oldByID[id]; !exists {
			changes = append(changes, newChange(claimPath(id), Added, nil, formatClaim(newByID[id])))
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
