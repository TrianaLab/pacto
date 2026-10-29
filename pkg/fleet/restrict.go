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
// only from a registry or files, with no Kubernetes target, has no namespace:
// visible("") decides it, and a caller who may read every namespace says yes.
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
	hiddenSvc := hiddenServices(s, hiddenNS, !visible(""))
	if len(hiddenNS) == 0 && len(hiddenSvc) == 0 {
		return q
	}

	out := *s
	var hiddenNames []string
	out.Targets = make(map[TargetKey]*TargetRecord, len(s.Targets))
	for k, t := range s.Targets {
		if (kubeTarget(t) && hiddenNS[t.Scope]) || hiddenSvc[t.ServiceKey] {
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
	hiddenDeps, hiddenDependents := restrictRelationships(&out, s.Relationships, hiddenSvc)
	hiddenNames = append(hiddenNames, restrictServices(&out, s.Services, hiddenSvc, hiddenDeps, hiddenDependents)...)

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
	recountSources(&out)

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

// kubeTarget reports whether t is a Kubernetes target with a namespace, the
// only kind a caller's permissions decide.
func kubeTarget(t *TargetRecord) bool { return t.Kind == SourceKindKubernetes && t.Scope != "" }

// hiddenServices is the services whose Kubernetes targets all run in a hidden
// namespace, and with hideUnscoped the services with no Kubernetes target.
func hiddenServices(s *FleetSnapshot, hiddenNS map[string]bool, hideUnscoped bool) map[ServiceKey]bool {
	hiddenSvc := map[ServiceKey]bool{}
	for k, svc := range s.Services {
		hidden, shown, scoped := false, false, false
		for _, tk := range svc.Targets {
			if t := s.Targets[tk]; kubeTarget(t) {
				scoped = true
				hidden = hidden || hiddenNS[t.Scope]
				shown = shown || !hiddenNS[t.Scope]
			}
		}
		if (hidden && !shown) || (!scoped && hideUnscoped) {
			hiddenSvc[k] = true
		}
	}
	return hiddenSvc
}

// restrictRelationships sets out.Relationships to rels without the hidden
// services, and returns, per visible service, the hidden services it depends
// on and that depend on it.
func restrictRelationships(out *FleetSnapshot, rels []Relationship, hiddenSvc map[ServiceKey]bool) (deps, dependents map[ServiceKey]map[ServiceKey]bool) {
	deps, dependents = map[ServiceKey]map[ServiceKey]bool{}, map[ServiceKey]map[ServiceKey]bool{}
	count := func(m map[ServiceKey]map[ServiceKey]bool, at, other ServiceKey) {
		if m[at] == nil {
			m[at] = map[ServiceKey]bool{}
		}
		m[at][other] = true
	}
	out.Relationships = make([]Relationship, 0, len(rels))
	for _, rel := range rels {
		from, to := hiddenSvc[rel.FromService], hiddenSvc[rel.ToService]
		switch {
		case from && to:
			continue
		case from:
			if rel.Type == RelationshipDependency && rel.ToService != "" {
				count(dependents, rel.ToService, rel.FromService)
			}
			continue
		case to:
			count(deps, rel.FromService, rel.ToService)
			if rel.Provenance == ProvenanceObserved {
				continue // runtime evidence about the hidden service, not the caller's declaration
			}
			rel.ToService, rel.ResolvedRevision, rel.Hidden, rel.Reason, rel.RangeStatus = "", "", true, hiddenMessage, ""
		}
		out.Relationships = append(out.Relationships, rel)
	}
	return deps, dependents
}

// restrictServices sets out.Services to the visible services, copying any
// that lost a target or gained a hidden count, and returns the names and keys
// of the hidden ones. out.Targets must already be restricted.
func restrictServices(out *FleetSnapshot, services map[ServiceKey]*ServiceRecord, hiddenSvc map[ServiceKey]bool, deps, dependents map[ServiceKey]map[ServiceKey]bool) []string {
	var hiddenNames []string
	out.Services = make(map[ServiceKey]*ServiceRecord, len(services))
	for k, svc := range services {
		if hiddenSvc[k] {
			hiddenNames = append(hiddenNames, svc.Name, string(k))
			continue
		}
		targets := slices.DeleteFunc(slices.Clone(svc.Targets), func(tk TargetKey) bool { return out.Targets[tk] == nil })
		if len(targets) == len(svc.Targets) && deps[k] == nil && dependents[k] == nil {
			out.Services[k] = svc
			continue
		}
		cp := *svc
		cp.Targets, cp.HiddenDependencies, cp.HiddenDependents = targets, len(deps[k]), len(dependents[k])
		cp.Status = serviceStatus(out, &cp)
		out.Services[k] = &cp
	}
	return hiddenNames
}

// recountSources recomputes each source's revision and target counts from
// what out still holds.
func recountSources(out *FleetSnapshot) {
	out.Sources = slices.Clone(out.Sources)
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
