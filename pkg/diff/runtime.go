package diff

// diffRuntime is a no-op in v2 (runtime wrapper removed; workload/state are top-level).
// Retained for call-site compatibility; the Compare function still invokes it.
func diffRuntime(old, new any) []Change {
	return nil
}

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

// intPtrChangeType returns the change type for an optional integer pointer
// transition. The caller must ensure intPtrChanged(old, new) is true.
func intPtrChangeType(old, new *int) ChangeType {
	if old == nil {
		return Added
	}
	if new == nil {
		return Removed
	}
	return Modified
}

// strChangeType classifies a string field change as Added (was empty), Removed
// (now empty), or Modified — so nil↔value transitions aren't reported as Modified.
func strChangeType(old, new string) ChangeType {
	if old == "" {
		return Added
	}
	if new == "" {
		return Removed
	}
	return Modified
}
