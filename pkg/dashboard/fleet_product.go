package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/trianalab/pacto/v3/pkg/diff"
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

	huma.Register(api, huma.Operation{
		OperationID: "fleet-revision-document", Method: http.MethodGet, Path: "/api/fleet/revisions/document",
		Summary: "Read a revision document", Tags: []string{"Fleet"},
		Description: "Reads ONE in-bundle document body belonging to exactly the given canonical revision. The path must be one the revision already published in its docs list, which is what prevents traversal and cross-revision reads; the body is size-bounded and an oversized or unreadable document is an explicit error rather than empty content.",
	}, s.fleetRevisionDocument)

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
	Ownership    string `query:"ownership" enum:"consistent,conflicting,unowned" doc:"Declared-ownership filter for service entities"`
	Readiness    string `query:"readiness" enum:"passing,below-threshold,expired,not-declared" doc:"Declared-readiness filter for contract revision entities"`
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
		Ownership: in.Ownership, Readiness: in.Readiness,
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

type fleetRevisionDocumentInput struct {
	Key  string `query:"key" required:"true" doc:"Canonical revision key the document belongs to"`
	Path string `query:"path" required:"true" doc:"In-bundle document path, exactly as published in the revision's docs list"`
}

type fleetRevisionDocumentOutput struct{ Body *ProductRevisionDocument }

func (s *Server) fleetRevisionDocument(ctx context.Context, in *fleetRevisionDocumentInput) (*fleetRevisionDocumentOutput, error) {
	q, err := s.fleetQuery(ctx)
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
	q, err := s.fleetQuery(ctx)
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
	// MaxImpactChanges bounds the field-level change list. MaxChangeValueRunes bounds
	// EACH rendered value, so one changed OpenAPI schema cannot make the answer
	// unbounded; both truncations are reported rather than silent.
	MaxImpactChanges    = 300
	MaxChangeValueRunes = 400
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
// ImpactTallyBucket is one bucket of a COMPLETE consumer distribution: a canonical
// dimension value and how many of the impact's consumers carry it.
type ImpactTallyBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type ProductImpactConsumersPage struct {
	Total      int                     `json:"total"`
	Count      int                     `json:"count"`
	Limit      int                     `json:"limit"`
	Offset     int                     `json:"offset"`
	Truncated  bool                    `json:"truncated"`
	NextOffset *int                    `json:"nextOffset,omitempty"`
	Items      []ProductImpactConsumer `json:"items"`
	// ByVerdict and ByConfidence are computed over EVERY consumer, not over Items.
	// They are what a chart must be drawn from: Items is one page, so counting it
	// would present "the first 50 consumers" as the blast radius.
	ByVerdict    []ImpactTallyBucket `json:"byVerdict"`
	ByConfidence []ImpactTallyBucket `json:"byConfidence"`
}

// ProductChange is one field-level semantic difference between the two revisions,
// rendered for display. Values are STRINGS (not `any`): a change can carry a whole
// OpenAPI schema, so the transport renders and bounds it here rather than shipping
// an unbounded, untyped payload the client would have to format.
type ProductChange struct {
	Path           string `json:"path"`
	Type           string `json:"type"`
	Classification string `json:"classification"`
	Reason         string `json:"reason,omitempty"`
	OldValue       string `json:"oldValue,omitempty"`
	NewValue       string `json:"newValue,omitempty"`
	OldTruncated   bool   `json:"oldTruncated,omitempty"`
	NewTruncated   bool   `json:"newTruncated,omitempty"`
}

// ProductChangesPreview is the bounded field-level diff, ordered breaking first.
// Total counts EVERY change found, so a truncated preview never reads as the
// complete set.
type ProductChangesPreview struct {
	Total       int             `json:"total"`
	Count       int             `json:"count"`
	Truncated   bool            `json:"truncated"`
	Breaking    int             `json:"breaking"`
	Potential   int             `json:"potential"`
	NonBreaking int             `json:"nonBreaking"`
	Items       []ProductChange `json:"items"`
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
	Changes        ProductChangesPreview      `json:"changes"`
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
		Changes:        boundProductChanges(res),
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
	// The two tallies cover EVERY consumer, not this page. A consumer chart drawn from
	// a page reads exactly like a chart of the whole blast radius, so the complete
	// distributions are computed here and the page carries them.
	return ProductImpactConsumersPage{
		Total: total, Count: len(page), Limit: limit, Offset: start,
		Truncated: truncated, NextOffset: next, Items: page,
		ByVerdict:    tallyConsumers(all, verdictOrder, func(c ProductImpactConsumer) string { return c.CompatibilityVerdict }),
		ByConfidence: tallyConsumers(all, confidenceOrder, func(c ProductImpactConsumer) string { return c.Confidence }),
	}
}

// verdictOrder and confidenceOrder are the canonical display orders of the two
// consumer dimensions, worst/strongest first. They are declared here rather than
// derived from the data so an empty bucket keeps its place and the chart does not
// reorder itself between two analyses of the same change.
var (
	verdictOrder    = []string{impact.CompatibilityIncompatible, impact.CompatibilityCompatible, impact.CompatibilityUnknown}
	confidenceOrder = []string{
		string(impact.ConfidenceCorroborated), string(impact.ConfidenceContractual), string(impact.ConfidenceDeclared),
		string(impact.ConfidenceObserved), string(impact.ConfidenceInferred), string(impact.ConfidenceUnknown),
	}
)

// tallyConsumers counts the COMPLETE consumer population by one dimension, emitting
// the known buckets in canonical order followed by any unrecognized value. Buckets
// always sum to len(all), so a consumer can draw a proportion without inventing a
// remainder, and a value the engine grows later shows up as itself instead of
// silently vanishing from the chart.
func tallyConsumers(all []ProductImpactConsumer, order []string, key func(ProductImpactConsumer) string) []ImpactTallyBucket {
	counts := map[string]int{}
	for i := range all {
		counts[key(all[i])]++
	}
	out := make([]ImpactTallyBucket, 0, len(order))
	for _, k := range order {
		out = append(out, ImpactTallyBucket{Key: k, Count: counts[k]})
		delete(counts, k)
	}
	extra := make([]string, 0, len(counts))
	for k := range counts {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, ImpactTallyBucket{Key: k, Count: counts[k]})
	}
	return out
}

// boundProductChanges renders the impact result's three change sets into ONE
// display-ordered, bounded field-level diff: breaking first, then potentially
// breaking, then non-breaking, which is the order a reviewer reads them in. Total
// counts every change found, so a truncated preview is never mistaken for the whole
// change.
func boundProductChanges(res *impact.Result) ProductChangesPreview {
	out := ProductChangesPreview{
		Breaking:    len(res.BreakingChanges),
		Potential:   len(res.PotentiallyBreakingChanges),
		NonBreaking: len(res.NonBreakingChanges),
		Items:       []ProductChange{},
	}
	out.Total = out.Breaking + out.Potential + out.NonBreaking
	for _, set := range [][]diff.Change{res.BreakingChanges, res.PotentiallyBreakingChanges, res.NonBreakingChanges} {
		for _, ch := range set {
			if len(out.Items) >= MaxImpactChanges {
				out.Truncated = true
				out.Count = len(out.Items)
				return out
			}
			oldVal, oldCut := renderChangeValue(ch.OldValue)
			newVal, newCut := renderChangeValue(ch.NewValue)
			out.Items = append(out.Items, ProductChange{
				Path:           ch.Path,
				Type:           ch.Type.String(),
				Classification: ch.Classification.String(),
				Reason:         ch.Reason,
				OldValue:       oldVal, OldTruncated: oldCut,
				NewValue: newVal, NewTruncated: newCut,
			})
		}
	}
	out.Count = len(out.Items)
	return out
}

// renderChangeValue renders one diff value as a bounded display string. Composite
// values are indented JSON (the same shape the client used to format itself); an
// unmarshalable value falls back to Go's default formatting rather than vanishing.
// It reports whether the value was cut so the UI can say so.
func renderChangeValue(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	var s string
	switch t := v.(type) {
	case string:
		s = t
	default:
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			s = string(b)
		} else {
			s = fmt.Sprint(v)
		}
	}
	r := []rune(s)
	if len(r) > MaxChangeValueRunes {
		return string(r[:MaxChangeValueRunes]), true
	}
	return s, false
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
