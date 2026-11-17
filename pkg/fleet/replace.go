package fleet

import (
	"maps"
	"slices"
	"strings"
	"time"
)

// linkPactoTarget links a replaced inferred service to the declared service
// whose Pacto target runs as its workload.
const linkPactoTarget = "pacto-target"

// replaceInferred hands each workload a Pacto target runs as to that target's
// declared service. It runs after linkTargets and before any inferred record
// exists, and returns the inputs that stay inferred services.
//
// A workload one declared service targets is no inferred service: its
// observed name resolves to that service (see
// [FleetSnapshot.ObservedNameResolver]), and a [Replacement] is recorded only
// when the cluster shows the workload was created before the Pacto. A workload
// more than one declared service targets, or one a target of unknown cluster
// names in a snapshot of several clusters, stays inferred with an
// INFERRED_REPLACEMENT_AMBIGUOUS limitation: an ambiguous match is never a
// merge.
func replaceInferred(snap *FleetSnapshot, inputs []inferredInput) []inferredInput {
	byWorkload := map[workloadKey][]inferredInput{}
	for _, in := range inputs {
		k := in.key(snap.clusterKeyed)
		byWorkload[k] = append(byWorkload[k], in)
	}
	targeting, unknown := targetsByWorkload(snap)
	replaced := map[workloadKey]bool{}
	for wk, keys := range targeting {
		if len(byWorkload[wk]) > 0 {
			replaced[wk] = replaceWorkload(snap, wk, keys, byWorkload[wk])
		}
	}
	for tk, wk := range unknown {
		ambiguousCluster(snap, tk, wk, byWorkload)
	}
	return slices.DeleteFunc(slices.Clone(inputs), func(in inferredInput) bool { return replaced[in.key(snap.clusterKeyed)] })
}

// targetsByWorkload is the Kubernetes targets that run as a known workload,
// keyed like an inferred input, and apart those whose cluster is unknown in a
// snapshot of several clusters (their key has no cluster).
func targetsByWorkload(snap *FleetSnapshot) (targeting map[workloadKey][]TargetKey, unknown map[TargetKey]workloadKey) {
	targeting, unknown = map[workloadKey][]TargetKey{}, map[TargetKey]workloadKey{}
	for k, t := range snap.Targets {
		if t.workload == nil {
			continue
		}
		wk := workloadKey{namespace: t.Scope, kind: t.workload.Kind, name: t.workload.Name}
		if snap.clusterKeyed && t.Cluster == "" {
			unknown[k] = wk
			continue
		}
		if snap.clusterKeyed {
			wk.cluster = t.Cluster
		}
		targeting[wk] = append(targeting[wk], k)
	}
	return targeting, unknown
}

// replaceWorkload hands one inferred workload to the one declared service
// keys target, reporting whether it did. It leaves the workload inferred, with
// a limitation, when the targets belong to more than one service.
//
// ponytail: the workload's identity is not re-validated here; Kubernetes
// object names hold no "/" or control character, so a targeted workload
// always has a valid inferred key.
func replaceWorkload(snap *FleetSnapshot, wk workloadKey, keys []TargetKey, ins []inferredInput) bool {
	slices.Sort(keys)
	services := map[ServiceKey]bool{}
	for _, k := range keys {
		services[snap.Targets[k].ServiceKey] = true
	}
	name := K8sCaller(wk.cluster, wk.namespace, wk.kind, wk.name)
	if len(services) > 1 {
		names := make([]string, 0, len(services))
		for _, k := range slices.Sorted(maps.Keys(services)) {
			names = append(names, string(k))
		}
		ambiguousReplacement(snap, "Pacto targets of more than one service ("+strings.Join(names, ", ")+") run as workload "+name+", so it stays an inferred service")
		return false
	}
	t := snap.Targets[keys[0]]
	if snap.replacedNames == nil {
		snap.replacedNames = map[string]ServiceKey{}
	}
	snap.replacedNames[name] = t.ServiceKey
	created := earliestCreated(ins)
	if created != nil && t.createdAt != nil && created.Before(*t.createdAt) {
		snap.Replacements = append(snap.Replacements, Replacement{
			From: NewServiceKey(name), To: t.ServiceKey, Link: linkPactoTarget, Target: t.Key, At: copyTime(t.createdAt), workload: wk,
		})
	}
	return true
}

// earliestCreated is the earliest creation time any source reported for one
// workload, nil when none did.
func earliestCreated(ins []inferredInput) *time.Time {
	var out *time.Time
	for _, in := range ins {
		if at := in.raw.CreatedAt; at != nil && (out == nil || at.Before(*out)) {
			out = at
		}
	}
	return out
}

// ambiguousCluster records that target tk names workload wk with no known
// cluster, while inferred services run as a workload of that name in some
// cluster: which one it runs as is unknown, so none is replaced.
func ambiguousCluster(snap *FleetSnapshot, tk TargetKey, wk workloadKey, byWorkload map[workloadKey][]inferredInput) {
	var names []string
	for k := range byWorkload {
		if k.namespace == wk.namespace && k.kind == wk.kind && k.name == wk.name {
			names = append(names, K8sCaller(k.cluster, k.namespace, k.kind, k.name))
		}
	}
	if len(names) == 0 {
		return
	}
	slices.Sort(names)
	ambiguousReplacement(snap, "Pacto target "+string(tk)+" runs as a workload in an unknown cluster, so "+strings.Join(names, ", ")+" stays an inferred service")
}

// ambiguousReplacement records why an inferred service was not replaced.
func ambiguousReplacement(snap *FleetSnapshot, message string) {
	snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredReplacementAmbiguous, Message: message})
}

// restrictReplacements keeps the replacements whose declared service is
// visible and whose workload hidden says the viewer may see, and the replaced
// names of visible services.
func restrictReplacements(out, s *FleetSnapshot, hiddenSvc map[ServiceKey]bool, hidden func(namespace, kind string) bool) {
	out.Replacements = slices.DeleteFunc(slices.Clone(s.Replacements), func(r Replacement) bool {
		return hiddenSvc[r.To] || hidden(r.workload.namespace, r.workload.kind)
	})
	out.replacedNames = maps.Clone(s.replacedNames)
	maps.DeleteFunc(out.replacedNames, func(_ string, k ServiceKey) bool { return hiddenSvc[k] })
}

// replacementHidden reports whether hidden hides the workload of some
// replacement of s.
func replacementHidden(s *FleetSnapshot, hidden func(namespace, kind string) bool) bool {
	return slices.ContainsFunc(s.Replacements, func(r Replacement) bool { return hidden(r.workload.namespace, r.workload.kind) })
}
