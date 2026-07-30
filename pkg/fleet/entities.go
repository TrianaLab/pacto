package fleet

import (
	"fmt"
	"sort"
	"strings"
)

// EntityFilter constrains a global entity search (requirement 2.2). The zero
// value matches every entity of every kind, bounded by the default limit.
type EntityFilter struct {
	Text   string
	Kinds  []EntityKind // empty means every kind
	Owner  string
	Domain string
	Scope  string
	Status string
	Source string
	Limit  int
	Offset int
}

// EntityList is a bounded, deterministically ordered page of entity references.
type EntityList struct {
	Meta     ProductMeta `json:"meta"`
	Total    int         `json:"total"`
	Count    int         `json:"count"`
	Entities []EntityRef `json:"entities"`
}

// Entities searches services, revisions, targets, owners and sources with one
// query, returning stable, navigable references. It powers global search, graph
// focus search, contextual links and entity pickers (requirement 2.2). It
// returns an [InvalidQueryError] for a malformed filter rather than silently
// defaulting.
func (q *Query) Entities(f EntityFilter) (*EntityList, error) {
	if err := validateEntityFilter(f); err != nil {
		return nil, err
	}
	want := kindSet(f.Kinds)
	refs := q.candidateRefs(want)

	filtered := refs[:0:0]
	for _, r := range refs {
		if q.entityMatches(r, f) {
			filtered = append(filtered, r)
		}
	}
	sortEntityRefs(filtered)

	total := len(filtered)
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultEntityLimit
	}
	if limit > MaxEntityLimit {
		limit = MaxEntityLimit
	}
	start := f.Offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	page := append([]EntityRef{}, filtered[start:end]...)
	return &EntityList{Meta: q.productMeta(), Total: total, Count: len(page), Entities: page}, nil
}

func validateEntityFilter(f EntityFilter) error {
	if f.Offset < 0 {
		return &InvalidQueryError{Field: "offset", Value: fmt.Sprint(f.Offset), Reason: "must be >= 0"}
	}
	if f.Limit < 0 {
		return &InvalidQueryError{Field: "limit", Value: fmt.Sprint(f.Limit), Reason: "must be >= 0"}
	}
	if f.Status != "" && !ValidStatus(f.Status) {
		return &InvalidQueryError{Field: "status", Value: f.Status, Reason: "not a canonical status"}
	}
	for _, k := range f.Kinds {
		if !validEntityKind(k) {
			return &InvalidQueryError{Field: "kind", Value: string(k), Reason: "not a known entity kind"}
		}
	}
	return nil
}

// kindSet returns the set of requested kinds; an empty request means all kinds.
func kindSet(kinds []EntityKind) map[EntityKind]bool {
	if len(kinds) == 0 {
		return map[EntityKind]bool{KindService: true, KindRevision: true, KindTarget: true, KindOwner: true, KindSource: true}
	}
	m := map[EntityKind]bool{}
	for _, k := range kinds {
		m[k] = true
	}
	return m
}

// candidateRefs collects one reference per entity of every requested kind.
func (q *Query) candidateRefs(want map[EntityKind]bool) []EntityRef {
	var refs []EntityRef
	if want[KindService] {
		for _, s := range q.snap.Services {
			refs = append(refs, serviceEntityRef(s))
		}
	}
	if want[KindRevision] {
		for _, r := range q.snap.Revisions {
			refs = append(refs, revisionEntityRef(r))
		}
	}
	if want[KindTarget] {
		for _, t := range q.snap.Targets {
			refs = append(refs, targetEntityRef(t))
		}
	}
	if want[KindOwner] {
		for _, o := range q.owners() {
			refs = append(refs, ownerEntityRef(o))
		}
	}
	if want[KindSource] {
		for _, st := range q.snap.Sources {
			refs = append(refs, sourceEntityRef(st))
		}
	}
	return refs
}

// owners returns the distinct, non-empty owner display strings across services.
func (q *Query) owners() []string {
	seen := map[string]bool{}
	for _, s := range q.snap.Services {
		if o := s.Owner.DisplayString(); o != "" {
			seen[o] = true
		}
	}
	out := make([]string, 0, len(seen))
	for o := range seen {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// entityMatches applies the filter guards to one reference.
func (q *Query) entityMatches(r EntityRef, f EntityFilter) bool {
	if !matchEntityText(r, f.Text) {
		return false
	}
	if f.Domain != "" && r.Domain != f.Domain {
		return false
	}
	if f.Scope != "" && r.Scope != f.Scope {
		return false
	}
	if f.Status != "" && r.Status != f.Status {
		return false
	}
	if f.Owner != "" && !q.entityOwnedBy(r, f.Owner) {
		return false
	}
	if f.Source != "" && !q.entityFromSource(r, f.Source) {
		return false
	}
	return true
}

// matchEntityText matches text as a case-insensitive substring over the label,
// key, secondary context and domain.
func matchEntityText(r EntityRef, text string) bool {
	if text == "" {
		return true
	}
	text = strings.ToLower(text)
	for _, field := range []string{r.Label, r.Key, r.Secondary, r.Domain} {
		if strings.Contains(strings.ToLower(field), text) {
			return true
		}
	}
	return false
}

// entityOwnedBy reports whether the referenced entity is associated with owner.
// A source has no owner and never matches an owner filter. Every reference here
// comes from [candidateRefs], so its key always maps to a real record; the
// lookups are direct and never nil.
func (q *Query) entityOwnedBy(r EntityRef, owner string) bool {
	switch r.Kind {
	case KindService:
		return q.ownerMatches(q.snap.Services[ServiceKey(r.Key)].Owner.DisplayString(), owner)
	case KindRevision:
		return q.ownerMatches(q.snap.Revisions[RevisionKey(r.Key)].Owner.DisplayString(), owner)
	case KindTarget:
		return q.ownerMatches(q.snap.Services[ServiceKey(r.ParentService)].Owner.DisplayString(), owner)
	case KindOwner:
		return r.Key == owner
	default:
		return false
	}
}

// ownerMatches reports whether have is a non-empty match for the requested owner.
func (q *Query) ownerMatches(have, owner string) bool {
	return have != "" && have == owner
}

// entityFromSource reports whether the referenced entity was contributed by the
// named source. An owner is derived, not sourced, and never matches. As in
// [entityOwnedBy], every reference maps to a real record.
func (q *Query) entityFromSource(r EntityRef, source string) bool {
	switch r.Kind {
	case KindService:
		return containsStr(q.snap.Services[ServiceKey(r.Key)].Sources, source)
	case KindRevision:
		rev := q.snap.Revisions[RevisionKey(r.Key)]
		return containsStr(rev.Sources, source) || rev.Source == source
	case KindTarget:
		t := q.snap.Targets[TargetKey(r.Key)]
		return containsStr(t.Sources, source) || t.Source == source
	case KindSource:
		return r.Key == source
	default:
		return false
	}
}

// sortEntityRefs orders references by kind then key, so search output is stable.
func sortEntityRefs(refs []EntityRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].Key < refs[j].Key
	})
}
