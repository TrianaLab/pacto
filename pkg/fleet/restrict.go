package fleet

import (
	"maps"
	"slices"
	"strings"
)

// LimitationHiddenByPermissions: the answer leaves out services the caller may
// not read. It never names them, or the namespaces they run in.
const LimitationHiddenByPermissions = "HIDDEN_BY_PERMISSIONS"

// hiddenMessage is what a restricted answer says about what it left out.
const hiddenMessage = "some services are hidden because Kubernetes permissions do not let you read their namespace"

// Namespaces lists, sorted, the namespaces the snapshot's Kubernetes targets
// run in: the ones a caller's permissions decide.
func (q *Query) Namespaces() []string {
	seen := map[string]bool{}
	for _, t := range q.snap.Targets {
		if t.Kind == SourceKindKubernetes && t.Scope != "" {
			seen[t.Scope] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// Restrict is the snapshot as a caller who may read only the namespaces
// visible says yes to sees it. A Kubernetes target in another namespace is
// gone, and so is a service none of whose Kubernetes targets they may read,
// with its revisions, its targets everywhere and its edges. A service known
// only from a registry or files, with no Kubernetes target, stays.
//
// An edge between a visible and a hidden service keeps the visible side:
// each visible service counts the hidden services it depends on and that
// depend on it, never naming them. Its own declared dependency on a hidden
// service stays as a Hidden relationship, its resolution blanked. Counts,
// sources, limitations and the snapshot id are recomputed, so nothing is
// derived from what was hidden. Restrict returns q when nothing is hidden.
func (q *Query) Restrict(visible func(namespace string) bool) *Query {
	s := q.snap
	hiddenNS := map[string]bool{}
	for _, ns := range q.Namespaces() {
		if !visible(ns) {
			hiddenNS[ns] = true
		}
	}
	if len(hiddenNS) == 0 {
		return q
	}
	kube := func(t *TargetRecord) bool { return t.Kind == SourceKindKubernetes && t.Scope != "" }
	hiddenSvc := map[ServiceKey]bool{}
	for k, svc := range s.Services {
		hidden, shown := false, false
		for _, tk := range svc.Targets {
			if t := s.Targets[tk]; kube(t) {
				hidden = hidden || hiddenNS[t.Scope]
				shown = shown || !hiddenNS[t.Scope]
			}
		}
		if hidden && !shown {
			hiddenSvc[k] = true
		}
	}

	out := *s
	out.Targets = make(map[TargetKey]*TargetRecord, len(s.Targets))
	var hiddenNames []string
	for k, t := range s.Targets {
		if (kube(t) && hiddenNS[t.Scope]) || hiddenSvc[t.ServiceKey] {
			hiddenNames = append(hiddenNames, t.Name, string(k))
			continue
		}
		out.Targets[k] = t
	}
	out.Revisions = make(map[RevisionKey]*ContractRevision, len(s.Revisions))
	for k, r := range s.Revisions {
		if !hiddenSvc[r.ServiceKey] {
			out.Revisions[k] = r
		}
	}

	hiddenDeps, hiddenDependents := map[ServiceKey]map[ServiceKey]bool{}, map[ServiceKey]map[ServiceKey]bool{}
	count := func(m map[ServiceKey]map[ServiceKey]bool, at, other ServiceKey) {
		if m[at] == nil {
			m[at] = map[ServiceKey]bool{}
		}
		m[at][other] = true
	}
	out.Relationships = make([]Relationship, 0, len(s.Relationships))
	for _, rel := range s.Relationships {
		from, to := hiddenSvc[rel.FromService], hiddenSvc[rel.ToService]
		switch {
		case from && to:
			continue
		case from:
			if rel.Type == RelationshipDependency && rel.ToService != "" {
				count(hiddenDependents, rel.ToService, rel.FromService)
			}
			continue
		case to:
			count(hiddenDeps, rel.FromService, rel.ToService)
			if rel.Provenance == ProvenanceObserved {
				continue // runtime evidence about the hidden service, not the caller's declaration
			}
			rel.ToService, rel.ResolvedRevision, rel.Hidden, rel.Reason = "", "", true, hiddenMessage
		}
		out.Relationships = append(out.Relationships, rel)
	}

	out.Services = make(map[ServiceKey]*ServiceRecord, len(s.Services))
	for k, svc := range s.Services {
		if hiddenSvc[k] {
			hiddenNames = append(hiddenNames, svc.Name, string(k))
			continue
		}
		targets := slices.DeleteFunc(slices.Clone(svc.Targets), func(tk TargetKey) bool { return out.Targets[tk] == nil })
		if len(targets) == len(svc.Targets) && hiddenDeps[k] == nil && hiddenDependents[k] == nil {
			out.Services[k] = svc
			continue
		}
		cp := *svc
		cp.Targets, cp.HiddenDependencies, cp.HiddenDependents = targets, len(hiddenDeps[k]), len(hiddenDependents[k])
		cp.Status = serviceStatus(&out, &cp)
		out.Services[k] = &cp
	}

	keep := func(k ServiceKey) bool { return !hiddenSvc[k] }
	out.reverseDeps = restrictIndex(s.reverseDeps, keep, keep)
	out.forwardDeps = restrictIndex(s.forwardDeps, keep, keep)
	out.observedReverse = restrictIndex(s.observedReverse, keep, keep)
	out.observedForward = restrictIndex(s.observedForward, keep, keep)
	out.forwardDepsByRevision = restrictIndex(s.forwardDepsByRevision, func(k RevisionKey) bool { return out.Revisions[k] != nil }, keep)

	out.UncontractedCallers = slices.DeleteFunc(slices.Clone(s.UncontractedCallers), func(c UncontractedCaller) bool {
		rest, k8s := strings.CutPrefix(c.Caller, "k8s:")
		ns, _, _ := strings.Cut(rest, "/")
		return hiddenSvc[c.ToService] || (k8s && hiddenNS[ns])
	})

	out.Sources = slices.Clone(s.Sources)
	for i := range out.Sources {
		id := out.Sources[i].ID
		out.Sources[i].RevisionCount, out.Sources[i].TargetCount = 0, 0
		for _, r := range out.Revisions {
			if slices.Contains(r.Sources, id) {
				out.Sources[i].RevisionCount++
			}
		}
		for _, t := range out.Targets {
			if slices.Contains(t.Sources, id) {
				out.Sources[i].TargetCount++
			}
		}
	}

	// ponytail: a limitation's subject is only in its prose, so any that mentions
	// a hidden name goes, even where the match is a coincidence. It costs
	// limitations x hidden names substring checks, once per snapshot and set of
	// visible namespaces. Structured subjects on Limitation would make it exact
	// and linear.
	hiddenNames = append(hiddenNames, slices.Collect(maps.Keys(hiddenNS))...)
	out.Limitations = slices.DeleteFunc(slices.Clone(s.Limitations), func(l Limitation) bool {
		return slices.ContainsFunc(hiddenNames, func(n string) bool { return n != "" && strings.Contains(l.Message, n) })
	})
	out.Limitations = append(out.Limitations, Limitation{Code: LimitationHiddenByPermissions, Message: hiddenMessage})
	sortSnapshotLimitations(&out)
	out.Completeness = CompletenessPartial
	out.SnapshotID = computeSnapshotID(&out)
	return NewQuery(&out)
}

// restrictIndex copies an adjacency index without the keys keyOK refuses and
// the neighbours neighbourOK refuses.
func restrictIndex[K comparable](m map[K][]ServiceKey, keyOK func(K) bool, neighbourOK func(ServiceKey) bool) map[K][]ServiceKey {
	out := make(map[K][]ServiceKey, len(m))
	for k, vs := range m {
		if !keyOK(k) {
			continue
		}
		if kept := slices.DeleteFunc(slices.Clone(vs), func(v ServiceKey) bool { return !neighbourOK(v) }); len(kept) > 0 {
			out[k] = kept
		}
	}
	return out
}
