package impact

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// Product-impact bounds. Every list in a [Product] is bounded; consumers are
// pageable.
const (
	DefaultProductConsumers = 100
	MaxProductConsumers     = 500
	MaxProductPath          = 50
	MaxProductOwners        = 200
	MaxProductActiveTargets = 200
	// MaxProductLimitations bounded the limitations preview.
	//
	// Deprecated: the analysis now caps limitations at [fleet.MaxMetaLimitations].
	MaxProductLimitations = fleet.MaxMetaLimitations
	// MaxProductChanges bounds the field-level change list. MaxChangeValueRunes
	// bounds EACH rendered value, so one changed OpenAPI schema cannot make the
	// answer unbounded; both truncations are reported rather than silent.
	MaxProductChanges   = 300
	MaxChangeValueRunes = 400
	// MaxHighlights bounds [ConsumersPage.Highlights].
	MaxHighlights = 8
	// MaxProductConsumerTargets bounds [ProductConsumer.Targets].
	MaxProductConsumerTargets = 20
)

// ProductConsumer is an affected consumer with route-neutral references and a
// bounded path.
type ProductConsumer struct {
	Service              fleet.EntityRef   `json:"service"`
	Path                 []fleet.EntityRef `json:"path"`
	PathTotal            int               `json:"pathTotal"`
	PathTruncated        bool              `json:"pathTruncated"`
	Depth                int               `json:"depth"`
	Direct               bool              `json:"direct"`
	Confidence           string            `json:"confidence"`
	CompatibilityVerdict string            `json:"compatibilityVerdict"`
	Owner                string            `json:"owner,omitempty"`
	// Note qualifies the verdict, as [AffectedConsumer.Note] does.
	Note string `json:"note,omitempty"`
	// Unmet lists the guarantees the consumer requires that the new revision
	// refuses, as [AffectedConsumer.Unmet] does.
	Unmet []contract.Mismatch `json:"unmet,omitempty"`
	// Compatibility is the version range the consumer declared for the changed
	// service, as written. Empty when it declared none (an inferred or observed-only
	// consumer).
	Compatibility string `json:"compatibility,omitempty"`
	// Blocking is true when this consumer is one of the reasons the release gate is
	// blocked: the change is BREAKING, the consumer does not accept the new version
	// or requires a guarantee it does not meet, and it runs on a known target.
	Blocking bool `json:"blocking,omitempty"`
	// Provenance, Via, Required, Uncontracted and Evidence are the consumer's
	// [AffectedConsumer] facts.
	Provenance   string     `json:"provenance" doc:"Where the consumer is known from: declared, observed, declared+observed or inferred"`
	Via          []string   `json:"via,omitempty" doc:"The edge types that make this service a consumer: dependency, configuration_reference, policy_reference or observed"`
	Required     bool       `json:"required,omitempty" doc:"The consumer declares its dependency on the changed service as required"`
	Uncontracted bool       `json:"uncontracted,omitempty" doc:"A runtime caller no contract describes: its service reference names no page"`
	Evidence     []Evidence `json:"evidence,omitempty" doc:"The facts behind this consumer and their age, newest first"`
	// Targets are the targets the consumer runs on, bounded to
	// MaxProductConsumerTargets; TargetsTotal counts them all.
	Targets      []fleet.EntityRef `json:"targets,omitempty" doc:"The targets the consumer runs on, at most 20"`
	TargetsTotal int               `json:"targetsTotal,omitempty" doc:"How many targets the consumer runs on, including those beyond the bound"`
	// OwnerKey and OwnerRef name the owner canonically; Owner is its label.
	OwnerKey string           `json:"ownerKey,omitempty" doc:"The canonical owner key (for example team:shop); owner is the display label"`
	OwnerRef *fleet.EntityRef `json:"ownerRef,omitempty" doc:"A reference to the owner's page"`
}

// TallyBucket is one bucket of a COMPLETE consumer distribution: a canonical
// dimension value and how many of the impact's consumers carry it.
type TallyBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// ConsumersPage is a stable, offset-paged consumer answer. Items are ordered by
// outcome (incompatible, unknown, compatible, unclassified), the highlighted
// consumers first within each outcome in their ranked order, then by key.
type ConsumersPage struct {
	Total int `json:"total"`
	// Outcome is the outcome Items are filtered to, when the caller asked for one.
	Outcome string `json:"outcome,omitempty"`
	// Matching counts the consumers Items page over: Total, or the consumers whose
	// outcome is Outcome when the page is filtered. Every tally still covers Total.
	Matching   int               `json:"matching"`
	Count      int               `json:"count"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
	Truncated  bool              `json:"truncated"`
	NextOffset *int              `json:"nextOffset,omitempty"`
	Items      []ProductConsumer `json:"items"`
	// ByVerdict and ByConfidence are computed over EVERY consumer, not over Items.
	// They are what a chart must be drawn from: Items is one page, so counting it
	// would present "the first 50 consumers" as the blast radius.
	ByVerdict    []TallyBucket `json:"byVerdict"`
	ByConfidence []TallyBucket `json:"byConfidence"`
	// Refused counts, over EVERY consumer, those the new revision leaves with
	// a guarantee they require unmet. They break whatever their verdict, which
	// judges only the range.
	Refused int `json:"refused"`
	// Highlights are at most MaxHighlights direct consumers, ranked over every
	// consumer (blocking, other incompatible or refused, unknown, compatible, then
	// key). They are identical on every page, so a drawing of them never depends on
	// which page of Items is loaded.
	Highlights []ProductConsumer `json:"highlights"`
	// Direct counts every direct consumer, not only this page's.
	Direct int `json:"direct"`
	// Hidden counts the direct consumers this viewer may not see. They are in
	// neither Items nor Total, and are never named.
	Hidden int `json:"hidden,omitempty"`
	// ConsumerTallies (byOutcome, byOwner and the owners outside the ranking) cover
	// EVERY consumer, like ByVerdict.
	ConsumerTallies
}

// ProductChange is one field-level semantic difference between the two revisions,
// rendered for display. Values are STRINGS (not `any`): a change can carry a whole
// OpenAPI schema, so it is rendered and bounded here rather than shipped as an
// unbounded, untyped payload the client would have to format.
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

// ChangesPreview is the bounded field-level diff, ordered breaking first. Total
// counts EVERY change found, so a truncated preview never reads as the complete
// set.
type ChangesPreview struct {
	Total       int             `json:"total"`
	Count       int             `json:"count"`
	Truncated   bool            `json:"truncated"`
	Breaking    int             `json:"breaking"`
	Potential   int             `json:"potential"`
	NonBreaking int             `json:"nonBreaking"`
	Items       []ProductChange `json:"items"`
}

// Product is the bounded, snapshot-anchored impact answer between two canonical
// revisions of one service: route-neutral references for the changed service,
// revisions, consumers, path steps, owners and active targets, every list
// bounded. A host adds its own navigation on top. SnapshotMatch is true ONLY on a
// success, which means BOTH the graph snapshot matched AND the old/new content
// analyzed was the exact immutable content the canonical revisions name.
type Product struct {
	Meta           fleet.ProductMeta        `json:"meta"`
	SnapshotID     string                   `json:"snapshotId"`
	SnapshotMatch  bool                     `json:"snapshotMatch"`
	Service        fleet.EntityRef          `json:"service"`
	OldRevision    *fleet.EntityRef         `json:"oldRevision,omitempty"`
	NewRevision    *fleet.EntityRef         `json:"newRevision,omitempty"`
	Classification string                   `json:"classification"`
	Changes        ChangesPreview           `json:"changes"`
	Consumers      ConsumersPage            `json:"consumers"`
	Owners         fleet.RefPreview         `json:"owners"`
	ActiveTargets  fleet.RefPreview         `json:"activeTargets"`
	Limitations    fleet.LimitationsPreview `json:"limitations"`
	// ChangedOwner is the changed service's own owner.
	ChangedOwner *fleet.EntityRef `json:"changedOwner,omitempty" doc:"A reference to the changed service's owner"`
	// Gate and GateReason are the release gate of the analysis, as
	// [Result.Gate] and [Result.GateReason].
	Gate       string `json:"gate" enum:"blocked,unverified,clear"`
	GateReason string `json:"gateReason"`
}

// ProductRevisions looks up both revisions, requires them to exist and to belong
// to the same logical service, and (when serviceKey is given) requires it to match
// that service exactly. It returns the verified service key and the two revision
// records so labels come from records, never reconstructed from strings.
func ProductRevisions(snap *fleet.FleetSnapshot, serviceKey, fromKey, toKey string) (fleet.ServiceKey, *fleet.ContractRevision, *fleet.ContractRevision, error) {
	from := snap.Revisions[fleet.RevisionKey(fromKey)]
	if from == nil {
		return "", nil, nil, errors.New("no revision with this fromRevisionKey in the published snapshot")
	}
	to := snap.Revisions[fleet.RevisionKey(toKey)]
	if to == nil {
		return "", nil, nil, errors.New("no revision with this toRevisionKey in the published snapshot")
	}
	if from.ServiceKey != to.ServiceKey {
		return "", nil, nil, fmt.Errorf("the two revisions belong to different services (%s vs %s)", from.ServiceKey, to.ServiceKey)
	}
	if serviceKey != "" && fleet.ServiceKey(serviceKey) != from.ServiceKey {
		return "", nil, nil, fmt.Errorf("serviceKey %q does not match the revisions' service %q", serviceKey, from.ServiceKey)
	}
	return from.ServiceKey, from, to, nil
}

// ImmutableRef returns the IMMUTABLE, canonical, resolver-compatible reference a
// product impact must analyze. This enforces the CONTENT-RETRIEVABILITY dimension
// only: content is retrievable ONLY when the revision's ResolvedRef is a canonical
// OCI digest reference (oci://registry/repo@<validated digest>) AND that digest is
// internally consistent with the revision's recorded content digest. A mutable
// tag, a local filesystem path, a scheme-less ref (which the resolver would treat
// as a local path), a missing ref or an inconsistent/malformed digest is rejected,
// using the SAME classifier (fleet.ClassifyContentIdentity) the detail Retrievable
// flag uses. It is independent of any target's revision-match certainty: a target
// can match this revision EXACTLY (a trusted digest with no canonical ref) yet a
// product impact still rejects it here, because the content is not retrievable
// through a canonical ref.
func ImmutableRef(rev *fleet.ContractRevision) (string, error) {
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

// BuildProduct enriches a raw impact result with canonical references (all looked
// up in the snapshot so identities are exact and labels come from records) and
// bounds every list. SnapshotMatch is true because the caller analyzed only the
// immutable content [ImmutableRef] returned, over this snapshot.
//
// Deprecated: use [BuildProductOutcome] with an empty outcome.
func BuildProduct(meta fleet.ProductMeta, snap *fleet.FleetSnapshot, svcKey fleet.ServiceKey, fromRev, toRev *fleet.ContractRevision, res *Result, limit, offset int) *Product {
	return BuildProductOutcome(meta, snap, svcKey, fromRev, toRev, res, limit, offset, "")
}

// BuildProductOutcome is [BuildProduct] with the consumer page filtered to one
// outcome (one of the Outcome constants); "" pages over every consumer. The tallies, the
// highlights and Total always cover every consumer, so a drawing made from them is
// the same whichever filter the table below it shows.
func BuildProductOutcome(meta fleet.ProductMeta, snap *fleet.FleetSnapshot, svcKey fleet.ServiceKey, fromRev, toRev *fleet.ContractRevision, res *Result, limit, offset int, outcome string) *Product {
	consumers := make([]ProductConsumer, 0, len(res.Consumers))
	for _, c := range res.Consumers {
		consumers = append(consumers, productConsumer(snap, res, c))
	}
	// Deterministic order so paging is stable across identical requests.
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].Service.Key < consumers[j].Service.Key })

	owners := make([]fleet.EntityRef, 0, len(res.Owners))
	for _, o := range res.Owners {
		owners = append(owners, fleet.OwnerRef(o))
	}
	hidden := 0
	if s := snap.Services[svcKey]; s != nil {
		hidden = s.HiddenDependents + s.HiddenPolicyReferrers
	}
	// meta describes the snapshot; the analysis can know less than it (a truncated
	// blast radius, a service missing from the fleet), and the answer must say so.
	if r, ok := completenessRank[res.Completeness]; ok && r < completenessRank[meta.Completeness] {
		meta.Completeness = res.Completeness
	}
	targets := make([]fleet.EntityRef, 0, len(res.ActiveTargets))
	for _, tk := range res.ActiveTargets {
		targets = append(targets, targetRef(snap, tk))
	}
	return &Product{
		Meta: meta, SnapshotID: snap.SnapshotID, SnapshotMatch: true,
		Service:        serviceRef(snap, svcKey),
		OldRevision:    revisionRef(fromRev),
		NewRevision:    revisionRef(toRev),
		Classification: res.Classification,
		Changes:        boundChanges(res),
		Consumers:      pageConsumersOutcome(consumers, limit, offset, hidden, outcome),
		Owners:         boundRefs(owners, MaxProductOwners),
		ActiveTargets:  boundRefs(targets, MaxProductActiveTargets),
		Limitations:    limitationsPreview(res),
		ChangedOwner:   ownerRef(res.ChangedOwnerKey),
		Gate:           res.Gate,
		GateReason:     res.GateReason,
	}
}

// productConsumer is one affected consumer with route-neutral references and
// a bounded path and target list.
func productConsumer(snap *fleet.FleetSnapshot, res *Result, c AffectedConsumer) ProductConsumer {
	path := pathRefs(snap, c.Path)
	pathTotal := len(path)
	if pathTotal > MaxProductPath {
		path = path[:MaxProductPath]
	}
	var targets []fleet.EntityRef
	for _, tk := range c.Targets[:min(len(c.Targets), MaxProductConsumerTargets)] {
		targets = append(targets, targetRef(snap, tk))
	}
	return ProductConsumer{
		Service:              serviceRef(snap, fleet.NewServiceKeyDomain(c.Domain, c.Service)),
		Path:                 path,
		PathTotal:            pathTotal,
		PathTruncated:        pathTotal > MaxProductPath,
		Depth:                c.Depth,
		Direct:               c.Direct,
		Confidence:           string(c.Confidence),
		CompatibilityVerdict: c.CompatibilityVerdict,
		Owner:                c.Owner,
		Note:                 c.Note,
		Unmet:                slices.Clone(c.Unmet),
		Compatibility:        c.Compatibility,
		Blocking:             res.blocks(c),
		Provenance:           c.Provenance,
		Via:                  slices.Clone(c.Via),
		Required:             c.Required,
		Uncontracted:         c.Uncontracted,
		Evidence:             slices.Clone(c.Evidence),
		Targets:              targets,
		TargetsTotal:         len(c.Targets),
		OwnerKey:             c.OwnerKey,
		OwnerRef:             ownerRef(c.OwnerKey),
	}
}

// ownerRef is the reference to the owner with canonical key, nil for none.
func ownerRef(key string) *fleet.EntityRef {
	if key == "" {
		return nil
	}
	ref := fleet.OwnerRef(key)
	return &ref
}

// completenessRank orders completeness from least to most known.
var completenessRank = map[fleet.Completeness]int{
	fleet.CompletenessEmpty: 0, fleet.CompletenessPartial: 1, fleet.CompletenessComplete: 2,
}

// pageConsumersOutcome offset-pages a sorted consumer list over the consumers of
// one outcome ("" for all), with defaulted/capped limit and a next-offset cursor.
// hidden is the count of direct consumers the viewer may not see. all must be
// sorted by key.
func pageConsumersOutcome(all []ProductConsumer, limit, offset, hidden int, outcome string) ConsumersPage {
	// The two tallies cover EVERY consumer, not this page. A consumer chart drawn from
	// a page reads exactly like a chart of the whole blast radius, so the complete
	// distributions are computed here and the page carries them.
	refused := 0
	direct := make([]ProductConsumer, 0, len(all))
	for i := range all {
		if len(all[i].Unmet) > 0 {
			refused++
		}
		if all[i].Direct {
			direct = append(direct, all[i])
		}
	}
	// all is sorted by key, so a stable sort by rank keeps key as the tiebreak.
	sort.SliceStable(direct, func(i, j int) bool { return highlightRank(direct[i]) < highlightRank(direct[j]) })
	highlights := direct[:min(len(direct), MaxHighlights)]
	listed := orderByOutcome(all, highlights, outcome)
	pg := fleet.Paginate(listed, limit, offset, DefaultProductConsumers, MaxProductConsumers)
	return ConsumersPage{
		Refused: refused, Direct: len(direct), Hidden: hidden,
		Highlights: highlights,
		Total:      len(all), Outcome: outcome, Matching: len(listed),
		Count: len(pg.Items), Limit: pg.Limit, Offset: pg.Offset,
		Truncated: pg.Truncated, NextOffset: pg.NextOffset, Items: pg.Items,
		ByVerdict:       tallyConsumers(all, verdictOrder, func(c ProductConsumer) string { return c.CompatibilityVerdict }),
		ByConfidence:    tallyConsumers(all, confidenceOrder, func(c ProductConsumer) string { return c.Confidence }),
		ConsumerTallies: tallyProductConsumers(all),
	}
}

// orderByOutcome keeps the consumers of one outcome ("" for all) and orders them as
// the figure reads: by outcome, the highlighted ones first within each in their
// ranked order, then by key (all is sorted by key and the sort is stable).
func orderByOutcome(all, highlights []ProductConsumer, outcome string) []ProductConsumer {
	rank := make(map[string]int, len(highlights))
	for i, h := range highlights {
		rank[h.Service.Key] = i
	}
	pos := func(c ProductConsumer) (int, int) {
		r, ok := rank[c.Service.Key]
		if !ok {
			r = len(highlights)
		}
		return slices.Index(outcomeOrder, consumerOutcome(c)), r
	}
	out := make([]ProductConsumer, 0, len(all))
	for _, c := range all {
		if outcome == "" || consumerOutcome(c) == outcome {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		oi, ri := pos(out[i])
		oj, rj := pos(out[j])
		if oi != oj {
			return oi < oj
		}
		return ri < rj
	})
	return out
}

func consumerOutcome(c ProductConsumer) string {
	return outcomeOf(c.CompatibilityVerdict, len(c.Unmet) > 0)
}

// highlightRank orders consumers worth drawing: blocking, other broken
// (incompatible or refused), unknown, then compatible. Unknown sorts before
// compatible so a cap hides the positive ones first. The dashboard's verdictOf
// mirrors it.
func highlightRank(c ProductConsumer) int {
	switch {
	case c.Blocking:
		return 0
	case len(c.Unmet) > 0 || c.CompatibilityVerdict == CompatibilityIncompatible:
		return 1
	case c.CompatibilityVerdict == CompatibilityCompatible:
		return 3
	default:
		return 2
	}
}

// verdictOrder and confidenceOrder are the canonical display orders of the two
// consumer dimensions, worst/strongest first. Verdicts follow the dashboard's one
// order for every distribution: err, warn, unknown, ok, so unknown sits between
// incompatible and compatible and never next to the green bucket's place by accident.
// They are declared here rather than
// derived from the data so an empty bucket keeps its place and the chart does not
// reorder itself between two analyses of the same change.
var (
	verdictOrder    = []string{CompatibilityIncompatible, CompatibilityUnknown, CompatibilityCompatible}
	confidenceOrder = []string{
		string(ConfidenceCorroborated), string(ConfidenceContractual), string(ConfidenceDeclared),
		string(ConfidenceObserved), string(ConfidenceInferred), string(ConfidenceUnknown),
	}
)

// tallyConsumers counts the COMPLETE consumer population by one dimension, emitting
// the known buckets in canonical order followed by any unrecognized value. Buckets
// always sum to len(all), so a consumer can draw a proportion without inventing a
// remainder, and a value the engine grows later shows up as itself instead of
// silently vanishing from the chart.
func tallyConsumers(all []ProductConsumer, order []string, key func(ProductConsumer) string) []TallyBucket {
	counts := map[string]int{}
	for i := range all {
		counts[key(all[i])]++
	}
	out := make([]TallyBucket, 0, len(order))
	for _, k := range order {
		out = append(out, TallyBucket{Key: k, Count: counts[k]})
		delete(counts, k)
	}
	extra := make([]string, 0, len(counts))
	for k := range counts {
		extra = append(extra, k)
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, TallyBucket{Key: k, Count: counts[k]})
	}
	return out
}

// boundChanges renders the result's three change sets into ONE display-ordered,
// bounded field-level diff: breaking first, then potentially breaking, then
// non-breaking, which is the order a reviewer reads them in. Total counts every
// change found, so a truncated preview is never mistaken for the whole change.
func boundChanges(res *Result) ChangesPreview {
	out := ChangesPreview{
		Breaking:    len(res.BreakingChanges),
		Potential:   len(res.PotentiallyBreakingChanges),
		NonBreaking: len(res.NonBreakingChanges),
		Items:       []ProductChange{},
	}
	out.Total = out.Breaking + out.Potential + out.NonBreaking
	for _, set := range [][]diff.Change{res.BreakingChanges, res.PotentiallyBreakingChanges, res.NonBreakingChanges} {
		for _, ch := range set {
			if len(out.Items) >= MaxProductChanges {
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
// values are indented JSON; an unmarshalable value falls back to Go's default
// formatting rather than vanishing. It reports whether the value was cut so the
// UI can say so.
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

// boundRefs bounds a reference list into a preview with truncation metadata.
// Callers always pass a non-nil (make-initialized) slice.
func boundRefs(refs []fleet.EntityRef, limit int) fleet.RefPreview {
	total := len(refs)
	truncated := total > limit
	items := refs
	if truncated {
		items = refs[:limit]
	}
	return fleet.RefPreview{Total: total, Count: len(items), Truncated: truncated, Items: items}
}

// limitationsPreview copies the impact limitations into a preview so the answer
// never aliases the raw impact result. The analysis already capped the list
// (fleet.BoundLimitations), so the preview carries that truncation rather than
// applying a second bound.
func limitationsPreview(res *Result) fleet.LimitationsPreview {
	items := append([]fleet.Limitation{}, res.Limitations...)
	return fleet.LimitationsPreview{Total: len(items), Count: len(items), Truncated: res.LimitationsTruncated, Items: items}
}

// serviceRef builds a route-neutral service reference for an already-canonical
// key, taking the human label from the snapshot record when present and otherwise
// decoding the key (never re-encoding it).
func serviceRef(snap *fleet.FleetSnapshot, key fleet.ServiceKey) fleet.EntityRef {
	if s := snap.Services[key]; s != nil {
		return fleet.ServiceRef(s)
	}
	domain, name := fleet.ParseServiceKey(key)
	return fleet.EntityRef{Kind: fleet.KindService, Key: string(key), Label: name, Domain: domain}
}

// revisionRef builds a route-neutral revision reference whose label is the
// record's service and version, never the raw key.
func revisionRef(rev *fleet.ContractRevision) *fleet.EntityRef {
	label := rev.Service
	if rev.Version != "" {
		label = rev.Service + " " + rev.Version
	}
	return &fleet.EntityRef{Kind: fleet.KindRevision, Key: string(rev.Key), Label: label, Version: rev.Version, Secondary: rev.Digest, Domain: rev.Domain}
}

// targetRef builds a route-neutral target reference whose label is the record's
// DisplayName when present, otherwise the raw key.
func targetRef(snap *fleet.FleetSnapshot, key string) fleet.EntityRef {
	label, domain := key, ""
	if t := snap.Targets[fleet.TargetKey(key)]; t != nil {
		label, domain = t.DisplayName(), t.Domain
	}
	return fleet.EntityRef{Kind: fleet.KindTarget, Key: key, Label: label, Domain: domain}
}

// pathRefs turns an impact path of ALREADY-CANONICAL service keys into
// route-neutral references. Each element is a canonical ServiceKey (its own domain
// baked in), so it is used verbatim and never re-encoded with another step's domain.
func pathRefs(snap *fleet.FleetSnapshot, keys []string) []fleet.EntityRef {
	out := make([]fleet.EntityRef, 0, len(keys))
	for _, k := range keys {
		out = append(out, serviceRef(snap, fleet.ServiceKey(k)))
	}
	return out
}
