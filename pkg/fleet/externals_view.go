package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// foldExternals is the external systems an answer over s names (decision D4).
// Up to MaxExternalNodes it is s itself and s.Externals. Beyond it the
// MaxExternalNodes-1 most-consumed records are kept and the rest fold into one
// OtherEgress record; the answer snapshot is then a shallow copy of s whose
// relationships carry FoldedInto, whose limitations gain LimitationExternalCap
// and whose completeness is partial. s itself, every record and every
// ToExternal stay as built.
func foldExternals(s *FleetSnapshot) (*FleetSnapshot, map[ExternalKey]*ExternalRecord, map[ExternalKey]bool) {
	if len(s.Externals) <= MaxExternalNodes {
		return s, s.Externals, nil
	}
	keys := slices.SortedFunc(maps.Keys(s.Externals), func(a, b ExternalKey) int {
		if c := cmp.Compare(s.Externals[b].ConsumersTotal, s.Externals[a].ConsumersTotal); c != 0 {
			return c
		}
		return strings.Compare(string(a), string(b))
	})
	keep := MaxExternalNodes - 1
	view := make(map[ExternalKey]*ExternalRecord, MaxExternalNodes)
	for _, k := range keys[:keep] {
		view[k] = s.Externals[k]
	}
	other := &ExternalRecord{Key: OtherEgress, catalogLabel: "Other egress", Class: ClassOtherEgress, declarations: map[ServiceKey]int{}, hidden: map[ServiceKey]bool{}}
	folded := make(map[ExternalKey]bool, len(keys)-keep)
	for _, k := range keys[keep:] {
		rec := s.Externals[k]
		folded[k] = true
		other.Folded++
		other.Sources = append(other.Sources, rec.Sources...)
		for c, n := range rec.declarations {
			other.declarations[c] += n
		}
		// A union, so a hidden service behind several folded systems counts once.
		maps.Copy(other.hidden, rec.hidden)
	}
	finalizeExternal(other)
	view[OtherEgress] = other

	out := *s
	out.Relationships = slices.Clone(s.Relationships)
	for i := range out.Relationships {
		if folded[out.Relationships[i].ToExternal] {
			out.Relationships[i].FoldedInto = OtherEgress
		}
	}
	out.Limitations = append(slices.Clone(s.Limitations), Limitation{Code: LimitationExternalCap, Source: "fleet",
		Message: fmt.Sprintf("%d external systems are folded into other egress; raise MaxExternalNodes if a real fleet hits it", other.Folded)})
	sortSnapshotLimitations(&out)
	out.Completeness = CompletenessPartial
	return &out, view, folded
}

// restrictExternals keeps, in out, the external systems a visible service
// names, each with the sources and counts of visible consumers only, and
// counts the rest into hiddenExternals (decision 5). out's services,
// revisions and targets must already be restricted.
func restrictExternals(out, s *FleetSnapshot) {
	out.Externals = nil
	out.hiddenExternals = s.hiddenExternals
	for k, rec := range s.Externals {
		r := restrictExternal(out, rec)
		if r == nil {
			out.hiddenExternals++
			continue
		}
		if out.Externals == nil {
			out.Externals = map[ExternalKey]*ExternalRecord{}
		}
		out.Externals[k] = r
	}
}

// restrictExternal is rec as out's viewer sees it: a source stays only for a
// visible service, a kept revision and a kept (or no) target, and a consumer
// only while one of its sources stays. Every consumer it loses is counted
// into HiddenConsumers, never named. It is nil when no source stays.
func restrictExternal(out *FleetSnapshot, rec *ExternalRecord) *ExternalRecord {
	r := *rec
	r.Sources = nil
	kept := map[ServiceKey]bool{}
	// An inferred use counts once per source, so one dropped with its
	// target leaves its consumer's count too.
	droppedUses := map[ServiceKey]int{}
	for _, src := range rec.Sources {
		if out.Services[src.Service] != nil && (src.Revision == "" || out.Revisions[src.Revision] != nil) &&
			(src.Target == "" || out.Targets[src.Target] != nil) {
			r.Sources = append(r.Sources, src)
			kept[src.Service] = true
		} else if src.Provenance == ProvenanceInferred {
			droppedUses[src.Service]++
		}
	}
	if len(r.Sources) == 0 {
		return nil
	}
	r.declarations = map[ServiceKey]int{}
	for c, n := range rec.declarations {
		if kept[c] {
			r.declarations[c] = n - droppedUses[c]
		}
	}
	r.hidden = maps.Clone(rec.hidden)
	for _, c := range rec.Consumers {
		if !kept[c] {
			if r.hidden == nil {
				r.hidden = map[ServiceKey]bool{}
			}
			r.hidden[c] = true
		}
	}
	finalizeExternal(&r)
	return &r
}

// ExternalSubjectPrefix marks an external system where a service is
// expected: `pacto fleet get external:stripe`.
const ExternalSubjectPrefix = "external:"

// ParseExternalSubject reports whether s names an external system, and which.
func ParseExternalSubject(s string) (ExternalKey, bool) {
	k, ok := strings.CutPrefix(s, ExternalSubjectPrefix)
	return ExternalKey(k), ok && k != ""
}

// lookupExternal is the record of key an answer names, and the node it is
// folded into when the answer folds it. key may carry ExternalSubjectPrefix.
func (q *Query) lookupExternal(key string) (*ExternalRecord, ExternalKey, error) {
	k := ExternalKey(key)
	if p, ok := ParseExternalSubject(key); ok {
		k = p
	}
	if rec := q.externals[k]; rec != nil {
		return rec, "", nil
	}
	if q.folded[k] {
		return q.snap.Externals[k], OtherEgress, nil
	}
	return nil, "", &NotFoundError{Kind: "external", ID: key}
}

// boundExternal is a copy of rec listing at most MaxExternalSources
// consumers, in key order, and only their sources.
func boundExternal(rec *ExternalRecord) ExternalRecord {
	out := *rec
	out.Consumers, _, _ = boundSlice(rec.Consumers, MaxExternalSources)
	out.Sources = nil
	for _, src := range rec.Sources {
		if slices.Contains(out.Consumers, src.Service) {
			out.Sources = append(out.Sources, src)
		}
	}
	return out
}

// externalCatalogRef is the catalog the snapshot classified with that rec's
// catalog match names, nil when it is joined by name only.
func (q *Query) externalCatalogRef(rec *ExternalRecord) *ExternalCatalogRef {
	if rec.Catalog == nil {
		return nil
	}
	for _, c := range q.snap.ExternalCatalogs {
		if c.Origin == rec.Catalog.Origin && c.Digest == rec.Catalog.Digest {
			return &c
		}
	}
	return nil
}

// sortedExternals is the records an answer names, by identity.
func (q *Query) sortedExternals() []*ExternalRecord {
	out := make([]*ExternalRecord, 0, len(q.externals))
	for _, k := range slices.Sorted(maps.Keys(q.externals)) {
		out = append(out, q.externals[k])
	}
	return out
}

// externalEntityRef is the route-neutral reference to an external system.
// Its status is always unknown: Pacto never observes an external's health.
func externalEntityRef(r *ExternalRecord) EntityRef {
	return EntityRef{Kind: KindExternal, Key: string(r.Key), Label: r.Label, Secondary: r.Class, Status: StatusUnknown}
}

// externalListRef is an external list row: its reference with how many
// visible services declare it and the catalog entry that joined it (empty
// when joined by name only). Hidden consumers stay off the row, so a
// restricted list never varies with them; the entity page counts them.
func externalListRef(r *ExternalRecord) EntityRef {
	ref := externalEntityRef(r)
	ref.Consumers = r.ConsumersTotal
	if r.Catalog != nil {
		ref.CatalogEntry = r.Catalog.Entry
	}
	ref.JoinedBy = r.JoinedBy
	return ref
}

// JoinWords is how the consumers of a record no catalog entry classifies
// join it, for a text surface: only a normalized-name join is "by name".
func (r ExternalRecord) JoinWords() string {
	switch r.JoinedBy {
	case externalcatalog.MatchIdentity:
		return "joined by its declared identity"
	case externalcatalog.MatchHost:
		return "joined by its registrable domain"
	}
	return "joined by name"
}

// externalTextMatches reports whether text, compared without case, is in
// rec's identity, label or a catalog alias.
func externalTextMatches(rec *ExternalRecord, text string) bool {
	text = strings.ToLower(text)
	for _, f := range append([]string{string(rec.Key), rec.Label}, rec.Aliases...) {
		if strings.Contains(strings.ToLower(f), text) {
			return true
		}
	}
	return false
}

// ExternalClasses are the classes of an external system.
func ExternalClasses() []string {
	return []string{externalcatalog.ClassVendorAPI, externalcatalog.ClassManagedCloud, externalcatalog.ClassLegacy, externalcatalog.ClassUnclassified, ClassOtherEgress}
}

// validExternalClass reports whether s is an external class.
func validExternalClass(s string) bool { return slices.Contains(ExternalClasses(), s) }
