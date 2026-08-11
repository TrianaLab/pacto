package fleet

import (
	"fmt"
	"sort"
	"strings"
)

// EntityFilter constrains a global entity search (requirement 2.2). The zero
// value matches every entity of every kind, bounded by the default limit.
type EntityFilter struct {
	Text  string
	Kinds []EntityKind // empty means every kind
	// Owner is free-text owner SEARCH: an entity matches when at least one contract
	// revision behind it declares an owner whose team, DRI or any contact value
	// contains the query, case-insensitively (see [contract.Owner.MatchesFilter] and
	// [Query.ownerClaims]). Both teams disputing a service therefore find it. It is
	// what a reader typed, and it is deliberately generous.
	//
	// It is NOT owner identity: with owners `team-a` and `team-a-platform` in one
	// fleet, Owner: "team-a" matches both. Use OwnerKey wherever the answer is
	// canonical.
	Owner string
	// OwnerKey is exact canonical owner IDENTITY: an entity matches when at least
	// one contract revision behind it declares this owner, compared against the wire
	// form of [contract.Owner.Key] (see [contract.Owner.IsKey]) — `team:platform`,
	// `dri:alice`. It is the filter every canonical owner link carries — an owner
	// page's estate, a per-owner ranking's destination, an owner-scoped attention
	// link — so the population a figure counted and the population its link opens are
	// the same one. Narrow to "consistently owned by this owner" by pairing it with
	// Ownership: OwnershipConsistent.
	//
	// The namespace is required, and a bare name matches nothing: `alice` names a
	// team and a DRI equally well, and answering it with either would hand a reader
	// somebody else's estate under the right heading.
	//
	// OwnerKey and Owner compose (both must hold) rather than overriding each other:
	// they are two different questions, and a consumer that asks both means both.
	OwnerKey string
	Domain   string
	Scope    string
	// Status filters compliance-bearing entities (service/revision/target) by
	// their canonical compliance status. Source health is a separate axis.
	Status string
	// Ownership filters SERVICES by how their revisions declare ownership
	// (consistent, conflicting, unowned) — see [OwnershipTally] for the rule. It is
	// what turns "12 services have no declared owner" from a number into a list.
	Ownership string
	// Readiness filters CONTRACT REVISIONS by their declared readiness assessment
	// (passing, below-threshold, expired, not-declared) — see [ReadinessTally]. It is
	// a separate axis from Status: readiness is authored preparedness, compliance is
	// observed behaviour, and a revision can pass one and fail the other.
	Readiness string
	// SourceHealth filters source entities by their health (available, partial,
	// stale, unavailable) - kept distinct from compliance Status so the two are
	// never validated against one enum.
	SourceHealth string
	Source       string
	// Service scopes revision and target entities (and the service itself) to a
	// single canonical parent ServiceKey. It is how a consumer pages ALL revisions
	// of one service (the Product Impact revision selectors) without falling back to
	// the raw FleetSnapshot, so the service-detail revisions preview never has to be
	// treated as the complete revision universe.
	Service string
	Limit   int
	Offset  int
}

// EntityList is a bounded, deterministically ordered page of entity references.
// Limit and Offset are the effective (defaulted and capped) page bounds;
// Truncated reports that more entities matched than this page carries, and
// NextOffset is the offset of the next page (nil on the last page).
type EntityList struct {
	Meta       ProductMeta `json:"meta"`
	Total      int         `json:"total"`
	Count      int         `json:"count"`
	Limit      int         `json:"limit"`
	Offset     int         `json:"offset"`
	Truncated  bool        `json:"truncated"`
	NextOffset *int        `json:"nextOffset,omitempty"`
	Entities   []EntityRef `json:"entities"`
	// Aggregate tallies everything the filter matched, not this page. It is what
	// makes a filtered list page able to say something true about the whole match
	// instead of about its first 25 rows.
	Aggregate EntityAggregate `json:"aggregate"`
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
	truncated := end < total
	var next *int
	if truncated {
		n := end
		next = &n
	}
	return &EntityList{
		Meta: q.productMeta(), Total: total, Count: len(page),
		Limit: limit, Offset: start, Truncated: truncated, NextOffset: next, Entities: page,
		Aggregate: q.aggregate(filtered),
	}, nil
}

// Ownership filter values (see [OwnershipTally]).
const (
	OwnershipConsistent  = "consistent"
	OwnershipConflicting = "conflicting"
	OwnershipUnowned     = "unowned"
)

// Readiness filter values (see [ReadinessTally]).
const (
	ReadinessPassing        = "passing"
	ReadinessBelowThreshold = "below-threshold"
	ReadinessExpired        = "expired"
	ReadinessNotDeclared    = "not-declared"
)

func validOwnershipFilter(s string) bool {
	switch s {
	case OwnershipConsistent, OwnershipConflicting, OwnershipUnowned:
		return true
	default:
		return false
	}
}

func validReadinessFilter(s string) bool {
	switch s {
	case ReadinessPassing, ReadinessBelowThreshold, ReadinessExpired, ReadinessNotDeclared:
		return true
	default:
		return false
	}
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
	if f.SourceHealth != "" && !validSourceHealth(f.SourceHealth) {
		return &InvalidQueryError{Field: "sourceHealth", Value: f.SourceHealth, Reason: "not a source-health value (available, partial, stale, unavailable)"}
	}
	if f.Ownership != "" && !validOwnershipFilter(f.Ownership) {
		return &InvalidQueryError{Field: "ownership", Value: f.Ownership, Reason: "not an ownership state (consistent, conflicting, unowned)"}
	}
	if f.Readiness != "" && !validReadinessFilter(f.Readiness) {
		return &InvalidQueryError{Field: "readiness", Value: f.Readiness, Reason: "not a readiness state (passing, below-threshold, expired, not-declared)"}
	}
	for _, k := range f.Kinds {
		if !validEntityKind(k) {
			return &InvalidQueryError{Field: "kind", Value: string(k), Reason: "not a known entity kind"}
		}
	}
	return validateFilterCombos(f)
}

// validSourceHealth reports whether s is a known source-health value.
func validSourceHealth(s string) bool {
	switch SourceStatus(s) {
	case SourceAvailable, SourcePartial, SourceStale, SourceUnavailable:
		return true
	default:
		return false
	}
}

// validateFilterCombos rejects a filter that cannot apply to any of the requested
// kinds (e.g. an owner filter on a sources-only query), so a nonsensical
// combination is a typed error instead of a silent empty result. When no kinds
// are requested (every kind) any filter applies to something, so nothing is
// rejected.
func validateFilterCombos(f EntityFilter) error {
	want := kindSet(f.Kinds)
	appliesTo := func(field, value string, kinds ...EntityKind) error {
		if value == "" {
			return nil
		}
		for _, k := range kinds {
			if want[k] {
				return nil
			}
		}
		return &InvalidQueryError{Field: field, Value: value, Reason: "filter does not apply to the requested kinds"}
	}
	for _, c := range []struct {
		field, value string
		kinds        []EntityKind
	}{
		{"owner", f.Owner, []EntityKind{KindService, KindRevision, KindTarget, KindOwner}},
		{"ownerKey", f.OwnerKey, []EntityKind{KindService, KindRevision, KindTarget, KindOwner}},
		{"status", f.Status, []EntityKind{KindService, KindRevision, KindTarget}},
		{"sourceHealth", f.SourceHealth, []EntityKind{KindSource}},
		{"scope", f.Scope, []EntityKind{KindTarget}},
		{"domain", f.Domain, []EntityKind{KindService, KindRevision, KindTarget}},
		{"service", f.Service, []EntityKind{KindService, KindRevision, KindTarget}},
		// Ownership is declared per revision but classified per SERVICE (the whole
		// question is whether a service's revisions agree), and readiness is declared
		// and assessed per REVISION. Each applies to exactly one kind.
		{"ownership", f.Ownership, []EntityKind{KindService}},
		{"readiness", f.Readiness, []EntityKind{KindRevision}},
	} {
		if err := appliesTo(c.field, c.value, c.kinds...); err != nil {
			return err
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

// owners returns the distinct canonical owner KEYS across BOTH services and
// revisions. Because a service's summary owner is only its lowest-keyed revision's
// owner (see deriveOwner), a service with conflicting revision owners has
// revision-only owners that a services-only scan would miss; including revision
// owners makes every real owner discoverable.
//
// The roster is keyed, not labelled: a team and a DRI who share a name are two
// rows here, because they are two owners with two estates.
func (q *Query) owners() []string {
	seen := map[string]bool{}
	for _, s := range q.snap.Services {
		if o := s.Owner.KeyString(); o != "" {
			seen[o] = true
		}
	}
	for _, r := range q.snap.Revisions {
		if o := r.Owner.KeyString(); o != "" {
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

// entityMatches applies the filter guards to one reference. The scalar comparisons
// (fields read directly off the reference) live in matchScalarFields; the checks that
// need a snapshot lookup (text, owner, source) stay here.
func (q *Query) entityMatches(r EntityRef, f EntityFilter) bool {
	return matchScalarFields(r, f) &&
		matchEntityText(r, f.Text) &&
		(f.Owner == "" || q.entityOwnedBy(r, f.Owner)) &&
		(f.OwnerKey == "" || q.entityOwnedByKey(r, f.OwnerKey)) &&
		(f.Source == "" || q.entityFromSource(r, f.Source)) &&
		(f.Ownership == "" || q.matchOwnershipState(r, f.Ownership)) &&
		(f.Readiness == "" || q.matchReadinessState(r, f.Readiness))
}

// matchOwnershipState reports whether a SERVICE reference is in the requested
// ownership state. Any other kind never matches, so an ownership filter on a mixed
// query narrows to services rather than silently letting revisions through.
func (q *Query) matchOwnershipState(r EntityRef, want string) bool {
	if r.Kind != KindService {
		return false
	}
	n, _ := q.ownershipState(q.snap.Services[ServiceKey(r.Key)])
	switch n {
	case 0:
		return want == OwnershipUnowned
	case 1:
		return want == OwnershipConsistent
	default:
		return want == OwnershipConflicting
	}
}

// matchReadinessState reports whether a REVISION reference is in the requested
// readiness state, bucketing it by the SAME rule [ReadinessTally.add] uses so a
// filtered list and the distribution above it always agree.
func (q *Query) matchReadinessState(r EntityRef, want string) bool {
	if r.Kind != KindRevision {
		return false
	}
	var t ReadinessTally
	t.add(q.snap.Revisions[RevisionKey(r.Key)].Readiness)
	switch want {
	case ReadinessPassing:
		return t.Passing == 1
	case ReadinessExpired:
		return t.Expired == 1
	case ReadinessBelowThreshold:
		return t.BelowThreshold == 1
	default:
		return t.NotDeclared == 1
	}
}

// matchScalarFields checks the filter fields that are a direct comparison against a
// field already carried on the reference (no snapshot lookup): domain, scope,
// compliance status, source health and the canonical parent-service scope.
func matchScalarFields(r EntityRef, f EntityFilter) bool {
	if f.Domain != "" && r.Domain != f.Domain {
		return false
	}
	if f.Scope != "" && r.Scope != f.Scope {
		return false
	}
	if f.Status != "" && r.Status != f.Status {
		return false
	}
	if f.SourceHealth != "" && (r.Kind != KindSource || r.Status != f.SourceHealth) {
		return false
	}
	if f.Service != "" && !entityInService(r, f.Service) {
		return false
	}
	return true
}

// entityInService reports whether the referenced entity belongs to the given
// canonical parent ServiceKey: the service itself, or a revision/target whose
// parent is that service. Owners and sources have no parent service and never match
// (the combo validation already rejects a service filter on those kinds).
func entityInService(r EntityRef, service string) bool {
	switch r.Kind {
	case KindService:
		return r.Key == service
	case KindRevision, KindTarget:
		return r.ParentService == service
	default:
		return false
	}
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

// entityOwnedBy reports whether the referenced entity is associated with owner,
// using the same structured [contract.Owner.MatchesFilter] semantics (a
// case-insensitive substring over team, DRI and contacts) that attention and the
// rest of the product layer use - never a bare exact display-string comparison.
// A service (and the targets that belong to it) is asked through [Query.ownerClaims],
// so both co-owners of a disputed service can find it; a revision declares its
// own owner and is asked directly. A source has no owner and never matches.
// Every reference here comes from [candidateRefs], so its key maps to a real record.
func (q *Query) entityOwnedBy(r EntityRef, owner string) bool {
	switch r.Kind {
	case KindService:
		return q.matchOwner(q.snap.Services[ServiceKey(r.Key)], owner)
	case KindRevision:
		return q.snap.Revisions[RevisionKey(r.Key)].Owner.MatchesFilter(owner)
	case KindTarget:
		return q.matchOwner(q.snap.Services[ServiceKey(r.ParentService)], owner)
	case KindOwner:
		// An owner entity carries no contacts, so the search is over the NAME it was
		// declared under — the label, not the canonical key, whose namespace prefix is
		// identity rather than anything a reader typed.
		return strings.Contains(strings.ToLower(ownerEntityRef(r.Key).Label), strings.ToLower(owner))
	default:
		return false
	}
}

// entityOwnedByKey is [Query.entityOwnedBy]'s identity twin: the same walk over the
// same declarations, asked with [contract.Owner.IsKey] instead of the substring
// search, so a canonical owner link opens exactly the estate the owner page draws.
// The two differ ONLY in the predicate; anything else would be a second ownership
// model. An owner entity IS its key, so it is compared directly.
func (q *Query) entityOwnedByKey(r EntityRef, key string) bool {
	switch r.Kind {
	case KindService:
		return q.matchOwnerKey(q.snap.Services[ServiceKey(r.Key)], key)
	case KindRevision:
		return q.snap.Revisions[RevisionKey(r.Key)].Owner.IsKey(key)
	case KindTarget:
		return q.matchOwnerKey(q.snap.Services[ServiceKey(r.ParentService)], key)
	case KindOwner:
		return r.Key == key
	default:
		return false
	}
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
