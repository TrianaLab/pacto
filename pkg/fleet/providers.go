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

// Row ids and UsedFor values of the Kubernetes rows. An extender adds its own
// beside them; UsedFor stays a free string list, never an enum.
const (
	// ProviderPactoCRD is the row of the Pacto CRD a Kubernetes source discovered.
	ProviderPactoCRD = "pacto-crd"
	// ProviderKubernetesWorkloads is the row of the workloads a workload source listed.
	ProviderKubernetesWorkloads = "kubernetes-workloads"
	// UseInferredServices: Pacto infers services from what the row read.
	UseInferredServices = "inferred-services"
	// UseDeclaredTargets: Pacto reads declared targets from what the row read.
	UseDeclaredTargets = "declared-targets"
)

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
		// A row that sets no status claims available, so taking its source's
		// lower status is a downgrade that says why.
		status, _ := canonicalSourceStatus(cmp.Or(p.Status, SourceAvailable))
		switch {
		case sourceHealthRank(src) < sourceHealthRank(status):
			status = src
			p.Limitations = append(p.Limitations, Limitation{Code: LimitationProviderSourceDegraded, Source: p.Source,
				Message: "source " + p.Source + " is " + string(src) + "; this row covers only what it read"})
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
// read everywhere is "<Kind>s unreadable" (never 0), or "at least 12
// Deployments (some unreadable)" when some were read, and "no workloads found"
// means every kind was read and held nothing.
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
		case slices.Contains(unreadable, k) && n > 0:
			parts = append(parts, "at least "+kindCount(n, k)+" (some unreadable)")
		case slices.Contains(unreadable, k):
			parts = append(parts, k+"s unreadable")
		case n > 0:
			parts = append(parts, kindCount(n, k))
		}
	}
	if len(parts) == 0 {
		return "no workloads found"
	}
	return strings.Join(parts, ", ")
}

// kindCount is "1 Deployment" or "n Deployments".
func kindCount(n int, kind string) string {
	if n == 1 {
		return "1 " + kind
	}
	return strconv.Itoa(n) + " " + kind + "s"
}

// restrictProviders is rows as a reader visible and workloadVisible say yes
// to sees them. Every row stays; what changes is cloned. An object in a
// namespace the reader may not read goes, a cluster-scoped one included unless
// visible("") says yes, and ObjectsTotal keeps the count. A count stays only
// when the reader may read its namespace and list its kind there (a nil
// workloadVisible keeps none), and Evidence is rendered again with one hidden
// total. A cluster that clusters does not name goes from the row: the view
// hides it everywhere else. A limitation restrictScope would drop, or one that
// names a hidden name, goes; the degraded limitation names no namespace and
// stays. A row that is not available and lost every reason gets a degraded
// one, so it still says why. Evidence a producer wrote that names a hidden
// name is masked; evidence rendered from counts names none.
func restrictProviders(rows []Provider, hiddenNames []string, visible func(string) bool, workloadVisible func(namespace, kind string) bool, clusters []ClusterRecord) []Provider {
	if rows == nil {
		return nil
	}
	mentionsHidden := func(s string) bool {
		return slices.ContainsFunc(hiddenNames, func(n string) bool { return n != "" && strings.Contains(s, n) })
	}
	shown := map[string]bool{}
	for _, c := range clusters {
		shown[c.ID], shown[c.Name] = true, true
	}
	out := make([]Provider, len(rows))
	for i, p := range rows {
		if !shown[p.Cluster] {
			p.Cluster = ""
		}
		p.Objects = slices.DeleteFunc(slices.Clone(p.Objects), func(o ProviderObject) bool { return !visible(o.Namespace) })
		if len(p.Objects) < len(rows[i].Objects) {
			p.ObjectsTotal = cmp.Or(p.ObjectsTotal, len(rows[i].Objects))
		}
		hidden := 0
		p.Counts = slices.DeleteFunc(slices.Clone(p.Counts), func(c ProviderCount) bool {
			if countVisible(c, visible, workloadVisible) {
				return false
			}
			hidden += c.N
			return true
		})
		p.Evidence = restrictedEvidence(p, hidden, hiddenNames)
		p.Limitations = slices.DeleteFunc(slices.Clone(p.Limitations), func(l Limitation) bool {
			return l.Code != LimitationProviderSourceDegraded && (hiddenByScope(l, visible) || mentionsHidden(l.Message))
		})
		if p.Status != SourceAvailable && len(p.Limitations) == 0 {
			p.Limitations = []Limitation{{Code: LimitationProviderSourceDegraded, Source: p.Source,
				Message: "source " + p.Source + " reported this row as " + string(p.Status) + "; the reason is hidden by your permissions"}}
		}
		out[i] = p
	}
	return out
}

// restrictedEvidence is the evidence of p, already narrowed, with hidden
// counts left out: counted evidence is rendered again with the hidden total,
// and evidence a producer wrote is masked when it names a hidden name.
func restrictedEvidence(p Provider, hidden int, hiddenNames []string) string {
	suffix := " (+" + strconv.Itoa(hidden) + " hidden by your permissions)"
	switch {
	case p.Counts == nil && p.UnreadableKinds == nil:
		if slices.ContainsFunc(hiddenNames, func(n string) bool { return mentionsWord(p.Evidence, n) }) {
			return "hidden by your permissions"
		}
		return p.Evidence
	case hidden == 0:
		return countsEvidence(p.Counts, p.UnreadableKinds)
	case len(p.Counts) == 0 && len(p.UnreadableKinds) == 0:
		return "none you may see" + suffix
	}
	return countsEvidence(p.Counts, p.UnreadableKinds) + suffix
}

// mentionsWord reports whether s holds n as a whole name: not inside a longer
// one, so a hidden namespace "pacto" leaves "pacto.trianalab.io" alone.
func mentionsWord(s, n string) bool {
	if n == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(s[i:], n)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(n)
		if (start == 0 || !nameByte(s[start-1])) && (end == len(s) || !nameByte(s[end])) {
			return true
		}
		i = start + 1
	}
}

// nameByte reports whether b may appear inside a Kubernetes or service name.
func nameByte(b byte) bool {
	return strings.IndexByte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._", b) >= 0
}

// countVisible reports whether a reader may see c: read its namespace and
// list its kind there. A nil workloadVisible lists nothing.
func countVisible(c ProviderCount, visible func(string) bool, workloadVisible func(namespace, kind string) bool) bool {
	return visible(c.Namespace) && workloadVisible != nil && workloadVisible(c.Namespace, c.Kind)
}

// providersNarrowed reports whether a reader visible and workloadVisible say
// yes to would lose an object or a count of some row of s.
func providersNarrowed(s *FleetSnapshot, visible func(string) bool, workloadVisible func(namespace, kind string) bool) bool {
	return slices.ContainsFunc(s.Providers, func(p Provider) bool {
		return slices.ContainsFunc(p.Objects, func(o ProviderObject) bool { return !visible(o.Namespace) }) ||
			slices.ContainsFunc(p.Counts, func(c ProviderCount) bool { return !countVisible(c, visible, workloadVisible) })
	})
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

// ProvidersResult answers "what did Pacto detect": every provider row of the
// query's snapshot, bounded, with the product meta that names each source.
// The Kubernetes facts come from every source, never from the bounded meta.
type ProvidersResult struct {
	Meta              ProductMeta   `json:"meta"`
	Total             int           `json:"total" doc:"How many rows the snapshot holds before the bound"`
	Truncated         bool          `json:"truncated,omitempty"`
	Providers         []Provider    `json:"providers" doc:"At most 200 rows, least healthy first, then by source and id"`
	KubernetesSources int           `json:"kubernetesSources" doc:"How many Kubernetes and workload sources the view reads, the unavailable ones included"`
	NotRead           []SourceState `json:"notRead" doc:"The Kubernetes and workload sources Pacto could not read, at most 50"`
	NotReadTotal      int           `json:"notReadTotal" doc:"How many Kubernetes and workload sources Pacto could not read before the bound"`
}

// Providers lists the detected-provider rows of the query, least healthy first.
func (q *Query) Providers() *ProvidersResult {
	rows, truncated := boundProviders(q.snap.Providers)
	if rows == nil {
		rows = []Provider{}
	}
	res := &ProvidersResult{Meta: q.productMeta(), Total: len(q.snap.Providers), Truncated: truncated, Providers: rows, NotRead: []SourceState{}}
	for _, st := range q.snap.Sources {
		if st.Kind != SourceKindKubernetes && st.Kind != SourceKindKubernetesWorkloads {
			continue
		}
		res.KubernetesSources++
		if st.Status == SourceUnavailable {
			res.NotReadTotal++
			if len(res.NotRead) < MaxMetaSources {
				res.NotRead = append(res.NotRead, st)
			}
		}
	}
	res.NotRead = cloneSources(res.NotRead)
	return res
}
