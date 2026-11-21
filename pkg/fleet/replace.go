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
		// A target of unknown cluster may run as this workload too: it
		// joins the candidates, so another service's makes it ambiguous.
		for tk, u := range unknown {
			if u == (workloadKey{namespace: wk.namespace, kind: wk.kind, name: wk.name}) {
				keys = append(keys, tk)
			}
		}
		replaced[wk] = replaceWorkload(snap, wk, keys, byWorkload[wk])
	}
	for tk, wk := range unknown {
		ambiguousCluster(snap, tk, wk, byWorkload, targeting)
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

// replaceWorkload hands one workload to the one declared service keys
// target, reporting whether it did. The workload's observed name resolves to
// that service even when no source reported the workload (ins is empty). It
// leaves the workload inferred, with a limitation, when the targets belong to
// more than one service.
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
		if len(ins) == 0 {
			return false
		}
		names := make([]string, 0, len(services))
		for _, k := range slices.Sorted(maps.Keys(services)) {
			names = append(names, string(k))
		}
		ambiguousReplacement(snap, "Pacto targets of more than one service ("+strings.Join(names, ", ")+") run as workload "+name+", so it stays an inferred service")
		return false
	}
	if snap.replacedNames == nil {
		snap.replacedNames = map[string]replacedName{}
	}
	snap.replacedNames[name] = replacedName{to: snap.Targets[keys[0]].ServiceKey, workload: wk}
	t := earliestTarget(snap, keys)
	created := earliestCreated(ins)
	if created != nil && t != nil && created.Add(minReplacementGap).Before(*t.createdAt) {
		snap.Replacements = append(snap.Replacements, Replacement{
			From: NewServiceKey(name), To: t.ServiceKey, Link: linkPactoTarget, Target: t.Key, At: copyTime(t.createdAt), workload: wk,
		})
	}
	return true
}

// minReplacementGap is how much older than the Pacto a workload must be for a
// replacement to be recorded: install tools create a Pacto seconds or minutes
// after its workload (Helm applies custom resources last, Argo CD sync waves
// wait for the Deployment), and that is a fresh install, not a takeover.
//
// ponytail: a fixed gap; an install whose Pacto lands more than ten minutes
// after its workload still records one. Runtime evidence that the workload
// ran uncontracted (an observed edge first seen before the Pacto) is the
// upgrade path.
const minReplacementGap = 10 * time.Minute

// earliestTarget is the target among keys whose Pacto was created first, nil
// when any of them has no known creation time: a contract may have described
// the workload all along.
func earliestTarget(snap *FleetSnapshot, keys []TargetKey) *TargetRecord {
	var out *TargetRecord
	for _, k := range keys {
		t := snap.Targets[k]
		if t.createdAt == nil {
			return nil
		}
		if out == nil || t.createdAt.Before(*out.createdAt) {
			out = t
		}
	}
	return out
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
// cluster: which one it runs as is unknown, so none is replaced. A workload a
// target of known cluster names was settled by [replaceWorkload], with tk
// among its candidates, and is not named again.
func ambiguousCluster(snap *FleetSnapshot, tk TargetKey, wk workloadKey, byWorkload map[workloadKey][]inferredInput, targeting map[workloadKey][]TargetKey) {
	var names []string
	for k := range byWorkload {
		if len(targeting[k]) == 0 && k.namespace == wk.namespace && k.kind == wk.kind && k.name == wk.name {
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
// names under the same rule.
func restrictReplacements(out, s *FleetSnapshot, hiddenSvc map[ServiceKey]bool, hidden func(cluster, namespace, kind string) bool) {
	out.Replacements = slices.DeleteFunc(slices.Clone(s.Replacements), func(r Replacement) bool {
		return hiddenSvc[r.To] || hidden(r.workload.cluster, r.workload.namespace, r.workload.kind)
	})
	out.replacedNames = maps.Clone(s.replacedNames)
	maps.DeleteFunc(out.replacedNames, func(_ string, r replacedName) bool {
		return hiddenSvc[r.to] || hidden(r.workload.cluster, r.workload.namespace, r.workload.kind)
	})
}

// replacementHidden reports whether hidden hides the workload of some
// replacement of s.
func replacementHidden(s *FleetSnapshot, hidden func(cluster, namespace, kind string) bool) bool {
	return slices.ContainsFunc(s.Replacements, func(r Replacement) bool {
		return hidden(r.workload.cluster, r.workload.namespace, r.workload.kind)
	})
}
