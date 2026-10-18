package impact

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

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
	MaxProductLimitations   = 100
	// MaxProductChanges bounds the field-level change list. MaxChangeValueRunes
	// bounds EACH rendered value, so one changed OpenAPI schema cannot make the
	// answer unbounded; both truncations are reported rather than silent.
	MaxProductChanges   = 300
	MaxChangeValueRunes = 400
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
}

// TallyBucket is one bucket of a COMPLETE consumer distribution: a canonical
// dimension value and how many of the impact's consumers carry it.
type TallyBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// ConsumersPage is a stable, offset-paged consumer answer.
type ConsumersPage struct {
	Total      int               `json:"total"`
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
func BuildProduct(meta fleet.ProductMeta, snap *fleet.FleetSnapshot, svcKey fleet.ServiceKey, fromRev, toRev *fleet.ContractRevision, res *Result, limit, offset int) *Product {
	consumers := make([]ProductConsumer, 0, len(res.Consumers))
	for _, c := range res.Consumers {
		path := pathRefs(snap, c.Path)
		pathTotal := len(path)
		pathTruncated := false
		if pathTotal > MaxProductPath {
			path = path[:MaxProductPath]
			pathTruncated = true
		}
		consumers = append(consumers, ProductConsumer{
			Service:              serviceRef(snap, fleet.NewServiceKeyDomain(c.Domain, c.Service)),
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

	owners := make([]fleet.EntityRef, 0, len(res.Owners))
	for _, o := range res.Owners {
		owners = append(owners, fleet.EntityRef{Kind: fleet.KindOwner, Key: o, Label: o})
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
		Consumers:      pageConsumers(consumers, limit, offset),
		Owners:         boundRefs(owners, MaxProductOwners),
		ActiveTargets:  boundRefs(targets, MaxProductActiveTargets),
		Limitations:    boundLimitations(res.Limitations),
	}
}

// pageConsumers offset-pages a sorted consumer list with defaulted/capped limit
// and a next-offset cursor.
func pageConsumers(all []ProductConsumer, limit, offset int) ConsumersPage {
	pg := fleet.Paginate(all, limit, offset, DefaultProductConsumers, MaxProductConsumers)
	// The two tallies cover EVERY consumer, not this page. A consumer chart drawn from
	// a page reads exactly like a chart of the whole blast radius, so the complete
	// distributions are computed here and the page carries them.
	return ConsumersPage{
		Total: len(all), Count: len(pg.Items), Limit: pg.Limit, Offset: pg.Offset,
		Truncated: pg.Truncated, NextOffset: pg.NextOffset, Items: pg.Items,
		ByVerdict:    tallyConsumers(all, verdictOrder, func(c ProductConsumer) string { return c.CompatibilityVerdict }),
		ByConfidence: tallyConsumers(all, confidenceOrder, func(c ProductConsumer) string { return c.Confidence }),
	}
}

// verdictOrder and confidenceOrder are the canonical display orders of the two
// consumer dimensions, worst/strongest first. They are declared here rather than
// derived from the data so an empty bucket keeps its place and the chart does not
// reorder itself between two analyses of the same change.
var (
	verdictOrder    = []string{CompatibilityIncompatible, CompatibilityCompatible, CompatibilityUnknown}
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

// boundLimitations bounds the impact limitations into a preview. It copies the
// slice so the answer never aliases the raw impact result.
func boundLimitations(ls []fleet.Limitation) fleet.LimitationsPreview {
	total := len(ls)
	truncated := total > MaxProductLimitations
	n := total
	if truncated {
		n = MaxProductLimitations
	}
	items := append([]fleet.Limitation{}, ls[:n]...)
	return fleet.LimitationsPreview{Total: total, Count: len(items), Truncated: truncated, Items: items}
}

// serviceRef builds a route-neutral service reference for an already-canonical
// key, taking the human label from the snapshot record when present and otherwise
// decoding the key (never re-encoding it).
func serviceRef(snap *fleet.FleetSnapshot, key fleet.ServiceKey) fleet.EntityRef {
	name, domain := "", ""
	if s := snap.Services[key]; s != nil {
		name, domain = s.Name, s.Domain
	} else {
		domain, name = fleet.ParseServiceKey(key)
	}
	return fleet.EntityRef{Kind: fleet.KindService, Key: string(key), Label: name, Domain: domain}
}

// revisionRef builds a route-neutral revision reference whose label is the
// record's service and version, never the raw key.
func revisionRef(rev *fleet.ContractRevision) *fleet.EntityRef {
	label := rev.Service
	if rev.Version != "" {
		label = rev.Service + " " + rev.Version
	}
	return &fleet.EntityRef{Kind: fleet.KindRevision, Key: string(rev.Key), Label: label, Secondary: rev.Digest, Domain: rev.Domain}
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
