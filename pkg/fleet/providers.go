package fleet

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// LimitationProviderSourceDegraded: a detected-provider row is not available
// and says why, or took the lower status of the source that reported it.
const LimitationProviderSourceDegraded = "PROVIDER_SOURCE_DEGRADED"

// A Provider is a system Pacto detected and read evidence from, never an
// offering provider. A source reports its rows in [Collection.Providers]; ids
// are per source, so every source may report a pacto-crd row.
//
// Extenders keep the shape: UsedFor is a free string list, never an enum;
// Evidence never names a namespace or a namespaced object (those go in
// Objects, which a restricted view narrows); evidence counted in namespaces
// goes in Counts and pkg/fleet words it through [WorkloadEvidence].
type Provider struct {
	ID           string           `json:"id" doc:"Stable id of the row within its source, for example pacto-crd or kubernetes-workloads"`
	Source       string           `json:"source" doc:"The id of the source that detected it"`
	Cluster      string           `json:"cluster,omitempty" doc:"The display name of the cluster the source read, when it reads one"`
	Name         string           `json:"name"`
	Evidence     string           `json:"evidence" doc:"What Pacto found. It never names a namespace or a namespaced object; those go in objects"`
	UsedFor      []string         `json:"usedFor" doc:"What Pacto uses it for, for example inferred-services or declared-targets"`
	Status       SourceStatus     `json:"status" enum:"available,partial,stale,unavailable"`
	Objects      []ProviderObject `json:"objects,omitempty" doc:"The objects the evidence names, at most 20"`
	ObjectsTotal int              `json:"objectsTotal,omitempty" doc:"How many objects the row had before the cap or your permissions narrowed it"`
	Limitations  []Limitation     `json:"limitations,omitempty"`
	// Counts and UnreadableKinds are never serialised: Build renders Evidence
	// from them and Restrict renders it again from the visible namespaces. A
	// producer that read and found nothing passes a non-nil empty Counts.
	Counts          []ProviderCount `json:"-"`
	UnreadableKinds []string        `json:"-"`
}

// ProviderObject is one object a provider row's evidence names. Namespace is
// empty for a cluster-scoped object, which a restricted view hides.
type ProviderObject struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// ProviderCount is how many objects of one kind a source read in one namespace.
type ProviderCount struct {
	Kind      string // singular Kubernetes kind, for example Deployment
	Namespace string
	N         int
}

// ingestProviders appends a copy of each row col reports, with its source and
// cluster set. The copy matters: a MemorySource hands back the same Collection
// on every Collect, so Build never writes one.
func ingestProviders(snap *FleetSnapshot, sourceID string, col *Collection) {
	cluster := ""
	if col.Cluster != nil {
		cluster = cmp.Or(col.Cluster.Name, col.Cluster.ID)
	}
	for _, p := range col.Providers {
		p.UsedFor, p.Objects, p.Limitations = slices.Clone(p.UsedFor), slices.Clone(p.Objects), slices.Clone(p.Limitations)
		p.Counts, p.UnreadableKinds = slices.Clone(p.Counts), slices.Clone(p.UnreadableKinds)
		p.Source, p.Cluster = sourceID, cluster
		snap.Providers = append(snap.Providers, p)
	}
}

// finalizeProviders runs once every source state is final. It drops the rows
// of an unavailable source, never lets a row claim better health than its
// source, gives every row that is not available a reason, renders counted
// evidence, sorts by source then id, keeps the first of a duplicate and caps
// the objects of each row.
func finalizeProviders(snap *FleetSnapshot) {
	state := map[string]SourceStatus{}
	for _, st := range snap.Sources {
		if cur, ok := state[st.ID]; !ok || sourceHealthRank(st.Status) < sourceHealthRank(cur) {
			state[st.ID] = st.Status
		}
	}
	rows := slices.DeleteFunc(snap.Providers, func(p Provider) bool { return state[p.Source] == SourceUnavailable })
	slices.SortStableFunc(rows, func(a, b Provider) int { return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.ID, b.ID)) })
	rows = slices.CompactFunc(rows, func(a, b Provider) bool { return a.Source == b.Source && a.ID == b.ID })
	for i := range rows {
		p := &rows[i]
		src := state[p.Source]
		status, _ := canonicalSourceStatus(cmp.Or(p.Status, src))
		switch {
		case sourceHealthRank(src) < sourceHealthRank(status):
			status = src
			p.Limitations = append(p.Limitations, Limitation{Code: LimitationProviderSourceDegraded, Source: p.Source,
				Message: "source " + p.Source + " is " + string(src) + "; the counts cover only what it read"})
		case status != SourceAvailable && len(p.Limitations) == 0:
			p.Limitations = append(p.Limitations, Limitation{Code: LimitationProviderSourceDegraded, Source: p.Source,
				Message: "source " + p.Source + " reported this row as " + string(status) + " and gave no reason"})
		}
		p.Status = status
		if p.Counts != nil || p.UnreadableKinds != nil {
			p.Evidence = countsEvidence(p.Counts, p.UnreadableKinds)
		}
		slices.SortFunc(p.Objects, func(a, b ProviderObject) int {
			return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
		})
		if len(p.Objects) > MaxProviderObjects {
			p.ObjectsTotal = max(p.ObjectsTotal, len(p.Objects))
			p.Objects = p.Objects[:MaxProviderObjects]
		}
	}
	snap.Providers = rows
}

// countsEvidence is WorkloadEvidence over per-namespace counts.
func countsEvidence(counts []ProviderCount, unreadable []string) string {
	byKind := map[string]int{}
	for _, c := range counts {
		byKind[c.Kind] += c.N
	}
	return WorkloadEvidence(byKind, unreadable)
}

// workloadKindOrder is the order WorkloadEvidence names the common kinds in;
// any other kind follows, sorted.
var workloadKindOrder = []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob"}

// WorkloadEvidence is the one wording of counted evidence: "412 Deployments,
// 3 StatefulSets". A kind read with 0 is omitted, a kind that could not be
// read is "<Kind>s unreadable" (never 0), and "no workloads found" means every
// kind was read and held nothing.
func WorkloadEvidence(counts map[string]int, unreadable []string) string {
	kinds := map[string]bool{}
	for k := range counts {
		kinds[k] = true
	}
	for _, k := range unreadable {
		kinds[k] = true
	}
	rank := func(k string) int {
		if i := slices.Index(workloadKindOrder, k); i >= 0 {
			return i
		}
		return len(workloadKindOrder)
	}
	var parts []string
	for _, k := range slices.SortedFunc(maps.Keys(kinds), func(a, b string) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b)) }) {
		switch n := counts[k]; {
		case slices.Contains(unreadable, k):
			parts = append(parts, k+"s unreadable")
		case n == 1:
			parts = append(parts, "1 "+k)
		case n > 1:
			parts = append(parts, strconv.Itoa(n)+" "+k+"s")
		}
	}
	if len(parts) == 0 {
		return "no workloads found"
	}
	return strings.Join(parts, ", ")
}

// restrictProviders is rows as a reader visible says yes to sees them. Every
// row stays; what changes is cloned. An object in a namespace the reader may
// not read goes, a cluster-scoped one included unless visible("") says yes,
// and ObjectsTotal keeps the count. Counts keep the visible namespaces and
// Evidence is rendered again with one hidden total. A limitation restrictScope
// would drop, or one that names a hidden name, goes; the degraded limitation
// names no namespace and stays. Evidence that still names a hidden name is
// masked.
func restrictProviders(rows []Provider, hiddenNames []string, visible func(string) bool) []Provider {
	if rows == nil {
		return nil
	}
	mentionsHidden := func(s string) bool {
		return slices.ContainsFunc(hiddenNames, func(n string) bool { return n != "" && strings.Contains(s, n) })
	}
	out := make([]Provider, len(rows))
	for i, p := range rows {
		p.Objects = slices.DeleteFunc(slices.Clone(p.Objects), func(o ProviderObject) bool { return !visible(o.Namespace) })
		if len(p.Objects) < len(rows[i].Objects) {
			p.ObjectsTotal = cmp.Or(p.ObjectsTotal, len(rows[i].Objects))
		}
		hidden := 0
		p.Counts = slices.DeleteFunc(slices.Clone(p.Counts), func(c ProviderCount) bool {
			if visible(c.Namespace) {
				return false
			}
			hidden += c.N
			return true
		})
		if p.Counts != nil || p.UnreadableKinds != nil {
			p.Evidence = countsEvidence(p.Counts, p.UnreadableKinds)
			if hidden > 0 {
				p.Evidence += " (+" + strconv.Itoa(hidden) + " in namespaces hidden from you)"
			}
		}
		p.Limitations = slices.DeleteFunc(slices.Clone(p.Limitations), func(l Limitation) bool {
			return l.Code != LimitationProviderSourceDegraded && (hiddenByScope(l, visible) || mentionsHidden(l.Message))
		})
		if mentionsHidden(p.Evidence) {
			p.Evidence = "hidden by your permissions"
		}
		out[i] = p
	}
	return out
}

// boundProviders is a copy of rows, least healthy first then by source and
// id, capped at MaxProviders.
func boundProviders(rows []Provider) ([]Provider, bool) {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b Provider) int { return cmp.Compare(sourceHealthRank(a.Status), sourceHealthRank(b.Status)) })
	if len(out) <= MaxProviders {
		return out, false
	}
	return out[:MaxProviders], true
}
