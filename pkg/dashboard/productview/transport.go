// Package productview is the dashboard product transport: fleet product answers
// wrapped with canonical hash-router hrefs. It imports only net/url and pkg/fleet,
// so the MCP server builds the same hrefs without linking the dashboard server.
package productview

import "github.com/trianalab/pacto/v4/pkg/fleet"

// This file is the dashboard product-transport boundary (ADR-2). pkg/fleet returns
// route-neutral facts; this layer wraps each fleet product answer in a transport
// DTO that adds a canonical navigation href to every entity reference. Every href
// is derived from the EXACT canonical key via the single route builder
// (route.go), never from a display label. The conversion functions are pure
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

// Ref wraps an entity reference with its canonical dashboard href.
func Ref(e fleet.EntityRef) ProductRef {
	return ProductRef{EntityRef: e, Href: Entity(e.Kind, e.Key)}
}

// RefPtr is [Ref] for an optional reference; nil stays nil.
func RefPtr(e *fleet.EntityRef) *ProductRef {
	if e == nil {
		return nil
	}
	r := Ref(*e)
	return &r
}

// Refs is [Ref] over a slice.
func Refs(es []fleet.EntityRef) []ProductRef {
	out := make([]ProductRef, len(es))
	for i, e := range es {
		out[i] = Ref(e)
	}
	return out
}

// ── reusable transport previews ──────────────────────────────────────────────

// ProductRefPreview is a bounded preview of navigable references.
type ProductRefPreview struct {
	fleet.RefPreview
	Items []ProductRef `json:"items"`
}

// RefPreview wraps every reference of a bounded preview.
func RefPreview(p fleet.RefPreview) ProductRefPreview {
	return ProductRefPreview{RefPreview: p, Items: Refs(p.Items)}
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
		items[i] = ProductEvidenceItem{EvidenceItem: e, Target: Ref(e.Target)}
	}
	return ProductEvidencePreview{EvidencePreview: p, Items: items}
}

// ProductAttentionItem is a navigable attention row.
type ProductAttentionItem struct {
	fleet.AttentionItem
	Entity ProductRef `json:"entity"`
}

func productAttentionItem(it fleet.AttentionItem) ProductAttentionItem {
	return ProductAttentionItem{AttentionItem: it, Entity: Ref(it.Entity)}
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
		NeighborhoodEdge: e, From: Ref(e.From), To: Ref(e.To),
		// Focus the target endpoint in its OWN kind's graph (a mixed projection has
		// revision and target endpoints, not only services).
		Href: Graph(e.To.Kind, e.To.Key),
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
		items[i] = ProductAttributedFinding{AttributedFinding: f, Entity: Ref(f.Entity)}
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
		items[i] = ProductAttributedLimitation{AttributedLimitation: l, Entity: Ref(l.Entity)}
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
	return &ProductOwnership{OwnershipInfo: *o, Ref: RefPtr(o.Ref)}
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

// ToOverview adds an href to every overview entry point.
func ToOverview(ov *fleet.Overview) *ProductOverview {
	out := &ProductOverview{Overview: *ov}
	out.Attention = productAttentionPreview(ov.Attention)
	out.RecentEvidence = productEvidencePreview(ov.RecentEvidence)
	out.EntryPoints = make([]ProductEntryPoint, len(ov.EntryPoints))
	for i, ep := range ov.EntryPoints {
		out.EntryPoints[i] = ProductEntryPoint{EntryPoint: ep, Href: EntryPoint(ep.View, ep.Category)}
	}
	return out
}

// ── entities ─────────────────────────────────────────────────────────────────

// ProductEntityList is a navigable page of entity references.
type ProductEntityList struct {
	fleet.EntityList
	Entities []ProductRef `json:"entities"`
}

// ToEntityList adds an href to every listed entity.
func ToEntityList(l *fleet.EntityList) *ProductEntityList {
	return &ProductEntityList{EntityList: *l, Entities: Refs(l.Entities)}
}

// ── attention ────────────────────────────────────────────────────────────────

// ProductAttentionList is a navigable, offset-paged attention answer.
type ProductAttentionList struct {
	fleet.AttentionList
	Items []ProductAttentionItem `json:"items"`
}

// ToAttentionList adds an href to every attention item.
func ToAttentionList(l *fleet.AttentionList) *ProductAttentionList {
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

// ToNeighborhood adds an href to every neighborhood node and edge.
func ToNeighborhood(nb *fleet.Neighborhood) *ProductNeighborhood {
	out := &ProductNeighborhood{
		Neighborhood:    *nb,
		RequestedFocus:  Ref(nb.RequestedFocus),
		ProjectionFocus: RefPtr(nb.ProjectionFocus),
		FocusService:    Ref(nb.FocusService),
	}
	out.Nodes = make([]ProductNode, len(nb.Nodes))
	for i, n := range nb.Nodes {
		out.Nodes[i] = ProductNode{NeighborhoodNode: n, Ref: Ref(n.Ref)}
	}
	out.Edges = make([]ProductEdge, len(nb.Edges))
	for i, e := range nb.Edges {
		out.Edges[i] = productEdge(e)
	}
	out.UnresolvedDependencies = productUnresolvedDependencies(nb.UnresolvedDependencies)
	return out
}

func productUnresolvedDependencies(p fleet.UnresolvedDependenciesPreview) ProductUnresolvedDependenciesPreview {
	deps := make([]ProductUnresolvedDependency, len(p.Items))
	for i, u := range p.Items {
		deps[i] = ProductUnresolvedDependency{UnresolvedDependency: u, From: Ref(u.From)}
	}
	return ProductUnresolvedDependenciesPreview{UnresolvedDependenciesPreview: p, Items: deps}
}

// ── entity detail (discriminated) ────────────────────────────────────────────

// ProductRevisionSignature is one in-use revision's signature check with a
// navigable revision.
type ProductRevisionSignature struct {
	fleet.RevisionSignature
	Revision ProductRef `json:"revision"`
}

// ProductRevisionSignaturesPreview is a bounded preview of revision signatures.
type ProductRevisionSignaturesPreview struct {
	fleet.RevisionSignaturesPreview
	Items []ProductRevisionSignature `json:"items"`
}

func productRevisionSignatures(p fleet.RevisionSignaturesPreview) ProductRevisionSignaturesPreview {
	items := make([]ProductRevisionSignature, len(p.Items))
	for i, s := range p.Items {
		items[i] = ProductRevisionSignature{RevisionSignature: s, Revision: Ref(s.Revision)}
	}
	return ProductRevisionSignaturesPreview{RevisionSignaturesPreview: p, Items: items}
}

// ProductServiceDetail is the navigable service-kind detail payload.
type ProductServiceDetail struct {
	fleet.ServiceDetailData
	Ownership              *ProductOwnership                    `json:"ownership,omitempty"`
	OwnerRevision          *ProductRef                          `json:"ownerRevision,omitempty"`
	Sources                ProductRefPreview                    `json:"sources"`
	UnresolvedDependencies ProductUnresolvedDependenciesPreview `json:"unresolvedDependencies"`
	ConsumedOfferings      ProductOfferingUsesPreview           `json:"consumedOfferings"`
	InferredOfferings      []ProductInferredOffering            `json:"inferredOfferings"`
	CurrentRevision        *ProductRef                          `json:"currentRevision,omitempty"`
	Policies               ProductPoliciesPreview               `json:"policies"`
	Configurations         ProductConfigurationsPreview         `json:"configurations"`
	Revisions              ProductRefPreview                    `json:"revisions"`
	ActiveRevisions        ProductRefPreview                    `json:"activeRevisions"`
	Signatures             ProductRevisionSignaturesPreview     `json:"signatures"`
	Deployments            ProductRefPreview                    `json:"deployments"`
	Dependencies           ProductRefPreview                    `json:"dependencies"`
	Dependents             ProductRefPreview                    `json:"dependents"`
	ReferencedBy           ProductRefPreview                    `json:"referencedBy"`
	Relationships          ProductRelationshipsPreview          `json:"relationships"`
	Findings               ProductAttributedFindingsPreview     `json:"findings"`
	Evidence               ProductEvidencePreview               `json:"evidence"`
	Limitations            ProductAttributedLimitationsPreview  `json:"limitations"`
}

func toProductServiceDetail(d *fleet.ServiceDetailData) *ProductServiceDetail {
	return &ProductServiceDetail{
		ServiceDetailData:      *d,
		Ownership:              productOwnership(d.Ownership),
		OwnerRevision:          RefPtr(d.OwnerRevision),
		Sources:                RefPreview(d.Sources),
		UnresolvedDependencies: productUnresolvedDependencies(d.UnresolvedDependencies),
		ConsumedOfferings:      productOfferingUses(d.ConsumedOfferings),
		InferredOfferings:      productInferredOfferings(d.InferredOfferings),
		CurrentRevision:        RefPtr(d.CurrentRevision),
		Policies:               productPoliciesPreview(d.Policies),
		Configurations:         productConfigurationsPreview(d.Configurations),
		Revisions:              RefPreview(d.Revisions),
		ActiveRevisions:        RefPreview(d.ActiveRevisions),
		Signatures:             productRevisionSignatures(d.Signatures),
		Deployments:            RefPreview(d.Deployments),
		Dependencies:           RefPreview(d.Dependencies),
		Dependents:             RefPreview(d.Dependents),
		ReferencedBy:           RefPreview(d.ReferencedBy),
		Relationships:          productRelationshipsPreview(d.Relationships),
		Findings:               productAttributedFindingsPreview(d.Findings),
		Evidence:               productEvidencePreview(d.Evidence),
		Limitations:            productAttributedLimitationsPreview(d.Limitations),
	}
}

// ── contract references ──────────────────────────────────────────────────────

// ProductRefResolution is a contract reference's resolution with a canonical
// href on the destination, so the UI links to the referenced service instead of
// re-deriving one from the raw ref string. The raw ref itself stays on the
// summary that carries this: it is authored contract information and is never
// replaced by the resolution.
type ProductRefResolution struct {
	fleet.RefResolution
	Service  *ProductRef `json:"service,omitempty"`
	Revision *ProductRef `json:"revision,omitempty"`
}

func productRefResolution(r *fleet.RefResolution) *ProductRefResolution {
	if r == nil {
		return nil
	}
	return &ProductRefResolution{RefResolution: *r, Service: RefPtr(r.Service), Revision: RefPtr(r.Revision)}
}

// ProductConfigurationSummary is a declared configuration scope whose ref, when
// it resolves, is navigable.
type ProductConfigurationSummary struct {
	fleet.ConfigurationSummary
	Resolution *ProductRefResolution `json:"resolution,omitempty"`
}

// ProductConfigurationsPreview is the bounded preview of those scopes.
type ProductConfigurationsPreview struct {
	fleet.ConfigurationsPreview
	Items []ProductConfigurationSummary `json:"items"`
}

func productConfigurationsPreview(p fleet.ConfigurationsPreview) ProductConfigurationsPreview {
	items := make([]ProductConfigurationSummary, len(p.Items))
	for i, c := range p.Items {
		items[i] = ProductConfigurationSummary{ConfigurationSummary: c, Resolution: productRefResolution(c.Resolution)}
	}
	return ProductConfigurationsPreview{ConfigurationsPreview: p, Items: items}
}

// ProductPolicySummary is a declared policy whose ref, when it resolves, is navigable.
type ProductPolicySummary struct {
	fleet.PolicySummary
	Resolution *ProductRefResolution `json:"resolution,omitempty"`
}

// ProductPoliciesPreview is the bounded preview of those policies.
type ProductPoliciesPreview struct {
	fleet.PoliciesPreview
	Items []ProductPolicySummary `json:"items"`
}

func productPoliciesPreview(p fleet.PoliciesPreview) ProductPoliciesPreview {
	items := make([]ProductPolicySummary, len(p.Items))
	for i, s := range p.Items {
		items[i] = ProductPolicySummary{PolicySummary: s, Resolution: productRefResolution(s.Resolution)}
	}
	return ProductPoliciesPreview{PoliciesPreview: p, Items: items}
}

// ProductRevisionDetail is the navigable revision-kind detail payload.
type ProductRevisionDetail struct {
	fleet.RevisionDetailData
	Service         ProductRef                   `json:"service"`
	Configurations  ProductConfigurationsPreview `json:"configurations"`
	Policies        ProductPoliciesPreview       `json:"policies"`
	Dependencies    ProductRelationshipsPreview  `json:"dependencies"`
	ExactTargets    ProductRefPreview            `json:"exactTargets"`
	InferredTargets ProductRefPreview            `json:"inferredTargets"`
	Previous        *ProductRef                  `json:"previous,omitempty"`
	Next            *ProductRef                  `json:"next,omitempty"`
	Ownership       *ProductOwnership            `json:"ownership,omitempty"`
}

func toProductRevisionDetail(d *fleet.RevisionDetailData) *ProductRevisionDetail {
	return &ProductRevisionDetail{
		RevisionDetailData: *d,
		Service:            Ref(d.Service),
		Configurations:     productConfigurationsPreview(d.Configurations),
		Policies:           productPoliciesPreview(d.Policies),
		Dependencies:       productRelationshipsPreview(d.Dependencies),
		ExactTargets:       RefPreview(d.ExactTargets),
		InferredTargets:    RefPreview(d.InferredTargets),
		Previous:           RefPtr(d.Previous),
		Next:               RefPtr(d.Next),
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
		Service:              Ref(d.Service),
		Revision:             RefPtr(d.Revision),
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
		Services:        RefPreview(d.Services),
		Revisions:       RefPreview(d.Revisions),
		Deployments:     RefPreview(d.Deployments),
		Attention:       productAttentionPreview(d.Attention),
	}
}

// ProductDomainDetail is the navigable domain-kind detail payload.
type ProductDomainDetail struct {
	fleet.DomainDetailData
	Services    ProductRefPreview       `json:"services"`
	Owners      ProductRefPreview       `json:"owners"`
	Deployments ProductRefPreview       `json:"deployments"`
	Attention   ProductAttentionPreview `json:"attention"`
}

func toProductDomainDetail(d *fleet.DomainDetailData) *ProductDomainDetail {
	return &ProductDomainDetail{
		DomainDetailData: *d,
		Services:         RefPreview(d.Services),
		Owners:           RefPreview(d.Owners),
		Deployments:      RefPreview(d.Deployments),
		Attention:        productAttentionPreview(d.Attention),
	}
}

// ProductOfferingUse is one service consuming a provider of a platform offering,
// both navigable.
type ProductOfferingUse struct {
	fleet.OfferingUse
	Consumer     ProductRef                  `json:"consumer"`
	Provider     ProductRef                  `json:"provider"`
	Provisioning []ProductProvisioningStatus `json:"provisioning,omitempty"`
}

// ProductProvisioningStatus is a provisioning status whose consumer revision is
// navigable.
type ProductProvisioningStatus struct {
	fleet.ProvisioningStatus
	Revision *ProductRef `json:"revision,omitempty"`
}

// ProductOfferingUsesPreview is a bounded preview of offering uses.
type ProductOfferingUsesPreview struct {
	fleet.OfferingUsesPreview
	Items []ProductOfferingUse `json:"items"`
}

func productOfferingUses(p fleet.OfferingUsesPreview) ProductOfferingUsesPreview {
	items := make([]ProductOfferingUse, len(p.Items))
	for i, u := range p.Items {
		items[i] = ProductOfferingUse{OfferingUse: u, Consumer: Ref(u.Consumer), Provider: Ref(u.Provider)}
		for _, st := range u.Provisioning {
			items[i].Provisioning = append(items[i].Provisioning, ProductProvisioningStatus{ProvisioningStatus: st, Revision: RefPtr(st.Revision)})
		}
	}
	return ProductOfferingUsesPreview{OfferingUsesPreview: p, Items: items}
}

// ProductInferredOffering is a navigable inferred offering suggestion.
type ProductInferredOffering struct {
	fleet.InferredOffering
	Offering ProductRef  `json:"offering"`
	Provider *ProductRef `json:"provider,omitempty"`
}

func productInferredOfferings(in []fleet.InferredOffering) []ProductInferredOffering {
	out := make([]ProductInferredOffering, len(in))
	for i, o := range in {
		out[i] = ProductInferredOffering{InferredOffering: o, Offering: Ref(o.Offering), Provider: RefPtr(o.Provider)}
	}
	return out
}

// ProductOfferingProvider is one navigable provider of a platform offering.
type ProductOfferingProvider struct {
	fleet.OfferingProvider
	Service         ProductRef                   `json:"service"`
	Ownership       *ProductOwnership            `json:"ownership,omitempty"`
	CurrentRevision *ProductRef                  `json:"currentRevision,omitempty"`
	Versions        ProductProviderVersions      `json:"versions"`
	Policies        ProductPoliciesPreview       `json:"policies"`
	Configurations  ProductConfigurationsPreview `json:"configurations"`
}

// ProductProviderVersion is one navigable version of an offering provider.
type ProductProviderVersion struct {
	fleet.ProviderVersion
	Revision ProductRef `json:"revision"`
}

// ProductProviderVersions is a navigable preview of an offering provider's
// versions.
type ProductProviderVersions struct {
	fleet.ProviderVersionsPreview
	Items            []ProductProviderVersion `json:"items"`
	LatestCompatible *ProductRef              `json:"latestCompatible,omitempty"`
}

func productProviderVersions(p fleet.ProviderVersionsPreview) ProductProviderVersions {
	items := make([]ProductProviderVersion, len(p.Items))
	for i, v := range p.Items {
		items[i] = ProductProviderVersion{ProviderVersion: v, Revision: Ref(v.Revision)}
	}
	return ProductProviderVersions{ProviderVersionsPreview: p, Items: items, LatestCompatible: RefPtr(p.LatestCompatible)}
}

// ProductOfferingProviderVersions is one navigable provider's versions.
type ProductOfferingProviderVersions struct {
	fleet.OfferingProviderVersions
	Provider ProductRef              `json:"provider"`
	Versions ProductProviderVersions `json:"versions"`
}

// ProductOfferingVersions is the navigable answer to which versions each
// provider of an offering has and which satisfy a range.
type ProductOfferingVersions struct {
	fleet.OfferingVersionsResult
	Offering  ProductRef                        `json:"offering"`
	Providers []ProductOfferingProviderVersions `json:"providers"`
}

// ToOfferingVersions adds an href to the offering, every provider and every
// version.
func ToOfferingVersions(r *fleet.OfferingVersionsResult) *ProductOfferingVersions {
	out := &ProductOfferingVersions{OfferingVersionsResult: *r, Offering: Ref(r.Offering), Providers: make([]ProductOfferingProviderVersions, len(r.Providers))}
	for i, p := range r.Providers {
		out.Providers[i] = ProductOfferingProviderVersions{OfferingProviderVersions: p, Provider: Ref(p.Provider), Versions: productProviderVersions(p.Versions)}
	}
	return out
}

// ProductOfferingProvidersPreview is a bounded preview of offering providers.
type ProductOfferingProvidersPreview struct {
	fleet.OfferingProvidersPreview
	Items []ProductOfferingProvider `json:"items"`
}

// ProductOfferingDetail is the navigable offering-kind detail payload.
type ProductOfferingDetail struct {
	fleet.OfferingDetailData
	Providers ProductOfferingProvidersPreview `json:"providers"`
	Consumers ProductOfferingUsesPreview      `json:"consumers"`
}

func toProductOfferingDetail(d *fleet.OfferingDetailData) *ProductOfferingDetail {
	items := make([]ProductOfferingProvider, len(d.Providers.Items))
	for i, p := range d.Providers.Items {
		items[i] = ProductOfferingProvider{
			OfferingProvider: p,
			Service:          Ref(p.Service),
			Ownership:        productOwnership(p.Ownership),
			CurrentRevision:  RefPtr(p.CurrentRevision),
			Versions:         productProviderVersions(p.Versions),
			Policies:         productPoliciesPreview(p.Policies),
			Configurations:   productConfigurationsPreview(p.Configurations),
		}
	}
	return &ProductOfferingDetail{
		OfferingDetailData: *d,
		Providers:          ProductOfferingProvidersPreview{OfferingProvidersPreview: d.Providers, Items: items},
		Consumers:          productOfferingUses(d.Consumers),
	}
}

// ProductSourceDetail is the navigable source-kind detail payload.
type ProductSourceDetail struct {
	fleet.SourceDetailData
	Entities ProductRefPreview `json:"entities"`
}

func toProductSourceDetail(d *fleet.SourceDetailData) *ProductSourceDetail {
	return &ProductSourceDetail{SourceDetailData: *d, Entities: RefPreview(d.Entities)}
}

// ProductRevisionDocument is one lazily-read in-bundle document with a navigable
// reference back to the revision that owns it.
type ProductRevisionDocument struct {
	fleet.RevisionDocument
	Revision ProductRef `json:"revision"`
}

// ToRevisionDocument adds an href to the owning revision.
func ToRevisionDocument(d *fleet.RevisionDocument) *ProductRevisionDocument {
	return &ProductRevisionDocument{RevisionDocument: *d, Revision: Ref(d.Revision)}
}

// ProductEntityDetail is the navigable, discriminated entity-detail envelope.
// Exactly one of Service/Revision/Target/Owner/Source/Domain/Offering is populated, matching
// Entity.Kind.
type ProductEntityDetail struct {
	fleet.EntityDetail
	Entity   ProductRef             `json:"entity"`
	Service  *ProductServiceDetail  `json:"service,omitempty"`
	Revision *ProductRevisionDetail `json:"revision,omitempty"`
	Target   *ProductTargetDetail   `json:"target,omitempty"`
	Owner    *ProductOwnerDetail    `json:"owner,omitempty"`
	Source   *ProductSourceDetail   `json:"source,omitempty"`
	Domain   *ProductDomainDetail   `json:"domain,omitempty"`
	Offering *ProductOfferingDetail `json:"offering,omitempty"`
}

// ToEntityDetail adds an href to every reference of an entity detail.
func ToEntityDetail(d *fleet.EntityDetail) *ProductEntityDetail {
	out := &ProductEntityDetail{EntityDetail: *d, Entity: Ref(d.Entity)}
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
	case d.Domain != nil:
		out.Domain = toProductDomainDetail(d.Domain)
	case d.Offering != nil:
		out.Offering = toProductOfferingDetail(d.Offering)
	}
	return out
}
