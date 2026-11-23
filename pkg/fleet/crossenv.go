package fleet

import (
	"cmp"
	"maps"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// categoryCrossEnvironment is the attention category of an observed call from
// one environment into another.
const categoryCrossEnvironment = "cross-environment"

// The codes of a cross-environment call: into a persistent environment, and
// into a preview.
const (
	codeCrossEnvironmentCall        = "CROSS_ENVIRONMENT_CALL"
	codeCrossEnvironmentPreviewCall = "CROSS_ENVIRONMENT_PREVIEW_CALL"
)

// maxEnvPairs bounds the target pairs one relationship keeps.
const maxEnvPairs = 8

// envPair is one (caller target, callee target) pair of raw observed calls
// and when the newest of them was seen.
type envPair struct {
	from, to TargetKey
	lastSeen *time.Time
}

// envPairJudge returns the function that maps both ends of a raw observed
// edge to targets. An end is judged only when it is a Kubernetes workload id
// that one target runs as, and that target names its environment. In a
// snapshot of several clusters an end must name its cluster: a 3-part id is
// ambiguous. Anything else, an end named by service included, maps to nil:
// the environment is unknown and raises nothing.
func envPairJudge(snap *FleetSnapshot) func(ObservedEdge) *envPair {
	targeting, _ := targetsByWorkload(snap)
	target := func(end string) (TargetKey, bool) {
		cluster, ns, kind, name, ok := ParseK8sCaller(end)
		if !ok || (snap.clusterKeyed && cluster == "") {
			return "", false
		}
		wk := workloadKey{namespace: ns, kind: kind, name: name}
		if snap.clusterKeyed {
			wk.cluster = cluster
		}
		ks := targeting[wk]
		if len(ks) != 1 {
			return "", false
		}
		t := snap.Targets[ks[0]]
		return ks[0], t.Environment != "" && (cluster == "" || t.Cluster == "" || t.Cluster == cluster)
	}
	return func(e ObservedEdge) *envPair {
		from, okFrom := target(e.From)
		to, okTo := target(e.To)
		if !okFrom || !okTo {
			return nil
		}
		p := &envPair{from: from, to: to}
		if !e.LastSeen.IsZero() {
			p.lastSeen = new(e.LastSeen)
		}
		return p
	}
}

// addEnvPair adds p to pairs, keeping the newest last-seen time of a pair
// already there.
//
// ponytail: past maxEnvPairs distinct pairs a new one is dropped; a workload
// maps to one target, so a relationship rarely holds more than one pair per
// environment.
func addEnvPair(pairs []envPair, p envPair) []envPair {
	i := slices.IndexFunc(pairs, func(q envPair) bool { return q.from == p.from && q.to == p.to })
	switch {
	case i >= 0:
		pairs[i].lastSeen = later(pairs[i].lastSeen, p.lastSeen)
	case len(pairs) < maxEnvPairs:
		pairs = append(pairs, p)
	}
	return pairs
}

// sortedEnvPairs sorts pairs by caller then callee target, nil when empty.
func sortedEnvPairs(pairs []envPair) []envPair {
	slices.SortFunc(pairs, func(a, b envPair) int { return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to)) })
	return slices.Clip(pairs)
}

// crossEnvironmentAttention reports each observed call whose caller and callee
// targets run in different environments, against the caller's target: an
// error, a warning when the callee's environment is a preview, and at most a
// warning when the call was last seen outside the recent window. A pair with a
// target the view may not see raises nothing.
func (q *Query) crossEnvironmentAttention() []AttentionItem {
	var out []AttentionItem
	var previews map[string]bool // read once, on the first call across environments
	for _, rel := range q.snap.Relationships {
		for _, p := range rel.envPairs {
			from, to := q.snap.Targets[p.from], q.snap.Targets[p.to]
			if from == nil || to == nil || from.Environment == to.Environment {
				continue
			}
			if previews == nil {
				previews = q.previewEnvironments()
			}
			preview := previews[to.Environment]
			out = append(out, crossEnvironmentItem(from, to, preview, p.lastSeen, q.observedStale(p.lastSeen)))
		}
	}
	return out
}

// crossEnvironmentItem is the attention item of one call from target from
// into target to.
func crossEnvironmentItem(from, to *TargetRecord, preview bool, lastSeen *time.Time, stale bool) AttentionItem {
	it := AttentionItem{
		Entity: targetEntityRef(from), Service: string(from.ServiceKey),
		Label:    from.Service + " (" + from.Environment + ") calls " + to.Service + " (" + to.Environment + ")",
		Severity: severityError, Code: codeCrossEnvironmentCall, Category: categoryCrossEnvironment,
		Summary:  "Call across environments",
		Reason:   "observed calls from " + from.DisplayName() + " in " + from.Environment + " to " + to.DisplayName() + " in " + to.Environment,
		NextStep: "Find out why " + from.Service + " in " + from.Environment + " calls " + to.Environment,
	}
	if preview {
		it.Severity, it.Code, it.Summary = severityWarning, codeCrossEnvironmentPreviewCall, "Call into a preview environment"
	}
	if stale {
		it.Severity = severityWarning
		it.Reason += ", last seen " + lastSeen.UTC().Format(time.RFC3339)
	}
	return it
}

// crossEnvironmentTally is how many cross-environment items there are and the
// severity of the worst.
func crossEnvironmentTally(items []AttentionItem) (n int, severity string) {
	for _, it := range items {
		if it.Category != categoryCrossEnvironment {
			continue
		}
		n++
		if severity != severityError {
			severity = it.Severity
		}
	}
	return n, severity
}

// previewEnvironments is every preview environment: at least one revision a
// visible target runs declares it, and every such revision declares it
// temporary.
func (q *Query) previewEnvironments() map[string]bool {
	out := map[string]bool{}
	for _, t := range q.snap.Targets {
		r := q.snap.Revisions[t.ContractRevision]
		if r == nil || r.Contract == nil {
			continue
		}
		for _, e := range r.Contract.Environments {
			preview, seen := out[e.Name]
			out[e.Name] = e.Lifetime == contract.EnvironmentTemporary && (preview || !seen)
		}
	}
	maps.DeleteFunc(out, func(_ string, preview bool) bool { return !preview })
	return out
}
