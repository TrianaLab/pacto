package fleet

import (
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// This file defines the reusable bounded preview shapes every entity-detail and
// neighborhood answer uses for its nested collections, so no product answer ever
// carries an unbounded list. Each preview reports Total (the full pre-cap count),
// Count (the items carried), Truncated (whether Total exceeded the cap) and the
// bounded Items, so a consumer always knows WHAT was truncated and how much it is
// missing. Ordering is the caller's responsibility and is deterministic.

// MaxDetailPreview is the hard cap on every nested list carried in an entity
// detail (revisions, deployments, dependencies, findings, evidence, tools, docs,
// skills, contributed entities, per-owner services and so on). A realistic entity
// is far below this; the cap exists so a pathological entity can never hand a
// consumer an unbounded detail.
const MaxDetailPreview = 200

// MaxEvidenceRefsPreview caps the evidence references carried per product finding.
// pacto's own validation emits one ref per finding and the k8s source is bounded by
// the object-size limit, so a realistic finding is far below this; the cap exists so
// an untrusted extension source cannot smuggle an unbounded evidence list inside an
// otherwise-bounded findings preview.
const MaxEvidenceRefsPreview = 50

// ProductSeverity is the finite product finding severity. It mirrors
// [finding.Severity] EXACTLY (error / warning / info / unknown) so Huma/OpenAPI
// emits the closed enum a generated client can narrow on. "unknown" is a real
// engine output (an assertion that could not be evaluated), so it MUST appear.
type ProductSeverity string

const (
	ProductSeverityError   ProductSeverity = ProductSeverity(finding.SeverityError)
	ProductSeverityWarning ProductSeverity = ProductSeverity(finding.SeverityWarning)
	ProductSeverityInfo    ProductSeverity = ProductSeverity(finding.SeverityInfo)
	ProductSeverityUnknown ProductSeverity = ProductSeverity(finding.SeverityUnknown)
)

// ProductSubjectRef is the product-shaped, camelCase subject a finding is about.
type ProductSubjectRef struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

// ProductEvidenceRef is the product-shaped, camelCase evidence reference.
type ProductEvidenceRef struct {
	Source     string `json:"source,omitempty"`
	ObservedAt string `json:"observedAt,omitempty"`
}

// ProductEvidenceRefsPreview is a bounded preview of a finding's evidence refs.
type ProductEvidenceRefsPreview struct {
	Total     int                  `json:"total"`
	Count     int                  `json:"count"`
	Truncated bool                 `json:"truncated"`
	Items     []ProductEvidenceRef `json:"items"`
}

// ProductFinding is the bounded product-transport shape of an engine finding.
// [finding.Finding] carries an UNBOUNDED EvidenceRefs slice and PascalCase JSON
// (it has no tags); ProductFinding is camelCase, uses the finite [ProductSeverity]
// enum, and bounds the evidence refs, so a product response that accepts findings
// from extension sources can never carry an unbounded per-finding evidence
// list. The low-level snapshot record keeps raw finding.Finding.
type ProductFinding struct {
	Code         string                     `json:"code,omitempty"`
	Severity     ProductSeverity            `json:"severity" enum:"error,warning,info,unknown"`
	Category     string                     `json:"category,omitempty"`
	Subject      ProductSubjectRef          `json:"subject"`
	ContractPath string                     `json:"contractPath,omitempty"`
	Message      string                     `json:"message,omitempty"`
	EvidenceRefs ProductEvidenceRefsPreview `json:"evidenceRefs"`
}

// productFinding maps a raw engine finding to the bounded product shape, capping
// its evidence refs at MaxEvidenceRefsPreview with honest total/count/truncated.
// It converts only the emitted prefix: an untrusted extension source can attach an
// arbitrarily large EvidenceRefs slice, and building a bounded answer must never do
// work (allocation/copy) proportional to that width.
func productFinding(f finding.Finding) ProductFinding {
	total := len(f.EvidenceRefs)
	n := min(total, MaxEvidenceRefsPreview)
	items := make([]ProductEvidenceRef, 0, n)
	for _, r := range f.EvidenceRefs[:n] {
		items = append(items, ProductEvidenceRef{Source: r.Source, ObservedAt: r.ObservedAt})
	}
	return ProductFinding{
		Code:         string(f.Code),
		Severity:     ProductSeverity(f.Severity),
		Category:     string(f.Category),
		Subject:      ProductSubjectRef{Kind: f.Subject.Kind, Name: f.Subject.Name},
		ContractPath: f.ContractPath,
		Message:      f.Message,
		EvidenceRefs: ProductEvidenceRefsPreview{Total: total, Count: len(items), Truncated: total > n, Items: items},
	}
}

// boundSlice caps items at max, copying so the preview never aliases its input,
// and reports the full total and whether truncation occurred. A nil or empty
// input yields an empty (non-nil) slice so JSON emits [] not null.
func boundSlice[T any](items []T, max int) (out []T, total int, truncated bool) {
	total = len(items)
	if total > max {
		return append([]T{}, items[:max]...), total, true
	}
	return append([]T{}, items...), total, false
}

// AttributedFinding is a bounded product finding paired with the canonical
// reference of the entity it actually affects, so a finding aggregated across
// multiple targets or revisions never loses which entity it belongs to.
type AttributedFinding struct {
	Finding ProductFinding `json:"finding"`
	Entity  EntityRef      `json:"entity"`
}

// AttributedLimitation is a limitation paired with the canonical reference of the
// entity it affects. Entity is omitted only for a limitation that genuinely
// belongs to the detail's own entity (never for one aggregated across many).
type AttributedLimitation struct {
	Limitation Limitation `json:"limitation"`
	Entity     EntityRef  `json:"entity,omitempty"`
}

// RefPreview is a bounded preview of entity references.
type RefPreview struct {
	Total     int         `json:"total"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated"`
	Items     []EntityRef `json:"items"`
}

// refPreview builds a bounded RefPreview from refs.
func refPreview(refs []EntityRef) RefPreview {
	it, total, trunc := boundSlice(refs, MaxDetailPreview)
	return RefPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// FindingsPreview is a bounded preview of bounded product findings that all
// belong to the detail's own single entity.
type FindingsPreview struct {
	Total     int              `json:"total"`
	Count     int              `json:"count"`
	Truncated bool             `json:"truncated"`
	Items     []ProductFinding `json:"items"`
}

func findingsPreview(fs []finding.Finding) FindingsPreview {
	total := len(fs)
	n := min(total, MaxDetailPreview)
	items := make([]ProductFinding, 0, n)
	for _, f := range fs[:n] {
		items = append(items, productFinding(f))
	}
	return FindingsPreview{Total: total, Count: len(items), Truncated: total > n, Items: items}
}

// AttributedFindingsPreview is a bounded preview of findings aggregated across
// multiple entities, each carrying its affected entity reference.
type AttributedFindingsPreview struct {
	Total     int                 `json:"total"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
	Items     []AttributedFinding `json:"items"`
}

func attributedFindingsPreview(fs []AttributedFinding) AttributedFindingsPreview {
	it, total, trunc := boundSlice(fs, MaxDetailPreview)
	return AttributedFindingsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// LimitationsPreview is a bounded preview of limitations belonging to the
// detail's own single entity.
type LimitationsPreview struct {
	Total     int          `json:"total"`
	Count     int          `json:"count"`
	Truncated bool         `json:"truncated"`
	Items     []Limitation `json:"items"`
}

func limitationsPreview(ls []Limitation) LimitationsPreview {
	it, total, trunc := boundSlice(ls, MaxDetailPreview)
	return LimitationsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// AttributedLimitationsPreview is a bounded preview of limitations aggregated
// across multiple entities, each carrying its affected entity reference.
type AttributedLimitationsPreview struct {
	Total     int                    `json:"total"`
	Count     int                    `json:"count"`
	Truncated bool                   `json:"truncated"`
	Items     []AttributedLimitation `json:"items"`
}

func attributedLimitationsPreview(ls []AttributedLimitation) AttributedLimitationsPreview {
	it, total, trunc := boundSlice(ls, MaxDetailPreview)
	return AttributedLimitationsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// EvidencePreview is a bounded preview of evidence items.
type EvidencePreview struct {
	Total     int            `json:"total"`
	Count     int            `json:"count"`
	Truncated bool           `json:"truncated"`
	Items     []EvidenceItem `json:"items"`
}

func evidencePreview(es []EvidenceItem) EvidencePreview {
	it, total, trunc := boundSlice(es, MaxDetailPreview)
	return EvidencePreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// RelationshipsPreview is a bounded preview of neighborhood edges. Total is the
// EXACT full relationship count and is present ONLY when it is truthfully known:
// when the preview is built from a complete edge slice, or from a neighborhood
// that did NOT truncate. When it is built from an ALREADY-truncated neighborhood
// the true total was bounded (nodes and edges) before it could be counted, so
// Total is omitted rather than reported as the pre-truncation scanned count.
// Count is always the number of edges carried; Truncated
// reports that more relationships exist than are carried.
type RelationshipsPreview struct {
	Total     *int               `json:"total,omitempty"`
	Count     int                `json:"count"`
	Truncated bool               `json:"truncated"`
	Items     []NeighborhoodEdge `json:"items"`
}

// relationshipsPreview builds a preview from a COMPLETE edge slice, so the total is
// always truthfully known (the full pre-cap count).
func relationshipsPreview(es []NeighborhoodEdge) RelationshipsPreview {
	it, total, trunc := boundSlice(es, MaxDetailPreview)
	t := total
	return RelationshipsPreview{Total: &t, Count: len(it), Truncated: trunc, Items: it}
}

// relationshipsPreviewFromNeighborhood builds the service-relationships preview
// from an ALREADY-bounded neighborhood. If the neighborhood did not truncate, its
// edges are the complete set and the total is known; if it truncated, the true
// total was bounded before it could be counted, so the total is left UNKNOWN (nil)
// and the preview is reported truncated. It never claims the pre-truncation edge
// count as the total.
//
// Only the edges INCIDENT to the focus service become relationships. A neighborhood is
// a graph: at depth 1 it already carries edges between two neighbours (api-gateway ->
// audit-log), which are somebody else's relationships, not this entity's. Rendered in a
// flat list keyed on the counterpart they read as this entity's own -- and, because the
// counterpart of every outbound edge of one neighbour is a different service while the
// row names only the neighbour, several of them collapsed into repeated
// indistinguishable rows ("Used by api-gateway" twice, above a Dependents list of two).
// The full graph is one click away in the same section; the list is about the entity.
//
// Only the service perspective is filtered, because only there are the edge endpoints
// service-keyed like FocusService; a finer perspective would match nothing and silently
// empty the list rather than trim it.
func relationshipsPreviewFromNeighborhood(nb *Neighborhood) RelationshipsPreview {
	incident := nb.Edges
	if nb.Perspective == PerspectiveService {
		focus := nb.FocusService.Key
		incident = make([]NeighborhoodEdge, 0, len(nb.Edges))
		for _, e := range nb.Edges {
			if e.From.Key == focus || e.To.Key == focus {
				incident = append(incident, e)
			}
		}
	}
	it, total, capTrunc := boundSlice(incident, MaxDetailPreview)
	rp := RelationshipsPreview{Count: len(it), Truncated: capTrunc || nb.Truncated, Items: it}
	if !nb.Truncated {
		// The neighborhood carried every edge, so `total` is the exact relationship
		// count (possibly further capped into Items, but the total is still known).
		t := total
		rp.Total = &t
	}
	return rp
}

// ToolsPreview is a bounded preview of tool summaries.
type ToolsPreview struct {
	Total     int           `json:"total"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated"`
	Items     []ToolSummary `json:"items"`
}

func toolsPreview(ts []ToolSummary) ToolsPreview {
	it, total, trunc := boundSlice(ts, MaxDetailPreview)
	return ToolsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// DocsPreview is a bounded preview of doc references.
type DocsPreview struct {
	Total     int      `json:"total"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated"`
	Items     []DocRef `json:"items"`
}

func docsPreview(ds []DocRef) DocsPreview {
	it, total, trunc := boundSlice(ds, MaxDetailPreview)
	return DocsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// StringsPreview is a bounded preview of plain strings (skills, conflicts).
type StringsPreview struct {
	Total     int      `json:"total"`
	Count     int      `json:"count"`
	Truncated bool     `json:"truncated"`
	Items     []string `json:"items"`
}

func stringsPreview(ss []string) StringsPreview {
	it, total, trunc := boundSlice(ss, MaxDetailPreview)
	return StringsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// AttentionPreview is a bounded preview of attention items.
type AttentionPreview struct {
	Total     int             `json:"total"`
	Count     int             `json:"count"`
	Truncated bool            `json:"truncated"`
	Items     []AttentionItem `json:"items"`
}

func attentionPreview(items []AttentionItem) AttentionPreview {
	it, total, trunc := boundSlice(items, MaxDetailPreview)
	return AttentionPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// attentionPreviewFromList builds a bounded attention preview from an ALREADY
// offset-paged AttentionList, preserving the TRUE matched total and truncation.
// Building a preview from a paged result's Items alone would double-truncate: the
// preview's Total would be the page size (never the real match count) and its
// Truncated would be false even when more items matched. This carries the list's
// own Total and Truncated so the preview reports the true total.
func attentionPreviewFromList(l *AttentionList) AttentionPreview {
	it, _, capTrunc := boundSlice(l.Items, MaxDetailPreview)
	return AttentionPreview{Total: l.Total, Count: len(it), Truncated: l.Truncated || capTrunc, Items: it}
}

// ReadinessCheck is the product-shaped view of one derived readiness check. It
// mirrors readiness.CheckResult but carries camelCase JSON so the product API is
// consistent, and it is only ever carried inside a bounded preview.
type ReadinessCheck struct {
	ID           string `json:"id"`
	Type         string `json:"type,omitempty"`
	Category     string `json:"category,omitempty"`
	Status       string `json:"status,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
	Description  string `json:"description,omitempty"`
	Weight       int    `json:"weight"`
	EarnedWeight int    `json:"earnedWeight"`
	Excluded     bool   `json:"excluded,omitempty"`
}

// ReadinessChecksPreview is a bounded preview of readiness checks.
type ReadinessChecksPreview struct {
	Total     int              `json:"total"`
	Count     int              `json:"count"`
	Truncated bool             `json:"truncated"`
	Items     []ReadinessCheck `json:"items"`
}

func readinessChecksPreview(cs []readiness.CheckResult) ReadinessChecksPreview {
	out := make([]ReadinessCheck, 0, len(cs))
	for _, c := range cs {
		out = append(out, ReadinessCheck{
			ID: c.ID, Type: c.Type, Category: c.Category, Status: c.Status,
			Evidence: c.Evidence, Description: c.Description, Weight: c.Weight,
			EarnedWeight: c.EarnedWeight, Excluded: c.Excluded,
		})
	}
	it, total, trunc := boundSlice(out, MaxDetailPreview)
	return ReadinessChecksPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// OwnerContactPoint is one declared way to reach an owner. It is contact
// METADATA, never identity: an email address or a chat channel names a route, not
// an owner the product can file a service under (see [contract.OwnerKey]). Nothing
// downstream may derive a key, a link or a ranking row from it.
type OwnerContactPoint struct {
	Type    string `json:"type,omitempty"`
	Value   string `json:"value,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

// ContactsPreview is a bounded preview of an owner's declared contact points, in
// the normalized set order [contract.Owner.ContactSet] defines — so the same
// declaration presents identically however the YAML was written, and a contact
// point repeated in the file appears once.
type ContactsPreview struct {
	Total     int                 `json:"total"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated"`
	Items     []OwnerContactPoint `json:"items"`
}

func contactsPreview(cs []contract.OwnerContact) ContactsPreview {
	out := make([]OwnerContactPoint, 0, len(cs))
	for _, c := range cs {
		out = append(out, OwnerContactPoint{Type: c.Type, Value: c.Value, Purpose: c.Purpose})
	}
	it, total, trunc := boundSlice(out, MaxDetailPreview)
	return ContactsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
}

// RuntimeFact is one flattened observed-runtime leaf: a dotted key path
// (length-capped) and its scalar value (stringified and length-capped).
type RuntimeFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// RuntimePreview is a bounded, flattened preview of a target's observed runtime.
// It is computed ONCE at Build time from the untrusted raw source map and is the
// ONLY form the snapshot retains — the raw arbitrary map (recursively unbounded) is
// never kept, so no query, clone or snapshot export ever does work proportional to
// its width. Both the OUTPUT and the ingestion WORK are bounded: at most
// MaxDetailPreview facts emitted, each key/value length-capped, the walk stops at
// maxRuntimeScan inspected facts and maxRuntimeDepth nesting. The single pass over
// the raw source map at Build is the one documented unbounded-source boundary;
// the product query then reads this projection in O(bound).
//
// Total is an EXACT count of flattened facts and is present ONLY when the bounded
// walk visited the whole structure; when the walk stopped early (scan budget or a
// per-level key cap) the true total is unknowable, so Total is omitted and
// Scanned reports the facts actually inspected as a lower bound. Truncated is true
// whenever fewer facts are emitted than were inspected OR the walk stopped early.
type RuntimePreview struct {
	// Total is the exact total flattened-fact count, present only when the walk
	// completed within bounds (see Truncated/Scanned otherwise).
	Total     *int          `json:"total,omitempty"`
	Count     int           `json:"count"`
	Scanned   int           `json:"scanned"`
	Truncated bool          `json:"truncated"`
	Items     []RuntimeFact `json:"items"`
}
