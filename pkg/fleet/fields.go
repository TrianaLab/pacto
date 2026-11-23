package fleet

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Where a field of a declared service comes from: written in its pacto.yaml,
// accepted there by the person whose verified signature carries the
// declaration, attested by a signed CI read, or inferred and never declared.
const (
	FieldDeclared = "declared"
	FieldAccepted = "accepted"
	FieldAttested = "attested"
	FieldInferred = "inferred"
)

// FieldProvenance is one field of a service's current revision and where its
// value comes from. It is presentation only: no gate reads it.
type FieldProvenance struct {
	Field        string     `json:"field" doc:"service.owner, dependencies[<name>].compatibility or a field of the inferred record"`
	Value        string     `json:"value"`
	Provenance   string     `json:"provenance" enum:"declared,accepted,attested,inferred"`
	By           string     `json:"by,omitempty" doc:"Who accepted it: the person whose verified signature carries the accepted declaration"`
	Source       string     `json:"source,omitempty"`
	AsOf         *time.Time `json:"asOf,omitempty"`
	Completeness string     `json:"completeness,omitempty" enum:"complete,partial"`
	Proposed     string     `json:"proposed,omitempty" doc:"A range Pacto proposes for a compatibility the contract leaves unknown; never declared"`
}

// FieldsPreview is the bounded list of a service's field provenance,
// essentials first.
type FieldsPreview struct {
	Items     []FieldProvenance `json:"items"`
	Count     int               `json:"count"`
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
	Hidden    int               `json:"hidden,omitempty" doc:"Fields hidden by your permissions: read from a workload, or naming a service, you may not see"`
}

// fieldsOf says where each field of s's current revision comes from: the
// owner, each dependency's compatibility, then every other field of its
// inferred record. Nil unless that revision carries a declaration, cites a
// registry decision that carried the owner or a dependency into it, or s
// replaced an inferred service through a link other than a Pacto target,
// visible or hidden from the viewer, so every viewer gets the same shape: a
// plain inferred record (shown as inferredFields) and a Pacto target's
// takeover answer as they did before.
func (q *Query) fieldsOf(s *ServiceRecord) *FieldsPreview {
	cur := q.currentRevision(s)
	if cur == nil || !q.hasFields(s, cur) {
		return nil
	}
	byField := map[string]contract.Inferred{}
	if cur.Record != nil {
		for _, f := range cur.Record.Fields {
			byField[f.Field] = f
		}
	}
	rows := q.ownerRow(s, byField)
	rows = append(rows, dependencyRows(cur, byField)...)
	accepted, by := acceptedRanges(cur)
	shown := map[string]bool{}
	for i, r := range rows {
		shown[r.Field] = true
		if r.Provenance == FieldDeclared && accepted[r.Field] {
			rows[i].Provenance, rows[i].By = FieldAccepted, by
		}
	}
	if cur.Record != nil {
		for _, f := range cur.Record.Fields {
			if !shown[f.Field] {
				rows = append(rows, inferredRow(f))
			}
		}
	}
	items, total, truncated := boundSlice(rows, MaxDetailPreview)
	return &FieldsPreview{Items: items, Count: len(items), Total: total, Truncated: truncated, Hidden: cur.hiddenClusterFields}
}

func (q *Query) hasFields(s *ServiceRecord, cur *ContractRevision) bool {
	return (cur.Record != nil && (cur.Record.Declaration != nil || citesRowField(cur.Record))) ||
		q.snap.hiddenHandovers[s.Key] ||
		slices.ContainsFunc(q.snap.Replacements, func(r Replacement) bool { return r.To == s.Key && r.Link != linkPactoTarget })
}

// ownerRow is s's owner and where it comes from, read from what the owner
// rule (claimOwner) settled on; none when s has no owner of any tier.
func (q *Query) ownerRow(s *ServiceRecord, byField map[string]contract.Inferred) []FieldProvenance {
	row := FieldProvenance{Field: "service.owner", Value: s.Owner.DisplayString()}
	switch s.OwnerProvenance {
	case OwnerProvenanceDeclared:
		t, _ := ownerTier(q.snap, s)
		row.Provenance = FieldDeclared
		if r := newestOwnerRevision(q.snap, s, t); r != nil {
			row.Source, row.AsOf = "pacto.yaml of "+string(r.Key)+carriedFrom(r, row.Field), copyTime(r.FetchedAt)
		}
	case OwnerProvenanceDeclaredViaTarget:
		row.Provenance, row.Source, row.AsOf = FieldDeclared, "Pacto target", q.ownerTargetAsOf(s)
	case OwnerProvenanceAttested:
		row.Provenance, row.Source = FieldAttested, s.OwnerSource
		if r := newestOwnerRevision(q.snap, s, tierAttested); r != nil {
			row.AsOf = copyTime(r.FetchedAt)
		}
	default:
		f, ok := byField["service.owner"]
		if !ok {
			return nil
		}
		return []FieldProvenance{inferredRow(f)}
	}
	return []FieldProvenance{row}
}

// ownerTargetAsOf is when the source last read the target deriveOwner took
// s's owner from (the first, sorted, that reports one), nil when unknown.
func (q *Query) ownerTargetAsOf(s *ServiceRecord) *time.Time {
	for _, tk := range slices.Sorted(slices.Values(s.Targets)) {
		if t := q.snap.Targets[tk]; t != nil && !t.Owner.IsEmpty() {
			return copyTime(cmp.Or(t.ReconciledAt, t.EvidenceAt))
		}
	}
	return nil
}

// dependencyRows is one row per dependency of cur: its compatibility,
// declared, and for an unknown one the range Pacto proposes.
func dependencyRows(cur *ContractRevision, byField map[string]contract.Inferred) []FieldProvenance {
	if cur.Contract == nil {
		return nil
	}
	var rows []FieldProvenance
	for _, d := range cur.Contract.Dependencies {
		field := "dependencies[" + d.Name + "].compatibility"
		row := FieldProvenance{Field: field, Value: cmp.Or(d.Compatibility, contract.CompatibilityUnknown), Provenance: FieldDeclared,
			Source: "pacto.yaml of " + string(cur.Key) + carriedFrom(cur, field), AsOf: copyTime(cur.FetchedAt)}
		if f, ok := byField[field]; ok && row.Value == contract.CompatibilityUnknown {
			row.Proposed, row.Source, row.AsOf, row.Completeness = f.Proposed, f.Source, parseAsOf(f.AsOf), f.Completeness
		}
		rows = append(rows, row)
	}
	return rows
}

// acceptedRanges is the fields cur's declaration accepted and who accepted
// them, empty unless a person's verified signature carries the declaration
// and the installation's signer policy accepts that person: the rule that
// hands an inferred service over (declarationRejected).
func acceptedRanges(cur *ContractRevision) (map[string]bool, string) {
	if cur.Record == nil || cur.Record.Declaration == nil {
		return nil, ""
	}
	if why, _ := declarationRejected(cur); why != "" {
		return nil, ""
	}
	out := map[string]bool{}
	for _, a := range cur.Record.Declaration.Accepted {
		out[a.Field] = true
	}
	return out, cur.Record.Declaration.Asker.Subject
}

// citation is one registry decision a record cites: the record and the
// comma-separated keys it carried.
type citation struct{ record, fields string }

// citations are the registry decisions rec cites, the declaration first.
func citations(rec *contract.InferredRecord) []citation {
	var out []citation
	if d := rec.Inputs.Declaration; d != nil {
		out = append(out, citation{d.Record, d.Fields})
	}
	for _, a := range rec.Inputs.Acceptances {
		out = append(out, citation{a.Record, a.Fields})
	}
	return out
}

// citesRowField reports whether rec cites a decision that carried a field
// fieldsOf shows as a row: the owner, or a dependency's compatibility or
// whole entry. A cited break or ref alone draws no rows.
func citesRowField(rec *contract.InferredRecord) bool {
	for _, c := range citations(rec) {
		for k := range strings.SplitSeq(c.fields, ",") {
			k = strings.TrimSpace(k)
			if k == "service.owner" || strings.HasPrefix(k, "dependencies[") && (strings.HasSuffix(k, "]") || strings.HasSuffix(k, "].compatibility")) {
				return true
			}
		}
	}
	return false
}

// carriedFrom is ", carried from <repo>@sha256:<12 hex>" when r's record
// says pacto ci carried field into its pacto.yaml from a registry
// declaration or a signed acceptance and a CI identity published r, ",
// cites <repo>@sha256:<12 hex>, not a verified CI publish" when anyone else
// pushed it, "" otherwise. The reader does not re-verify the cited record,
// so the row stays declared. A whole carried dependency entry covers its
// compatibility.
func carriedFrom(r *ContractRevision, field string) string {
	if r == nil || r.Record == nil {
		return ""
	}
	for _, c := range citations(r.Record) {
		for k := range strings.SplitSeq(c.fields, ",") {
			if k = strings.TrimSpace(k); k != field && k+".compatibility" != field {
				continue
			}
			repo, digest, _ := strings.Cut(c.record, "@")
			if !CIPublished(r.Provenance) {
				return ", cites " + repo + "@" + shortRecord(digest) + ", not a verified CI publish"
			}
			return ", carried from " + repo + "@" + shortRecord(digest)
		}
	}
	return ""
}

func inferredRow(f contract.Inferred) FieldProvenance {
	return FieldProvenance{Field: f.Field, Value: f.Value, Provenance: FieldInferred, Source: f.Source,
		AsOf: parseAsOf(f.AsOf), Completeness: f.Completeness, Proposed: f.Proposed}
}

// parseAsOf reads a record field's RFC 3339 read time, nil when it has none.
func parseAsOf(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

// sourceKind is the kind of cluster read a source names, the word after
// "cluster:" (runtime, manifest, label, fleet); "" for any other source.
func sourceKind(source string) string {
	rest, ok := strings.CutPrefix(source, "cluster:")
	kind, _, _ := strings.Cut(rest, " ")
	if !ok {
		return ""
	}
	return kind
}

// Line is f as one text row: the field, its value and where it comes from.
func (f FieldProvenance) Line() string {
	label := f.Field
	if name, ok := strings.CutPrefix(f.Field, "dependencies["); ok && strings.HasSuffix(name, "].compatibility") {
		label = strings.TrimSuffix(name, "].compatibility") + " range"
	} else if f.Field == "service.owner" {
		label = "owner"
	}
	return fmt.Sprintf("  %-13s %-10s %s", label, f.Value, f.where())
}

func (f FieldProvenance) where() string {
	switch {
	case f.Proposed != "":
		return strings.TrimSuffix("proposed "+f.Proposed+" ("+strings.Trim(sourceKind(f.Source)+", "+f.Completeness, ", ")+")", " ()")
	case f.Provenance == FieldAccepted:
		return "accepted by " + f.By
	case f.Provenance == FieldInferred:
		return "inferred from " + f.Source
	}
	return f.Provenance
}

// Lines is p as text rows, with what it leaves out.
func (p *FieldsPreview) Lines() []string {
	var out []string
	for _, f := range p.Items {
		out = append(out, f.Line())
	}
	if more := p.Total - p.Count; more > 0 {
		out = append(out, "  "+strconv.Itoa(more)+" more")
	}
	if p.Hidden > 0 {
		out = append(out, "  "+strconv.Itoa(p.Hidden)+" fields hidden by your permissions")
	}
	return out
}
