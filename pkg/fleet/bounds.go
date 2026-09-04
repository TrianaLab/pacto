package fleet

import "sort"

// This file holds the hard bounds every product answer applies to snapshot-owned
// collections carried in the completeness envelope, so ProductMeta never becomes
// an unbounded copy of every source or limitation (requirement: bounded product
// responses). Per-answer paging bounds live with each answer (entities,
// attention, neighborhood).

// Meta-envelope caps. A realistic fleet is far below these; the caps exist so a
// pathological fleet can never hand a consumer an unbounded meta.
const (
	// MaxMetaSources caps the sources carried in a product answer's meta.
	MaxMetaSources = 50
	// MaxMetaLimitations caps the limitations carried in a product answer's meta.
	MaxMetaLimitations = 100
)

// MaxGraphNodes caps the nodes one graph traversal answer carries. The walk itself
// is linear, but every node carries the chain of keys taken to reach it, so the
// PAYLOAD grows as nodes x depth -- a deep chain is quadratic on the wire even
// though it is cheap to compute. The cap is the ceiling on the whole answer rather
// than a per-level limit, and it is deliberately generous: a realistic dependency
// closure is orders of magnitude under it.
const MaxGraphNodes = 500

// sourceHealthRank orders source statuses most-relevant (least healthy) first,
// so that when the meta's source list is truncated the sources that matter -
// unavailable, stale, partial - are the ones kept.
func sourceHealthRank(s SourceStatus) int {
	switch s {
	case SourceUnavailable:
		return 0
	case SourceStale:
		return 1
	case SourcePartial:
		return 2
	default: // available or unknown
		return 3
	}
}

// boundSources caps a (already-cloned) source slice at MaxMetaSources. Under the
// cap the slice is returned verbatim in its stable snapshot order; over the cap
// the unhealthy sources are kept first and truncation is reported.
func boundSources(ss []SourceState) ([]SourceState, bool) {
	if len(ss) <= MaxMetaSources {
		return ss, false
	}
	sorted := append([]SourceState(nil), ss...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sourceHealthRank(sorted[i].Status) < sourceHealthRank(sorted[j].Status)
	})
	return sorted[:MaxMetaSources], true
}

// BoundLimitations caps a (already-cloned) limitation slice at
// MaxMetaLimitations, preserving order, and reports truncation. Exported because
// the impact answer carries the same snapshot-owned slice and must apply the same
// cap; a second, differently-sized bound in another package is how two answers
// from one snapshot start disagreeing about how much they left out.
func BoundLimitations(ls []Limitation) ([]Limitation, bool) { return boundLimitations(ls) }

// boundLimitations caps a (already-cloned) limitation slice at
// MaxMetaLimitations, preserving order, and reports truncation.
func boundLimitations(ls []Limitation) ([]Limitation, bool) {
	if len(ls) <= MaxMetaLimitations {
		return ls, false
	}
	return ls[:MaxMetaLimitations], true
}
