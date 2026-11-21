package fleet

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// linkInferredFrom links a replaced inferred service to the declared service
// whose accepted declaration names it in its inferred record.
const linkInferredFrom = "inferred-from"

// declareDocs is where a person reads how a declaration takes over its
// inferred service.
const declareDocs = "https://pacto.run/latest/operational-graph-inferred/#when-a-declaration-replaces-an-inferred-service"

// workloadNote is a limitation that names a workload, with that workload and
// the services whose declarations it is about, so Restrict can drop it.
type workloadNote struct {
	workload workloadKey
	services []ServiceKey
	l        Limitation
}

// inferredClaim is the newest accepted declaration of one service and the
// workload it names.
type inferredClaim struct {
	rev *ContractRevision
	by  string
}

// handOver is the one place a workload changes hands: its observed name
// resolves to service to from now on (see
// [FleetSnapshot.ObservedNameResolver]), and r is recorded as a
// [Replacement] unless its Link is empty. A link other than pacto-target
// carries what the workload showed, read from the input of the lowest source
// id so the value is independent of source order.
//
// ponytail: one source's view of the workload; merging every source's facts
// like mergeInferred if a twin is ever reported twice with different facts.
func handOver(snap *FleetSnapshot, wk workloadKey, to ServiceKey, r Replacement, ins []inferredInput) {
	name := K8sCaller(wk.cluster, wk.namespace, wk.kind, wk.name)
	if snap.replacedNames == nil {
		snap.replacedNames = map[string]replacedName{}
	}
	snap.replacedNames[name] = replacedName{to: to, workload: wk}
	if r.Link == "" {
		return
	}
	r.From, r.To, r.workload = NewServiceKey(name), to, wk
	if r.Link != linkPactoTarget && len(ins) > 0 {
		in := slices.MinFunc(ins, func(a, b inferredInput) int { return cmp.Compare(a.source, b.source) })
		if rec, _ := inferredServiceRecord(in, snap.clusterKeyed); rec != nil {
			r.Workload = rec.Workload
		}
	}
	snap.Replacements = append(snap.Replacements, r)
}

// replaceByInferredFrom hands each workload an accepted declaration names to
// the declaring service. It runs right after replaceInferred, so a Pacto
// target that may run as the workload (resolved, ambiguous or of unknown
// cluster) wins, and returns the inputs that stay inferred services. Two
// services claiming one workload never merge: the inferred service stays.
func replaceByInferredFrom(snap *FleetSnapshot, inputs []inferredInput) []inferredInput {
	byWorkload := map[workloadKey][]inferredInput{}
	for _, in := range inputs {
		k := in.key(snap.clusterKeyed)
		byWorkload[k] = append(byWorkload[k], in)
	}
	claims := inferredFromClaims(snap, byWorkload)
	targeting, unknown := targetsByWorkload(snap)
	handed := map[workloadKey]bool{}
	for _, wk := range slices.SortedFunc(maps.Keys(claims), compareWorkloads) {
		cs := claims[wk]
		name := K8sCaller(wk.cluster, wk.namespace, wk.kind, wk.name)
		if ts := targetsOf(wk, targeting, unknown); len(ts) > 0 {
			targetWins(snap, wk, name, ts, cs)
			continue
		}
		if len(cs) > 1 {
			svcs := claimServices(cs)
			linkLimitation(snap, wk, Limitation{Code: LimitationInferredReplacementAmbiguous,
				Message: "workload " + name + " is claimed by the declarations of " + keyList(svcs) + ", so it stays an inferred service"}, svcs...)
			continue
		}
		c := cs[0]
		if ns := targetNamespaces(snap, c.rev.ServiceKey); len(ns) > 0 && !slices.Contains(ns, wk.namespace) {
			linkLimitation(snap, wk, Limitation{Code: LimitationInferredLinkIgnored, Source: c.rev.Source,
				Message: string(c.rev.ServiceKey) + " runs in namespaces " + strings.Join(ns, ", ") + ", so its inferredFrom " + name +
					" in namespace " + wk.namespace + " is ignored"}, c.rev.ServiceKey)
			continue
		}
		handOver(snap, wk, c.rev.ServiceKey, Replacement{Link: linkInferredFrom, Revision: c.rev.Key, By: c.by}, byWorkload[wk])
		handed[wk] = true
	}
	return slices.DeleteFunc(slices.Clone(inputs), func(in inferredInput) bool { return handed[in.key(snap.clusterKeyed)] })
}

// inferredFromClaims is, per workload, the services whose newest declared
// revision names it and whose declaration a person's verified signature
// carries and the installation's signer policy accepts. An older declaration
// of a service counts for nothing, a newer one naming no workload retracts
// it, and a revision with no declaration (a CI publish) retracts none.
func inferredFromClaims(snap *FleetSnapshot, byWorkload map[workloadKey][]inferredInput) map[workloadKey][]inferredClaim {
	newest := map[ServiceKey]*ContractRevision{}
	for _, rev := range snap.Revisions {
		if rev.Record == nil || rev.Record.Declaration == nil || rev.Record.Declaration.State != contract.DeclarationDeclared {
			continue
		}
		if cur := newest[rev.ServiceKey]; cur == nil || LessRevisionChrono(cur, rev) {
			newest[rev.ServiceKey] = rev
		}
	}
	claims := map[workloadKey][]inferredClaim{}
	for _, svc := range slices.Sorted(maps.Keys(newest)) {
		rev := newest[svc]
		if rev.Record.Declaration.InferredFrom == "" {
			continue // declared from its folder alone: it claims no workload
		}
		if wk, ok := claimedWorkload(snap, rev, byWorkload); ok {
			claims[wk] = append(claims[wk], inferredClaim{rev: rev, by: rev.Record.Declaration.Asker.Subject})
		}
	}
	return claims
}

// claimedWorkload is the workload rev's declaration names, false with a
// limitation when the name does not parse, its cluster is unknown or
// contradicted, or the declaration is not accepted.
func claimedWorkload(snap *FleetSnapshot, rev *ContractRevision, byWorkload map[workloadKey][]inferredInput) (workloadKey, bool) {
	d := rev.Record.Declaration
	label := "revision " + rev.Service + "@" + rev.Version
	wk, cluster, ok := claimKey(d.InferredFrom, d.Cluster, snap.clusterKeyed)
	if !ok {
		snap.Limitations = append(snap.Limitations, Limitation{Code: LimitationInferredLinkIgnored, Source: rev.Source,
			Message: label + " names the inferred service it was declared from in a form Pacto cannot read, so nothing is handed over"})
		return wk, false
	}
	name := K8sCaller(wk.cluster, wk.namespace, wk.kind, wk.name)
	if snap.clusterKeyed && cluster == "" {
		linkLimitation(snap, wk, Limitation{Code: LimitationInferredReplacementAmbiguous, Source: rev.Source,
			Message: label + " says it was declared from " + name + " in an unknown cluster, and this fleet has several, so nothing is handed over"}, rev.ServiceKey)
		return wk, false
	}
	if known := workloadCluster(snap, byWorkload[wk]); !snap.clusterKeyed && cluster != "" && known != "" && known != cluster {
		linkLimitation(snap, wk, Limitation{Code: LimitationInferredLinkIgnored, Source: rev.Source,
			Message: name + " was declared in cluster " + cluster + ", and this fleet's cluster is " + known + ", so " + label + " hands nothing over"}, rev.ServiceKey)
		return wk, false
	}
	if why, fix := declarationRejected(rev); why != "" {
		linkLimitation(snap, wk, Limitation{Code: LimitationInferredLinkIgnored, Source: rev.Source,
			Message: label + " says it was declared from " + name + ", but " + why + ", so " + name + " stays an inferred service; " + fix}, rev.ServiceKey)
		return wk, false
	}
	return wk, true
}

// claimKey reads a declaration's inferredFrom, the inferred service name
// (the record's parser accepts no other form). The cluster is the name's
// own, else the declaration's; the key carries it only in a snapshot of
// several clusters.
func claimKey(raw, declared string, multi bool) (workloadKey, string, bool) {
	c, ns, kind, name, ok := ParseK8sCaller(raw)
	if !ok || !validWorkloadPart(ns) || !validWorkloadPart(kind) || !validWorkloadPart(name) {
		return workloadKey{}, "", false
	}
	c = cmp.Or(c, declared)
	wk := workloadKey{namespace: ns, kind: kind, name: name}
	if multi {
		wk.cluster = c
	}
	return wk, c, true
}

// workloadCluster is the cluster the workload's source reported, else the
// snapshot's one known cluster, "" when neither is known.
func workloadCluster(snap *FleetSnapshot, ins []inferredInput) string {
	for _, in := range ins {
		if in.cluster != "" {
			return in.cluster
		}
	}
	if len(snap.Clusters) == 1 {
		return snap.Clusters[0].ID
	}
	return ""
}

// declarationRejected is why rev's declaration does not hand a workload over
// and the fix, "" when a person's verified signature carries it and the
// installation's signer policy accepts that person. Unknown is never
// positive: a revision read with no signature check never merges.
func declarationRejected(rev *ContractRevision) (why, fix string) {
	rule := "add the person's issuer and subject to the installation signer rule (delivery.identities.signers, see " + declareDocs + ")"
	switch {
	case rev.Provenance == nil:
		return "this source checks no signatures (a --local fleet or the Kubernetes source)", "read the registry with signature checks (see " + declareDocs + ")"
	case !rev.declarationChecked:
		return "no verified person signer matches its asker", rule
	case rev.declarationRefused != "":
		return rev.declarationRefused, rule
	}
	return "", ""
}

// targetsOf is the Pacto targets that may run as wk: those that do, and those
// of unknown cluster with its namespace, kind and name.
func targetsOf(wk workloadKey, targeting map[workloadKey][]TargetKey, unknown map[TargetKey]workloadKey) []TargetKey {
	ts := slices.Clone(targeting[wk])
	for tk, u := range unknown {
		if u == (workloadKey{namespace: wk.namespace, kind: wk.kind, name: wk.name}) {
			ts = append(ts, tk)
		}
	}
	slices.Sort(ts)
	return ts
}

// targetWins records that a Pacto target that may run as wk wins over each
// claim, unless replaceInferred already handed wk to the claiming service.
func targetWins(snap *FleetSnapshot, wk workloadKey, name string, ts []TargetKey, cs []inferredClaim) {
	keys := make([]string, len(ts))
	for i, tk := range ts {
		keys[i] = string(tk)
	}
	for _, c := range cs {
		if r, ok := snap.replacedNames[name]; ok && r.to == c.rev.ServiceKey {
			continue
		}
		linkLimitation(snap, wk, Limitation{Code: LimitationInferredLinkIgnored, Source: c.rev.Source,
			Message: "Pacto targets " + strings.Join(keys, ", ") + " run as " + name + ", so the inferredFrom of " + string(c.rev.ServiceKey) +
				" is ignored: a Pacto target wins"}, c.rev.ServiceKey)
	}
}

// targetNamespaces is the sorted namespaces of service's Kubernetes targets.
func targetNamespaces(snap *FleetSnapshot, service ServiceKey) []string {
	var out []string
	for _, t := range snap.Targets {
		if t.ServiceKey == service && t.Kind == SourceKindKubernetes && !slices.Contains(out, t.Scope) {
			out = append(out, t.Scope)
		}
	}
	slices.Sort(out)
	return out
}

// linkLimitation records l and, for Restrict, the workload and services it
// names.
func linkLimitation(snap *FleetSnapshot, wk workloadKey, l Limitation, services ...ServiceKey) {
	snap.Limitations = append(snap.Limitations, l)
	snap.inferredLinkNotes = append(snap.inferredLinkNotes, workloadNote{workload: wk, services: services, l: l})
}

func claimServices(cs []inferredClaim) []ServiceKey {
	out := make([]ServiceKey, len(cs))
	for i, c := range cs {
		out[i] = c.rev.ServiceKey
	}
	return out
}

func keyList(ks []ServiceKey) string {
	s := make([]string, len(ks))
	for i, k := range ks {
		s[i] = string(k)
	}
	return strings.Join(s, ", ")
}

func compareWorkloads(a, b workloadKey) int {
	return cmp.Or(cmp.Compare(a.cluster, b.cluster), cmp.Compare(a.namespace, b.namespace), cmp.Compare(a.kind, b.kind), cmp.Compare(a.name, b.name))
}
