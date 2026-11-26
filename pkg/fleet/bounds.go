package fleet

import (
	"slices"
	"sort"
	"time"
)

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
	// MaxMetaClusters caps the clusters carried in an answer's meta.
	MaxMetaClusters = 50
	// MaxSourceNamespaces caps the namespaces one source carries in a meta;
	// NamespacesTotal keeps the full count.
	MaxSourceNamespaces = 64
	// MaxProviders caps the detected-provider rows one answer carries.
	MaxProviders = 200
	// MaxProviderObjects caps the objects one provider row names;
	// ObjectsTotal keeps the count.
	MaxProviderObjects = 20
)

// MaxGraphNodes caps the nodes one graph traversal answer carries. The walk itself
// is linear, but every node carries the chain of keys taken to reach it, so the
// PAYLOAD grows as nodes x depth -- a deep chain is quadratic on the wire even
// though it is cheap to compute. The cap is the ceiling on the whole answer rather
// than a per-level limit, and it is deliberately generous: a realistic dependency
// closure is orders of magnitude under it.
const MaxGraphNodes = 500

// MaxExternalNodes caps the external systems one answer names (decision D4).
// Beyond it the least-consumed fold into one other-egress node and the answer
// is partial (LimitationExternalCap). It is per installation, with no chart
// value: raise it here if a real fleet hits it.
const MaxExternalNodes = 200

// MaxExternalSources caps the consumers, and their sources, one answer lists
// for one external system; ConsumersTotal and SourcesTotal keep the counts.
const MaxExternalSources = 50

// MaxExternalResources caps the managed resources one answer lists for one
// external system; ResourcesTotal keeps the count.
//
// ponytail: a fixed page of 20; a cursor over the rest if an external with
// more resources needs them listed.
const MaxExternalResources = 20

// MaxExternalChangeAge is how long a checked change of an external system's
// inferred contract raises EXTERNAL_CHANGED, measured from the newer
// revision's surface time to the snapshot's.
//
// ponytail: a fixed window; an acknowledgement record (a write with the
// actor's identity) if teams ask to dismiss the item.
const MaxExternalChangeAge = 7 * 24 * time.Hour

// MaxInferredServices caps the inferred services one source contributes.
// Past it the rest are not read and the source is partial
// (LimitationInferredServicesCapped). Raise it here if a real cluster hits it.
const MaxInferredServices = 5000

// MaxInferredTemplates caps the inferred golden paths a snapshot lists
// (those a declared golden path holds the name of are not counted). Past it
// the rest are left out with LimitationInferredTemplatesCapped.
const MaxInferredTemplates = 200

// MaxInferredImageRunes and MaxInferredPathRunes bound the image and probe
// path of an inferred service: anyone who may create a workload writes them
// and the apiserver bounds neither, so a longer value is dropped.
const (
	MaxInferredImageRunes = 512
	MaxInferredPathRunes  = 256
	// MaxInferredRepositories caps the repositories one inferred record keeps.
	MaxInferredRepositories = 5
	// MaxInferredGroupKeyRunes bounds a group key filter, above the longest
	// key a group can carry (cluster, rule, namespace, kind and name).
	MaxInferredGroupKeyRunes = 1024
)

// MaxSearchExternals caps the external systems a search lists.
const MaxSearchExternals = 20

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

// boundSources caps a (already-cloned) source slice at MaxMetaSources, and each
// source's namespaces at MaxSourceNamespaces. Under the
// cap the slice is returned verbatim in its stable snapshot order; over the cap
// the unhealthy sources are kept first and truncation is reported.
func boundSources(ss []SourceState) ([]SourceState, bool) {
	for i := range ss {
		if n := len(ss[i].Namespaces); n > MaxSourceNamespaces {
			ss[i].NamespacesTotal = max(ss[i].NamespacesTotal, n)
			ss[i].Namespaces = ss[i].Namespaces[:MaxSourceNamespaces]
		}
	}
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
//
// HIDDEN_BY_PERMISSIONS and the namespace scope limitations are about the
// whole answer, not one entity, so the cap never drops them: past the cap
// they take the last kept places, in order.
func BoundLimitations(ls []Limitation) ([]Limitation, bool) {
	if len(ls) <= MaxMetaLimitations {
		return ls, false
	}
	whole := min(MaxMetaLimitations, len(slices.DeleteFunc(slices.Clone(ls), func(l Limitation) bool { return !keptPastCap(l) })))
	rest := MaxMetaLimitations - whole
	kept := make([]Limitation, 0, MaxMetaLimitations)
	for _, l := range ls {
		switch {
		case keptPastCap(l) && whole > 0:
			whole--
		case !keptPastCap(l) && rest > 0:
			rest--
		default:
			continue
		}
		kept = append(kept, l)
	}
	return kept, true
}

// keptPastCap says whether l is about the whole answer and survives the cap.
func keptPastCap(l Limitation) bool {
	switch l.Code {
	case LimitationHiddenByPermissions, LimitationK8sNamespaceScoped, LimitationK8sNamespaceUnreadable, LimitationClustersUnread:
		return true
	}
	return false
}
