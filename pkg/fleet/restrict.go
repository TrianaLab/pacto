package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// LimitationHiddenByPermissions: the answer leaves out services the caller may
// not read. It never names them, or the namespaces they run in.
const LimitationHiddenByPermissions = "HIDDEN_BY_PERMISSIONS"

// hiddenMessage is what a restricted answer says about what it left out.
const hiddenMessage = "some services are hidden because Kubernetes permissions do not let you read their namespace"

// Namespaces lists, sorted, the namespaces the snapshot's Kubernetes targets,
// inferred services and k8s uncontracted callers run in, and the namespaces
// its Kubernetes sources were told to read: the ones a caller's permissions
// decide.
func (q *Query) Namespaces() []string {
	seen := map[string]bool{}
	for _, t := range q.snap.Targets {
		if t.Kind == SourceKindKubernetes && t.Scope != "" {
			seen[t.Scope] = true
		}
	}
	for _, sc := range q.InferredWorkloadScopes() {
		seen[sc.Namespace] = true
	}
	for _, c := range q.snap.UncontractedCallers {
		if ns, ok := callerNamespace(c); ok {
			seen[ns] = true
		}
	}
	scopeNamespaces(q.snap, seen)
	providerNamespaces(q.snap.Providers, seen)
	return slices.Sorted(maps.Keys(seen))
}

// providerNamespaces adds the namespace of every provider object and count:
// one seen only on a provider row must still be reviewed.
func providerNamespaces(rows []Provider, seen map[string]bool) {
	for _, p := range rows {
		for _, o := range p.Objects {
			if o.Namespace != "" {
				seen[o.Namespace] = true
			}
		}
		for _, c := range p.Counts {
			if c.Namespace != "" {
				seen[c.Namespace] = true
			}
		}
	}
}

// WorkloadScope is one namespace and workload kind that holds inferred
// services: what a caller must be allowed to list to see them.
type WorkloadScope struct {
	Namespace string
	Kind      string
}

// InferredWorkloadScopes lists, sorted and unique, the (namespace, kind) pairs
// of the snapshot's inferred services, of the workloads its replacements
// replaced or a Pacto target runs as, of the GitOps objects their
// repositories or repository reasons were read from, and of its provider
// counts and workload objects: a count is workload data too, so a caller must
// be allowed to list its kind to see it. A zero count discloses nothing and
// needs no review.
func (q *Query) InferredWorkloadScopes() []WorkloadScope {
	seen := map[WorkloadScope]bool{}
	for _, s := range q.snap.Services {
		if !s.Inferred() {
			continue
		}
		seen[WorkloadScope{s.Workload.Namespace, s.Workload.Kind}] = true
		for _, r := range s.Workload.Repositories {
			if r.Namespace != "" {
				seen[WorkloadScope{r.Namespace, r.Kind}] = true
			}
		}
		for _, sc := range s.Workload.repositoryUnknownFrom {
			if sc.Namespace != "" {
				seen[sc] = true
			}
		}
	}
	for _, r := range q.snap.Replacements {
		seen[WorkloadScope{r.workload.namespace, r.workload.kind}] = true
	}
	for _, r := range q.snap.replacedNames {
		seen[WorkloadScope{r.workload.namespace, r.workload.kind}] = true
	}
	for _, p := range q.snap.Providers {
		for _, c := range p.Counts {
			if c.N > 0 {
				seen[WorkloadScope{c.Namespace, c.Kind}] = true
			}
		}
		for _, o := range p.Objects {
			if workloadKind(o.Kind) && o.Namespace != "" {
				seen[WorkloadScope{o.Namespace, o.Kind}] = true
			}
		}
	}
	return slices.SortedFunc(maps.Keys(seen), func(a, b WorkloadScope) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Kind, b.Kind))
	})
}

// callerNamespace is the namespace of a k8s caller (see [ParseK8sCaller]),
// false for a caller with no namespace.
func callerNamespace(c UncontractedCaller) (string, bool) {
	_, ns, _, _, ok := ParseK8sCaller(c.Caller)
	return ns, ok
}

// Restrict is the snapshot as a caller who may read only the namespaces
// visible says yes to sees it. A Kubernetes target in another namespace is
// gone, and so is a service none of whose Kubernetes targets they may read,
// with its revisions, its targets everywhere and its edges. A service known
// only from a registry or files, with no Kubernetes target, has no namespace:
// visible("") decides it, and a caller who may read every namespace says yes.
// Two such services stay for a caller visible("") refuses, when every
// Kubernetes source is available, so they truly have no Kubernetes placement:
// a service whose contract revisions declare a platform offering, and a golden
// path. Its revisions, owner and offering show and all its targets are gone
// from the view, so no target name an adapter chose reaches the caller. A
// golden path keeps only the revisions that declare service.template. A
// visible service whose declared origin names a hidden golden path keeps the
// origin's ref and digest without the path, and a visible golden path counts
// the hidden services created from it, never naming them.
//
// An edge between a visible and a hidden service keeps the visible side:
// each visible service counts the hidden services it depends on and that
// depend on it, never naming them. Its own declared dependency on a hidden
// service stays as a Hidden relationship, its resolution blanked. Counts,
// sources, limitations and the snapshot id are recomputed, so nothing is
// derived from what was hidden.
//
// A caller who may not read the cluster scope (visible("") false) never sees a
// name with no namespace to check: every uncontracted caller outside
// Kubernetes, whatever source observed it, is hidden and counted into the
// service it calls, and the unresolved observed names of each source become
// one count. Every uncontracted caller Restrict removes from a visible service
// is counted into its hidden dependents.
//
// An inferred service shows only when visible says yes to its namespace and
// workloadVisible says yes to its namespace and workload kind: Kubernetes
// grants Pacto read and workload read apart, and the image, ports and probes
// of an inferred service are workload data. A nil workloadVisible hides every
// inferred service. A hidden inferred service is counted like any other, on
// the declared service it calls, and a visible one counts the hidden services
// it calls. A replacement shows only when its declared service and the
// workload it replaced are both visible. Restrict returns q when nothing is
// hidden.
func (q *Query) Restrict(visible func(namespace string) bool, workloadVisible func(namespace, kind string) bool) *Query {
	s := q.base
	hiddenNS := map[string]bool{}
	for _, ns := range q.Namespaces() {
		if !visible(ns) {
			hiddenNS[ns] = true
		}
	}
	hideUnreadable(s, visible, hiddenNS)
	restricted := !visible("")
	hiddenSvc, offeringOnly := hiddenServices(s, hiddenNS, restricted, workloadVisible)
	if len(hiddenNS) == 0 && len(hiddenSvc) == 0 && len(offeringOnly) == 0 && keepsEveryName(s, restricted, workloadHidden(hiddenNS, workloadVisible)) {
		if !providersNarrowed(s, visible, workloadVisible) {
			return q
		}
		// Only provider rows narrow: they feed no answer's completeness, so
		// the view stays as complete as q.
		out := *s
		out.Providers = restrictProviders(s.Providers, nil, nil, visible, workloadVisible, s.Clusters)
		out.SnapshotID = computeSnapshotID(&out)
		return NewQuery(&out)
	}

	out := *s
	var hiddenNames []string
	out.Targets = make(map[TargetKey]*TargetRecord, len(s.Targets))
	for k, t := range s.Targets {
		if (kubeTarget(t) && hiddenNS[t.Scope]) || hiddenSvc[t.ServiceKey] || offeringOnly[t.ServiceKey] {
			hiddenNames = append(hiddenNames, t.Name, string(k))
			continue
		}
		out.Targets[k] = t
	}
	out.Clusters = restrictClusters(s.Clusters, out.Targets)
	out.Revisions = restrictRevisions(s, hiddenSvc, offeringOnly)
	// The older revisions a golden path visible only as one drops are hidden
	// like a hidden service's: nothing visible may resolve to or name them.
	dropped := map[RevisionKey]bool{}
	for k := range s.Revisions {
		if out.Revisions[k] == nil {
			dropped[k] = true
			hiddenNames = append(hiddenNames, string(k))
		}
	}
	restrictRevalidation(&out, append(slices.Clone(s.Relationships), s.templateEdges...), hiddenSvc, dropped)
	hiddenDeps, hiddenDependents, hiddenConsumers, hiddenReferrers := restrictRelationships(&out, s.Relationships, hiddenSvc, dropped)
	restrictInferredEvidence(&out)
	out.hiddenConsumers = hiddenConsumers
	out.hiddenServices = hiddenSvc
	out.templateEdges = restrictTemplateEdges(&out, s.templateEdges, hiddenSvc, dropped)
	out.UncontractedCallers = restrictCallers(s, hiddenSvc, hiddenNS, restricted, hiddenDeps, hiddenDependents)
	out.InferredRelationships = restrictInferredEdges(s.InferredRelationships, hiddenSvc, hiddenDeps, hiddenDependents)
	restrictReplacements(&out, s, hiddenSvc, workloadHidden(hiddenNS, workloadVisible))
	out.hiddenDependents = hiddenDependents
	out.hiddenReferrers = hiddenReferrers
	origins, hiddenCreated := restrictOrigins(s, hiddenSvc)
	hiddenNames = append(hiddenNames, restrictServices(&out, s.Services, hiddenSvc, hiddenCounts{hiddenDeps, hiddenDependents, hiddenReferrers}, origins, hiddenCreated, workloadVisible)...)
	restrictExternals(&out, s)
	out.targetsHidden = targetsHidden(&out, s.Services, offeringOnly)
	out.hiddenProviders = hiddenProviders(&out, s.Services, hiddenSvc)
	out.hiddenProviderMatch = hiddenProviderMatch(s, out.hiddenProviders, hiddenSvc)
	restrictReports(&out, s)

	keep := func(k ServiceKey) bool { return !hiddenSvc[k] }
	out.reverseDeps = restrictIndex(s.reverseDeps, keep, keep)
	out.forwardDeps = restrictIndex(s.forwardDeps, keep, keep)
	out.observedReverse = restrictIndex(s.observedReverse, keep, keep)
	out.observedForward = restrictIndex(s.observedForward, keep, keep)
	out.inferredReverse = restrictIndex(s.inferredReverse, keep, keep)
	out.inferredForward = restrictIndex(s.inferredForward, keep, keep)
	out.forwardDepsByRevision = restrictIndex(s.forwardDepsByRevision, func(k RevisionKey) bool { return out.Revisions[k] != nil }, keep)

	recountSources(&out)

	hiddenNames = append(hiddenNames, slices.Collect(maps.Keys(hiddenNS))...)
	restrictLimitations(&out, s.Limitations, hiddenNames, restricted)
	restrictScope(&out, visible)
	out.Providers = restrictedProviders(&out, s.Providers, hiddenSvc, hiddenNames, visible, workloadVisible)
	out.Limitations = append(out.Limitations, Limitation{Code: LimitationHiddenByPermissions, Message: hiddenMessage + kindHiddenNote(s, hiddenSvc, hiddenNS)})
	sortSnapshotLimitations(&out)
	out.Completeness = CompletenessPartial
	out.workloadOnlyPartial = false
	out.SnapshotID = computeSnapshotID(&out)
	return NewQuery(&out)
}

// kindHiddenNote is the sentence a restricted answer adds when it hides an
// inferred service whose namespace the viewer may read: only workload read of
// its kind is missing, so the namespace sentence alone would be wrong.
func kindHiddenNote(s *FleetSnapshot, hiddenSvc map[ServiceKey]bool, hiddenNS map[string]bool) string {
	for k := range hiddenSvc {
		if svc := s.Services[k]; svc.Inferred() && !hiddenNS[svc.Workload.Namespace] {
			return "; some inferred services are hidden because you may not list their workload kind"
		}
	}
	return ""
}

// restrictReports keeps the provider reports a restricted view may hold.
func restrictReports(out, s *FleetSnapshot) {
	// An allowlist: a report stays only when its consumer is visible, so an
	// orphaned report or one about a hidden service never reaches the view.
	// A visible consumer's report about a provider the viewer may not see
	// keeps its phase, which raises attention, but never names the provider:
	// its adapter, external reference and signing identity go. Each hidden
	// producer gets an opaque name instead, so two of them stay two origins
	// and can still disagree. Its usage and cost go whole, since no surface
	// shows a resource of a hidden provider.
	hiddenUse := hiddenUseKeys(out)
	out.Provisioning = slices.DeleteFunc(slices.Clone(s.Provisioning), func(rec ProvisioningRecord) bool { return out.Services[rec.ServiceKey] == nil })
	hidden := func(rec *ProvisioningRecord) bool {
		return hiddenUse[provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}]
	}
	producers := map[string]bool{}
	for i := range out.Provisioning {
		if rec := &out.Provisioning[i]; hidden(rec) {
			producers[rec.Producer] = true
		}
	}
	alias := map[string]string{}
	for i, p := range slices.Sorted(maps.Keys(producers)) {
		alias[p] = fmt.Sprintf("hidden producer %d", i+1)
	}
	for i := range out.Provisioning {
		if rec := &out.Provisioning[i]; hidden(rec) {
			rec.Provider, rec.ExternalRef, rec.Producer, rec.ProducerKeyID = "", "", alias[rec.Producer], ""
		}
	}
	out.Usage = slices.DeleteFunc(slices.Clone(s.Usage), func(rec UsageRecord) bool {
		return out.Services[rec.ServiceKey] == nil || hiddenUse[provisioningKey{rec.ServiceKey, rec.Digest, rec.Kind, rec.Name}]
	})
}

// hiddenUseKeys is every entry a visible consumer's report may name on a
// provider the viewer may not see: its hidden, declared dependency or
// configuration reference edges, keyed as a report names them.
func hiddenUseKeys(out *FleetSnapshot) map[provisioningKey]bool {
	keys := map[provisioningKey]bool{}
	for i := range out.Relationships {
		rel := &out.Relationships[i]
		if r := out.Revisions[rel.FromRevision]; r != nil && hiddenProviderEdge(rel, rel.FromService) {
			keys[provisioningKey{rel.FromService, r.Digest, provisioningKind(rel.Type), rel.To}] = true
		}
	}
	return keys
}

// restrictRevisions is the revisions of s's visible services. A golden path
// visible only as one keeps only the revisions that declare service.template.
func restrictRevisions(s *FleetSnapshot, hiddenSvc, offeringOnly map[ServiceKey]bool) map[RevisionKey]*ContractRevision {
	out := make(map[RevisionKey]*ContractRevision, len(s.Revisions))
	for k, r := range s.Revisions {
		if !hiddenSvc[r.ServiceKey] && (!offeringOnly[r.ServiceKey] || !s.Services[r.ServiceKey].Template || declaresTemplate(r)) {
			out[k] = r
		}
	}
	return out
}

// targetsHidden is offeringOnly plus every visible service of out that lost a
// target to the view. out.Services must already be restricted.
func targetsHidden(out *FleetSnapshot, services map[ServiceKey]*ServiceRecord, offeringOnly map[ServiceKey]bool) map[ServiceKey]bool {
	for k, svc := range out.Services {
		if !svc.Inferred() && len(svc.Targets) != len(services[k].Targets) {
			offeringOnly[k] = true
		}
	}
	return offeringOnly
}

// keepsEveryName reports whether a caller restricted or not may see every
// name with no namespace, every provisioning and usage report of s and every
// workload its replacements replaced (hidden says which workloads they may
// not see).
func keepsEveryName(s *FleetSnapshot, restricted bool, hidden func(namespace, kind string) bool) bool {
	return (!restricted || !hasBareNames(s)) && provisioningVisible(s) && !replacementHidden(s, hidden) && !repositoryHidden(s, hidden)
}

// repositoryHidden reports whether some inferred service of s holds a
// repository, or a repository reason, read from an object hidden says no to,
// or from one with no namespace to check.
func repositoryHidden(s *FleetSnapshot, hidden func(namespace, kind string) bool) bool {
	for _, svc := range s.Services {
		if svc.Inferred() && (slices.ContainsFunc(svc.Workload.Repositories, func(r InferredRepository) bool { return r.Namespace == "" || hidden(r.Namespace, r.Kind) }) ||
			slices.ContainsFunc(svc.Workload.repositoryUnknownFrom, func(sc WorkloadScope) bool { return sc.Namespace == "" || hidden(sc.Namespace, sc.Kind) })) {
			return true
		}
	}
	return false
}

// provisioningVisible reports whether every provisioning and usage report of
// s is about a service s holds.
func provisioningVisible(s *FleetSnapshot) bool {
	return !slices.ContainsFunc(s.Provisioning, func(rec ProvisioningRecord) bool { return s.Services[rec.ServiceKey] == nil }) &&
		!slices.ContainsFunc(s.Usage, func(rec UsageRecord) bool { return s.Services[rec.ServiceKey] == nil })
}

// restrictCallers is s's uncontracted callers without those of a hidden
// service, those in a hidden namespace and those a hidden service shares a name
// with: such a caller may be that service's call, and its ambiguity counts it.
// With hideBare, every caller with no namespace goes. A k8s: caller that does
// not parse names a namespace no permission check can read, so it always goes.
// A caller that is a hidden inferred service goes too. Each caller removed
// from a visible service is counted into its dependents, and a hidden callee
// into the dependencies of the visible inferred service that called it.
func restrictCallers(s *FleetSnapshot, hiddenSvc map[ServiceKey]bool, hiddenNS map[string]bool, hideBare bool, hiddenDeps, dependents map[ServiceKey]map[ServiceKey]bool) []UncontractedCaller {
	hiddenNames := map[string]bool{}
	for k := range hiddenSvc {
		hiddenNames[s.Services[k].Name] = true
	}
	return slices.DeleteFunc(slices.Clone(s.UncontractedCallers), func(c UncontractedCaller) bool {
		if hiddenSvc[c.ToService] {
			if c.Entity != "" && !hiddenSvc[c.Entity] {
				if hiddenDeps[c.Entity] == nil {
					hiddenDeps[c.Entity] = map[ServiceKey]bool{}
				}
				hiddenDeps[c.Entity][c.ToService] = true
			}
			return true
		}
		ns, k8s := callerNamespace(c)
		malformed := !k8s && strings.HasPrefix(c.Caller, "k8s:")
		if !hiddenSvc[c.Entity] && ((k8s && !hiddenNS[ns]) || (!k8s && !malformed && !hideBare && !hiddenNames[c.Caller])) {
			return false
		}
		if dependents[c.ToService] == nil {
			dependents[c.ToService] = map[ServiceKey]bool{}
		}
		dependents[c.ToService][hiddenCallerKey(c)] = true
		return true
	})
}

// hiddenCallerKey is the key a hidden uncontracted caller counts by: its
// inferred service, so its inferred edge to the same service counts it once.
func hiddenCallerKey(c UncontractedCaller) ServiceKey {
	if c.Entity != "" {
		return c.Entity
	}
	// ponytail: a pseudo key only counted, never emitted, for a caller with
	// no entity, so it and a service of the same name count twice.
	return ServiceKey("caller:" + c.Caller)
}

// restrictInferredEdges is rels without the edges a hidden service ends. A
// hidden caller counts in the visible callee's dependents and a hidden callee
// in the visible caller's dependencies, both by the hidden end's key, so a
// workload also counted as an uncontracted caller counts once. An external
// end is never hidden; an edge from a hidden caller to one goes uncounted,
// as its record drops the caller.
func restrictInferredEdges(rels []Relationship, hiddenSvc map[ServiceKey]bool, deps, dependents map[ServiceKey]map[ServiceKey]bool) []Relationship {
	count := func(m map[ServiceKey]map[ServiceKey]bool, at, other ServiceKey) {
		if m[at] == nil {
			m[at] = map[ServiceKey]bool{}
		}
		m[at][other] = true
	}
	return slices.DeleteFunc(slices.Clone(rels), func(rel Relationship) bool {
		from, to := hiddenSvc[rel.FromService], hiddenSvc[rel.ToService]
		switch {
		case from && to:
		case from && rel.ToService != "":
			count(dependents, rel.ToService, rel.FromService)
		case to:
			count(deps, rel.FromService, rel.ToService)
		}
		return from || to
	})
}

// bareCaller reports whether c carries no namespace to check permissions
// against: any caller outside Kubernetes, whatever source observed it, and a
// k8s: caller that does not parse.
func bareCaller(c UncontractedCaller) bool {
	_, ok := callerNamespace(c)
	return !ok
}

// hasBareNames reports whether s holds a name with no namespace: a bare
// caller or an unresolved observed name. A cluster-scoped provider object is
// left to providersNarrowed: it hides from a row, never from an answer.
func hasBareNames(s *FleetSnapshot) bool {
	return slices.ContainsFunc(s.UncontractedCallers, bareCaller) ||
		slices.ContainsFunc(s.Limitations, func(l Limitation) bool { return l.Code == LimitationObservedIdentityUnresolved })
}

// unresolvedCounts drops the unresolved observed names from limitations and
// returns them with how many each source had.
func unresolvedCounts(limitations []Limitation) ([]Limitation, map[string]int) {
	counts := map[string]int{}
	return slices.DeleteFunc(limitations, func(l Limitation) bool {
		if l.Code == LimitationObservedIdentityUnresolved {
			counts[l.Source]++
			return true
		}
		return false
	}), counts
}

// rebuiltLimitation reports whether restrictLimitations rebuilds l from what
// a restricted view still holds rather than keeping it.
func rebuiltLimitation(l Limitation) bool {
	switch l.Code {
	// A conflict lists values hidden targets may have reported.
	case LimitationDomainConflict, LimitationOfferingConflict, LimitationOwnerConflict,
		// A fleet-wide count of reports, counts of clusterless targets and
		// per-source counts of inferred records.
		LimitationProvisioningTruncated, LimitationUsageTruncated, LimitationClusterUnknown, LimitationClusterConflict, LimitationDeclarationUnverified, LimitationServiceGroupAmbiguous:
		return true
	case LimitationSourceRecordInvalid:
		// Per-source counts of records that may be hidden.
		return strings.Contains(l.Message, clusterInvalidPhrase) || strings.Contains(l.Message, droppedFieldsPhrase)
	}
	return false
}

// restrictLimitations keeps the limitations that name nothing hidden and
// rebuilds the domain, offering and owner conflicts from what out still holds.
// With hideBare, each source's unresolved observed names become one count.
func restrictLimitations(out *FleetSnapshot, limitations []Limitation, hiddenNames []string, hideBare bool) {
	limitations = slices.Clone(limitations)
	var unresolved map[string]int
	if hideBare {
		limitations, unresolved = unresolvedCounts(limitations)
	}
	// ponytail: a limitation's subject is only in its prose, so any that mentions
	// a hidden name goes, even where the match is a coincidence. It costs
	// limitations x hidden names substring checks, once per snapshot and set of
	// visible namespaces. Structured subjects on Limitation would make it exact
	// and linear.
	out.Limitations = slices.DeleteFunc(limitations, func(l Limitation) bool {
		return rebuiltLimitation(l) || slices.ContainsFunc(hiddenNames, func(n string) bool { return n != "" && strings.Contains(l.Message, n) })
	})
	for _, svc := range out.Services {
		_, _, dc := businessDomainOf(out, svc)
		_, _, oc := offeringOf(out, svc)
		out.Limitations = append(append(append(out.Limitations, domainConflict(svc.Name, dc)...), offeringConflict(svc.Name, oc)...), ownerConflict(out, svc)...)
	}
	out.Limitations = append(out.Limitations, droppedReports(out, out.provisioningDropped, LimitationProvisioningTruncated, "provisioning", MaxProvisioningRecords)...)
	out.Limitations = append(out.Limitations, droppedReports(out, out.usageDropped, LimitationUsageTruncated, "usage", MaxUsageRecords)...)
	out.Limitations = append(out.Limitations, clusterUnknownOf(out)...)
	out.Limitations = append(out.Limitations, clusterRecordProblemsOf(out)...)
	out.Limitations = append(out.Limitations, inferredSnapshotLimitations(out)...)
	for _, src := range slices.Sorted(maps.Keys(unresolved)) {
		out.Limitations = append(out.Limitations, Limitation{
			Code: LimitationObservedIdentityUnresolved, Source: src,
			Message: strconv.Itoa(unresolved[src]) + " names observed by " + src + " are not shown: they carry no namespace to check permissions against",
		})
	}
}

// restrictClusters is the clusters some target of targets carries.
func restrictClusters(clusters []ClusterRecord, targets map[TargetKey]*TargetRecord) []ClusterRecord {
	carried := map[string]bool{}
	for _, t := range targets {
		carried[t.Cluster] = true
	}
	return slices.DeleteFunc(slices.Clone(clusters), func(c ClusterRecord) bool { return !carried[c.ID] })
}

// clusterUnknownOf rebuilds CLUSTER_UNKNOWN from the targets out holds, by
// the rule Build applies: with several clusters one per source that delivered
// a clusterless target; with one, one per source whose clusterless targets
// merged into clustered ones, or a single informational count when none did.
func clusterUnknownOf(out *FleetSnapshot) []Limitation {
	counts, merged, clusterName, total := map[string]int{}, map[string][]string{}, "", 0
	for k, t := range out.Targets {
		for _, src := range t.clusterUnknownFrom {
			counts[src]++
			total++
			if t.Cluster != "" {
				merged[src], clusterName = append(merged[src], string(k)), t.ClusterName
			}
		}
	}
	var lims []Limitation
	switch {
	case out.clusterKeyed:
		for _, src := range slices.Sorted(maps.Keys(counts)) {
			lims = append(lims, clusterUnknownMulti(src, counts[src]))
		}
	case len(merged) > 0:
		for _, src := range slices.Sorted(maps.Keys(merged)) {
			lims = append(lims, clusterUnknownMerged(src, clusterName, slices.Sorted(slices.Values(merged[src]))))
		}
	case total > 0:
		lims = append(lims, clusterUnknownInfo(total))
	}
	return lims
}

// clusterRecordProblemsOf rebuilds CLUSTER_CONFLICT and the invalid cluster
// id SOURCE_RECORD_INVALID from the targets out holds, one per source that
// reported a visible affected target.
func clusterRecordProblemsOf(out *FleetSnapshot) []Limitation {
	conflicts, invalid := map[string]int{}, map[string]int{}
	for _, t := range out.Targets {
		for _, src := range t.clusterConflictFrom {
			conflicts[src]++
		}
		for _, src := range t.clusterInvalidFrom {
			invalid[src]++
		}
	}
	var lims []Limitation
	for _, src := range slices.Sorted(maps.Keys(conflicts)) {
		lims = append(lims, clusterConflict(src, conflicts[src]))
	}
	for _, src := range slices.Sorted(maps.Keys(invalid)) {
		lims = append(lims, clusterInvalid(src, strconv.Itoa(invalid[src])+" on targets you can see"))
	}
	return lims
}

// droppedReports is the truncation limitation (code) of a restricted view:
// how many reports of the kind what about its visible consumers the fleet-wide
// bound dropped, none when it dropped none of theirs.
func droppedReports(out *FleetSnapshot, dropped map[ServiceKey]int, code, what string, limit int) []Limitation {
	n := 0
	for k := range out.Services {
		n += dropped[k]
	}
	if n == 0 {
		return nil
	}
	return []Limitation{{Code: code, Message: fmt.Sprintf(
		"%d %s reports about the services you can see were dropped: the fleet holds more than the limit of %d", n, what, limit)}}
}

// workloadHidden reports whether the viewer may not see the workloads of a
// kind in a namespace: an inferred service's or a replaced one's. It is the
// one place workload visibility is decided.
func workloadHidden(hiddenNS map[string]bool, workloadVisible func(namespace, kind string) bool) func(namespace, kind string) bool {
	return func(namespace, kind string) bool {
		return hiddenNS[namespace] || workloadVisible == nil || !workloadVisible(namespace, kind)
	}
}

// kubeTarget reports whether t is a Kubernetes target with a namespace, the
// only kind a caller's permissions decide.
func kubeTarget(t *TargetRecord) bool { return t.Kind == SourceKindKubernetes && t.Scope != "" }

// hiddenServices is the services whose Kubernetes targets all run in a hidden
// namespace, and with hideUnscoped the services with no Kubernetes target. Of
// those, a service that declares its offering and a golden path stay visible
// when the snapshot knows they have no Kubernetes placement; they are returned
// in offeringOnly. An inferred service has no target: its namespace and
// workloadVisible (nil hides it) decide it alone.
func hiddenServices(s *FleetSnapshot, hiddenNS map[string]bool, hideUnscoped bool, workloadVisible func(namespace, kind string) bool) (hiddenSvc, offeringOnly map[ServiceKey]bool) {
	hiddenSvc, offeringOnly = map[ServiceKey]bool{}, map[ServiceKey]bool{}
	placed := kubePlacementKnown(s)
	for k, svc := range s.Services {
		if svc.Inferred() {
			if workloadHidden(hiddenNS, workloadVisible)(svc.Workload.Namespace, svc.Workload.Kind) {
				hiddenSvc[k] = true
			}
			continue
		}
		hidden, shown, scoped := kubeScope(s, svc, hiddenNS)
		switch {
		case hidden && !shown:
			hiddenSvc[k] = true
		case !scoped && hideUnscoped && (svc.OfferingProvenance == ProvenanceDeclared || svc.Template) && placed:
			offeringOnly[k] = true
		case !scoped && hideUnscoped:
			hiddenSvc[k] = true
		}
	}
	return hiddenSvc, offeringOnly
}

// kubeScope reports whether some Kubernetes target of svc runs in a hidden
// namespace, whether some runs in a visible one, and whether it has any.
func kubeScope(s *FleetSnapshot, svc *ServiceRecord, hiddenNS map[string]bool) (hidden, shown, scoped bool) {
	for _, tk := range svc.Targets {
		if t := s.Targets[tk]; kubeTarget(t) {
			scoped = true
			hidden = hidden || hiddenNS[t.Scope]
			shown = shown || !hiddenNS[t.Scope]
		}
	}
	return hidden, shown, scoped
}

// kubePlacementKnown reports whether a service with no Kubernetes target in s
// truly has none: every Kubernetes source is available, or there is none. An
// unavailable or partial source contributes no records, so the absence of a
// target proves nothing.
func kubePlacementKnown(s *FleetSnapshot) bool {
	return !slices.ContainsFunc(s.Sources, func(src SourceState) bool {
		return src.Kind == SourceKindKubernetes && src.Status != SourceAvailable
	})
}

// targetPlacementKnown reports whether a service with no target in s truly
// runs nowhere: no source that reports operational targets (Kubernetes, the
// Evidence Server, a target state file or a target snapshot) is unavailable or
// partial.
func targetPlacementKnown(s *FleetSnapshot) bool {
	id, _ := firstIncompleteTargetSource(s)
	return id == ""
}

// hasTargetSource reports whether any source in s reports operational targets:
// one of a target kind, or any that contributed a target. With none, Pacto
// looked at no runtime and a missing target proves nothing.
func hasTargetSource(s *FleetSnapshot) bool {
	return len(s.Targets) > 0 || slices.ContainsFunc(s.Sources, func(src SourceState) bool { return slices.Contains(targetSourceKinds, src.Kind) })
}

// targetSourceKinds are the source kinds that report operational targets.
var targetSourceKinds = []string{SourceKindKubernetes, SourceKindEvidenceHTTP, SourceKindTargetState, SourceKindTargetSnapshot}

// firstIncompleteTargetSource is the first source reporting operational
// targets that is not available, and its status; "" when there is none.
func firstIncompleteTargetSource(s *FleetSnapshot) (id, status string) {
	for _, src := range s.Sources {
		if src.Status != SourceAvailable && slices.Contains(targetSourceKinds, src.Kind) {
			return src.ID, string(src.Status)
		}
	}
	return "", ""
}

// hiddenProviders counts, per offering that keeps a visible provider in out,
// its providers among hiddenSvc. An offering only hidden services provide is
// never revealed.
func hiddenProviders(out *FleetSnapshot, services map[ServiceKey]*ServiceRecord, hiddenSvc map[ServiceKey]bool) map[string]int {
	visible := map[string]bool{}
	for _, svc := range out.Services {
		visible[svc.Offering] = true
	}
	counts := map[string]int{}
	for k := range hiddenSvc {
		if o := services[k].Offering; o != "" && visible[o] {
			counts[o]++
		}
	}
	return counts
}

// hiddenProviderMatch is, per offering counted in hidden, what its hidden
// providers in s may be named by.
func hiddenProviderMatch(s *FleetSnapshot, hidden map[string]int, hiddenSvc map[ServiceKey]bool) map[string]providerMatch {
	keys := map[string][]ServiceKey{}
	for k := range hiddenSvc {
		if o := s.Services[k].Offering; hidden[o] > 0 {
			keys[o] = append(keys[o], k)
		}
	}
	out := make(map[string]providerMatch, len(keys))
	for o, ks := range keys {
		out[o] = providerMatchOf(s, ks)
	}
	return out
}

// restrictRelationships sets out.Relationships to rels without the hidden
// services, and returns, per visible service, the hidden services it depends
// on, that depend on it and that consume it (see [ConsumesEdge]; observed
// callers depend on a service without consuming it). An edge to a visible
// service that resolved to a dropped revision loses that resolution. It also
// returns the hidden services that reference each one through a policy
// reference.
func restrictRelationships(out *FleetSnapshot, rels []Relationship, hiddenSvc map[ServiceKey]bool, dropped map[RevisionKey]bool) (deps, dependents, consumers, referrers map[ServiceKey]map[ServiceKey]bool) {
	deps, dependents, consumers = map[ServiceKey]map[ServiceKey]bool{}, map[ServiceKey]map[ServiceKey]bool{}, map[ServiceKey]map[ServiceKey]bool{}
	referrers = map[ServiceKey]map[ServiceKey]bool{}
	count := func(m map[ServiceKey]map[ServiceKey]bool, at, other ServiceKey) {
		if m[at] == nil {
			m[at] = map[ServiceKey]bool{}
		}
		m[at][other] = true
	}
	out.Relationships = make([]Relationship, 0, len(rels))
	out.hiddenUnresolvedRefs = slices.Clip(out.hiddenUnresolvedRefs) // never appends into the source snapshot's list
	for _, rel := range rels {
		from, to := hiddenSvc[rel.FromService], hiddenSvc[rel.ToService]
		switch {
		case from && to:
			continue
		case from:
			// Observed callers count too: they are dependents the viewer cannot see.
			if rel.ToService != "" && (rel.Type == RelationshipDependency || ConsumesEdge(rel)) {
				count(dependents, rel.ToService, rel.FromService)
			}
			if ConsumesEdge(rel) {
				count(consumers, rel.ToService, rel.FromService)
			}
			countPolicyReferrer(referrers, rel)
			out.hiddenUnresolvedRefs = appendHiddenUnresolved(out.hiddenUnresolvedRefs, rel)
			continue
		case to:
			if rel.Provenance == ProvenanceObserved {
				continue // runtime evidence about the hidden service, not the caller's declaration
			}
			if ConsumesEdge(rel) {
				count(deps, rel.FromService, rel.ToService)
			}
			hideProvider(&rel)
		case dropped[rel.ResolvedRevision]:
			rel.ResolvedRevision, rel.LockedDigest = "", ""
		}
		out.Relationships = append(out.Relationships, rel)
	}
	return deps, dependents, consumers, referrers
}

// countPolicyReferrer records rel, an edge from a hidden service, in
// referrers when it is a resolved policy reference.
func countPolicyReferrer(referrers map[ServiceKey]map[ServiceKey]bool, rel Relationship) {
	if rel.Type != RelationshipPolicyRef || rel.ToService == "" {
		return
	}
	if referrers[rel.ToService] == nil {
		referrers[rel.ToService] = map[ServiceKey]bool{}
	}
	referrers[rel.ToService][rel.FromService] = true
}

// hideProvider blanks what rel, an edge to a hidden service, says about it.
func hideProvider(rel *Relationship) {
	rel.hiddenTo = rel.ToService // keeps tallyServiceEdges collapsing it like a visible provider
	rel.ToService, rel.ResolvedRevision, rel.Hidden, rel.Reason, rel.RangeStatus = "", "", true, hiddenMessage, ""
	// What the hidden provider offers and deprecates is its own contract.
	rel.Unmet, rel.DeprecatedInterfaces, rel.ProviderLifecycle = nil, nil, nil
	if rel.GuaranteeStatus != "" {
		rel.GuaranteeStatus, rel.GuaranteeReason = GuaranteeUnknown, hiddenMessage
	}
}

// restrictRevalidation validates again, against out, each visible revision
// whose configuration references resolve to a hidden service, directly or
// through a provider's own configuration ref, so no finding carries what the
// hidden provider declares: the reference becomes unverified. A revision is
// copied, never changed in place. out.Services must not be restricted yet: its
// statuses read the revalidated revisions.
func restrictRevalidation(out *FleetSnapshot, rels []Relationship, hiddenSvc map[ServiceKey]bool, dropped map[RevisionKey]bool) {
	stale := map[RevisionKey]bool{}
	// ponytail: a fixpoint costing config refs x chain depth passes.
	for changed := true; changed; {
		changed = false
		for _, rel := range rels {
			if rel.Type == RelationshipConfigRef && !stale[rel.FromRevision] && out.Revisions[rel.FromRevision] != nil &&
				(hiddenSvc[rel.ToService] || stale[rel.ResolvedRevision] || dropped[rel.ResolvedRevision]) {
				stale[rel.FromRevision], changed = true, true
			}
		}
	}
	if len(stale) == 0 {
		return
	}
	revs := make(map[RevisionKey]*ContractRevision, len(out.Revisions))
	for k, r := range out.Revisions {
		if stale[k] {
			cp := *r
			r = &cp
		}
		revs[k] = r
	}
	out.Revisions = revs
	for k := range stale {
		revalidateRevision(out, revs[k])
	}
}

// restrictTemplateEdges is the golden path edges of the visible revisions of
// out, restricted like Relationships. A golden path consumes nothing, so its
// edges to hidden services count nowhere. out.Revisions must already be
// restricted.
func restrictTemplateEdges(out *FleetSnapshot, edges []Relationship, hiddenSvc map[ServiceKey]bool, dropped map[RevisionKey]bool) []Relationship {
	var scratch FleetSnapshot
	restrictRelationships(&scratch, edges, hiddenSvc, dropped)
	return slices.DeleteFunc(scratch.Relationships, func(rel Relationship) bool { return out.Revisions[rel.FromRevision] == nil })
}

// restrictOrigins reads only s, never writing to it: for each visible service
// whose declared origin resolves to a hidden golden path, a fresh origin
// without that path, and per visible golden path the number of hidden services
// created from it.
func restrictOrigins(s *FleetSnapshot, hiddenSvc map[ServiceKey]bool) (map[ServiceKey]*ServiceOrigin, map[ServiceKey]int) {
	origins, hiddenCreated := map[ServiceKey]*ServiceOrigin{}, map[ServiceKey]int{}
	for k, svc := range s.Services {
		o := svc.Origin
		switch {
		case o == nil || svc.Inferred() || o.GoldenPath == "" || hiddenSvc[k] == hiddenSvc[o.GoldenPath]:
		case hiddenSvc[k]:
			hiddenCreated[o.GoldenPath]++
		default:
			cp := *o
			cp.GoldenPath, cp.Revision, cp.Reason = "", "", "the golden path it names is not in this view"
			origins[k] = &cp
		}
	}
	return origins, hiddenCreated
}

// hiddenCounts are, per visible service, the hidden services it depends on,
// that depend on it and that reference it through a policy reference.
type hiddenCounts struct {
	deps, dependents, referrers map[ServiceKey]map[ServiceKey]bool
}

// referrersBeyondDependents counts the hidden policy referrers of k that are
// not already counted as its hidden dependents.
func (h hiddenCounts) referrersBeyondDependents(k ServiceKey) int {
	n := 0
	for r := range h.referrers[k] {
		if !h.dependents[k][r] {
			n++
		}
	}
	return n
}

// restrictServices sets out.Services to the visible services, copying any
// that lost a target or a revision, gained a hidden count or changed origin,
// and returns the names and keys of the hidden ones. It never writes through a
// record of services. out.Targets and out.Revisions must already be restricted.
func restrictServices(out *FleetSnapshot, services map[ServiceKey]*ServiceRecord, hiddenSvc map[ServiceKey]bool, hidden hiddenCounts, origins map[ServiceKey]*ServiceOrigin, hiddenCreated map[ServiceKey]int, workloadVisible func(namespace, kind string) bool) []string {
	var hiddenNames []string
	out.Services = make(map[ServiceKey]*ServiceRecord, len(services))
	out.revisionsHidden = map[ServiceKey]bool{}
	for k, svc := range services {
		if hiddenSvc[k] {
			hiddenNames = append(hiddenNames, svc.Name, string(k))
			continue
		}
		if svc.Inferred() {
			out.Services[k] = restrictInferred(svc, hidden, workloadVisible)
			continue
		}
		targets := slices.DeleteFunc(slices.Clone(svc.Targets), func(tk TargetKey) bool { return out.Targets[tk] == nil })
		revisions := slices.DeleteFunc(slices.Clone(svc.Revisions), func(rk RevisionKey) bool { return out.Revisions[rk] == nil })
		out.revisionsHidden[k] = len(revisions) != len(svc.Revisions)
		origin, reorigin := origins[k]
		// A revision revalidated against the view may change the status; it is
		// read last, once every revision of svc is known to be in out.
		referrers := hidden.referrersBeyondDependents(k)
		if len(targets) == len(svc.Targets) && len(revisions) == len(svc.Revisions) && hidden.deps[k] == nil && hidden.dependents[k] == nil && referrers == 0 &&
			!reorigin && hiddenCreated[k] == 0 && serviceStatus(out, svc) == svc.Status {
			out.Services[k] = svc
			continue
		}
		cp := *svc
		cp.Targets, cp.Revisions, cp.HiddenDependencies, cp.HiddenDependents = targets, revisions, len(hidden.deps[k]), len(hidden.dependents[k])
		cp.HiddenPolicyReferrers = referrers
		cp.HiddenCreated = hiddenCreated[k]
		if reorigin {
			cp.Origin = origin
		}
		cp.Status = serviceStatus(out, &cp)
		restrictBusinessDomain(out, &cp)
		restrictOffering(out, &cp)
		// A target-only owner may have come from a hidden target.
		cp.Owner, cp.OwnerProvenance = contract.Owner{}, ""
		deriveOwner(out, &cp)
		out.Services[k] = &cp
	}
	return hiddenNames
}

// restrictInferred is a visible inferred service as a restricted view holds
// it: a copy carrying its hidden counts when it has any, and without the
// repositories read from an object workloadVisible says no to (or one with no
// namespace), which then reads "hidden by your permissions". A repository
// reason read from such an object reads the same. The repository is another
// object's content the dashboard read, so the workload's own read never
// decides it. Nothing else of it was derived from what the view may hide.
func restrictInferred(svc *ServiceRecord, hidden hiddenCounts, workloadVisible func(namespace, kind string) bool) *ServiceRecord {
	listed := func(ns, kind string) bool { return ns != "" && workloadVisible != nil && workloadVisible(ns, kind) }
	repos := slices.DeleteFunc(slices.Clone(svc.Workload.Repositories), func(r InferredRepository) bool { return !listed(r.Namespace, r.Kind) })
	reposHidden := len(repos) != len(svc.Workload.Repositories) ||
		slices.ContainsFunc(svc.Workload.repositoryUnknownFrom, func(sc WorkloadScope) bool { return !listed(sc.Namespace, sc.Kind) })
	if hidden.deps[svc.Key] == nil && hidden.dependents[svc.Key] == nil && !reposHidden {
		return svc
	}
	cp := *svc
	cp.HiddenDependencies, cp.HiddenDependents = len(hidden.deps[svc.Key]), len(hidden.dependents[svc.Key])
	if reposHidden {
		w := *svc.Workload
		w.Repositories, w.RepositoryUnknown = repos, "hidden by your permissions"
		cp.Workload = &w
	}
	return &cp
}

// restrictBusinessDomain recomputes a service's business domain from what out
// still holds, so a domain a hidden target reported never shows, and restamps the
// visible targets, copied, when it changes.
func restrictBusinessDomain(out *FleetSnapshot, s *ServiceRecord) {
	d, unknown, _ := businessDomainOf(out, s)
	if d == s.BusinessDomain && unknown == s.BusinessDomainUnknown {
		return
	}
	s.BusinessDomain, s.BusinessDomainUnknown = d, unknown
	for _, tk := range s.Targets {
		t := *out.Targets[tk]
		t.BusinessDomain, t.BusinessDomainUnknown = d, unknown
		out.Targets[tk] = &t
	}
}

// restrictOffering recomputes a service's platform offering and summary from
// what out still holds, so a value only a hidden target reported never shows.
func restrictOffering(out *FleetSnapshot, s *ServiceRecord) {
	s.Offering, s.OfferingProvenance, _ = offeringOf(out, s)
	s.Summary, s.SummaryUnknown = summaryOf(out, s)
}

// recountSources recomputes each source's revision, target and inferred
// service counts from what out still holds.
func recountSources(out *FleetSnapshot) {
	out.Sources = slices.Clone(out.Sources)
	countInferred(out)
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
