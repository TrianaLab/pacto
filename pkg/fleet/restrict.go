package fleet

import (
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

// Namespaces lists, sorted, the namespaces the snapshot's Kubernetes targets
// and k8s uncontracted callers run in: the ones a caller's permissions decide.
func (q *Query) Namespaces() []string {
	seen := map[string]bool{}
	for _, t := range q.snap.Targets {
		if t.Kind == SourceKindKubernetes && t.Scope != "" {
			seen[t.Scope] = true
		}
	}
	for _, c := range q.snap.UncontractedCallers {
		if ns, ok := callerNamespace(c); ok {
			seen[ns] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// callerNamespace is the namespace of a k8s caller, false for a caller with
// no namespace.
func callerNamespace(c UncontractedCaller) (string, bool) {
	rest, k8s := strings.CutPrefix(c.Caller, "k8s:")
	ns, _, _ := strings.Cut(rest, "/")
	return ns, k8s
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
// is counted into its hidden dependents. Restrict returns q when nothing is
// hidden.
func (q *Query) Restrict(visible func(namespace string) bool) *Query {
	s := q.snap
	hiddenNS := map[string]bool{}
	for _, ns := range q.Namespaces() {
		if !visible(ns) {
			hiddenNS[ns] = true
		}
	}
	restricted := !visible("")
	hiddenSvc, offeringOnly := hiddenServices(s, hiddenNS, restricted)
	if len(hiddenNS) == 0 && len(hiddenSvc) == 0 && len(offeringOnly) == 0 && keepsEveryName(s, restricted) {
		return q
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
	out.hiddenConsumers = hiddenConsumers
	out.templateEdges = restrictTemplateEdges(&out, s.templateEdges, hiddenSvc, dropped)
	out.UncontractedCallers = restrictCallers(s, hiddenSvc, hiddenNS, restricted, hiddenDependents)
	origins, hiddenCreated := restrictOrigins(s, hiddenSvc)
	hiddenNames = append(hiddenNames, restrictServices(&out, s.Services, hiddenSvc, hiddenCounts{hiddenDeps, hiddenDependents, hiddenReferrers}, origins, hiddenCreated)...)
	out.targetsHidden = targetsHidden(&out, s.Services, offeringOnly)
	// An allowlist: a report stays only when its consumer is visible, so an
	// orphaned report or one about a hidden service never reaches the view.
	out.Provisioning = slices.DeleteFunc(slices.Clone(s.Provisioning), func(rec ProvisioningRecord) bool { return out.Services[rec.ServiceKey] == nil })

	keep := func(k ServiceKey) bool { return !hiddenSvc[k] }
	out.reverseDeps = restrictIndex(s.reverseDeps, keep, keep)
	out.forwardDeps = restrictIndex(s.forwardDeps, keep, keep)
	out.observedReverse = restrictIndex(s.observedReverse, keep, keep)
	out.observedForward = restrictIndex(s.observedForward, keep, keep)
	out.forwardDepsByRevision = restrictIndex(s.forwardDepsByRevision, func(k RevisionKey) bool { return out.Revisions[k] != nil }, keep)

	recountSources(&out)

	restrictLimitations(&out, s.Limitations, append(hiddenNames, slices.Collect(maps.Keys(hiddenNS))...), restricted)
	out.Limitations = append(out.Limitations, Limitation{Code: LimitationHiddenByPermissions, Message: hiddenMessage})
	sortSnapshotLimitations(&out)
	out.Completeness = CompletenessPartial
	out.SnapshotID = computeSnapshotID(&out)
	return NewQuery(&out)
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
		if len(svc.Targets) != len(services[k].Targets) {
			offeringOnly[k] = true
		}
	}
	return offeringOnly
}

// keepsEveryName reports whether a caller restricted or not may see every
// name with no namespace and every provisioning report of s.
func keepsEveryName(s *FleetSnapshot, restricted bool) bool {
	return (!restricted || !hasBareNames(s)) && provisioningVisible(s)
}

// provisioningVisible reports whether every provisioning report of s is about
// a service s holds.
func provisioningVisible(s *FleetSnapshot) bool {
	return !slices.ContainsFunc(s.Provisioning, func(rec ProvisioningRecord) bool { return s.Services[rec.ServiceKey] == nil })
}

// restrictCallers is s's uncontracted callers without those of a hidden
// service, those in a hidden namespace and those a hidden service shares a name
// with: such a caller may be that service's call, and its ambiguity counts it.
// With hideBare, every caller with no namespace goes. Each caller removed from
// a visible service is counted into its dependents.
func restrictCallers(s *FleetSnapshot, hiddenSvc map[ServiceKey]bool, hiddenNS map[string]bool, hideBare bool, dependents map[ServiceKey]map[ServiceKey]bool) []UncontractedCaller {
	hiddenNames := map[string]bool{}
	for k := range hiddenSvc {
		hiddenNames[s.Services[k].Name] = true
	}
	return slices.DeleteFunc(slices.Clone(s.UncontractedCallers), func(c UncontractedCaller) bool {
		if hiddenSvc[c.ToService] {
			return true
		}
		ns, k8s := callerNamespace(c)
		if (k8s && !hiddenNS[ns]) || (!k8s && !hideBare && !hiddenNames[c.Caller]) {
			return false
		}
		if dependents[c.ToService] == nil {
			dependents[c.ToService] = map[ServiceKey]bool{}
		}
		// ponytail: a pseudo key only counted, never emitted, so a caller and a
		// service of the same name count twice.
		dependents[c.ToService][ServiceKey("caller:"+c.Caller)] = true
		return true
	})
}

// bareCaller reports whether c carries no namespace to check permissions
// against: any caller outside Kubernetes, whatever source observed it.
func bareCaller(c UncontractedCaller) bool { return !strings.HasPrefix(c.Caller, "k8s:") }

// hasBareNames reports whether s holds a name with no namespace: a bare caller
// or an unresolved observed name.
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
		// A conflict lists values hidden targets may have reported, so it is
		// rebuilt from what out still holds below.
		if l.Code == LimitationDomainConflict || l.Code == LimitationOfferingConflict || l.Code == LimitationOwnerConflict {
			return true
		}
		// A fleet-wide count of reports; rebuilt below from the visible ones.
		if l.Code == LimitationProvisioningTruncated {
			return true
		}
		return slices.ContainsFunc(hiddenNames, func(n string) bool { return n != "" && strings.Contains(l.Message, n) })
	})
	for _, svc := range out.Services {
		_, _, dc := businessDomainOf(out, svc)
		_, _, oc := offeringOf(out, svc)
		out.Limitations = append(append(append(out.Limitations, domainConflict(svc.Name, dc)...), offeringConflict(svc.Name, oc)...), ownerConflict(out, svc)...)
	}
	out.Limitations = append(out.Limitations, provisioningTruncated(out)...)
	for _, src := range slices.Sorted(maps.Keys(unresolved)) {
		out.Limitations = append(out.Limitations, Limitation{
			Code: LimitationObservedIdentityUnresolved, Source: src,
			Message: strconv.Itoa(unresolved[src]) + " names observed by " + src + " are not shown: they carry no namespace to check permissions against",
		})
	}
}

// provisioningTruncated is the PROVISIONING_TRUNCATED limitation of a
// restricted view: how many reports about its visible consumers the fleet-wide
// bound dropped, none when it dropped none of theirs.
func provisioningTruncated(out *FleetSnapshot) []Limitation {
	dropped := 0
	for k := range out.Services {
		dropped += out.provisioningDropped[k]
	}
	if dropped == 0 {
		return nil
	}
	return []Limitation{{Code: LimitationProvisioningTruncated, Message: fmt.Sprintf(
		"%d provisioning reports about the services you can see were dropped: the fleet holds more than the limit of %d", dropped, MaxProvisioningRecords)}}
}

// kubeTarget reports whether t is a Kubernetes target with a namespace, the
// only kind a caller's permissions decide.
func kubeTarget(t *TargetRecord) bool { return t.Kind == SourceKindKubernetes && t.Scope != "" }

// hiddenServices is the services whose Kubernetes targets all run in a hidden
// namespace, and with hideUnscoped the services with no Kubernetes target. Of
// those, a service that declares its offering and a golden path stay visible
// when the snapshot knows they have no Kubernetes placement; they are returned
// in offeringOnly.
func hiddenServices(s *FleetSnapshot, hiddenNS map[string]bool, hideUnscoped bool) (hiddenSvc, offeringOnly map[ServiceKey]bool) {
	hiddenSvc, offeringOnly = map[ServiceKey]bool{}, map[ServiceKey]bool{}
	placed := kubePlacementKnown(s)
	for k, svc := range s.Services {
		hidden, shown, scoped := false, false, false
		for _, tk := range svc.Targets {
			if t := s.Targets[tk]; kubeTarget(t) {
				scoped = true
				hidden = hidden || hiddenNS[t.Scope]
				shown = shown || !hiddenNS[t.Scope]
			}
		}
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
// Evidence Server or a target state file) is unavailable or partial.
func targetPlacementKnown(s *FleetSnapshot) bool {
	return !slices.ContainsFunc(s.Sources, func(src SourceState) bool {
		return src.Status != SourceAvailable && slices.Contains([]string{SourceKindKubernetes, SourceKindEvidenceHTTP, SourceKindTargetState}, src.Kind)
	})
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
		case o == nil || o.GoldenPath == "" || hiddenSvc[k] == hiddenSvc[o.GoldenPath]:
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
func restrictServices(out *FleetSnapshot, services map[ServiceKey]*ServiceRecord, hiddenSvc map[ServiceKey]bool, hidden hiddenCounts, origins map[ServiceKey]*ServiceOrigin, hiddenCreated map[ServiceKey]int) []string {
	var hiddenNames []string
	out.Services = make(map[ServiceKey]*ServiceRecord, len(services))
	for k, svc := range services {
		if hiddenSvc[k] {
			hiddenNames = append(hiddenNames, svc.Name, string(k))
			continue
		}
		targets := slices.DeleteFunc(slices.Clone(svc.Targets), func(tk TargetKey) bool { return out.Targets[tk] == nil })
		revisions := slices.DeleteFunc(slices.Clone(svc.Revisions), func(rk RevisionKey) bool { return out.Revisions[rk] == nil })
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
		cp.Owner = contract.Owner{}
		deriveOwner(out, &cp)
		out.Services[k] = &cp
	}
	return hiddenNames
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
	s.Summary = summaryOf(out, s)
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
