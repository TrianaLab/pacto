package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

// registerProductOperations registers the product-oriented dashboard APIs
// (requirement 2). They are bounded, versioned answers built for product
// questions — the frontend consumes these instead of reconstructing meaning from
// the full snapshot. All require the fleet provider; the POST-impact endpoint also
// requires the impact provider.
func (s *Server) registerProductOperations(api huma.API) {
	if s.fleetQuery == nil {
		return
	}
	huma.Register(api, huma.Operation{
		OperationID: "fleet-overview", Method: http.MethodGet, Path: "/api/fleet/overview",
		Summary: "Operational overview", Tags: []string{"Fleet"},
		Description: "A product-oriented summary: what needs attention, incomplete sources, recent evidence and suggested entry points, each with a canonical route.",
	}, s.fleetOverview)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-entities", Method: http.MethodGet, Path: "/api/fleet/entities",
		Summary: "Search entities", Tags: []string{"Fleet"},
		Description: "Searches services, revisions, targets, owners and sources, returning stable navigable references. Powers global search, graph focus and entity pickers.",
	}, s.fleetEntities)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-entity-detail", Method: http.MethodGet, Path: "/api/fleet/entities/{kind}",
		Summary: "Entity detail", Tags: []string{"Fleet"},
		Description: "Returns one entity's unified detail envelope (service, revision, target, owner or source), keyed by the query-param key so a key containing a slash is transported safely.",
	}, s.fleetEntityDetail)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-neighborhood", Method: http.MethodGet, Path: "/api/fleet/neighborhood",
		Summary: "Bounded neighborhood", Tags: []string{"Fleet"},
		Description: "Returns the focus entity's bounded local neighborhood across the expected, observed and differences knowledge views, with graph-ready nodes and edges.",
	}, s.fleetNeighborhood)

	huma.Register(api, huma.Operation{
		OperationID: "fleet-attention", Method: http.MethodGet, Path: "/api/fleet/attention",
		Summary: "Attention list", Tags: []string{"Fleet"},
		Description: "Returns navigable attention items across every category; each links to the exact affected entity and recommends a next step.",
	}, s.fleetAttention)

	if s.impactProvider != nil {
		huma.Register(api, huma.Operation{
			OperationID: "fleet-impact-post", Method: http.MethodPost, Path: "/api/fleet/impact",
			Summary: "Analyze impact by canonical identity", Tags: []string{"Fleet"},
			Description: "Analyzes a change between two contract revisions identified by canonical revision keys, rejecting a stale snapshot id, and returns canonical navigable references for the changed service, revisions, consumers, path steps, owners and active targets.",
		}, s.fleetImpactPost)
	}
}

type fleetOverviewOutput struct{ Body *fleet.Overview }

func (s *Server) fleetOverview(ctx context.Context, _ *struct{}) (*fleetOverviewOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	return &fleetOverviewOutput{Body: q.Overview()}, nil
}

type fleetEntitiesInput struct {
	Text   string `query:"text"`
	Kinds  string `query:"kinds" doc:"Comma-separated entity kinds (service,revision,target,owner,source)"`
	Owner  string `query:"owner"`
	Domain string `query:"domain"`
	Scope  string `query:"scope"`
	Status string `query:"status"`
	Source string `query:"source"`
	Limit  int    `query:"limit"`
	Offset int    `query:"offset"`
}

type fleetEntitiesOutput struct{ Body *fleet.EntityList }

func (s *Server) fleetEntities(ctx context.Context, in *fleetEntitiesInput) (*fleetEntitiesOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Entities(fleet.EntityFilter{
		Text: in.Text, Kinds: parseKinds(in.Kinds), Owner: in.Owner, Domain: in.Domain,
		Scope: in.Scope, Status: in.Status, Source: in.Source, Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetEntitiesOutput{Body: res}, nil
}

type fleetEntityDetailInput struct {
	Kind string `path:"kind" doc:"Entity kind: service, revision, target, owner or source"`
	Key  string `query:"key" required:"true" doc:"Canonical entity key (a slash-bearing key is transported safely as a query param)"`
}

type fleetEntityDetailOutput struct{ Body *fleet.EntityDetail }

func (s *Server) fleetEntityDetail(ctx context.Context, in *fleetEntityDetailInput) (*fleetEntityDetailOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.EntityDetail(fleet.EntityKind(in.Kind), in.Key)
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetEntityDetailOutput{Body: res}, nil
}

type fleetNeighborhoodInput struct {
	Kind      string `query:"kind" required:"true" doc:"Focus entity kind: service, revision or target"`
	Key       string `query:"key" required:"true"`
	Direction string `query:"direction" doc:"dependencies, dependents or both (default both)"`
	Depth     int    `query:"depth"`
	Views     string `query:"views" doc:"Comma-separated knowledge views (expected,observed,differences)"`
	MaxNodes  int    `query:"maxNodes"`
	MaxEdges  int    `query:"maxEdges"`
}

type fleetNeighborhoodOutput struct{ Body *fleet.Neighborhood }

func (s *Server) fleetNeighborhood(ctx context.Context, in *fleetNeighborhoodInput) (*fleetNeighborhoodOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Neighborhood(fleet.NeighborhoodQuery{
		Kind: fleet.EntityKind(in.Kind), Key: in.Key, Direction: fleet.Direction(in.Direction),
		Depth: in.Depth, Views: parseViews(in.Views), MaxNodes: in.MaxNodes, MaxEdges: in.MaxEdges,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetNeighborhoodOutput{Body: res}, nil
}

type fleetAttentionInput struct {
	Category  string `query:"category"`
	Kind      string `query:"kind"`
	Key       string `query:"key"`
	Service   string `query:"service"`
	Owner     string `query:"owner"`
	Source    string `query:"source"`
	Severity  string `query:"severity"`
	Status    string `query:"status"`
	StaleOnly bool   `query:"staleOnly"`
	Limit     int    `query:"limit"`
}

type fleetAttentionOutput struct{ Body *fleet.AttentionList }

func (s *Server) fleetAttention(ctx context.Context, in *fleetAttentionInput) (*fleetAttentionOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res := q.Attention(fleet.AttentionFilter{
		Category: in.Category, Kind: in.Kind, Key: in.Key, Service: in.Service, Owner: in.Owner,
		Source: in.Source, Severity: in.Severity, Status: in.Status, StaleOnly: in.StaleOnly, Limit: in.Limit,
	})
	return &fleetAttentionOutput{Body: res}, nil
}

// ProductImpactConsumer is a navigable affected consumer.
type ProductImpactConsumer struct {
	Service              fleet.EntityRef   `json:"service"`
	Path                 []fleet.EntityRef `json:"path"`
	Depth                int               `json:"depth"`
	Direct               bool              `json:"direct"`
	Confidence           string            `json:"confidence"`
	CompatibilityVerdict string            `json:"compatibilityVerdict"`
	Owner                string            `json:"owner,omitempty"`
}

// ProductImpact is the navigable impact answer: canonical references for the
// changed service, revisions, consumers, path steps, owners and active targets,
// plus the raw result for advanced consumers.
type ProductImpact struct {
	Meta           fleet.ProductMeta       `json:"meta"`
	SnapshotID     string                  `json:"snapshotId"`
	SnapshotMatch  bool                    `json:"snapshotMatch"`
	Service        fleet.EntityRef         `json:"service"`
	OldRevision    *fleet.EntityRef        `json:"oldRevision,omitempty"`
	NewRevision    *fleet.EntityRef        `json:"newRevision,omitempty"`
	Classification string                  `json:"classification"`
	Consumers      []ProductImpactConsumer `json:"consumers"`
	Owners         []fleet.EntityRef       `json:"owners"`
	ActiveTargets  []fleet.EntityRef       `json:"activeTargets"`
	Result         *impact.Result          `json:"result"`
	Limitations    []fleet.Limitation      `json:"limitations,omitempty"`
}

// impactRequest is the POST-impact body: canonical revision keys plus the
// snapshot the client analyzed (for staleness rejection).
type impactRequest struct {
	SnapshotID      string `json:"snapshotId,omitempty" doc:"The snapshot the client analyzed; a mismatch with the published snapshot is rejected"`
	ServiceKey      string `json:"serviceKey,omitempty"`
	FromRevisionKey string `json:"fromRevisionKey" doc:"Canonical revision key of the old revision"`
	ToRevisionKey   string `json:"toRevisionKey" doc:"Canonical revision key of the new revision"`
	IncludeObserved bool   `json:"includeObserved,omitempty"`
}

type fleetImpactPostInput struct {
	Body impactRequest
}

type fleetImpactPostOutput struct{ Body *ProductImpact }

func (s *Server) fleetImpactPost(ctx context.Context, in *fleetImpactPostInput) (*fleetImpactPostOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	snap := q.Snapshot()
	b := in.Body
	if b.SnapshotID != "" && b.SnapshotID != snap.SnapshotID {
		return nil, huma.Error409Conflict("snapshot mismatch: the published snapshot changed; refetch and retry")
	}
	oldRef, err := revisionRef(snap, b.FromRevisionKey)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("fromRevisionKey", err)
	}
	newRef, err := revisionRef(snap, b.ToRevisionKey)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("toRevisionKey", err)
	}
	res, err := s.impactProvider(ctx, oldRef, newRef, b.IncludeObserved)
	if err != nil {
		return nil, impactHTTPError(err)
	}
	return &fleetImpactPostOutput{Body: buildProductImpact(q.ProductMeta(), snap.SnapshotID, b, res)}, nil
}

// revisionRef resolves a canonical revision key to a resolvable contract ref from
// the snapshot, so the impact provider can fetch and diff the two revisions.
func revisionRef(snap *fleet.FleetSnapshot, key string) (string, error) {
	rev := snap.Revisions[fleet.RevisionKey(key)]
	if rev == nil {
		return "", errors.New("no revision with this key in the published snapshot")
	}
	if rev.ResolvedRef != "" {
		return rev.ResolvedRef, nil
	}
	if rev.RequestedRef != "" {
		return rev.RequestedRef, nil
	}
	return "", errors.New("the revision has no resolvable reference")
}

// buildProductImpact enriches a raw impact result with canonical, navigable
// references. Consumer, owner and target identities are domain-qualified so a
// same-named service in another domain is never conflated.
func buildProductImpact(meta fleet.ProductMeta, snapshotID string, req impactRequest, res *impact.Result) *ProductImpact {
	svcKey := fleet.ServiceKey(req.ServiceKey)
	if svcKey == "" {
		svcKey = fleet.NewServiceKey(res.Service)
	}
	out := &ProductImpact{
		Meta: meta, SnapshotID: snapshotID, SnapshotMatch: req.SnapshotID == "" || req.SnapshotID == snapshotID,
		Service:        serviceRef(svcKey, res.Service, ""),
		Classification: res.Classification, Result: res, Limitations: res.Limitations,
		Consumers: []ProductImpactConsumer{}, Owners: []fleet.EntityRef{}, ActiveTargets: []fleet.EntityRef{},
	}
	out.OldRevision = revisionRefLink(req.FromRevisionKey)
	out.NewRevision = revisionRefLink(req.ToRevisionKey)
	for _, c := range res.Consumers {
		out.Consumers = append(out.Consumers, ProductImpactConsumer{
			Service:              serviceRef(fleet.NewServiceKeyDomain(c.Domain, c.Service), c.Service, c.Domain),
			Path:                 pathRefs(c.Path, c.Domain),
			Depth:                c.Depth,
			Direct:               c.Direct,
			Confidence:           string(c.Confidence),
			CompatibilityVerdict: c.CompatibilityVerdict,
			Owner:                c.Owner,
		})
	}
	for _, o := range res.Owners {
		out.Owners = append(out.Owners, fleet.EntityRef{Kind: fleet.KindOwner, Key: o, Label: o, Route: fleet.RouteForOwner(o)})
	}
	for _, tk := range res.ActiveTargets {
		out.ActiveTargets = append(out.ActiveTargets, fleet.EntityRef{Kind: fleet.KindTarget, Key: tk, Label: tk, Route: fleet.RouteForTarget(fleet.TargetKey(tk))})
	}
	return out
}

func serviceRef(key fleet.ServiceKey, name, domain string) fleet.EntityRef {
	return fleet.EntityRef{Kind: fleet.KindService, Key: string(key), Label: name, Domain: domain, Route: fleet.RouteForService(key)}
}

func revisionRefLink(key string) *fleet.EntityRef {
	if key == "" {
		return nil
	}
	return &fleet.EntityRef{Kind: fleet.KindRevision, Key: key, Label: key, Route: fleet.RouteForRevision(fleet.RevisionKey(key))}
}

// pathRefs turns an impact path of service names into navigable references,
// keying each step in the consumer's domain.
func pathRefs(names []string, domain string) []fleet.EntityRef {
	out := make([]fleet.EntityRef, 0, len(names))
	for _, n := range names {
		out = append(out, serviceRef(fleet.NewServiceKeyDomain(domain, n), n, domain))
	}
	return out
}

// productQueryError maps a product-query error to HTTP: absent → 404, everything
// else (invalid filter, ambiguous key) → 422 with an actionable message.
func productQueryError(err error) error {
	var nf *fleet.NotFoundError
	if errors.As(err, &nf) {
		return huma.Error404NotFound("not found", err)
	}
	return huma.Error422UnprocessableEntity("invalid query", err)
}

// parseKinds splits a comma-separated kinds parameter into entity kinds, ignoring
// empty segments so a trailing comma is harmless.
func parseKinds(s string) []fleet.EntityKind {
	if s == "" {
		return nil
	}
	var out []fleet.EntityKind
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, fleet.EntityKind(p))
		}
	}
	return out
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
