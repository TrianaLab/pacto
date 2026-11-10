package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v4/pkg/dashboard/productview"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// registerProductOperations registers the product-oriented dashboard APIs. They
// are bounded, versioned answers built for product
// questions — the frontend consumes these instead of reconstructing meaning from
// the full snapshot. Every response is a dashboard transport DTO that wraps the
// route-neutral fleet fact and adds canonical navigation hrefs (ADR-2). All
// require the fleet provider; the POST-impact endpoint also requires the impact
// provider.
func (s *Server) registerProductOperations(api huma.API) {
	if s.opts.Fleet == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-overview", Method: http.MethodGet, Path: "/api/fleet/overview",
		Summary: "Operational overview", Tags: []string{"Fleet"},
		Description: "A product-oriented summary: what needs attention, incomplete sources, recent evidence and suggested entry points, each with a canonical href.",
	}, s.fleetOverview)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-entities", Method: http.MethodGet, Path: "/api/fleet/entities",
		Summary: "Search entities", Tags: []string{"Fleet"},
		Description: "Searches services, revisions, targets, owners and sources, returning stable navigable references. Powers global search, graph focus and entity pickers.",
	}, s.fleetEntities)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-versions", Method: http.MethodGet, Path: "/api/fleet/versions",
		Summary: "Offering provider versions", Tags: []string{"Fleet"},
		Description: "Lists the versions of every provider of a platform offering, or of one provider, newest first, with each version's lifecycle and, when a range is given, whether it satisfies the range and the latest compatible version. The list comes from the contract revisions the fleet holds; the registry may publish others.",
	}, s.fleetVersions)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-entity-detail", Method: http.MethodGet, Path: "/api/fleet/entities/{kind}",
		Summary: "Entity detail", Tags: []string{"Fleet"},
		Description: "Returns one entity's strongly typed, discriminated detail (service, revision, target, owner or source), keyed by the query-param key so a key containing a slash is transported safely.",
	}, s.fleetEntityDetail)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-neighborhood", Method: http.MethodGet, Path: "/api/fleet/neighborhood",
		Summary: "Bounded neighborhood", Tags: []string{"Fleet"},
		Description: "Returns the focus entity's bounded local neighborhood across the expected, observed and differences knowledge views, with graph-ready nodes and edges.",
	}, s.fleetNeighborhood)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-attention", Method: http.MethodGet, Path: "/api/fleet/attention",
		Summary: "Attention list", Tags: []string{"Fleet"},
		Description: "Returns navigable, offset-paged attention items across every category; each links to the exact affected entity and recommends a next step.",
	}, s.fleetAttention)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-revision-document", Method: http.MethodGet, Path: "/api/fleet/revisions/document",
		Summary: "Read a revision document", Tags: []string{"Fleet"},
		Description: "Reads ONE in-bundle document body belonging to exactly the given canonical revision. The path must be one the revision already published in its docs list, which is what prevents traversal and cross-revision reads; the body is size-bounded and an oversized or unreadable document is an explicit error rather than empty content.",
	}, s.fleetRevisionDocument)

	if s.opts.Impact != nil {
		huma.Register(api, huma.Operation{
			OperationID: "fleet-impact-post", Method: http.MethodPost, Path: "/api/fleet/impact",
			Summary: "Analyze impact by canonical identity", Tags: []string{"Fleet"},
			Description: "Analyzes a change between two contract revisions identified by canonical revision keys. It rejects a stale snapshot id AND any revision whose exact snapshot content is not retrievable (a mutable tag or a local path), so a success always analyzed the exact content the snapshot represents. With event set it instead answers what happens if serviceKey becomes unavailable, is removed or regressed, over the same snapshot; a regressed artifact digest cites what delivery recorded, read with the caller's own identity.",
		}, s.fleetImpactPost)
	}
}

type fleetOverviewOutput struct{ Body *ProductOverview }

func (s *Server) fleetOverview(ctx context.Context, _ *struct{}) (*fleetOverviewOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	return &fleetOverviewOutput{Body: productview.ToOverview(q.Overview())}, nil
}

type fleetEntitiesInput struct {
	Text            string   `query:"text"`
	Kinds           []string `query:"kinds" enum:"service,revision,target,owner,source,domain,offering" doc:"Comma-separated entity kinds; an unknown or empty kind is rejected. A domain is a business domain (service.domain). An offering is a platform offering (service.offering)"`
	Owner           string   `query:"owner" doc:"Free-text owner SEARCH: case-insensitive substring over team, DRI and contacts"`
	OwnerKey        string   `query:"ownerKey" doc:"Exact canonical owner IDENTITY, namespaced as team:NAME or dri:NAME, exactly as canonical owner links carry it. A bare owner name matches nothing"`
	Domain          string   `query:"domain" doc:"Exact registry path (the domain part of the canonical service key), shown as Registry"`
	BusinessDomain  string   `query:"businessDomain" doc:"Exact business domain (service.domain) of the service an entity belongs to (a revision is kept by its service's domain), (none) for the services with no domain, or (unknown) for the services whose domain Pacto cannot tell"`
	Environment     string   `query:"environment" doc:"Exact environment a target runs in, from the delivery.pacto.trianalab.io/environment label (target entities)"`
	Capability      string   `query:"capability" doc:"Capability key a service's revisions declare (service entities): health, metrics or, for an extension, its namespaced ref such as acme.io/cost"`
	Offering        string   `query:"offering" doc:"Exact platform offering (service.offering) a service provides (service and offering entities)"`
	Consumes        string   `query:"consumes" doc:"Platform offering (service.offering) slug: keeps the services with a declared, resolved dependency or configuration reference to a provider of it (service entities)"`
	Phase           string   `query:"phase" enum:"requested,provisioning,ready,failed,deleting,unknown" doc:"With consumes: keeps the consumers with a provisioning status of that offering in this phase (unknown: no phase a provider reported can be trusted)"`
	Scope           string   `query:"scope"`
	Status          string   `query:"status" enum:"Compliant,Invalid,NonCompliant,NotEvaluated,Reference,Unknown,Warning" doc:"Compliance status filter (service/revision/target)"`
	Ownership       string   `query:"ownership" enum:"consistent,conflicting,unowned,unidentified" doc:"Declared-ownership filter for service entities; unidentified is every service with no single canonical owner (unowned, conflicting or contacts alone)"`
	Readiness       string   `query:"readiness" enum:"passing,below-threshold,expired,not-declared" doc:"Declared-readiness filter for contract revision entities"`
	SourceHealth    string   `query:"sourceHealth" enum:"available,partial,stale,unavailable" doc:"Source-health filter for source entities"`
	Source          string   `query:"source"`
	Service         string   `query:"service" doc:"Scope revision/target entities to a canonical parent ServiceKey (pages all revisions of one service)"`
	Template        string   `query:"template" enum:"only,exclude" doc:"Golden path filter for service and revision entities: only keeps the golden paths (services whose contract declares service.template and that run nowhere) and, with no kinds, lists services only; exclude leaves them and their revisions out. Absent lists every service, golden paths marked template"`
	CreatedFrom     string   `query:"createdFrom" doc:"Keeps the services whose declared origin resolves to this golden path, by canonical ServiceKey (service entities); (none) keeps those that declare no origin and (unresolved) those whose origin resolves to no golden path. A hidden, absent or non-golden-path key answers an empty list"`
	HasNonCompliant bool     `query:"hasNonCompliant" doc:"Keeps the owners and domains with at least one non-compliant service, counted as each row counts it (owner and domain entities)"`
	SourceKind      string   `query:"sourceKind" doc:"Exact source kind (oci, kubernetes, evidence-http, ...) for source entities"`
	Sort            string   `query:"sort" enum:"name,health,services,nonCompliant,rate,created" doc:"Order: name (natural, so 2 before 10), health (least healthy first), services (owners with the most services first), nonCompliant (owners and domains with the most non-compliant services first), rate (owners and domains with the highest share of non-compliant services first) or created (golden paths that created the most services first); absent orders by kind then key"`
	Limit           int      `query:"limit" minimum:"0" doc:"Max entities to return (negatives rejected; excessive values capped)"`
	Offset          int      `query:"offset" minimum:"0"`
}

type fleetEntitiesOutput struct{ Body *ProductEntityList }

func (s *Server) fleetEntities(ctx context.Context, in *fleetEntitiesInput) (*fleetEntitiesOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	kinds := in.Kinds
	if len(kinds) == 0 && in.Template == fleet.TemplateOnly {
		kinds = []string{string(fleet.KindService)}
	}
	res, err := q.Entities(fleet.EntityFilter{
		Text: in.Text, Kinds: entityKinds(kinds), Owner: in.Owner, OwnerKey: in.OwnerKey, Domain: in.Domain,
		BusinessDomain: in.BusinessDomain, Environment: in.Environment, Capability: in.Capability, Offering: in.Offering, Consumes: in.Consumes, Phase: in.Phase,
		Scope: in.Scope, Status: in.Status, SourceHealth: in.SourceHealth, SourceKind: in.SourceKind, Source: in.Source,
		HasNonCompliant: in.HasNonCompliant,
		Ownership:       in.Ownership, Readiness: in.Readiness,
		Service: in.Service, Template: in.Template, CreatedFrom: in.CreatedFrom, Sort: in.Sort, Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetEntitiesOutput{Body: productview.ToEntityList(res)}, nil
}

type fleetVersionsInput struct {
	Offering string `query:"offering" required:"true" doc:"Platform offering (service.offering) slug"`
	Provider string `query:"provider" doc:"One provider of the offering, by canonical service key or unique name; absent lists every provider"`
	Range    string `query:"range" doc:"Semver range such as ^16.0.0; each version reports whether it satisfies it"`
}

type fleetVersionsOutput struct{ Body *ProductOfferingVersions }

func (s *Server) fleetVersions(ctx context.Context, in *fleetVersionsInput) (*fleetVersionsOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	res, err := q.OfferingVersions(in.Offering, in.Provider, in.Range)
	var bad *fleet.InvalidQueryError
	if errors.As(err, &bad) {
		return nil, huma.Error400BadRequest(bad.Error(), err)
	}
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetVersionsOutput{Body: productview.ToOfferingVersions(res)}, nil
}

type fleetEntityDetailInput struct {
	Kind string `path:"kind" doc:"Entity kind: service, revision, target, owner, source, domain or offering"`
	Key  string `query:"key" required:"true" doc:"Canonical entity key (a slash-bearing key is transported safely as a query param)"`
}

type fleetEntityDetailOutput struct{ Body *ProductEntityDetail }

func (s *Server) fleetEntityDetail(ctx context.Context, in *fleetEntityDetailInput) (*fleetEntityDetailOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	res, err := q.EntityDetail(fleet.EntityKind(in.Kind), in.Key)
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetEntityDetailOutput{Body: productview.ToEntityDetail(res)}, nil
}

type fleetRevisionDocumentInput struct {
	Key  string `query:"key" required:"true" doc:"Canonical revision key the document belongs to"`
	Path string `query:"path" required:"true" doc:"In-bundle document path, exactly as published in the revision's docs list"`
}

type fleetRevisionDocumentOutput struct{ Body *ProductRevisionDocument }

func (s *Server) fleetRevisionDocument(ctx context.Context, in *fleetRevisionDocumentInput) (*fleetRevisionDocumentOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	res, err := q.RevisionDocument(in.Key, in.Path)
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetRevisionDocumentOutput{Body: productview.ToRevisionDocument(res)}, nil
}

type fleetNeighborhoodInput struct {
	Kind        string `query:"kind" required:"true" doc:"Focus entity kind: service, revision or target"`
	Key         string `query:"key" required:"true"`
	Perspective string `query:"perspective" enum:"service,revision,target" doc:"Projection kind: service (default), revision or target"`
	Direction   string `query:"direction" enum:"dependencies,dependents,both" doc:"dependencies, dependents or both (default both)"`
	Depth       int    `query:"depth" minimum:"0" doc:"Traversal depth (negatives rejected; excessive values capped)"`
	Views       string `query:"views" doc:"Comma-separated knowledge views (expected,observed,differences)"`
	MaxNodes    int    `query:"maxNodes" minimum:"0" doc:"Max nodes (negatives rejected; excessive values capped)"`
	MaxEdges    int    `query:"maxEdges" minimum:"0" doc:"Max edges (negatives rejected; excessive values capped)"`
}

type fleetNeighborhoodOutput struct{ Body *ProductNeighborhood }

func (s *Server) fleetNeighborhood(ctx context.Context, in *fleetNeighborhoodInput) (*fleetNeighborhoodOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	res, err := q.Neighborhood(fleet.NeighborhoodQuery{
		Kind: fleet.EntityKind(in.Kind), Key: in.Key, Perspective: fleet.Perspective(in.Perspective),
		Direction: fleet.Direction(in.Direction),
		Depth:     in.Depth, Views: parseViews(in.Views), MaxNodes: in.MaxNodes, MaxEdges: in.MaxEdges,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetNeighborhoodOutput{Body: productview.ToNeighborhood(res)}, nil
}

type fleetAttentionInput struct {
	Category       string `query:"category"`
	Kind           string `query:"kind"`
	Key            string `query:"key"`
	Service        string `query:"service"`
	Owner          string `query:"owner" doc:"Free-text owner SEARCH: case-insensitive substring over team, DRI and contacts"`
	OwnerKey       string `query:"ownerKey" doc:"Exact canonical owner IDENTITY, namespaced as team:NAME or dri:NAME, exactly as canonical owner links carry it. A bare owner name matches nothing"`
	BusinessDomain string `query:"businessDomain" doc:"Keep the items of services in this business domain, (none) for the services with no domain, or (unknown) for the services whose domain Pacto cannot tell"`
	Source         string `query:"source"`
	Severity       string `query:"severity" enum:"error,warning,info"`
	Status         string `query:"status" enum:"Compliant,Invalid,NonCompliant,NotEvaluated,Reference,Unknown,Warning" doc:"Compliance status filter"`
	StaleOnly      bool   `query:"staleOnly"`
	Limit          int    `query:"limit" minimum:"0" doc:"Max items per page (negatives rejected; excessive values capped)"`
	Offset         int    `query:"offset" minimum:"0" doc:"Page offset (negatives rejected)"`
}

type fleetAttentionOutput struct{ Body *ProductAttentionList }

func (s *Server) fleetAttention(ctx context.Context, in *fleetAttentionInput) (*fleetAttentionOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	res, err := q.Attention(fleet.AttentionFilter{
		Category: in.Category, Kind: in.Kind, Key: in.Key, Service: in.Service,
		Owner: in.Owner, OwnerKey: in.OwnerKey, BusinessDomain: in.BusinessDomain, Source: in.Source, Severity: in.Severity, Status: in.Status, StaleOnly: in.StaleOnly,
		Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetAttentionOutput{Body: productview.ToAttentionList(res)}, nil
}

// ProductImpactConsumer is a navigable affected consumer with a bounded path.
type ProductImpactConsumer struct {
	impact.ProductConsumer
	Service  ProductRef   `json:"service"`
	Path     []ProductRef `json:"path"`
	Targets  []ProductRef `json:"targets,omitempty"`
	OwnerRef *ProductRef  `json:"ownerRef,omitempty"`
}

// ProductImpactConsumersPage is a stable, offset-paged consumer answer.
type ProductImpactConsumersPage struct {
	impact.ConsumersPage
	Items      []ProductImpactConsumer `json:"items"`
	Highlights []ProductImpactConsumer `json:"highlights"`
}

// ProductImpact is the navigable impact answer: [impact.Product] with a canonical
// href on every reference. It deliberately does NOT embed the raw impact.Result;
// the raw GET /api/fleet/impact endpoint remains for machine consumers that want
// it.
type ProductImpact struct {
	impact.Product
	Service       ProductRef                 `json:"service"`
	OldRevision   *ProductRef                `json:"oldRevision,omitempty"`
	NewRevision   *ProductRef                `json:"newRevision,omitempty"`
	Consumers     ProductImpactConsumersPage `json:"consumers"`
	Owners        ProductRefPreview          `json:"owners"`
	ActiveTargets ProductRefPreview          `json:"activeTargets"`
	ChangedOwner  *ProductRef                `json:"changedOwner,omitempty"`
}

func toProductImpact(p *impact.Product) *ProductImpact {
	consumers := func(cs []impact.ProductConsumer) []ProductImpactConsumer {
		out := make([]ProductImpactConsumer, len(cs))
		for i, c := range cs {
			out[i] = ProductImpactConsumer{ProductConsumer: c, Service: productview.Ref(c.Service), Path: productview.Refs(c.Path),
				OwnerRef: productview.RefPtr(c.OwnerRef)}
			if len(c.Targets) > 0 {
				out[i].Targets = productview.Refs(c.Targets)
			}
			// A caller no contract describes has no page to link to.
			if c.Uncontracted {
				out[i].Service.Href = ""
			}
		}
		return out
	}
	return &ProductImpact{
		Product:       *p,
		Service:       productview.Ref(p.Service),
		OldRevision:   productview.RefPtr(p.OldRevision),
		NewRevision:   productview.RefPtr(p.NewRevision),
		Consumers:     ProductImpactConsumersPage{ConsumersPage: p.Consumers, Items: consumers(p.Consumers.Items), Highlights: consumers(p.Consumers.Highlights)},
		Owners:        productview.RefPreview(p.Owners),
		ActiveTargets: productview.RefPreview(p.ActiveTargets),
		ChangedOwner:  productview.RefPtr(p.ChangedOwner),
	}
}

// impactRequest is the POST-impact body: canonical revision keys, the snapshot the
// client analyzed (for staleness rejection) and consumer paging.
type impactRequest struct {
	SnapshotID      string `json:"snapshotId,omitempty" doc:"The snapshot the client analyzed; a mismatch with the published snapshot is rejected"`
	ServiceKey      string `json:"serviceKey,omitempty"`
	FromRevisionKey string `json:"fromRevisionKey,omitempty" doc:"Canonical revision key of the old revision. Required unless event is set"`
	ToRevisionKey   string `json:"toRevisionKey,omitempty" doc:"Canonical revision key of the new revision. Required unless event is set"`
	Event           string `json:"event,omitempty" enum:"unavailable,removed,regressed" doc:"Ask what happens if serviceKey becomes unavailable, is removed or regressed, instead of comparing two revisions"`
	Revision        string `json:"revision,omitempty" doc:"With event regressed: the artifact digest (sha256:...) or version that regressed; a digest lets the answer cite delivery's recorded failures"`
	IncludeObserved bool   `json:"includeObserved,omitempty"`
	Limit           int    `json:"limit,omitempty" doc:"Max consumers per page (negatives rejected; excessive values capped)"`
	Offset          int    `json:"offset,omitempty" doc:"Consumer page offset (negatives rejected)"`
	Outcome         string `json:"outcome,omitempty" enum:"incompatible,unknown,compatible,unclassified,breaks,may-degrade" doc:"List only the consumers of this outcome; the tallies still cover every consumer. An event answer's outcomes are breaks, may-degrade and unknown"`
}

type fleetImpactPostInput struct {
	Body impactRequest
}

type fleetImpactPostOutput struct{ Body *ProductImpact }

func (s *Server) fleetImpactPost(ctx context.Context, in *fleetImpactPostInput) (*fleetImpactPostOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, err
	}
	snap := q.Snapshot()
	b := in.Body
	if b.Limit < 0 || b.Offset < 0 {
		return nil, huma.Error422UnprocessableEntity("paging", errors.New("limit and offset must be >= 0"))
	}
	if b.SnapshotID != "" && b.SnapshotID != snap.SnapshotID {
		return nil, huma.Error409Conflict("snapshot mismatch: the published snapshot changed; refetch and retry")
	}
	if b.Event != "" {
		return s.fleetImpactEvent(ctx, q, b)
	}
	if b.FromRevisionKey == "" || b.ToRevisionKey == "" {
		return nil, huma.Error422UnprocessableEntity("revisionKey", errors.New("a contract change needs fromRevisionKey and toRevisionKey; an event needs event and serviceKey"))
	}
	// Both keys must exist, belong to the SAME logical service, and (if given) match
	// the requested ServiceKey - before any content is fetched.
	svcKey, fromRev, toRev, err := impact.ProductRevisions(snap, b.ServiceKey, b.FromRevisionKey, b.ToRevisionKey)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("revisionKey", err)
	}
	// Exact-content invariant: a canonical Product Impact may only analyze the exact
	// immutable content the snapshot revisions name. A mutable tag or a local path is
	// rejected BEFORE the provider is invoked, so the server never fetches
	// potentially-different content and then claims snapshot parity.
	oldRef, err := impact.ImmutableRef(fromRev)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("fromRevisionKey", err)
	}
	newRef, err := impact.ImmutableRef(toRev)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("toRevisionKey", err)
	}
	res, err := s.opts.Impact(ctx, q.SnapshotForSerialization(), oldRef, newRef, b.IncludeObserved)
	if err != nil {
		return nil, impactHTTPError(err)
	}
	// Snapshot parity: the analysis must have run over the SAME snapshot the handler
	// validated. If the Manager published a new snapshot in between, the analysis id
	// differs; reject rather than stamp a divergent answer with our snapshot id.
	if res.SnapshotID != snap.SnapshotID {
		return nil, huma.Error409Conflict("snapshot changed during analysis; refetch and retry")
	}
	return &fleetImpactPostOutput{Body: toProductImpact(impact.BuildProductOutcome(q.ProductMeta(), snap, svcKey, fromRev, toRev, res, b.Limit, b.Offset, b.Outcome))}, nil
}

// productQueryError maps a product-query error to HTTP: absent → 404, a document
// that exists but cannot be served → 422 carrying the exact reason, everything
// else (invalid filter, ambiguous key) → 422 with an actionable message.
func productQueryError(err error) error {
	var nf *fleet.NotFoundError
	if errors.As(err, &nf) {
		return huma.Error404NotFound("not found", err)
	}
	// The document is listed but its body is oversized, unreadable or not text.
	// That is an explicit failure with its own explanation, never empty content.
	var du *fleet.DocumentUnavailableError
	if errors.As(err, &du) {
		return huma.Error422UnprocessableEntity(du.Error(), err)
	}
	return huma.Error422UnprocessableEntity("invalid query", err)
}

// entityKinds converts the validated kinds parameter into entity kinds.
func entityKinds(kinds []string) []fleet.EntityKind {
	out := make([]fleet.EntityKind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, fleet.EntityKind(k))
	}
	return out
}

// stubProvidersForSchemaExport wires no-op fleet and impact providers so
// [ExportOpenAPI] registers every fleet and product operation for SCHEMA
// generation. The handlers are never invoked during export, so the stubs' errors
// are unreachable; they exist only to satisfy the nil-provider gates.
func (s *Server) stubProvidersForSchemaExport() {
	s.opts.Fleet = func(context.Context) (*fleet.Query, error) {
		return nil, errors.New("schema export only")
	}
	s.opts.Impact = func(context.Context, *fleet.FleetSnapshot, string, string, bool) (*impact.Result, error) {
		return nil, errors.New("schema export only")
	}
	s.opts.Delivery = func(*http.Request) (DeliveryAPI, error) { return nil, errors.New("schema export only") }
}

func parseViews(s string) []fleet.KnowledgeView {
	if s == "" {
		return nil
	}
	var out []fleet.KnowledgeView
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, fleet.KnowledgeView(p))
		}
	}
	return out
}
