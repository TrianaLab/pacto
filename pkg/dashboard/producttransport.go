package dashboard

import "github.com/trianalab/pacto/v3/pkg/fleet"

// This file is the dashboard product-transport boundary (ADR-2). pkg/fleet returns
// route-neutral facts; this layer wraps each fleet product answer in a transport
// DTO that adds a canonical navigation href to every entity reference. Every href
// is derived from the EXACT canonical key via the single route builder
// (fleetroute.go), never from a display label. The conversion functions are pure
// and total; they are what the HTTP handlers return so the OpenAPI contract and
// the typed frontend client see concrete, href-bearing shapes. Each transport
// type embeds its fleet counterpart and shadows only the reference-bearing fields
// (encoding/json and Huma both take the shallower field), so pass-through facts
// stay in exactly one place.

// ── the reference atom ───────────────────────────────────────────────────────

// ProductRef is a navigable entity reference: the route-neutral fleet EntityRef
// plus the canonical dashboard href built from its key.
type ProductRef struct {
	fleet.EntityRef
	Href string `json:"href"`
}

func productRef(e fleet.EntityRef) ProductRef {
	return ProductRef{EntityRef: e, Href: hrefForEntity(e.Kind, e.Key)}
}

func productRefPtr(e *fleet.EntityRef) *ProductRef {
	if e == nil {
		return nil
	}
	r := productRef(*e)
	return &r
}

func productRefs(es []fleet.EntityRef) []ProductRef {
	out := make([]ProductRef, len(es))
	for i, e := range es {
		out[i] = productRef(e)
	}
	return out
}

// ── reusable transport previews ──────────────────────────────────────────────

// ProductRefPreview is a bounded preview of navigable references.
type ProductRefPreview struct {
	fleet.RefPreview
	Items []ProductRef `json:"items"`
}

func productRefPreview(p fleet.RefPreview) ProductRefPreview {
	return ProductRefPreview{RefPreview: p, Items: productRefs(p.Items)}
}

// ProductEvidenceItem links a navigable target to when it was evidenced.
type ProductEvidenceItem struct {
	fleet.EvidenceItem
	Target ProductRef `json:"target"`
}

// ProductEvidencePreview is a bounded preview of evidence items.
type ProductEvidencePreview struct {
	fleet.EvidencePreview
	Items []ProductEvidenceItem `json:"items"`
}

func productEvidencePreview(p fleet.EvidencePreview) ProductEvidencePreview {
	items := make([]ProductEvidenceItem, len(p.Items))
	for i, e := range p.Items {
		items[i] = ProductEvidenceItem{EvidenceItem: e, Target: productRef(e.Target)}
	}
	return ProductEvidencePreview{EvidencePreview: p, Items: items}
}

// ProductAttentionItem is a navigable attention row.
type ProductAttentionItem struct {
	fleet.AttentionItem
	Entity ProductRef `json:"entity"`
}

func productAttentionItem(it fleet.AttentionItem) ProductAttentionItem {
	return ProductAttentionItem{AttentionItem: it, Entity: productRef(it.Entity)}
}

// ProductAttentionPreview is a bounded preview of attention items.
type ProductAttentionPreview struct {
	fleet.AttentionPreview
	Items []ProductAttentionItem `json:"items"`
}

func productAttentionPreview(p fleet.AttentionPreview) ProductAttentionPreview {
	items := make([]ProductAttentionItem, len(p.Items))
	for i, it := range p.Items {
		items[i] = productAttentionItem(it)
	}
	return ProductAttentionPreview{AttentionPreview: p, Items: items}
}

// ProductEdge is a navigable neighborhood edge (endpoints hrefed, plus a focused
// graph href).
type ProductEdge struct {
	fleet.NeighborhoodEdge
	From ProductRef `json:"from"`
	To   ProductRef `json:"to"`
	Href string     `json:"href"`
}

func productEdge(e fleet.NeighborhoodEdge) ProductEdge {
	return ProductEdge{
		NeighborhoodEdge: e, From: productRef(e.From), To: productRef(e.To),
		// Focus the target endpoint in its OWN kind's graph (a mixed projection has
		// revision and target endpoints, not only services).
		Href: hrefForGraph(e.To.Kind, e.To.Key),
	}
}

// ProductRelationshipsPreview is a bounded preview of navigable edges.
type ProductRelationshipsPreview struct {
	fleet.RelationshipsPreview
	Items []ProductEdge `json:"items"`
}

func productRelationshipsPreview(p fleet.RelationshipsPreview) ProductRelationshipsPreview {
	items := make([]ProductEdge, len(p.Items))
	for i, e := range p.Items {
		items[i] = productEdge(e)
	}
	return ProductRelationshipsPreview{RelationshipsPreview: p, Items: items}
}

// ProductAttributedFinding is a finding with its navigable affected entity.
type ProductAttributedFinding struct {
	fleet.AttributedFinding
	Entity ProductRef `json:"entity"`
}

// ProductAttributedFindingsPreview is a bounded preview of attributed findings.
type ProductAttributedFindingsPreview struct {
	fleet.AttributedFindingsPreview
	Items []ProductAttributedFinding `json:"items"`
}

func productAttributedFindingsPreview(p fleet.AttributedFindingsPreview) ProductAttributedFindingsPreview {
	items := make([]ProductAttributedFinding, len(p.Items))
	for i, f := range p.Items {
		items[i] = ProductAttributedFinding{AttributedFinding: f, Entity: productRef(f.Entity)}
	}
	return ProductAttributedFindingsPreview{AttributedFindingsPreview: p, Items: items}
}

// ProductAttributedLimitation is a limitation with its navigable affected entity.
type ProductAttributedLimitation struct {
	fleet.AttributedLimitation
	Entity ProductRef `json:"entity,omitempty"`
}

// ProductAttributedLimitationsPreview is a bounded preview of attributed limitations.
type ProductAttributedLimitationsPreview struct {
	fleet.AttributedLimitationsPreview
	Items []ProductAttributedLimitation `json:"items"`
}

func productAttributedLimitationsPreview(p fleet.AttributedLimitationsPreview) ProductAttributedLimitationsPreview {
	items := make([]ProductAttributedLimitation, len(p.Items))
	for i, l := range p.Items {
		items[i] = ProductAttributedLimitation{AttributedLimitation: l, Entity: productRef(l.Entity)}
	}
	return ProductAttributedLimitationsPreview{AttributedLimitationsPreview: p, Items: items}
}

// ProductOwnership is an ownership summary with a navigable owner reference.
type ProductOwnership struct {
	fleet.OwnershipInfo
	Ref *ProductRef `json:"ref,omitempty"`
}

func productOwnership(o *fleet.OwnershipInfo) *ProductOwnership {
	if o == nil {
		return nil
	}
	return &ProductOwnership{OwnershipInfo: *o, Ref: productRefPtr(o.Ref)}
}

// ── overview ─────────────────────────────────────────────────────────────────

// ProductEntryPoint is a navigational entry point with a canonical href.
type ProductEntryPoint struct {
	fleet.EntryPoint
	Href string `json:"href"`
}

// ProductOverview is the navigable overview answer. Attention and RecentEvidence
// are explicit bounded previews (true total / count / truncated), not raw arrays.
type ProductOverview struct {
	fleet.Overview
	Attention      ProductAttentionPreview `json:"attention"`
	RecentEvidence ProductEvidencePreview  `json:"recentEvidence"`
	EntryPoints    []ProductEntryPoint     `json:"entryPoints"`
}

func toProductOverview(ov *fleet.Overview) *ProductOverview {
	out := &ProductOverview{Overview: *ov}
	out.Attention = productAttentionPreview(ov.Attention)
	out.RecentEvidence = productEvidencePreview(ov.RecentEvidence)
	out.EntryPoints = make([]ProductEntryPoint, len(ov.EntryPoints))
	for i, ep := range ov.EntryPoints {
		out.EntryPoints[i] = ProductEntryPoint{EntryPoint: ep, Href: hrefForEntryPoint(ep.View, ep.Category)}
	}
	return out
}

// ── entities ─────────────────────────────────────────────────────────────────

// ProductEntityList is a navigable page of entity references.
type ProductEntityList struct {
	fleet.EntityList
	Entities []ProductRef `json:"entities"`
}

func toProductEntityList(l *fleet.EntityList) *ProductEntityList {
	return &ProductEntityList{EntityList: *l, Entities: productRefs(l.Entities)}
}

// ── attention ────────────────────────────────────────────────────────────────

// ProductAttentionList is a navigable, offset-paged attention answer.
type ProductAttentionList struct {
	fleet.AttentionList
	Items []ProductAttentionItem `json:"items"`
}

func toProductAttentionList(l *fleet.AttentionList) *ProductAttentionList {
	items := make([]ProductAttentionItem, len(l.Items))
	for i, it := range l.Items {
		items[i] = productAttentionItem(it)
	}
	return &ProductAttentionList{AttentionList: *l, Items: items}
}

// ── neighborhood ─────────────────────────────────────────────────────────────

// ProductNode is a navigable neighborhood node.
type ProductNode struct {
	fleet.NeighborhoodNode
	Ref ProductRef `json:"ref"`
}

// ProductUnresolvedDependency is an unresolved dependency with a navigable source.
type ProductUnresolvedDependency struct {
	fleet.UnresolvedDependency
	From ProductRef `json:"from"`
}

// ProductUnresolvedDependenciesPreview is a bounded preview of unresolved deps.
type ProductUnresolvedDependenciesPreview struct {
	fleet.UnresolvedDependenciesPreview
	Items []ProductUnresolvedDependency `json:"items,omitempty"`
}

// ProductNeighborhood is the navigable neighborhood answer.
type ProductNeighborhood struct {
	fleet.Neighborhood
	RequestedFocus         ProductRef                           `json:"requestedFocus"`
	ProjectionFocus        *ProductRef                          `json:"projectionFocus,omitempty"`
	FocusService           ProductRef                           `json:"focusService"`
	Nodes                  []ProductNode                        `json:"nodes"`
	Edges                  []ProductEdge                        `json:"edges"`
	UnresolvedDependencies ProductUnresolvedDependenciesPreview `json:"unresolvedDependencies"`
}

func toProductNeighborhood(nb *fleet.Neighborhood) *ProductNeighborhood {
	out := &ProductNeighborhood{
		Neighborhood:    *nb,
		RequestedFocus:  productRef(nb.RequestedFocus),
		ProjectionFocus: productRefPtr(nb.ProjectionFocus),
		FocusService:    productRef(nb.FocusService),
	}
	out.Nodes = make([]ProductNode, len(nb.Nodes))
	for i, n := range nb.Nodes {
		out.Nodes[i] = ProductNode{NeighborhoodNode: n, Ref: productRef(n.Ref)}
	}
	out.Edges = make([]ProductEdge, len(nb.Edges))
	for i, e := range nb.Edges {
		out.Edges[i] = productEdge(e)
	}
	deps := make([]ProductUnresolvedDependency, len(nb.UnresolvedDependencies.Items))
	for i, u := range nb.UnresolvedDependencies.Items {
		deps[i] = ProductUnresolvedDependency{UnresolvedDependency: u, From: productRef(u.From)}
	}
	out.UnresolvedDependencies = ProductUnresolvedDependenciesPreview{
		UnresolvedDependenciesPreview: nb.UnresolvedDependencies, Items: deps,
	}
	return out
}

// ── entity detail (discriminated) ────────────────────────────────────────────

// ProductServiceDetail is the navigable service-kind detail payload.
type ProductServiceDetail struct {
	fleet.ServiceDetailData
	Ownership       *ProductOwnership                   `json:"ownership,omitempty"`
	Revisions       ProductRefPreview                   `json:"revisions"`
	ActiveRevisions ProductRefPreview                   `json:"activeRevisions"`
	Deployments     ProductRefPreview                   `json:"deployments"`
	Dependencies    ProductRefPreview                   `json:"dependencies"`
	Dependents      ProductRefPreview                   `json:"dependents"`
	Relationships   ProductRelationshipsPreview         `json:"relationships"`
	Findings        ProductAttributedFindingsPreview    `json:"findings"`
	Evidence        ProductEvidencePreview              `json:"evidence"`
	Limitations     ProductAttributedLimitationsPreview `json:"limitations"`
}

func toProductServiceDetail(d *fleet.ServiceDetailData) *ProductServiceDetail {
	return &ProductServiceDetail{
		ServiceDetailData: *d,
		Ownership:         productOwnership(d.Ownership),
		Revisions:         productRefPreview(d.Revisions),
		ActiveRevisions:   productRefPreview(d.ActiveRevisions),
		Deployments:       productRefPreview(d.Deployments),
		Dependencies:      productRefPreview(d.Dependencies),
		Dependents:        productRefPreview(d.Dependents),
		Relationships:     productRelationshipsPreview(d.Relationships),
		Findings:          productAttributedFindingsPreview(d.Findings),
		Evidence:          productEvidencePreview(d.Evidence),
		Limitations:       productAttributedLimitationsPreview(d.Limitations),
	}
}

// ProductRevisionDetail is the navigable revision-kind detail payload.
type ProductRevisionDetail struct {
	fleet.RevisionDetailData
	Service         ProductRef                  `json:"service"`
	Dependencies    ProductRelationshipsPreview `json:"dependencies"`
	ExactTargets    ProductRefPreview           `json:"exactTargets"`
	InferredTargets ProductRefPreview           `json:"inferredTargets"`
	Previous        *ProductRef                 `json:"previous,omitempty"`
	Next            *ProductRef                 `json:"next,omitempty"`
	Ownership       *ProductOwnership           `json:"ownership,omitempty"`
}

func toProductRevisionDetail(d *fleet.RevisionDetailData) *ProductRevisionDetail {
	return &ProductRevisionDetail{
		RevisionDetailData: *d,
		Service:            productRef(d.Service),
		Dependencies:       productRelationshipsPreview(d.Dependencies),
		ExactTargets:       productRefPreview(d.ExactTargets),
		InferredTargets:    productRefPreview(d.InferredTargets),
		Previous:           productRefPtr(d.Previous),
		Next:               productRefPtr(d.Next),
		Ownership:          productOwnership(d.Ownership),
	}
}

// ProductTargetDetail is the navigable target-kind detail payload.
type ProductTargetDetail struct {
	fleet.TargetDetailData
	Service              ProductRef                  `json:"service"`
	Revision             *ProductRef                 `json:"revision,omitempty"`
	ServiceRelationships ProductRelationshipsPreview `json:"serviceRelationships"`
	Ownership            *ProductOwnership           `json:"ownership,omitempty"`
}

func toProductTargetDetail(d *fleet.TargetDetailData) *ProductTargetDetail {
	return &ProductTargetDetail{
		TargetDetailData:     *d,
		Service:              productRef(d.Service),
		Revision:             productRefPtr(d.Revision),
		ServiceRelationships: productRelationshipsPreview(d.ServiceRelationships),
		Ownership:            productOwnership(d.Ownership),
	}
}

// ProductOwnerDetail is the navigable owner-kind detail payload.
type ProductOwnerDetail struct {
	fleet.OwnerDetailData
	Services    ProductRefPreview       `json:"services"`
	Revisions   ProductRefPreview       `json:"revisions"`
	Deployments ProductRefPreview       `json:"deployments"`
	Attention   ProductAttentionPreview `json:"attention"`
}

func toProductOwnerDetail(d *fleet.OwnerDetailData) *ProductOwnerDetail {
	return &ProductOwnerDetail{
		OwnerDetailData: *d,
		Services:        productRefPreview(d.Services),
		Revisions:       productRefPreview(d.Revisions),
		Deployments:     productRefPreview(d.Deployments),
		Attention:       productAttentionPreview(d.Attention),
	}
}

// ProductSourceDetail is the navigable source-kind detail payload.
type ProductSourceDetail struct {
	fleet.SourceDetailData
	Entities ProductRefPreview `json:"entities"`
}

func toProductSourceDetail(d *fleet.SourceDetailData) *ProductSourceDetail {
	return &ProductSourceDetail{SourceDetailData: *d, Entities: productRefPreview(d.Entities)}
}

// ProductEntityDetail is the navigable, discriminated entity-detail envelope.
// Exactly one of Service/Revision/Target/Owner/Source is populated, matching
// Entity.Kind.
type ProductEntityDetail struct {
	fleet.EntityDetail
	Entity   ProductRef             `json:"entity"`
	Service  *ProductServiceDetail  `json:"service,omitempty"`
	Revision *ProductRevisionDetail `json:"revision,omitempty"`
	Target   *ProductTargetDetail   `json:"target,omitempty"`
	Owner    *ProductOwnerDetail    `json:"owner,omitempty"`
	Source   *ProductSourceDetail   `json:"source,omitempty"`
}

func toProductEntityDetail(d *fleet.EntityDetail) *ProductEntityDetail {
	out := &ProductEntityDetail{EntityDetail: *d, Entity: productRef(d.Entity)}
	switch {
	case d.Service != nil:
		out.Service = toProductServiceDetail(d.Service)
	case d.Revision != nil:
		out.Revision = toProductRevisionDetail(d.Revision)
	case d.Target != nil:
		out.Target = toProductTargetDetail(d.Target)
	case d.Owner != nil:
		out.Owner = toProductOwnerDetail(d.Owner)
	case d.Source != nil:
		out.Source = toProductSourceDetail(d.Source)
	}
	return out
}
