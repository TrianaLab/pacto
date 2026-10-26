package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

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
			Description: "Analyzes a change between two contract revisions identified by canonical revision keys. It rejects a stale snapshot id AND any revision whose exact snapshot content is not retrievable (a mutable tag or a local path), so a success always analyzed the exact content the snapshot represents.",
		}, s.fleetImpactPost)
	}
}

type fleetOverviewOutput struct{ Body *ProductOverview }

func (s *Server) fleetOverview(ctx context.Context, _ *struct{}) (*fleetOverviewOutput, error) {
	q, err := s.opts.Fleet(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	return &fleetOverviewOutput{Body: toProductOverview(q.Overview())}, nil
}

type fleetEntitiesInput struct {
	Text         string   `query:"text"`
	Kinds        []string `query:"kinds" enum:"service,revision,target,owner,source" doc:"Comma-separated entity kinds; an unknown or empty kind is rejected"`
	Owner        string   `query:"owner" doc:"Free-text owner SEARCH: case-insensitive substring over team, DRI and contacts"`
	OwnerKey     string   `query:"ownerKey" doc:"Exact canonical owner IDENTITY, namespaced as team:NAME or dri:NAME, exactly as canonical owner links carry it. A bare owner name matches nothing"`
	Domain       string   `query:"domain"`
	Scope        string   `query:"scope"`
	Status       string   `query:"status" enum:"Compliant,Invalid,NonCompliant,NotEvaluated,Reference,Unknown,Warning" doc:"Compliance status filter (service/revision/target)"`
	Ownership    string   `query:"ownership" enum:"consistent,conflicting,unowned" doc:"Declared-ownership filter for service entities"`
	Readiness    string   `query:"readiness" enum:"passing,below-threshold,expired,not-declared" doc:"Declared-readiness filter for contract revision entities"`
	SourceHealth string   `query:"sourceHealth" enum:"available,partial,stale,unavailable" doc:"Source-health filter for source entities"`
	Source       string   `query:"source"`
	Service      string   `query:"service" doc:"Scope revision/target entities to a canonical parent ServiceKey (pages all revisions of one service)"`
	Sort         string   `query:"sort" enum:"name,health,services" doc:"Order: name (natural, so 2 before 10), health (least healthy first) or services (owners with the most services first); absent orders by kind then key"`
	Limit        int      `query:"limit" minimum:"0" doc:"Max entities to return (negatives rejected; excessive values capped)"`
	Offset       int      `query:"offset" minimum:"0"`
}

type fleetEntitiesOutput struct{ Body *ProductEntityList }

func (s *Server) fleetEntities(ctx context.Context, in *fleetEntitiesInput) (*fleetEntitiesOutput, error) {
	q, err := s.opts.Fleet(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Entities(fleet.EntityFilter{
		Text: in.Text, Kinds: entityKinds(in.Kinds), Owner: in.Owner, OwnerKey: in.OwnerKey, Domain: in.Domain,
		Scope: in.Scope, Status: in.Status, SourceHealth: in.SourceHealth, Source: in.Source,
		Ownership: in.Ownership, Readiness: in.Readiness,
		Service: in.Service, Sort: in.Sort, Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetEntitiesOutput{Body: toProductEntityList(res)}, nil
}

type fleetEntityDetailInput struct {
	Kind string `path:"kind" doc:"Entity kind: service, revision, target, owner or source"`
	Key  string `query:"key" required:"true" doc:"Canonical entity key (a slash-bearing key is transported safely as a query param)"`
}

type fleetEntityDetailOutput struct{ Body *ProductEntityDetail }

func (s *Server) fleetEntityDetail(ctx context.Context, in *fleetEntityDetailInput) (*fleetEntityDetailOutput, error) {
	q, err := s.opts.Fleet(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.EntityDetail(fleet.EntityKind(in.Kind), in.Key)
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetEntityDetailOutput{Body: toProductEntityDetail(res)}, nil
}

type fleetRevisionDocumentInput struct {
	Key  string `query:"key" required:"true" doc:"Canonical revision key the document belongs to"`
	Path string `query:"path" required:"true" doc:"In-bundle document path, exactly as published in the revision's docs list"`
}

type fleetRevisionDocumentOutput struct{ Body *ProductRevisionDocument }

func (s *Server) fleetRevisionDocument(ctx context.Context, in *fleetRevisionDocumentInput) (*fleetRevisionDocumentOutput, error) {
	q, err := s.opts.Fleet(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.RevisionDocument(in.Key, in.Path)
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetRevisionDocumentOutput{Body: toProductRevisionDocument(res)}, nil
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
	q, err := s.opts.Fleet(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Neighborhood(fleet.NeighborhoodQuery{
		Kind: fleet.EntityKind(in.Kind), Key: in.Key, Perspective: fleet.Perspective(in.Perspective),
		Direction: fleet.Direction(in.Direction),
		Depth:     in.Depth, Views: parseViews(in.Views), MaxNodes: in.MaxNodes, MaxEdges: in.MaxEdges,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetNeighborhoodOutput{Body: toProductNeighborhood(res)}, nil
}

type fleetAttentionInput struct {
	Category  string `query:"category"`
	Kind      string `query:"kind"`
	Key       string `query:"key"`
	Service   string `query:"service"`
	Owner     string `query:"owner" doc:"Free-text owner SEARCH: case-insensitive substring over team, DRI and contacts"`
	OwnerKey  string `query:"ownerKey" doc:"Exact canonical owner IDENTITY, namespaced as team:NAME or dri:NAME, exactly as canonical owner links carry it. A bare owner name matches nothing"`
	Source    string `query:"source"`
	Severity  string `query:"severity" enum:"error,warning,info"`
	Status    string `query:"status" enum:"Compliant,Invalid,NonCompliant,NotEvaluated,Reference,Unknown,Warning" doc:"Compliance status filter"`
	StaleOnly bool   `query:"staleOnly"`
	Limit     int    `query:"limit" minimum:"0" doc:"Max items per page (negatives rejected; excessive values capped)"`
	Offset    int    `query:"offset" minimum:"0" doc:"Page offset (negatives rejected)"`
}

type fleetAttentionOutput struct{ Body *ProductAttentionList }

func (s *Server) fleetAttention(ctx context.Context, in *fleetAttentionInput) (*fleetAttentionOutput, error) {
	q, err := s.opts.Fleet(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Attention(fleet.AttentionFilter{
		Category: in.Category, Kind: in.Kind, Key: in.Key, Service: in.Service,
		Owner: in.Owner, OwnerKey: in.OwnerKey, Source: in.Source, Severity: in.Severity, Status: in.Status, StaleOnly: in.StaleOnly,
		Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetAttentionOutput{Body: toProductAttentionList(res)}, nil
}

// ProductImpactConsumer is a navigable affected consumer with a bounded path.
type ProductImpactConsumer struct {
	impact.ProductConsumer
	Service ProductRef   `json:"service"`
	Path    []ProductRef `json:"path"`
}

// ProductImpactConsumersPage is a stable, offset-paged consumer answer.
type ProductImpactConsumersPage struct {
	impact.ConsumersPage
	Items []ProductImpactConsumer `json:"items"`
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
}

func toProductImpact(p *impact.Product) *ProductImpact {
	items := make([]ProductImpactConsumer, len(p.Consumers.Items))
	for i, c := range p.Consumers.Items {
		items[i] = ProductImpactConsumer{ProductConsumer: c, Service: productRef(c.Service), Path: productRefs(c.Path)}
	}
	return &ProductImpact{
		Product:       *p,
		Service:       productRef(p.Service),
		OldRevision:   productRefPtr(p.OldRevision),
		NewRevision:   productRefPtr(p.NewRevision),
		Consumers:     ProductImpactConsumersPage{ConsumersPage: p.Consumers, Items: items},
		Owners:        productRefPreview(p.Owners),
		ActiveTargets: productRefPreview(p.ActiveTargets),
	}
}

// impactRequest is the POST-impact body: canonical revision keys, the snapshot the
// client analyzed (for staleness rejection) and consumer paging.
type impactRequest struct {
	SnapshotID      string `json:"snapshotId,omitempty" doc:"The snapshot the client analyzed; a mismatch with the published snapshot is rejected"`
	ServiceKey      string `json:"serviceKey,omitempty"`
	FromRevisionKey string `json:"fromRevisionKey" doc:"Canonical revision key of the old revision"`
	ToRevisionKey   string `json:"toRevisionKey" doc:"Canonical revision key of the new revision"`
	IncludeObserved bool   `json:"includeObserved,omitempty"`
	Limit           int    `json:"limit,omitempty" doc:"Max consumers per page (negatives rejected; excessive values capped)"`
	Offset          int    `json:"offset,omitempty" doc:"Consumer page offset (negatives rejected)"`
}

type fleetImpactPostInput struct {
	Body impactRequest
}

type fleetImpactPostOutput struct{ Body *ProductImpact }

func (s *Server) fleetImpactPost(ctx context.Context, in *fleetImpactPostInput) (*fleetImpactPostOutput, error) {
	q, err := s.opts.Fleet(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	snap := q.Snapshot()
	b := in.Body
	if b.Limit < 0 || b.Offset < 0 {
		return nil, huma.Error422UnprocessableEntity("paging", errors.New("limit and offset must be >= 0"))
	}
	if b.SnapshotID != "" && b.SnapshotID != snap.SnapshotID {
		return nil, huma.Error409Conflict("snapshot mismatch: the published snapshot changed; refetch and retry")
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
	res, err := s.opts.Impact(ctx, oldRef, newRef, b.IncludeObserved)
	if err != nil {
		return nil, impactHTTPError(err)
	}
	// Snapshot parity: the analysis must have run over the SAME snapshot the handler
	// validated. If the Manager published a new snapshot in between, the analysis id
	// differs; reject rather than stamp a divergent answer with our snapshot id.
	if res.SnapshotID != snap.SnapshotID {
		return nil, huma.Error409Conflict("snapshot changed during analysis; refetch and retry")
	}
	return &fleetImpactPostOutput{Body: toProductImpact(impact.BuildProduct(q.ProductMeta(), snap, svcKey, fromRev, toRev, res, b.Limit, b.Offset))}, nil
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
	s.opts.Impact = func(context.Context, string, string, bool) (*impact.Result, error) {
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
