package fleet

import (
	"sort"
	"time"

	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// This file builds the strongly typed, discriminated entity-detail model. There
// is no map[string]any: the common envelope makes the entity kind explicit and
// carries EXACTLY ONE kind-specific payload (service / revision / target / owner
// / source). Every nested collection is a bounded preview (see preview.go), and
// every finding or limitation aggregated across more than one entity carries the
// canonical reference of the entity it affects, so a consumer never sees an
// orphan finding. The whole answer is deep-cloned before return, so a caller can
// mutate it without touching the immutable snapshot or any other answer.

// OwnershipInfo is an entity's ownership summary with per-revision conflicts.
type OwnershipInfo struct {
	Owner     string     `json:"owner,omitempty"`
	Ref       *EntityRef `json:"ref,omitempty"`
	Conflicts []string   `json:"conflicts,omitempty"`
}

// RevisionIdentity describes how a revision (or a target's contract reference)
// resolved to content. Immutable reports whether ResolvedRef is digest-pinned;
// only immutable identity may be treated as exact snapshot content.
type RevisionIdentity struct {
	Digest       string `json:"digest,omitempty"`
	RequestedRef string `json:"requestedRef,omitempty"`
	ResolvedRef  string `json:"resolvedRef,omitempty"`
	Immutable    bool   `json:"immutable"`
}

// ServiceDetailData is the service-kind payload: aggregate ownership, bounded
// previews of revisions / deployments / dependencies / dependents /
// relationships, and findings, evidence and limitations aggregated across the
// service's targets (each attributed to the affected target).
type ServiceDetailData struct {
	Domain        string                       `json:"domain,omitempty"`
	Ownership     *OwnershipInfo               `json:"ownership,omitempty"`
	Revisions     RefPreview                   `json:"revisions"`
	Deployments   RefPreview                   `json:"deployments"`
	Dependencies  RefPreview                   `json:"dependencies"`
	Dependents    RefPreview                   `json:"dependents"`
	Relationships RelationshipsPreview         `json:"relationships"`
	Findings      AttributedFindingsPreview    `json:"findings"`
	Evidence      EvidencePreview              `json:"evidence"`
	Limitations   AttributedLimitationsPreview `json:"limitations"`
}

// RevisionDetailData is the revision-kind payload: parent service, immutable
// identity/content, validation, readiness, declared-interface/config/policy/
// capability counts, bounded tools/skills/docs, exact/inferred targets, and the
// previous/next known revisions of the same logical service.
type RevisionDetailData struct {
	Service         EntityRef            `json:"service"`
	Version         string               `json:"version,omitempty"`
	PactoVersion    string               `json:"pactoVersion,omitempty"`
	Identity        RevisionIdentity     `json:"identity"`
	Valid           bool                 `json:"valid"`
	Readiness       *readiness.Result    `json:"readiness,omitempty"`
	Validation      FindingsPreview      `json:"validation"`
	Interfaces      int                  `json:"interfaces"`
	Configurations  int                  `json:"configurations"`
	Policies        int                  `json:"policies"`
	Capabilities    int                  `json:"capabilities"`
	Dependencies    RelationshipsPreview `json:"dependencies"`
	Tools           ToolsPreview         `json:"tools"`
	Skills          StringsPreview       `json:"skills"`
	Docs            DocsPreview          `json:"docs"`
	ExactTargets    RefPreview           `json:"exactTargets"`
	InferredTargets RefPreview           `json:"inferredTargets"`
	Previous        *EntityRef           `json:"previous,omitempty"`
	Next            *EntityRef           `json:"next,omitempty"`
	Ownership       *OwnershipInfo       `json:"ownership,omitempty"`
	Limitations     LimitationsPreview   `json:"limitations"`
}

// TargetDetailData is the target-kind payload: the logical service and linked
// revision, the exact/inferred/ambiguous/unresolved link state, compliance,
// coverage, findings, observed runtime, contributing sources, the target's own
// contract identity, evidence/reconciliation timestamps, and stale/quarantined
// state.
type TargetDetailData struct {
	Service         EntityRef          `json:"service"`
	Revision        *EntityRef         `json:"revision,omitempty"`
	LinkState       string             `json:"linkState"`
	Scope           string             `json:"scope,omitempty"`
	Kind            string             `json:"kind,omitempty"`
	Compliance      string             `json:"compliance"`
	Coverage        *Coverage          `json:"coverage,omitempty"`
	Findings        FindingsPreview    `json:"findings"`
	ObservedRuntime map[string]any     `json:"observedRuntime,omitempty"`
	Sources         StringsPreview     `json:"sources"`
	Source          string             `json:"source,omitempty"`
	Identity        RevisionIdentity   `json:"identity"`
	EvidenceAt      *time.Time         `json:"evidenceAt,omitempty"`
	ReconciledAt    *time.Time         `json:"reconciledAt,omitempty"`
	Stale           bool               `json:"stale"`
	Quarantined     bool               `json:"quarantined,omitempty"`
	Ownership       *OwnershipInfo     `json:"ownership,omitempty"`
	Limitations     LimitationsPreview `json:"limitations"`
}

// OwnerDetailData is the owner-kind payload: bounded previews of the owner's
// services, revisions and deployments, plus a bounded attention preview.
type OwnerDetailData struct {
	Services    RefPreview       `json:"services"`
	Revisions   RefPreview       `json:"revisions"`
	Deployments RefPreview       `json:"deployments"`
	Attention   AttentionPreview `json:"attention"`
}

// SourceDetailData is the source-kind payload: kind, health, sync/observation
// timestamps, record counts, a bounded preview of contributed entities, the
// sanitized source error, and the source's own limitations.
type SourceDetailData struct {
	Kind               string             `json:"kind,omitempty"`
	Health             string             `json:"health"`
	LastSuccessfulSync *time.Time         `json:"lastSuccessfulSync,omitempty"`
	ObservedAt         *time.Time         `json:"observedAt,omitempty"`
	RevisionCount      int                `json:"revisionCount"`
	TargetCount        int                `json:"targetCount"`
	Entities           RefPreview         `json:"entities"`
	Error              *SourceError       `json:"error,omitempty"`
	Limitations        LimitationsPreview `json:"limitations"`
}

// EntityDetail is the common, versioned, discriminated envelope for any entity's
// full detail (requirement 2.4). Entity.Kind is the discriminator; EXACTLY ONE
// of Service/Revision/Target/Owner/Source is populated. Actions lists the
// available semantic actions route-neutrally (the transport maps them to hrefs).
type EntityDetail struct {
	Meta     ProductMeta         `json:"meta"`
	Entity   EntityRef           `json:"entity"`
	Status   string              `json:"status,omitempty"`
	Service  *ServiceDetailData  `json:"service,omitempty"`
	Revision *RevisionDetailData `json:"revision,omitempty"`
	Target   *TargetDetailData   `json:"target,omitempty"`
	Owner    *OwnerDetailData    `json:"owner,omitempty"`
	Source   *SourceDetailData   `json:"source,omitempty"`
	Actions  []string            `json:"actions,omitempty"`
}

// EntityDetail returns the unified detail envelope for one entity, deep-cloned so
// the caller can mutate it freely. Supported kinds: service, revision, target,
// owner, source.
func (q *Query) EntityDetail(kind EntityKind, key string) (*EntityDetail, error) {
	d, err := q.entityDetail(kind, key)
	if err != nil {
		return nil, err
	}
	// One terminal deep copy severs every alias into the snapshot: a caller may
	// mutate any nested map, slice or pointer without touching the snapshot or a
	// later answer (requirement: product-query immutability).
	return jsonClone(d), nil
}

func (q *Query) entityDetail(kind EntityKind, key string) (*EntityDetail, error) {
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
	data := &ServiceDetailData{
		Domain:       s.Domain,
		Ownership:    serviceOwnership(s, view.Revisions),
		Revisions:    refPreview(revisionRefs(view.Revisions)),
		Deployments:  refPreview(targetRefs(view.Targets)),
		Dependencies: refPreview(q.dependencyRefs(view.Dependencies)),
		Dependents:   refPreview(q.serviceKeyRefs(view.Dependents)),
		Findings:     attributedFindingsPreview(attributedTargetFindings(view.Targets)),
		Evidence:     evidencePreview(evidenceForTargets(view.Targets)),
		Limitations:  attributedLimitationsPreview(attributedTargetLimitations(view.Targets)),
	}
	if nb, e := q.Neighborhood(NeighborhoodQuery{Kind: KindService, Key: string(s.Key), Direction: DirectionBoth, Views: allViews()}); e == nil {
		data.Relationships = relationshipsPreview(nb.Edges)
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: serviceEntityRef(s), Status: s.Status,
		Service: data, Actions: []string{"open-graph", "compare", "impact"},
	}, nil
}

func (q *Query) revisionDetail(key string) (*EntityDetail, error) {
	rev := q.snap.Revisions[RevisionKey(key)]
	if rev == nil {
		return nil, &NotFoundError{Kind: "revision", ID: key}
	}
	exact, inferred := q.targetsForRevision(rev.Key)
	prev, next := q.siblingRevisions(rev)
	data := &RevisionDetailData{
		Service:      q.serviceRef(rev.ServiceKey),
		Version:      rev.Version,
		PactoVersion: rev.PactoVersion,
		Identity: RevisionIdentity{
			Digest: rev.Digest, RequestedRef: rev.RequestedRef, ResolvedRef: rev.ResolvedRef,
			Immutable: IsDigestPinnedRef(rev.ResolvedRef),
		},
		Valid:           rev.Valid,
		Readiness:       rev.Readiness,
		Validation:      findingsPreview(rev.Validation),
		Capabilities:    len(rev.Contract.Capabilities),
		Interfaces:      len(rev.Contract.Interfaces),
		Configurations:  len(rev.Contract.Configurations),
		Policies:        len(rev.Contract.Policies),
		Dependencies:    relationshipsPreview(q.revisionEdges(rev.Key)),
		Tools:           toolsPreview(rev.Tools),
		Skills:          stringsPreview(rev.Skills),
		Docs:            docsPreview(rev.Docs),
		ExactTargets:    refPreview(exact),
		InferredTargets: refPreview(inferred),
		Previous:        prev,
		Next:            next,
		Ownership:       &OwnershipInfo{Owner: rev.Owner.DisplayString()},
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: revisionEntityRef(rev), Status: revisionStatus(rev),
		Revision: data, Actions: []string{"open-graph", "compare", "impact"},
	}, nil
}

func (q *Query) targetDetail(key string) (*EntityDetail, error) {
	tv, err := q.GetTarget(key)
	if err != nil {
		return nil, err
	}
	t := tv.Target
	data := &TargetDetailData{
		Service:    q.serviceRef(t.ServiceKey),
		LinkState:  targetLinkState(t),
		Scope:      t.Scope,
		Kind:       t.Kind,
		Compliance: t.Compliance,
		Coverage:   t.Coverage,
		Findings:   findingsPreview(t.Findings),
		Sources:    stringsPreview(t.Sources),
		Source:     t.Source,
		Identity: RevisionIdentity{
			Digest: t.Digest, RequestedRef: t.RequestedRef, ResolvedRef: t.ResolvedRef,
			Immutable: IsDigestPinnedRef(t.ResolvedRef),
		},
		EvidenceAt:   t.EvidenceAt,
		ReconciledAt: t.ReconciledAt,
		Stale:        t.Stale,
		Quarantined:  t.Quarantined,
		Limitations:  limitationsPreview(t.Limitations),
	}
	if len(t.ObservedRuntime) > 0 {
		data.ObservedRuntime = t.ObservedRuntime
	}
	if t.ContractRevision != "" {
		if rev := q.snap.Revisions[t.ContractRevision]; rev != nil {
			ref := revisionEntityRef(rev)
			data.Revision = &ref
		}
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: targetEntityRef(t), Status: t.Compliance,
		Target: data, Actions: []string{"open-graph", "service"},
	}, nil
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
	// A constant, valid filter (owner only) never errors; ignore it deliberately.
	ownerAttention, _ := q.Attention(AttentionFilter{Owner: key})
	return &EntityDetail{
		Meta: q.productMeta(), Entity: ownerEntityRef(key),
		Owner: &OwnerDetailData{
			Services:    refPreview(services),
			Revisions:   refPreview(revisions),
			Deployments: refPreview(deployments),
			Attention:   attentionPreview(ownerAttention.Items),
		},
	}, nil
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
	var lims []Limitation
	for _, l := range q.snap.Limitations {
		if l.Source == key {
			lims = append(lims, l)
		}
	}
	return &EntityDetail{
		Meta: q.productMeta(), Entity: sourceEntityRef(*st), Status: string(st.Status),
		Source: &SourceDetailData{
			Kind:               st.Kind,
			Health:             string(st.Status),
			LastSuccessfulSync: st.LastSuccessfulSync,
			ObservedAt:         st.ObservedAt,
			RevisionCount:      st.RevisionCount,
			TargetCount:        st.TargetCount,
			Entities:           refPreview(q.entitiesFromSource(key)),
			Error:              st.Error,
			Limitations:        limitationsPreview(lims),
		},
	}, nil
}

// targetLinkState classifies a target's revision link as exact, inferred,
// ambiguous or unresolved. Only an exact link (immutable digest) is authoritative.
func targetLinkState(t *TargetRecord) string {
	switch t.RevisionMatch {
	case revisionMatchExact:
		return "exact"
	case revisionMatchInferred:
		return "inferred"
	default:
		if hasLimitation(t.Limitations, LimitationRevisionAmbiguous) {
			return "ambiguous"
		}
		return "unresolved"
	}
}

// dependencyRefs turns a service's declared dependency relationships into DISTINCT
// provider-service references (skipping unresolved edges that name no provider
// service, and collapsing multiple relationships to the same provider).
func (q *Query) dependencyRefs(deps []Relationship) []EntityRef {
	seen := map[ServiceKey]bool{}
	var out []EntityRef
	for _, rel := range deps {
		if rel.ToService == "" || seen[rel.ToService] {
			continue
		}
		if s := q.snap.Services[rel.ToService]; s != nil {
			seen[rel.ToService] = true
			out = append(out, serviceEntityRef(s))
		}
	}
	sortEntityRefs(out)
	return out
}

// siblingRevisions returns the previous and next known revisions of the same
// logical service in canonical key order (nil at the ends).
func (q *Query) siblingRevisions(rev *ContractRevision) (prev, next *EntityRef) {
	var keys []RevisionKey
	for k, r := range q.snap.Revisions {
		if r.ServiceKey == rev.ServiceKey {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for i, k := range keys {
		if k != rev.Key {
			continue
		}
		if i > 0 {
			r := revisionEntityRef(q.snap.Revisions[keys[i-1]])
			prev = &r
		}
		if i < len(keys)-1 {
			r := revisionEntityRef(q.snap.Revisions[keys[i+1]])
			next = &r
		}
		break
	}
	return prev, next
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
		finalizeEdge(e)
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

// attributedTargetFindings aggregates every target's findings, attributing each
// to the target it affects so a service-level finding is never orphaned.
func attributedTargetFindings(targets []*TargetRecord) []AttributedFinding {
	var out []AttributedFinding
	for _, t := range targets {
		ref := targetEntityRef(t)
		for _, f := range t.Findings {
			out = append(out, AttributedFinding{Finding: f, Entity: ref})
		}
	}
	return out
}

// attributedTargetLimitations aggregates every target's limitations, attributing
// each to the target it affects.
func attributedTargetLimitations(targets []*TargetRecord) []AttributedLimitation {
	var out []AttributedLimitation
	for _, t := range targets {
		ref := targetEntityRef(t)
		for _, l := range t.Limitations {
			out = append(out, AttributedLimitation{Limitation: l, Entity: ref})
		}
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
