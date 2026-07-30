package fleet

import (
	"sort"

	"github.com/trianalab/pacto/v3/pkg/finding"
)

// Link is a canonical, labelled navigation link between related entities. The
// backend provides these so a component never reconstructs a route from a raw key
// (requirement 2.4 and requirement 13).
type Link struct {
	Rel   string `json:"rel"`
	Label string `json:"label"`
	Route string `json:"route"`
}

// OwnershipInfo is an entity's ownership summary with per-revision conflicts.
type OwnershipInfo struct {
	Owner     string     `json:"owner,omitempty"`
	Ref       *EntityRef `json:"ref,omitempty"`
	Conflicts []string   `json:"conflicts,omitempty"`
}

// EntityDetail is the common, versioned envelope for any entity's full detail
// (requirement 2.4). Sections and Summary are kind-specific; identity,
// relationships, findings, evidence, ownership, limitations, links and actions
// are common. Links are canonical, so components cross-link without re-deriving.
type EntityDetail struct {
	Meta             ProductMeta        `json:"meta"`
	Entity           EntityRef          `json:"entity"`
	Summary          map[string]any     `json:"summary,omitempty"`
	Status           string             `json:"status,omitempty"`
	Sections         map[string]any     `json:"sections,omitempty"`
	Relationships    []NeighborhoodEdge `json:"relationships,omitempty"`
	Findings         []finding.Finding  `json:"findings,omitempty"`
	Evidence         []EvidenceItem     `json:"evidence,omitempty"`
	Ownership        *OwnershipInfo     `json:"ownership,omitempty"`
	Limitations      []Limitation       `json:"limitations,omitempty"`
	Links            []Link             `json:"links,omitempty"`
	AvailableActions []string           `json:"availableActions,omitempty"`
}

// EntityDetail returns the unified detail envelope for one entity. Supported
// kinds: service, revision, target, owner, source.
func (q *Query) EntityDetail(kind EntityKind, key string) (*EntityDetail, error) {
	switch kind {
	case KindService:
		return q.serviceDetail(key)
	case KindRevision:
		return q.revisionDetail(key)
	case KindTarget:
		return q.targetDetail(key)
	case KindOwner:
		return q.ownerDetail(key)
	case KindSource:
		return q.sourceDetail(key)
	default:
		return nil, &InvalidQueryError{Field: "kind", Value: string(kind), Reason: "not a known entity kind"}
	}
}

func allViews() []KnowledgeView { return []KnowledgeView{ViewExpected, ViewObserved, ViewDifferences} }

func (q *Query) serviceDetail(key string) (*EntityDetail, error) {
	view, err := q.GetService(key)
	if err != nil {
		return nil, err
	}
	s := view.Service
	d := &EntityDetail{
		Meta: q.productMeta(), Entity: serviceEntityRef(s), Status: s.Status,
		Summary:  map[string]any{"domain": s.Domain, "revisions": len(view.Revisions), "deployments": len(view.Targets), "dependencies": len(view.Dependencies), "dependents": len(view.Dependents)},
		Sections: map[string]any{"revisions": revisionRefs(view.Revisions), "deployments": targetRefs(view.Targets), "dependents": q.serviceKeyRefs(view.Dependents)},
	}
	if nb, e := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(s.Key), Direction: DirectionBoth, Views: allViews()}); e == nil {
		d.Relationships = nb.Edges
	}
	d.Findings = aggregateTargetFindings(view.Targets)
	d.Evidence = evidenceForTargets(view.Targets)
	d.Ownership = serviceOwnership(s, view.Revisions)
	d.Limitations = aggregateTargetLimitations(view.Targets)
	d.Links = []Link{
		{Rel: "graph", Label: "Open in graph", Route: RouteForGraph(KindService, string(s.Key))},
		{Rel: "compare", Label: "Compare revisions", Route: RouteForCompare(s.Key)},
		{Rel: "impact", Label: "Analyze impact", Route: RouteForImpact(s.Key)},
	}
	d.AvailableActions = []string{"open-graph", "compare", "impact"}
	return d, nil
}

func (q *Query) revisionDetail(key string) (*EntityDetail, error) {
	rev := q.snap.Revisions[RevisionKey(key)]
	if rev == nil {
		return nil, &NotFoundError{Kind: "revision", ID: key}
	}
	d := &EntityDetail{
		Meta: q.productMeta(), Entity: revisionEntityRef(rev), Status: revisionStatus(rev),
		Summary: map[string]any{"service": rev.Service, "version": rev.Version, "digest": rev.Digest, "valid": rev.Valid, "pactoVersion": rev.PactoVersion},
		// A built revision always has a contract (revisionFrom drops nil ones).
		Sections: map[string]any{"tools": rev.Tools, "skills": rev.Skills, "docs": rev.Docs, "dependencies": len(rev.Contract.Dependencies), "capabilities": len(rev.Contract.Capabilities)},
	}
	if rev.Readiness != nil {
		d.Sections["readiness"] = rev.Readiness
	}
	exact, inferred := q.targetsForRevision(rev.Key)
	d.Sections["exactTargets"] = exact
	d.Sections["inferredTargets"] = inferred
	d.Relationships = q.revisionEdges(rev.Key)
	d.Findings = rev.Validation
	d.Ownership = &OwnershipInfo{Owner: rev.Owner.DisplayString()}
	d.Links = []Link{
		{Rel: "service", Label: "Service", Route: RouteForService(rev.ServiceKey)},
		{Rel: "diff", Label: "Diff", Route: RouteForCompare(rev.ServiceKey)},
		{Rel: "impact", Label: "Impact", Route: RouteForImpact(rev.ServiceKey)},
	}
	return d, nil
}

func (q *Query) targetDetail(key string) (*EntityDetail, error) {
	tv, err := q.GetTarget(key)
	if err != nil {
		return nil, err
	}
	t := tv.Target
	d := &EntityDetail{
		Meta: q.productMeta(), Entity: targetEntityRef(t), Status: t.Compliance,
		Summary:  map[string]any{"service": t.Service, "scope": t.Scope, "revisionMatch": t.RevisionMatch, "digest": t.Digest, "source": t.Source, "requestedRef": t.RequestedRef, "resolvedRef": t.ResolvedRef},
		Sections: map[string]any{},
	}
	if t.Coverage != nil {
		d.Sections["coverage"] = t.Coverage
	}
	if len(t.ObservedRuntime) > 0 {
		d.Sections["observedRuntime"] = t.ObservedRuntime
	}
	d.Findings = t.Findings
	if t.EvidenceAt != nil {
		d.Evidence = []EvidenceItem{{Target: targetEntityRef(t), At: copyTime(t.EvidenceAt)}}
	}
	d.Limitations = t.Limitations
	d.Links = q.targetLinks(t)
	return d, nil
}

func (q *Query) targetLinks(t *TargetRecord) []Link {
	links := []Link{{Rel: "service", Label: "Service", Route: RouteForService(t.ServiceKey)}}
	if t.ContractRevision != "" {
		links = append(links, Link{Rel: "revision", Label: "Revision", Route: RouteForRevision(t.ContractRevision)})
	}
	// A built target always carries its contributing source (Build sets it).
	links = append(links,
		Link{Rel: "source", Label: "Evidence source", Route: RouteForSource(t.Source)},
		Link{Rel: "graph", Label: "Focused graph", Route: RouteForGraph(KindTarget, string(t.Key))})
	return links
}

func (q *Query) ownerDetail(key string) (*EntityDetail, error) {
	var services, deployments, revisions []EntityRef
	for _, s := range q.snap.Services {
		if s.Owner.DisplayString() != key {
			continue
		}
		services = append(services, serviceEntityRef(s))
		for _, tk := range s.Targets {
			if t := q.snap.Targets[tk]; t != nil {
				deployments = append(deployments, targetEntityRef(t))
			}
		}
	}
	for _, r := range q.snap.Revisions {
		if r.Owner.DisplayString() == key {
			revisions = append(revisions, revisionEntityRef(r))
		}
	}
	if len(services) == 0 && len(revisions) == 0 {
		return nil, &NotFoundError{Kind: "owner", ID: key}
	}
	sortEntityRefs(services)
	sortEntityRefs(deployments)
	sortEntityRefs(revisions)
	d := &EntityDetail{
		Meta: q.productMeta(), Entity: ownerEntityRef(key),
		Sections: map[string]any{"services": services, "deployments": deployments, "revisions": revisions},
	}
	d.Sections["attention"] = q.Attention(AttentionFilter{Owner: key}).Items
	d.Ownership = &OwnershipInfo{Owner: key}
	return d, nil
}

func (q *Query) sourceDetail(key string) (*EntityDetail, error) {
	var st *SourceState
	for i := range q.snap.Sources {
		if q.snap.Sources[i].ID == key {
			st = &q.snap.Sources[i]
			break
		}
	}
	if st == nil {
		return nil, &NotFoundError{Kind: "source", ID: key}
	}
	d := &EntityDetail{
		Meta: q.productMeta(), Entity: sourceEntityRef(*st), Status: string(st.Status),
		Summary:  map[string]any{"kind": st.Kind, "status": string(st.Status), "revisionCount": st.RevisionCount, "targetCount": st.TargetCount},
		Sections: map[string]any{},
	}
	if st.LastSuccessfulSync != nil {
		d.Summary["lastSuccessfulSync"] = st.LastSuccessfulSync
	}
	if st.Error != nil {
		d.Sections["error"] = st.Error
	}
	d.Sections["entities"] = q.entitiesFromSource(key)
	for _, l := range q.snap.Limitations {
		if l.Source == key {
			d.Limitations = append(d.Limitations, l)
		}
	}
	return d, nil
}

// entitiesFromSource returns every entity contributed by the named source.
func (q *Query) entitiesFromSource(key string) []EntityRef {
	var out []EntityRef
	for _, s := range q.snap.Services {
		if containsStr(s.Sources, key) {
			out = append(out, serviceEntityRef(s))
		}
	}
	for _, r := range q.snap.Revisions {
		if containsStr(r.Sources, key) || r.Source == key {
			out = append(out, revisionEntityRef(r))
		}
	}
	for _, t := range q.snap.Targets {
		if containsStr(t.Sources, key) || t.Source == key {
			out = append(out, targetEntityRef(t))
		}
	}
	sortEntityRefs(out)
	return out
}

// revisionEdges builds the declared, resolved dependency edges a specific revision
// declares. Observed edges carry no revision, so only this revision's declared
// dependencies appear; every edge shares this revision's service as its source, so
// ordering by destination is sufficient and deterministic.
func (q *Query) revisionEdges(revKey RevisionKey) []NeighborhoodEdge {
	var out []NeighborhoodEdge
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency || rel.FromRevision != revKey || rel.ToService == "" {
			continue
		}
		e := q.newEdge(rel.FromService, rel.ToService)
		q.foldRelationshipIntoEdge(e, rel)
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].To.Key < out[j].To.Key })
	return out
}

// targetsForRevision splits the targets linked to a revision into exact and
// inferred references.
func (q *Query) targetsForRevision(revKey RevisionKey) (exact, inferred []EntityRef) {
	for _, t := range q.snap.Targets {
		if t.ContractRevision != revKey {
			continue
		}
		ref := targetEntityRef(t)
		if t.RevisionMatch == revisionMatchInferred {
			inferred = append(inferred, ref)
		} else {
			exact = append(exact, ref)
		}
	}
	sortEntityRefs(exact)
	sortEntityRefs(inferred)
	return exact, inferred
}

func (q *Query) serviceKeyRefs(keys []ServiceKey) []EntityRef {
	out := make([]EntityRef, 0, len(keys))
	for _, k := range keys {
		out = append(out, q.serviceRef(k))
	}
	return out
}

func revisionRefs(revs []*ContractRevision) []EntityRef {
	out := make([]EntityRef, 0, len(revs))
	for _, r := range revs {
		out = append(out, revisionEntityRef(r))
	}
	return out
}

func targetRefs(targets []*TargetRecord) []EntityRef {
	out := make([]EntityRef, 0, len(targets))
	for _, t := range targets {
		out = append(out, targetEntityRef(t))
	}
	return out
}

func aggregateTargetFindings(targets []*TargetRecord) []finding.Finding {
	var out []finding.Finding
	for _, t := range targets {
		out = append(out, t.Findings...)
	}
	return out
}

func aggregateTargetLimitations(targets []*TargetRecord) []Limitation {
	var out []Limitation
	for _, t := range targets {
		out = append(out, t.Limitations...)
	}
	return out
}

func evidenceForTargets(targets []*TargetRecord) []EvidenceItem {
	var out []EvidenceItem
	for _, t := range targets {
		if t.EvidenceAt != nil {
			out = append(out, EvidenceItem{Target: targetEntityRef(t), At: copyTime(t.EvidenceAt)})
		}
	}
	sortEvidenceDesc(out)
	return out
}

// serviceOwnership reports the service owner and any per-revision ownership
// conflict (a revision whose owner differs from the service owner).
func serviceOwnership(s *ServiceRecord, revs []*ContractRevision) *OwnershipInfo {
	owner := s.Owner.DisplayString()
	info := &OwnershipInfo{Owner: owner}
	if owner != "" {
		ref := ownerEntityRef(owner)
		info.Ref = &ref
	}
	for _, r := range revs {
		ro := r.Owner.DisplayString()
		if ro != "" && ro != owner {
			info.Conflicts = append(info.Conflicts, string(r.Key)+": "+ro)
		}
	}
	return info
}

// revisionStatus reports Invalid for a validated-and-invalid revision, else empty.
func revisionStatus(r *ContractRevision) string {
	if r.validated && !r.Valid {
		return StatusInvalid
	}
	return ""
}
