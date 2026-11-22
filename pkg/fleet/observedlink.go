package fleet

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// linkObserved links a replaced inferred service to the declared service whose
// published contract the operator linked to the running workload, with no
// Pacto, by its image digest or its pacto.trianalab.io/contract annotation.
const linkObserved = "observed"

// contractAnnotation is the workload annotation that names a published
// contract.
const contractAnnotation = "pacto.trianalab.io/contract"

// Line is l in words, "observed: image digest sha256:ab12… since 3m ago; no
// Pacto", with no "since" part when the time is unknown.
func (l *ObservedLink) Line(asOf time.Time) string {
	line := "observed: " + linkPhrase(l.Signal, l.Value)
	if l.Since != nil {
		line += " since " + Age(asOf.Sub(*l.Since)) + " ago"
	}
	return line + "; no Pacto"
}

// linkPhrase names the signal and its value: "image digest sha256:ab12…" or
// "pacto.trianalab.io/contract <ref>".
func linkPhrase(signal, value string) string {
	if signal == LinkSignalAnnotation {
		return contractAnnotation + " " + value
	}
	return "image digest " + shortDigestOf(value)
}

// shortDigestOf is a digest as short as a record id, with an ellipsis when cut.
func shortDigestOf(d string) string {
	if s := shortRecord(d); s != d {
		return s + "…"
	}
	return d
}

// replaceByObservedLink hands each workload the operator linked to a published
// contract to that contract's service. It runs after replaceInferred and
// replaceByInferredFrom, so a Pacto target that may run as the workload
// (resolved, ambiguous or of unknown cluster) wins, and its linked target is
// dropped, and a workload a declaration already handed over is not handed
// over twice. It returns the inputs that stay inferred services.
func replaceByObservedLink(snap *FleetSnapshot, inputs []inferredInput) []inferredInput {
	byWorkload := map[workloadKey][]inferredInput{}
	for _, in := range inputs {
		k := in.key(snap.clusterKeyed)
		byWorkload[k] = append(byWorkload[k], in)
	}
	targeting, unknown := targetsByWorkload(snap)
	handed := map[workloadKey]bool{}
	for _, k := range slices.Sorted(maps.Keys(snap.Targets)) {
		t := snap.Targets[k]
		if t.Link == nil || t.workload == nil {
			continue
		}
		wk := workloadKey{namespace: t.Scope, kind: t.workload.Kind, name: t.workload.Name}
		if snap.clusterKeyed {
			wk.cluster = t.Cluster
		}
		if len(targetsOf(wk, targeting, unknown)) > 0 {
			delete(snap.Targets, k) // a Pacto target wins
			continue
		}
		linkVersionAmbiguity(snap, t, wk)
		if handOverLinked(snap, t, wk, byWorkload[wk]) {
			handed[wk] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(inputs), func(in inferredInput) bool { return handed[in.key(snap.clusterKeyed)] })
}

// handOverLinked hands wk to t's service, reporting whether it did. A
// workload of unknown cluster in a snapshot of several, or one a declaration
// handed to another service, stays as it is with a limitation. A replacement
// is recorded only when the workload ran for minReplacementGap before the
// link: a fresh install is no takeover.
func handOverLinked(snap *FleetSnapshot, t *TargetRecord, wk workloadKey, ins []inferredInput) bool {
	name := K8sCaller(wk.cluster, wk.namespace, wk.kind, wk.name)
	if snap.clusterKeyed && t.Cluster == "" {
		linkLimitation(snap, wk, Limitation{Code: LimitationInferredReplacementAmbiguous, Source: t.Source,
			Message: "linked target " + string(t.Key) + " runs as " + name + " in an unknown cluster, and this fleet has several, so nothing is handed over"}, t.ServiceKey)
		return false
	}
	if r, ok := snap.replacedNames[name]; ok {
		if r.to != t.ServiceKey {
			linkLimitation(snap, wk, Limitation{Code: LimitationInferredReplacementAmbiguous, Source: t.Source,
				Message: "workload " + name + " runs the published contract of " + string(t.ServiceKey) + ", and a declaration hands it to " +
					string(r.to) + ", so it stays with " + string(r.to)}, t.ServiceKey, r.to)
		}
		return false
	}
	var r Replacement
	if created := earliestCreated(ins); created != nil && t.Link.Since != nil && created.Add(minReplacementGap).Before(*t.Link.Since) {
		r = Replacement{Link: linkObserved, Signal: t.Link.Signal, Value: t.Link.Value, Target: t.Key, Revision: t.ContractRevision, At: copyTime(t.Link.Since)}
	}
	handOver(snap, wk, t.ServiceKey, r, ins)
	return true
}

// maxLinkVersions is how many other versions a LINK_VERSION_AMBIGUOUS message
// names; the target's link holds them all.
const maxLinkVersions = 5

// linkVersionAmbiguity records LINK_VERSION_AMBIGUOUS on t and the snapshot
// when other release versions of its service record the digest it runs.
func linkVersionAmbiguity(snap *FleetSnapshot, t *TargetRecord, wk workloadKey) {
	if len(t.Link.OtherVersions) == 0 {
		return
	}
	versions := strings.Join(t.Link.OtherVersions[:min(len(t.Link.OtherVersions), maxLinkVersions)], ", ")
	if n := len(t.Link.OtherVersions) - maxLinkVersions; n > 0 {
		versions += fmt.Sprintf(" and %d more", n)
	}
	l := Limitation{Code: LimitationLinkVersionAmbiguous, Source: t.Source,
		Message: "workload " + wk.namespace + "/" + wk.kind + "/" + wk.name + " runs images that versions " + versions +
			" of " + string(t.ServiceKey) + " also record, so the newest is linked and impact reads " + string(t.ServiceKey) +
			" as an unverified consumer of the services it depends on"}
	t.Limitations = append(t.Limitations, l)
	linkLimitation(snap, wk, l, t.ServiceKey)
}
