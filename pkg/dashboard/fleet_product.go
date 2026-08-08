package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/impact"
)

// registerProductOperations registers the product-oriented dashboard APIs
// (requirement 2). They are bounded, versioned answers built for product
// questions — the frontend consumes these instead of reconstructing meaning from
// the full snapshot. Every response is a dashboard transport DTO that wraps the
// route-neutral fleet fact and adds canonical navigation hrefs (ADR-2). All
// require the fleet provider; the POST-impact endpoint also requires the impact
// provider.
func (s *Server) registerProductOperations(api huma.API) {
	if s.fleetQuery == nil {
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

	if s.impactProvider != nil {
		huma.Register(api, huma.Operation{
			OperationID: "fleet-impact-post", Method: http.MethodPost, Path: "/api/fleet/impact",
			Summary: "Analyze impact by canonical identity", Tags: []string{"Fleet"},
			Description: "Analyzes a change between two contract revisions identified by canonical revision keys. It rejects a stale snapshot id AND any revision whose exact snapshot content is not retrievable (a mutable tag or a local path), so a success always analyzed the exact content the snapshot represents.",
		}, s.fleetImpactPost)
	}
}

type fleetOverviewOutput struct{ Body *ProductOverview }

func (s *Server) fleetOverview(ctx context.Context, _ *struct{}) (*fleetOverviewOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	return &fleetOverviewOutput{Body: toProductOverview(q.Overview())}, nil
}

type fleetEntitiesInput struct {
	Text         string `query:"text"`
	Kinds        string `query:"kinds" doc:"Comma-separated entity kinds (service,revision,target,owner,source)"`
	Owner        string `query:"owner"`
	Domain       string `query:"domain"`
	Scope        string `query:"scope"`
	Status       string `query:"status" doc:"Compliance status filter (service/revision/target)"`
	SourceHealth string `query:"sourceHealth" enum:"available,partial,stale,unavailable" doc:"Source-health filter for source entities"`
	Source       string `query:"source"`
	Service      string `query:"service" doc:"Scope revision/target entities to a canonical parent ServiceKey (pages all revisions of one service)"`
	Limit        int    `query:"limit" minimum:"0" doc:"Max entities to return (negatives rejected; excessive values capped)"`
	Offset       int    `query:"offset" minimum:"0"`
}

type fleetEntitiesOutput struct{ Body *ProductEntityList }

func (s *Server) fleetEntities(ctx context.Context, in *fleetEntitiesInput) (*fleetEntitiesOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Entities(fleet.EntityFilter{
		Text: in.Text, Kinds: parseKinds(in.Kinds), Owner: in.Owner, Domain: in.Domain,
		Scope: in.Scope, Status: in.Status, SourceHealth: in.SourceHealth, Source: in.Source,
		Service: in.Service, Limit: in.Limit, Offset: in.Offset,
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
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.EntityDetail(fleet.EntityKind(in.Kind), in.Key)
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetEntityDetailOutput{Body: toProductEntityDetail(res)}, nil
}

type fleetNeighborhoodInput struct {
	Kind      string `query:"kind" required:"true" doc:"Focus entity kind: service, revision or target"`
	Key       string `query:"key" required:"true"`
	Direction string `query:"direction" enum:"dependencies,dependents,both" doc:"dependencies, dependents or both (default both)"`
	Depth     int    `query:"depth" minimum:"0" doc:"Traversal depth (negatives rejected; excessive values capped)"`
	Views     string `query:"views" doc:"Comma-separated knowledge views (expected,observed,differences)"`
	MaxNodes  int    `query:"maxNodes" minimum:"0" doc:"Max nodes (negatives rejected; excessive values capped)"`
	MaxEdges  int    `query:"maxEdges" minimum:"0" doc:"Max edges (negatives rejected; excessive values capped)"`
}

type fleetNeighborhoodOutput struct{ Body *ProductNeighborhood }

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
	return &fleetNeighborhoodOutput{Body: toProductNeighborhood(res)}, nil
}

type fleetAttentionInput struct {
	Category  string `query:"category"`
	Kind      string `query:"kind"`
	Key       string `query:"key"`
	Service   string `query:"service"`
	Owner     string `query:"owner"`
	Source    string `query:"source"`
	Severity  string `query:"severity" enum:"error,warning,info"`
	Status    string `query:"status"`
	StaleOnly bool   `query:"staleOnly"`
	Limit     int    `query:"limit" minimum:"0" doc:"Max items per page (negatives rejected; excessive values capped)"`
	Offset    int    `query:"offset" minimum:"0" doc:"Page offset (negatives rejected)"`
}

type fleetAttentionOutput struct{ Body *ProductAttentionList }

func (s *Server) fleetAttention(ctx context.Context, in *fleetAttentionInput) (*fleetAttentionOutput, error) {
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil, huma.Error503ServiceUnavailable("fleet snapshot unavailable", err)
	}
	res, err := q.Attention(fleet.AttentionFilter{
		Category: in.Category, Kind: in.Kind, Key: in.Key, Service: in.Service, Owner: in.Owner,
		Source: in.Source, Severity: in.Severity, Status: in.Status, StaleOnly: in.StaleOnly,
		Limit: in.Limit, Offset: in.Offset,
	})
	if err != nil {
		return nil, productQueryError(err)
	}
	return &fleetAttentionOutput{Body: toProductAttentionList(res)}, nil
}

// Product-impact bounds. Every impact list is bounded; consumers are pageable.
const (
	DefaultImpactConsumers = 100
	MaxImpactConsumers     = 500
	MaxImpactPath          = 50
	MaxImpactOwners        = 200
	MaxImpactActiveTargets = 200
	MaxImpactLimitations   = 100
)

// ProductImpactConsumer is a navigable affected consumer with a bounded path.
type ProductImpactConsumer struct {
	Service              ProductRef   `json:"service"`
	Path                 []ProductRef `json:"path"`
	PathTotal            int          `json:"pathTotal"`
	PathTruncated        bool         `json:"pathTruncated"`
	Depth                int          `json:"depth"`
	Direct               bool         `json:"direct"`
	Confidence           string       `json:"confidence"`
	CompatibilityVerdict string       `json:"compatibilityVerdict"`
	Owner                string       `json:"owner,omitempty"`
}

// ProductImpactConsumersPage is a stable, offset-paged consumer answer.
type ProductImpactConsumersPage struct {
	Total      int                     `json:"total"`
	Count      int                     `json:"count"`
	Limit      int                     `json:"limit"`
	Offset     int                     `json:"offset"`
	Truncated  bool                    `json:"truncated"`
	NextOffset *int                    `json:"nextOffset,omitempty"`
	Items      []ProductImpactConsumer `json:"items"`
}

// ProductRefsPreview is a bounded preview of navigable references (owners, targets).
type ProductRefsPreview struct {
	Total     int          `json:"total"`
	Count     int          `json:"count"`
	Truncated bool         `json:"truncated"`
	Items     []ProductRef `json:"items"`
}

// ProductImpact is the navigable impact answer: canonical, href-bearing references
// for the changed service, revisions, consumers, path steps, owners and active
// targets, all bounded. It deliberately does NOT embed the raw impact.Result; the
// raw GET /api/fleet/impact endpoint remains for machine consumers that want it.
// SnapshotMatch is true ONLY on a success, which means BOTH the graph snapshot
// matched AND the old/new content analyzed was the exact immutable content the
// canonical revisions name.
type ProductImpact struct {
	Meta           fleet.ProductMeta          `json:"meta"`
	SnapshotID     string                     `json:"snapshotId"`
	SnapshotMatch  bool                       `json:"snapshotMatch"`
	Service        ProductRef                 `json:"service"`
	OldRevision    *ProductRef                `json:"oldRevision,omitempty"`
	NewRevision    *ProductRef                `json:"newRevision,omitempty"`
	Classification string                     `json:"classification"`
	Consumers      ProductImpactConsumersPage `json:"consumers"`
	Owners         ProductRefsPreview         `json:"owners"`
	ActiveTargets  ProductRefsPreview         `json:"activeTargets"`
	Limitations    fleet.LimitationsPreview   `json:"limitations"`
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
	q, err := s.fleetQuery(ctx)
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
	svcKey, fromRev, toRev, err := validateImpactRevisions(snap, b)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("revisionKey", err)
	}
	// Exact-content invariant: a canonical Product Impact may only analyze the exact
	// immutable content the snapshot revisions name. A mutable tag or a local path is
	// rejected BEFORE the provider is invoked, so the server never fetches
	// potentially-different content and then claims snapshot parity.
	oldRef, err := immutableRef(fromRev)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("fromRevisionKey", err)
	}
	newRef, err := immutableRef(toRev)
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
	return &fleetImpactPostOutput{Body: buildProductImpact(q.ProductMeta(), snap, svcKey, fromRev, toRev, res, b.Limit, b.Offset)}, nil
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

// immutableRef returns the IMMUTABLE, canonical, resolver-compatible reference the
// provider must fetch for a canonical Product Impact. This enforces the CONTENT-
// RETRIEVABILITY dimension only: content is retrievable ONLY when the revision's
// ResolvedRef is a canonical OCI digest reference
// (oci://registry/repo@<validated digest>) AND that digest is internally
// consistent with the revision's recorded content digest. A mutable tag, a local
// filesystem path, a scheme-less ref (which the resolver would treat as a local
// path), a missing ref or an inconsistent/malformed digest is rejected -- using the
// SAME classifier (fleet.ClassifyContentIdentity) the detail Retrievable flag uses.
// It is independent of any target's revision-match certainty: a target can match this
// revision EXACTLY (a trusted digest with no canonical ref) yet Product Impact still
// rejects it here, because the content is not retrievable through a canonical ref.
func immutableRef(rev *fleet.ContractRevision) (string, error) {
	ei := fleet.ClassifyContentIdentity(rev.ResolvedRef, rev.Digest)
	if ei.Retrievable() {
		return rev.ResolvedRef, nil
	}
	if ei.Class == fleet.IdentityDigestMismatch {
		return "", fmt.Errorf("revision %s has an inconsistent immutable reference: %s", rev.Key, ei.Reason())
	}
	return "", fmt.Errorf(
		"exact snapshot content is not retrievable for revision %s: %s (%s); use the raw ref-based /api/fleet/impact endpoint for mutable-content analysis",
		rev.Key, ei.Reason(), mutableRefDescription(rev))
}

// mutableRefDescription names the mutable reference a revision resolved through,
// for the rejection message.
func mutableRefDescription(rev *fleet.ContractRevision) string {
	if rev.ResolvedRef != "" {
		return "resolvedRef " + rev.ResolvedRef
	}
	if rev.RequestedRef != "" {
		return "requestedRef " + rev.RequestedRef
	}
	return "no reference"
}

// buildProductImpact enriches a raw impact result with canonical, navigable,
// href-bearing references (all looked up in the snapshot so identities are exact
// and labels come from records) and bounds every list. SnapshotMatch is true
// because non-exact content was already rejected before analysis.
func buildProductImpact(meta fleet.ProductMeta, snap *fleet.FleetSnapshot, svcKey fleet.ServiceKey, fromRev, toRev *fleet.ContractRevision, res *impact.Result, limit, offset int) *ProductImpact {
	consumers := make([]ProductImpactConsumer, 0, len(res.Consumers))
	for _, c := range res.Consumers {
		path := productRefs(pathFleetRefs(snap, c.Path))
		pathTotal := len(path)
		pathTruncated := false
		if pathTotal > MaxImpactPath {
			path = path[:MaxImpactPath]
			pathTruncated = true
		}
		consumers = append(consumers, ProductImpactConsumer{
			Service:              productRef(serviceRefFromSnap(snap, fleet.NewServiceKeyDomain(c.Domain, c.Service))),
			Path:                 path,
			PathTotal:            pathTotal,
			PathTruncated:        pathTruncated,
			Depth:                c.Depth,
			Direct:               c.Direct,
			Confidence:           string(c.Confidence),
			CompatibilityVerdict: c.CompatibilityVerdict,
			Owner:                c.Owner,
		})
	}
	// Deterministic order so paging is stable across identical requests.
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].Service.Key < consumers[j].Service.Key })

	owners := make([]ProductRef, 0, len(res.Owners))
	for _, o := range res.Owners {
		owners = append(owners, productRef(fleet.EntityRef{Kind: fleet.KindOwner, Key: o, Label: o}))
	}
	targets := make([]ProductRef, 0, len(res.ActiveTargets))
	for _, tk := range res.ActiveTargets {
		targets = append(targets, productRef(targetRefFromSnap(snap, tk)))
	}
	return &ProductImpact{
		Meta: meta, SnapshotID: snap.SnapshotID, SnapshotMatch: true,
		Service:        productRef(serviceRefFromSnap(snap, svcKey)),
		OldRevision:    productRefPtr(revisionRefFromRecord(fromRev)),
		NewRevision:    productRefPtr(revisionRefFromRecord(toRev)),
		Classification: res.Classification,
		Consumers:      pageImpactConsumers(consumers, limit, offset),
		Owners:         boundProductRefs(owners, MaxImpactOwners),
		ActiveTargets:  boundProductRefs(targets, MaxImpactActiveTargets),
		Limitations:    boundImpactLimitations(res.Limitations),
	}
}

// pageImpactConsumers offset-pages a sorted consumer list with defaulted/capped
// limit and a next-offset cursor.
func pageImpactConsumers(all []ProductImpactConsumer, limit, offset int) ProductImpactConsumersPage {
	total := len(all)
	if limit <= 0 {
		limit = DefaultImpactConsumers
	}
	if limit > MaxImpactConsumers {
		limit = MaxImpactConsumers
	}
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := append([]ProductImpactConsumer{}, all[start:end]...)
	truncated := end < total
	var next *int
	if truncated {
		n := end
		next = &n
	}
	return ProductImpactConsumersPage{
		Total: total, Count: len(page), Limit: limit, Offset: start,
		Truncated: truncated, NextOffset: next, Items: page,
	}
}

// boundProductRefs bounds a reference list into a preview with truncation
// metadata. Callers always pass a non-nil (make-initialized) slice.
func boundProductRefs(refs []ProductRef, max int) ProductRefsPreview {
	total := len(refs)
	truncated := total > max
	items := refs
	if truncated {
		items = refs[:max]
	}
	return ProductRefsPreview{Total: total, Count: len(items), Truncated: truncated, Items: items}
}

// boundImpactLimitations bounds the impact limitations into a preview. It copies
// the slice so the answer never aliases the raw impact result.
func boundImpactLimitations(ls []fleet.Limitation) fleet.LimitationsPreview {
	total := len(ls)
	truncated := total > MaxImpactLimitations
	n := total
	if truncated {
		n = MaxImpactLimitations
	}
	items := append([]fleet.Limitation{}, ls[:n]...)
	return fleet.LimitationsPreview{Total: total, Count: len(items), Truncated: truncated, Items: items}
}

// serviceRefFromSnap builds a route-neutral service reference for an
// already-canonical key, taking the human label from the snapshot record when
// present and otherwise decoding the key (never re-encoding it).
func serviceRefFromSnap(snap *fleet.FleetSnapshot, key fleet.ServiceKey) fleet.EntityRef {
	name, domain := "", ""
	if s := snap.Services[key]; s != nil {
		name, domain = s.Name, s.Domain
	} else {
		domain, name = fleet.ParseServiceKey(key)
	}
	return fleet.EntityRef{Kind: fleet.KindService, Key: string(key), Label: name, Domain: domain}
}

// revisionRefFromRecord builds a route-neutral revision reference whose label is
// the record's service and version, never the raw key.
func revisionRefFromRecord(rev *fleet.ContractRevision) *fleet.EntityRef {
	label := rev.Service
	if rev.Version != "" {
		label = rev.Service + " " + rev.Version
	}
	return &fleet.EntityRef{Kind: fleet.KindRevision, Key: string(rev.Key), Label: label, Secondary: rev.Digest, Domain: rev.Domain}
}

// targetRefFromSnap builds a route-neutral target reference whose label is the
// record's DisplayName when present, otherwise the raw key.
func targetRefFromSnap(snap *fleet.FleetSnapshot, key string) fleet.EntityRef {
	tk := fleet.TargetKey(key)
	label := key
	domain := ""
	if t := snap.Targets[tk]; t != nil {
		label, domain = t.DisplayName(), t.Domain
	}
	return fleet.EntityRef{Kind: fleet.KindTarget, Key: key, Label: label, Domain: domain}
}

// pathFleetRefs turns an impact path of ALREADY-CANONICAL service keys into
// route-neutral references. Each element is a canonical ServiceKey (its own domain
// baked in), so it is used verbatim and never re-encoded with another step's domain.
func pathFleetRefs(snap *fleet.FleetSnapshot, keys []string) []fleet.EntityRef {
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

// stubProvidersForSchemaExport wires no-op fleet and impact providers so
// [ExportOpenAPI] registers every fleet and product operation for SCHEMA
// generation. The handlers are never invoked during export, so the stubs' errors
// are unreachable; they exist only to satisfy the nil-provider gates.
func (s *Server) stubProvidersForSchemaExport() {
	// schemaExport forces the runtime-conditional operations (resolve/versions and
	// debug) to register so the exported OpenAPI is the COMPLETE contract the
	// generated frontend SDK consumes.
	s.schemaExport = true
	s.fleetQuery = func(context.Context) (*fleet.Query, error) {
		return nil, errors.New("schema export only")
	}
	s.impactProvider = func(context.Context, string, string, bool) (*impact.Result, error) {
		return nil, errors.New("schema export only")
	}
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
