package fleet

import (
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

// AttributedFinding is a finding paired with the canonical reference of the
// entity it actually affects, so a finding aggregated across multiple targets or
// revisions never loses which entity it belongs to (requirement 2.4).
type AttributedFinding struct {
	Finding finding.Finding `json:"finding"`
	Entity  EntityRef       `json:"entity"`
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

// FindingsPreview is a bounded preview of findings that all belong to the
// detail's own single entity.
type FindingsPreview struct {
	Total     int               `json:"total"`
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated"`
	Items     []finding.Finding `json:"items"`
}

func findingsPreview(fs []finding.Finding) FindingsPreview {
	it, total, trunc := boundSlice(fs, MaxDetailPreview)
	return FindingsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
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

// RelationshipsPreview is a bounded preview of neighborhood edges.
type RelationshipsPreview struct {
	Total     int                `json:"total"`
	Count     int                `json:"count"`
	Truncated bool               `json:"truncated"`
	Items     []NeighborhoodEdge `json:"items"`
}

func relationshipsPreview(es []NeighborhoodEdge) RelationshipsPreview {
	it, total, trunc := boundSlice(es, MaxDetailPreview)
	return RelationshipsPreview{Total: total, Count: len(it), Truncated: trunc, Items: it}
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

// RuntimeFact is one flattened observed-runtime leaf: a dotted key path and its
// scalar value stringified and length-capped.
type RuntimeFact struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// RuntimePreview is a bounded, flattened preview of a target's observed runtime.
// The product API never copies the raw arbitrary runtime map verbatim (that is
// recursively unbounded); the full value stays on the low-level /api/fleet/snapshot
// export. This preview bounds the OUTPUT on nested size: at most MaxDetailPreview
// leaves, each value length-capped, and the walk itself stops at maxRuntimeScan
// leaves and maxRuntimeDepth nesting so a pathological source cannot blow it up.
type RuntimePreview struct {
	Total     int           `json:"total"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated"`
	Items     []RuntimeFact `json:"items"`
}
