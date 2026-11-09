package fleet

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/semver"
)

// EntityFilter constrains a global entity search. The zero
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
	// Domain is the exact registry path (see ServiceRecord.Domain).
	Domain string
	// BusinessDomain is the exact business domain of the SERVICE an entity belongs
	// to (see ServiceRecord.BusinessDomain), so a revision is kept by its service's
	// domain even when it declared another; a domain entity matches its own name.
	// NoBusinessDomain asks for the services that have none, UnknownBusinessDomain
	// for those whose domain is unknown.
	BusinessDomain string
	// Offering keeps the SERVICES that provide this platform offering, and the
	// offering reference of that name.
	Offering string
	// Consumes keeps the SERVICES with a declared, resolved use of a provider of
	// this platform offering: a dependency or a configuration or policy reference.
	Consumes string
	// Phase narrows Consumes to the consumers with a provisioning status of
	// the offering in this phase, bucketed as [PhaseTally] counts it
	// (requested, provisioning, ready, failed, deleting, unknown). It needs
	// Consumes.
	Phase string
	// Environment is the exact environment a target runs in (TargetRecord.Environment).
	Environment string
	// Capability keeps the SERVICES any of whose read revisions declares a
	// capability of this type (see [Query.serviceCapabilities]).
	Capability string
	// Template keeps only the golden path SERVICES (TemplateOnly) or leaves them
	// and their revisions out (TemplateExclude); every other kind stays under
	// TemplateExclude and goes under TemplateOnly. Empty keeps every service,
	// golden paths marked.
	Template string
	// CreatedFrom keeps the SERVICES whose declared origin resolves to this golden
	// path service key; NoOrigin keeps those that declare no origin and
	// UnresolvedOrigin those whose origin resolves to no golden path in this view.
	// Only the key's syntax is checked: a hidden, absent or non-path key answers an
	// empty list, so the answer never reveals a hidden path.
	CreatedFrom string
	Scope       string
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
	// HasNonCompliant keeps the OWNERS and DOMAINS with at least one non-compliant
	// service, counted as their rows count it ([EntityRef.NonCompliant]); it is the
	// list the rank figure's "Other" note opens.
	HasNonCompliant bool
	// SourceKind keeps the SOURCES of this exact kind (oci, kubernetes, ...), the
	// list a per-kind source tally ([SourceCounts.AvailableByKind]) opens.
	SourceKind string
	// Sort orders the answer: "" by kind then key, SortName by label in natural
	// order (target-2 before target-10), SortHealth least healthy first, then by
	// name. Paging walks the sorted answer, so every page agrees with the order.
	Sort   string
	Limit  int
	Offset int
}

// Entity sort orders (see [EntityFilter.Sort]).
const (
	SortName   = "name"
	SortHealth = "health"
	// SortServices puts the owners with the most services first, then by name.
	SortServices = "services"
	// SortNonCompliant puts the owners and domains with the most non-compliant
	// services first, then the most services, then by name.
	SortNonCompliant = "nonCompliant"
	// SortCreated puts the golden paths that created the most services first,
	// then the most non-compliant of those, then by name.
	SortCreated = "created"
	// SortRate puts the owners and domains with the highest share of non-compliant
	// services first ([EntityRef.NonCompliant] of its rate base), then the most
	// non-compliant, then by name.
	SortRate = "rate"
)

// Template filter values (see [EntityFilter.Template] and [SearchFilter.Template]).
const (
	TemplateOnly    = "only"
	TemplateExclude = "exclude"
)

// validTemplateFilter reports whether s is a template filter value.
func validTemplateFilter(s string) bool { return s == TemplateOnly || s == TemplateExclude }

// templateMatches reports whether an entity that is a golden path or not
// passes the template filter.
func templateMatches(filter string, goldenPath bool) bool {
	switch filter {
	case TemplateOnly:
		return goldenPath
	case TemplateExclude:
		return !goldenPath
	default:
		return true
	}
}

// validServiceKey reports whether s is a canonical service key: one that
// round-trips through ParseServiceKey and names a service.
func validServiceKey(s string) bool {
	domain, name := ParseServiceKey(ServiceKey(s))
	return name != "" && NewServiceKeyDomain(domain, name) == ServiceKey(s)
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
// focus search, contextual links and entity pickers. It
// returns an [InvalidQueryError] for a malformed filter rather than silently
// defaulting.
func (q *Query) Entities(f EntityFilter) (*EntityList, error) {
	if err := validateEntityFilter(f); err != nil {
		return nil, err
	}
	want := kindSet(f.Kinds)
	refs := q.candidateRefs(want)
	consumers := q.offeringConsumers(f.Consumes)
	if f.Phase != "" {
		consumers = q.offeringConsumersInPhase(f.Consumes, f.Phase)
	}

	x := q.crossKindSets(f)
	filtered := refs[:0:0]
	for _, r := range refs {
		if q.entityMatches(r, f, x) && (f.Consumes == "" || r.Kind == KindService && consumers[ServiceKey(r.Key)]) {
			filtered = append(filtered, r)
		}
	}
	sortEntityRefs(filtered)
	sortEntityList(filtered, f.Sort)

	pg := Paginate(filtered, f.Limit, f.Offset, DefaultEntityLimit, MaxEntityLimit)
	return &EntityList{
		Meta: q.productMeta(), Total: len(filtered), Count: len(pg.Items),
		Limit: pg.Limit, Offset: pg.Offset, Truncated: pg.Truncated, NextOffset: pg.NextOffset, Entities: pg.Items,
		Aggregate: q.aggregate(filtered),
	}, nil
}

// sortEntityList orders a filtered answer by the requested [EntityFilter.Sort],
// stably over the kind-then-key order it arrives in.
func sortEntityList(filtered []EntityRef, order string) {
	switch order {
	case SortName:
		sort.SliceStable(filtered, func(i, j int) bool { return naturalLess(filtered[i].Label, filtered[j].Label) })
	case SortHealth:
		sort.SliceStable(filtered, func(i, j int) bool {
			a, b := healthRank(filtered[i].Status), healthRank(filtered[j].Status)
			if a != b {
				return a < b
			}
			return naturalLess(filtered[i].Label, filtered[j].Label)
		})
	case SortServices:
		sort.SliceStable(filtered, func(i, j int) bool {
			if a, b := filtered[i].Services, filtered[j].Services; a != b {
				return a > b
			}
			return naturalLess(filtered[i].Label, filtered[j].Label)
		})
	case SortNonCompliant:
		sort.SliceStable(filtered, func(i, j int) bool {
			a, b := filtered[i], filtered[j]
			if a.NonCompliant != b.NonCompliant {
				return a.NonCompliant > b.NonCompliant
			}
			if a.Services != b.Services {
				return a.Services > b.Services
			}
			return naturalLess(a.Label, b.Label)
		})
	case SortCreated:
		sort.SliceStable(filtered, func(i, j int) bool {
			a, b := filtered[i], filtered[j]
			if a.Created != b.Created {
				return a.Created > b.Created
			}
			if a.NonCompliant != b.NonCompliant {
				return a.NonCompliant > b.NonCompliant
			}
			return naturalLess(a.Label, b.Label)
		})
	case SortRate:
		sort.SliceStable(filtered, func(i, j int) bool {
			a, b := filtered[i], filtered[j]
			// Cross-multiplied, so no float compares two rates: a.nc/a.base > b.nc/b.base.
			if l, r := a.NonCompliant*b.RateBase(), b.NonCompliant*a.RateBase(); l != r {
				return l > r
			}
			if a.NonCompliant != b.NonCompliant {
				return a.NonCompliant > b.NonCompliant
			}
			return naturalLess(a.Label, b.Label)
		})
	}
}

// RateBase is the denominator of an owner or domain row's non-compliant rate: the
// services an owner owns outright ([EntityRef.Owned]), the rows the rank figure
// divides by, and every service of a domain.
func (r EntityRef) RateBase() int {
	if r.Kind == KindOwner {
		return r.Owned
	}
	return r.Services
}

// crossKind holds the owners and domains a filter of the OTHER kind relates to,
// computed once per answer: the owners with a service in the BusinessDomain
// filter, and the domains with a service the OwnerKey filter's owner claims. Each
// is nil when its filter is absent or the query does not name the kind: on an
// every-kind query the two filters keep narrowing to what belongs to a service.
type crossKind struct {
	ownersInDomain, domainsOfOwner map[string]bool
}

func (q *Query) crossKindSets(f EntityFilter) crossKind {
	var x crossKind
	if f.BusinessDomain != "" && slices.Contains(f.Kinds, KindOwner) {
		x.ownersInDomain = map[string]bool{}
		for k, s := range q.snap.Services {
			if q.serviceInBusinessDomain(string(k), f.BusinessDomain) {
				for _, o := range q.ownerClaimsOf(s).keys {
					x.ownersInDomain[o] = true
				}
			}
		}
	}
	if f.OwnerKey != "" && slices.Contains(f.Kinds, KindDomain) {
		x.domainsOfOwner = map[string]bool{}
		for _, s := range q.snap.Services {
			if s.BusinessDomain != "" && q.matchOwnerKey(s, f.OwnerKey) {
				x.domainsOfOwner[s.BusinessDomain] = true
			}
		}
	}
	return x
}

// NoBusinessDomain is the BusinessDomain filter value for the services known to
// have no business domain, and UnknownBusinessDomain for those whose domain Pacto
// cannot tell. Neither is a slug, and a value that is not a slug never becomes a
// domain (see validBusinessDomain), so they cannot collide with one.
const (
	NoBusinessDomain      = "(none)"
	UnknownBusinessDomain = "(unknown)"
)

// Ownership filter values (see [OwnershipTally]).
const (
	OwnershipConsistent  = "consistent"
	OwnershipConflicting = "conflicting"
	OwnershipUnowned     = "unowned"
	// OwnershipUnidentified is every service with no single canonical owner:
	// unowned, conflicting or owned by contacts alone. It is the union the
	// non-compliant owner ranking counts as "no identified owner", not a fourth
	// partition bucket.
	OwnershipUnidentified = "unidentified"
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
	case OwnershipConsistent, OwnershipConflicting, OwnershipUnowned, OwnershipUnidentified:
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
	if err := validatePaging(f.Limit, f.Offset); err != nil {
		return err
	}
	if err := validateFields([]fieldCheck{
		{"sort", f.Sort, "not a sort order (name, health, services, nonCompliant, rate, created)", validSort},
		{"sourceKind", f.SourceKind, "not a source kind", validBusinessDomain},
		{"status", f.Status, "not a canonical status", ValidStatus},
		{"sourceHealth", f.SourceHealth, "not a source-health value (available, partial, stale, unavailable)", validSourceHealth},
		{"ownership", f.Ownership, "not an ownership state (consistent, conflicting, unowned, unidentified)", validOwnershipFilter},
		{"readiness", f.Readiness, "not a readiness state (passing, below-threshold, expired, not-declared)", validReadinessFilter},
		{"consumes", f.Consumes, "not an offering slug", validBusinessDomain},
		{"template", f.Template, "not a template filter (only, exclude)", validTemplateFilter},
		{"createdFrom", f.CreatedFrom, "not a service key, (none) or (unresolved)", validCreatedFrom},
		{"phase", f.Phase, "not a provisioning phase (requested, provisioning, ready, failed, deleting, unknown)", validPhaseBucket},
	}); err != nil {
		return err
	}
	if f.Phase != "" && f.Consumes == "" {
		return &InvalidQueryError{Field: "phase", Value: f.Phase, Reason: "needs consumes: a phase narrows the consumers of one offering"}
	}
	for _, k := range f.Kinds {
		if !validEntityKind(k) {
			return &InvalidQueryError{Field: "kind", Value: string(k), Reason: "not a known entity kind"}
		}
	}
	return validateFilterCombos(f)
}

// boolValue is a set boolean filter as the value [validateFilterCombos] checks.
func boolValue(b bool) string {
	if b {
		return "true"
	}
	return ""
}

func validSort(s string) bool {
	return s == SortName || s == SortHealth || s == SortServices || s == SortNonCompliant || s == SortRate || s == SortCreated
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
		{"ownerKey", f.OwnerKey, []EntityKind{KindService, KindRevision, KindTarget, KindOwner, KindDomain}},
		{"hasNonCompliant", boolValue(f.HasNonCompliant), []EntityKind{KindOwner, KindDomain}},
		{"sourceKind", f.SourceKind, []EntityKind{KindSource}},
		{"status", f.Status, []EntityKind{KindService, KindRevision, KindTarget}},
		{"sourceHealth", f.SourceHealth, []EntityKind{KindSource}},
		{"scope", f.Scope, []EntityKind{KindTarget}},
		{"environment", f.Environment, []EntityKind{KindTarget}},
		{"capability", f.Capability, []EntityKind{KindService}},
		{"template", f.Template, []EntityKind{KindService, KindRevision}},
		{"createdFrom", f.CreatedFrom, []EntityKind{KindService}},
		{"domain", f.Domain, []EntityKind{KindService, KindRevision, KindTarget}},
		{"businessDomain", f.BusinessDomain, []EntityKind{KindService, KindRevision, KindTarget, KindDomain, KindOwner}},
		{"offering", f.Offering, []EntityKind{KindService, KindOffering}},
		{"consumes", f.Consumes, []EntityKind{KindService}},
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

// kindSet returns the set of requested kinds. An empty request means every kind
// but KindDomain and KindOffering: they came later and are returned only when asked for, so a
// kind-less query keeps the answer it always had.
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
		created := q.createdCounts()
		for _, s := range q.snap.Services {
			refs = append(refs, q.serviceListRef(s, created[s.Key]))
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
		refs = append(refs, q.ownerListRefs()...)
	}
	if want[KindSource] {
		for _, st := range q.snap.Sources {
			refs = append(refs, sourceEntityRef(st))
		}
	}
	if want[KindDomain] {
		refs = append(refs, q.domainListRefs()...)
	}
	if want[KindOffering] {
		refs = append(refs, q.offeringListRefs()...)
	}
	return refs
}

// domainListRefs is one row per declared business domain, with the number of
// services in it and the targets they run on. No domain is not a row: it is asked
// for with the NoBusinessDomain filter. A golden path is no service of a domain.
func (q *Query) domainListRefs() []EntityRef {
	by := map[string]*EntityRef{}
	for _, s := range q.snap.Services {
		if s.BusinessDomain == "" || s.Template {
			continue
		}
		ref := by[s.BusinessDomain]
		if ref == nil {
			ref = new(domainEntityRef(s.BusinessDomain))
			by[s.BusinessDomain] = ref
		}
		ref.Services++
		ref.Targets += len(s.Targets)
		ref.NonCompliant += b2i(s.Status == StatusNonCompliant)
	}
	refs := make([]EntityRef, 0, len(by))
	for _, ref := range by {
		refs = append(refs, *ref)
	}
	return refs
}

// offeringListRefs is one row per platform offering, with the number of
// services that provide it and the targets they run on.
func (q *Query) offeringListRefs() []EntityRef {
	by := map[string]*EntityRef{}
	for _, s := range q.snap.Services {
		if s.Offering == "" {
			continue
		}
		ref := by[s.Offering]
		if ref == nil {
			ref = new(offeringEntityRef(s.Offering, ProvenanceObserved))
			by[s.Offering] = ref
		}
		if s.OfferingProvenance == ProvenanceDeclared {
			ref.OfferingProvenance = ProvenanceDeclared
		}
		ref.Services++
		ref.Targets += len(s.Targets)
	}
	refs := make([]EntityRef, 0, len(by))
	for _, ref := range by {
		refs = append(refs, *ref)
	}
	return refs
}

// serviceListRef is a service row with its target count and owner summary,
// and on a golden path the services created from it and its signature.
func (q *Query) serviceListRef(s *ServiceRecord, created OriginCount) EntityRef {
	ref := serviceEntityRef(s)
	ref.Targets = len(s.Targets)
	if s.Template {
		ref.Created, ref.NonCompliant, ref.Signature = created.Services, created.NonCompliant, q.pathSignature(s)
	}
	switch n, key := q.ownershipState(s); {
	case n == 1 && key != "":
		ref.Owner = ownerEntityRef(key).Label
	case n > 1:
		ref.Owner = fmt.Sprintf("%d owners claim it", n)
	}
	return ref
}

// ownerListRefs is one row per owner with the number of services it claims.
func (q *Query) ownerListRefs() []EntityRef {
	var refs []EntityRef
	// The same rule as the owner page: a service is an owner's when any of its
	// revisions names them.
	// Its non-compliant count is the rank figure's: only the services it owns
	// outright, the ones no other owner disputes, so a row and the figure lane
	// that links to it count the same services.
	claimed, owned, nonCompliant := map[string]int{}, map[string]int{}, map[string]int{}
	for _, s := range q.snap.Services {
		if s.Template {
			continue // a golden path is no service of its owner's estate
		}
		claims := q.ownerClaimsOf(s)
		for _, k := range claims.keys {
			claimed[k]++
		}
		if k := claims.soleKey(); claims.len() == 1 && k != "" {
			owned[k]++
			nonCompliant[k] += b2i(s.Status == StatusNonCompliant)
		}
	}
	for _, o := range q.owners() {
		ref := ownerEntityRef(o)
		ref.Services = claimed[o]
		ref.Owned = owned[o]
		ref.NonCompliant = nonCompliant[o]
		refs = append(refs, ref)
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
	// A golden path is no service of its owner's estate, so, like a domain that
	// holds only golden paths, an owner that only publishes golden paths has no
	// row. Its owner page still answers.
	for _, s := range q.snap.Services {
		if o := s.Owner.KeyString(); o != "" && !s.Template {
			seen[o] = true
		}
	}
	for _, r := range q.snap.Revisions {
		if o := r.Owner.KeyString(); o != "" && !q.goldenPath(r.ServiceKey) {
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
func (q *Query) entityMatches(r EntityRef, f EntityFilter, x crossKind) bool {
	return matchScalarFields(r, f) &&
		matchEntityText(r, f.Text) &&
		(f.Owner == "" || q.entityOwnedBy(r, f.Owner)) &&
		(f.OwnerKey == "" || q.entityOwnedByKey(r, f.OwnerKey, x)) &&
		(f.Source == "" || q.entityFromSource(r, f.Source)) &&
		(f.Ownership == "" || q.matchOwnershipState(r, f.Ownership)) &&
		(f.Readiness == "" || q.matchReadinessState(r, f.Readiness)) &&
		q.matchGoldenPath(r, f) &&
		q.matchOrganization(r, f, x)
}

// matchGoldenPath applies the template and created-from filters.
func (q *Query) matchGoldenPath(r EntityRef, f EntityFilter) bool {
	golden := r.Template
	if f.Template == TemplateExclude && r.Kind == KindRevision {
		// A golden path's revisions leave the population with it, so a revision
		// list a population count links to lists what it counted.
		golden = q.goldenPath(ServiceKey(r.ParentService))
	}
	return templateMatches(f.Template, golden) &&
		(f.CreatedFrom == "" || r.Kind == KindService && q.createdFrom(ServiceKey(r.Key), f.CreatedFrom))
}

// matchOrganization applies the business-domain, offering, capability and
// has-non-compliant filters.
func (q *Query) matchOrganization(r EntityRef, f EntityFilter, x crossKind) bool {
	return (!f.HasNonCompliant || r.NonCompliant > 0) &&
		(f.BusinessDomain == "" || q.matchBusinessDomain(r, f.BusinessDomain, x)) &&
		(f.Offering == "" || (r.Kind == KindService || r.Kind == KindOffering) && r.Offering == f.Offering) &&
		(f.Capability == "" || (r.Kind == KindService && slices.Contains(q.serviceCapabilities(q.snap.Services[ServiceKey(r.Key)]), f.Capability)))
}

// offeringConsumers is the services with a declared use of a provider of the
// platform offering (see offeringRelationship), nil when offering is empty.
func (q *Query) offeringConsumers(offering string) map[ServiceKey]bool {
	if offering == "" {
		return nil
	}
	out := map[ServiceKey]bool{}
	for i := range q.snap.Relationships {
		rel := &q.snap.Relationships[i]
		if p := q.snap.Services[rel.ToService]; offeringRelationship(rel, "") && p != nil && p.Offering == offering {
			out[rel.FromService] = true
		}
	}
	return out
}

// offeringConsumersInPhase is the consumers of offering with at least one
// provisioning status in phase, by the same bucketing [PhaseTally] counts, so
// the list a phase count links to names exactly the consumers behind it.
// ponytail: recomputes every use per call; index by offering if it shows in profiles.
func (q *Query) offeringConsumersInPhase(offering, phase string) map[ServiceKey]bool {
	uses, _ := q.offeringUses(func(p *ServiceRecord) bool { return p.Offering == offering }, "")
	out := map[ServiceKey]bool{}
	for _, u := range uses {
		for _, st := range u.Provisioning {
			if phaseBucket(st.Phase) == phase {
				out[ServiceKey(u.Consumer.Key)] = true
			}
		}
	}
	return out
}

// serviceCapabilities is the sorted, distinct self-declared assertion identities
// ([contract.Capability.AssertionKey]) a service's read revisions declare, so
// two extensions stay two.
func (q *Query) serviceCapabilities(s *ServiceRecord) []string {
	seen := map[string]bool{}
	for _, rk := range s.Revisions {
		if c := q.snap.Revisions[rk].Contract; c != nil {
			for _, capability := range c.Capabilities {
				seen[capability.AssertionKey()] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// matchOwnershipState reports whether a SERVICE reference is in the requested
// ownership state, bucketing it by the SAME rule [OwnershipTally.add] uses so a
// filtered list and the distribution above it always agree. Any other kind never
// matches, so an ownership filter on a mixed query narrows to services rather than
// silently letting revisions through.
func (q *Query) matchOwnershipState(r EntityRef, want string) bool {
	if r.Kind != KindService {
		return false
	}
	n, key := q.ownershipState(q.snap.Services[ServiceKey(r.Key)])
	var t OwnershipTally
	t.add(n)
	switch want {
	case OwnershipUnidentified:
		return n != 1 || key == ""
	case OwnershipUnowned:
		return t.Unowned == 1
	case OwnershipConsistent:
		return t.Consistent == 1
	default:
		return t.Conflicting == 1
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
	if f.Environment != "" && (r.Kind != KindTarget || r.Environment != f.Environment) {
		return false
	}
	if f.Status != "" && r.Status != f.Status {
		return false
	}
	if !matchSourceFields(r, f) {
		return false
	}
	if f.Service != "" && !entityInService(r, f.Service) {
		return false
	}
	return true
}

// matchSourceFields checks the source-only filters: health and kind. Any other
// kind never matches a set one.
func matchSourceFields(r EntityRef, f EntityFilter) bool {
	return (f.SourceHealth == "" || r.Kind == KindSource && r.Status == f.SourceHealth) &&
		(f.SourceKind == "" || r.Kind == KindSource && r.Secondary == f.SourceKind)
}

// matchBusinessDomain keeps a reference by the business domain of the service it
// belongs to, and a domain reference by its name; owners and sources belong to no
// service and never match a set filter.
func (q *Query) matchBusinessDomain(r EntityRef, want string, x crossKind) bool {
	switch r.Kind {
	case KindDomain:
		return r.Key == want
	case KindOwner:
		// An owner is in a domain when it claims a service there, by the owner
		// page's rule (any revision names it).
		return x.ownersInDomain[r.Key]
	case KindService:
		return q.serviceInBusinessDomain(r.Key, want)
	case KindRevision, KindTarget:
		return q.serviceInBusinessDomain(r.ParentService, want)
	default:
		return false
	}
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
	for _, field := range []string{r.Label, r.Key, r.Secondary, r.Domain, r.Summary, r.Offering} {
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
func (q *Query) entityOwnedByKey(r EntityRef, key string, x crossKind) bool {
	switch r.Kind {
	case KindService:
		return q.matchOwnerKey(q.snap.Services[ServiceKey(r.Key)], key)
	case KindRevision:
		return q.snap.Revisions[RevisionKey(r.Key)].Owner.IsKey(key)
	case KindTarget:
		return q.matchOwnerKey(q.snap.Services[ServiceKey(r.ParentService)], key)
	case KindOwner:
		return r.Key == key
	case KindDomain:
		return x.domainsOfOwner[r.Key]
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
// Revisions of one service go newest version first (see siblingRevisions): their
// key ends in a content digest, and digest order is no order a reader can use.
func sortEntityRefs(refs []EntityRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Kind == KindRevision && a.ParentService == b.ParentService {
			if c := semver.Compare(a.Version, b.Version); c != 0 {
				return c > 0
			}
		}
		return a.Key < b.Key
	})
}

// Page is one offset page of a sorted list: its items and the effective bounds.
// NextOffset is the offset of the next page, nil on the last one.
type Page[T any] struct {
	Items      []T
	Limit      int
	Offset     int
	Truncated  bool
	NextOffset *int
}

// Paginate returns the page of all starting at offset. A limit <= 0 becomes
// defLimit and a limit above maxLimit is capped there; an offset past the end
// yields an empty last page. Items is a copy, never a window onto all. Negative
// offsets are the caller's to reject.
func Paginate[T any](all []T, limit, offset, defLimit, maxLimit int) Page[T] {
	if limit <= 0 {
		limit = defLimit
	}
	limit = min(limit, maxLimit)
	start := min(offset, len(all))
	end := min(start+limit, len(all))
	p := Page[T]{Items: append([]T{}, all[start:end]...), Limit: limit, Offset: start, Truncated: end < len(all)}
	if p.Truncated {
		p.NextOffset = &end
	}
	return p
}

// healthRank is how unhealthy a status reads, lowest first: compliance
// statuses by [severityRank], source health by [sourceHealthRank]. An
// unrecognised one sorts last.
func healthRank(status string) int {
	if r, ok := severityRank[status]; ok {
		return r
	}
	if validSourceHealth(status) {
		return sourceHealthRank(SourceStatus(status))
	}
	return len(severityRank)
}

// naturalLess compares two labels case-insensitively, reading each run of
// digits as a number, so target-2 sorts before target-10.
func naturalLess(a, b string) bool {
	return NaturalCompare(strings.ToLower(a), strings.ToLower(b)) < 0
}

// NaturalCompare compares a and b case-sensitively, reading each run of ASCII
// digits as a number (env-2 before env-10) and the rest byte by byte.
func NaturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if c := cmp.Or(cmp.Compare(len(na), len(nb)), cmp.Compare(na, nb)); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		a, b = a[1:], b[1:]
	}
	return cmp.Compare(len(a), len(b))
}

// digitRun is the length of the ASCII digits s starts with.
func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}
