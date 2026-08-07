package dashboard

import (
	"context"
	"errors"
	"fmt"
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
	Text         string `query:"text"`
	Kinds        string `query:"kinds" doc:"Comma-separated entity kinds (service,revision,target,owner,source)"`
	Owner        string `query:"owner"`
	Domain       string `query:"domain"`
	Scope        string `query:"scope"`
	Status       string `query:"status" doc:"Compliance status filter (service/revision/target)"`
	SourceHealth string `query:"sourceHealth" doc:"Source-health filter (available, partial, stale, unavailable) for source entities"`
	Source       string `query:"source"`
	Limit        int    `query:"limit" minimum:"0" doc:"Max entities to return (negatives rejected; excessive values capped)"`
	Offset       int    `query:"offset" minimum:"0"`
}

type fleetEntitiesOutput struct{ Body *fleet.EntityList }

func (s *Server) fleetEntities(ctx context.Context, in *fleetEntitiesInput) (*fleetEntitiesOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Entities(fleet.EntityFilter{
		Text: in.Text, Kinds: parseKinds(in.Kinds), Owner: in.Owner, Domain: in.Domain,
		Scope: in.Scope, Status: in.Status, SourceHealth: in.SourceHealth, Source: in.Source,
		Limit: in.Limit, Offset: in.Offset,
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
	Depth     int    `query:"depth" minimum:"0" doc:"Traversal depth (negatives rejected; excessive values capped)"`
	Views     string `query:"views" doc:"Comma-separated knowledge views (expected,observed,differences)"`
	MaxNodes  int    `query:"maxNodes" minimum:"0" doc:"Max nodes (negatives rejected; excessive values capped)"`
	MaxEdges  int    `query:"maxEdges" minimum:"0" doc:"Max edges (negatives rejected; excessive values capped)"`
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
	Limit     int    `query:"limit" minimum:"0" doc:"Max items to return (negatives rejected; excessive values capped)"`
}

type fleetAttentionOutput struct{ Body *fleet.AttentionList }

func (s *Server) fleetAttention(ctx context.Context, in *fleetAttentionInput) (*fleetAttentionOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Attention(fleet.AttentionFilter{
		Category: in.Category, Kind: in.Kind, Key: in.Key, Service: in.Service, Owner: in.Owner,
		Source: in.Source, Severity: in.Severity, Status: in.Status, StaleOnly: in.StaleOnly, Limit: in.Limit,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
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
// changed service, revisions, consumers, path steps, owners and active targets.
// It deliberately does NOT embed the raw impact.Result (which carries
// non-canonical bare names); the raw GET /api/fleet/impact endpoint remains for
// machine consumers that want it.
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
	// Both keys must exist, belong to the SAME logical service, and (if given) match
	// the requested ServiceKey - before any content is fetched.
	svcKey, fromRev, toRev, err := validateImpactRevisions(snap, b)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("revisionKey", err)
	}
	oldRef, oldExact, err := revisionRef(fromRev)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("fromRevisionKey", err)
	}
	newRef, newExact, err := revisionRef(toRev)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("toRevisionKey", err)
	}
	res, err := s.impactProvider(ctx, oldRef, newRef, b.IncludeObserved)
	if err != nil {
		return nil, impactHTTPError(err)
	}
	// Snapshot parity: the analysis must have run over the SAME snapshot the handler
	// validated. If the Manager published a new snapshot in between, the analysis id
	// differs; reject rather than stamp a divergent answer with our snapshot id.
	if res.SnapshotID != snap.SnapshotID {
		return nil, huma.Error409Conflict("snapshot changed during analysis; refetch and retry")
	}
	return &fleetImpactPostOutput{Body: buildProductImpact(q.ProductMeta(), snap, svcKey, fromRev, toRev, res, oldExact && newExact)}, nil
}

// validateImpactRevisions looks up both revisions, requires them to exist and to
// belong to the same logical service, and (when a ServiceKey is given) requires it
// to match that service exactly. It returns the verified service key and the two
// revision records so labels come from records, never reconstructed from strings.
func validateImpactRevisions(snap *fleet.FleetSnapshot, req impactRequest) (fleet.ServiceKey, *fleet.ContractRevision, *fleet.ContractRevision, error) {
	from := snap.Revisions[fleet.RevisionKey(req.FromRevisionKey)]
	if from == nil {
		return "", nil, nil, errors.New("no revision with this fromRevisionKey in the published snapshot")
	}
	to := snap.Revisions[fleet.RevisionKey(req.ToRevisionKey)]
	if to == nil {
		return "", nil, nil, errors.New("no revision with this toRevisionKey in the published snapshot")
	}
	if from.ServiceKey != to.ServiceKey {
		return "", nil, nil, fmt.Errorf("the two revisions belong to different services (%s vs %s)", from.ServiceKey, to.ServiceKey)
	}
	if req.ServiceKey != "" && fleet.ServiceKey(req.ServiceKey) != from.ServiceKey {
		return "", nil, nil, fmt.Errorf("serviceKey %q does not match the revisions' service %q", req.ServiceKey, from.ServiceKey)
	}
	return from.ServiceKey, from, to, nil
}

// revisionRef resolves a revision to a resolvable contract ref. It prefers the
// IMMUTABLE ResolvedRef (a digest); exact is false when only a MUTABLE
// RequestedRef (a tag or local path) is available, whose content may differ from
// what the snapshot captured. A revision with no ref at all is an error.
func revisionRef(rev *fleet.ContractRevision) (ref string, exact bool, err error) {
	if rev.ResolvedRef != "" {
		return rev.ResolvedRef, true, nil
	}
	if rev.RequestedRef != "" {
		return rev.RequestedRef, false, nil
	}
	return "", false, errors.New("the revision has no resolvable reference")
}

// buildProductImpact enriches a raw impact result with canonical, navigable
// references, all looked up in the snapshot so identities are exact and labels
// come from records. Consumer, path, owner and target identities are
// domain-qualified so a same-named service in another domain is never conflated,
// and an already-canonical path key is never re-encoded.
func buildProductImpact(meta fleet.ProductMeta, snap *fleet.FleetSnapshot, svcKey fleet.ServiceKey, fromRev, toRev *fleet.ContractRevision, res *impact.Result, contentExact bool) *ProductImpact {
	out := &ProductImpact{
		Meta: meta, SnapshotID: snap.SnapshotID, SnapshotMatch: true,
		Service:        serviceRefFromSnap(snap, svcKey),
		OldRevision:    revisionRefFromRecord(fromRev),
		NewRevision:    revisionRefFromRecord(toRev),
		Classification: res.Classification, Limitations: append([]fleet.Limitation(nil), res.Limitations...),
		Consumers: []ProductImpactConsumer{}, Owners: []fleet.EntityRef{}, ActiveTargets: []fleet.EntityRef{},
	}
	if !contentExact {
		out.Limitations = append(out.Limitations, fleet.Limitation{
			Code: fleet.LimitationRevisionContentMutable, Source: "impact",
			Message: "a revision was resolved through a mutable reference; its content may differ from the snapshot, so exact snapshot parity is not claimed",
		})
	}
	for _, c := range res.Consumers {
		out.Consumers = append(out.Consumers, ProductImpactConsumer{
			Service:              serviceRefFromSnap(snap, fleet.NewServiceKeyDomain(c.Domain, c.Service)),
			Path:                 pathRefs(snap, c.Path),
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
		out.ActiveTargets = append(out.ActiveTargets, targetRefFromSnap(snap, tk))
	}
	return out
}

// serviceRefFromSnap builds a service reference for an already-canonical key,
// taking the human label from the snapshot record when present and otherwise
// decoding the key (never re-encoding it).
func serviceRefFromSnap(snap *fleet.FleetSnapshot, key fleet.ServiceKey) fleet.EntityRef {
	name, domain := "", ""
	if s := snap.Services[key]; s != nil {
		name, domain = s.Name, s.Domain
	} else {
		domain, name = fleet.ParseServiceKey(key)
	}
	return fleet.EntityRef{Kind: fleet.KindService, Key: string(key), Label: name, Domain: domain, Route: fleet.RouteForService(key)}
}

// revisionRefFromRecord builds a revision reference whose label is the record's
// service and version, never the raw key.
func revisionRefFromRecord(rev *fleet.ContractRevision) *fleet.EntityRef {
	label := rev.Service
	if rev.Version != "" {
		label = rev.Service + " " + rev.Version
	}
	return &fleet.EntityRef{Kind: fleet.KindRevision, Key: string(rev.Key), Label: label, Secondary: rev.Digest, Domain: rev.Domain, Route: fleet.RouteForRevision(rev.Key)}
}

// targetRefFromSnap builds a target reference whose label is the record's
// DisplayName when present, otherwise the raw key.
func targetRefFromSnap(snap *fleet.FleetSnapshot, key string) fleet.EntityRef {
	tk := fleet.TargetKey(key)
	label := key
	domain := ""
	if t := snap.Targets[tk]; t != nil {
		label, domain = t.DisplayName(), t.Domain
	}
	return fleet.EntityRef{Kind: fleet.KindTarget, Key: key, Label: label, Domain: domain, Route: fleet.RouteForTarget(tk)}
}

// pathRefs turns an impact path of ALREADY-CANONICAL service keys into navigable
// references. Each element is a canonical ServiceKey (its own domain baked in), so
// it is used verbatim and never re-encoded with another step's domain.
func pathRefs(snap *fleet.FleetSnapshot, keys []string) []fleet.EntityRef {
	out := make([]fleet.EntityRef, 0, len(keys))
	for _, k := range keys {
		out = append(out, serviceRefFromSnap(snap, fleet.ServiceKey(k)))
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
