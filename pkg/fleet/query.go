package fleet

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// Result-bounding defaults. Queries are bounded so an agent or UI can never be
// handed an unbounded response.
const (
	DefaultSearchLimit = 100
	MaxSearchLimit     = 500
	DefaultStatusLimit = 200
)

// Query is a pure, network-free view over an immutable [FleetSnapshot]. It never
// mutates the snapshot, so a single snapshot can serve concurrent queries. Every
// answer carries a [Meta] with the snapshot's as-of time, completeness, and
// limitations.
//
// Every query answers from memory with one deliberate exception:
// [Query.RevisionDocument] reads one bounded document body from the revision's
// own bundle filesystem, because inlining every bundle's prose into the snapshot
// would make the index grow with the documentation in the fleet. It performs no
// network I/O either.
type Query struct {
	// snap is what answers read: base itself, or a shallow copy of it that
	// folds the external systems beyond MaxExternalNodes (see foldExternals).
	snap *FleetSnapshot
	// base is the snapshot as built, which Snapshot and Restrict read.
	base *FleetSnapshot
	// externals are the external systems answers name, keyed by identity, and
	// folded the identities they fold into OtherEgress.
	externals map[ExternalKey]*ExternalRecord
	folded    map[ExternalKey]bool
}

// NewQuery wraps a snapshot in a query view. It projects the external
// systems once, after any restriction, so the fold ranks on what the viewer
// may see.
func NewQuery(s *FleetSnapshot) *Query {
	q := &Query{base: s}
	q.snap, q.externals, q.folded = foldExternals(s)
	return q
}

// resolveService resolves a user-supplied identifier to exactly one service.
// An exact canonical key (qualified "domain/name" or a bare default-domain name)
// wins; otherwise a bare name that matches exactly one service across all domains
// resolves; a bare name matching services in multiple domains returns an
// [AmbiguousError] listing the qualified keys, so unrelated same-named services
// are never silently conflated.
func (q *Query) resolveService(identifier string) (*ServiceRecord, error) {
	if s := q.snap.Services[ServiceKey(identifier)]; s != nil {
		return s, nil
	}
	var matches []*ServiceRecord
	for _, s := range q.snap.Services {
		if s.Name == identifier {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return nil, &NotFoundError{Kind: "service", ID: identifier}
	case 1:
		return matches[0], nil
	default:
		keys := make([]string, len(matches))
		for i, m := range matches {
			keys[i] = string(m.Key)
		}
		sort.Strings(keys)
		return nil, &AmbiguousError{Kind: "service", ID: identifier, Matches: keys}
	}
}

// resolveServiceOrReplaced is resolveService that, when identifier names no
// service, follows a visible [Replacement] from the inferred service it names
// to the declared service that replaced it, and returns that replacement. Only
// the service, graph and entity-detail lookups follow it, each answer carrying
// replacedFrom; every other lookup stays exact.
func (q *Query) resolveServiceOrReplaced(identifier string) (*ServiceRecord, *Replacement, error) {
	s, err := q.resolveService(identifier)
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		return s, nil, err
	}
	for _, r := range q.snap.Replacements {
		if _, name := ParseServiceKey(r.From); string(r.From) == identifier || name == identifier {
			// Restrict keeps only replacements of visible services.
			r.At = copyTime(r.At)
			return q.snap.Services[r.To], &r, nil
		}
	}
	return nil, nil, err
}

// replacesOf is the bounded list of the inferred services key replaced, the
// newest first, how many there are and how many the view hides.
func (q *Query) replacesOf(key ServiceKey) ([]Replacement, int, int) {
	var out []Replacement
	for _, r := range q.snap.Replacements {
		if r.To == key {
			r.At = copyTime(r.At)
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Replacement) int {
		at := func(r Replacement) time.Time { return *cmp.Or(r.At, new(time.Time)) }
		return cmp.Or(at(b).Compare(at(a)), cmp.Compare(a.From, b.From))
	})
	shown, total, _ := boundSlice(out, MaxDetailPreview)
	return shown, total, q.snap.hiddenReplacements[key]
}

// Snapshot returns a deep copy of the snapshot for serialization. It never
// returns the internal snapshot pointer, so a caller cannot mutate shared state;
// the copy carries data only (its private query indexes are not rebuilt, so it
// is for reading/serializing, not for constructing a new Query).
func (q *Query) Snapshot() *FleetSnapshot { return jsonClone(q.base) }

// SnapshotForSerialization returns the internal snapshot pointer for a caller that
// does nothing but serialize it, or project it read-only, as pkg/backstage
// does. The snapshot is immutable once built -- the manager builds a
// candidate off to the side and swaps the pointer, never mutating a live
// one -- so a marshal needs no defensive copy, and on a fleet-sized snapshot
// Snapshot()'s copy is a full marshal-plus-unmarshal round trip performed on a
// value that is about to be marshalled again. Any caller that might RETAIN or
// mutate the result must use Snapshot() instead.
func (q *Query) SnapshotForSerialization() *FleetSnapshot { return q.base }

// SnapshotID returns the snapshot's content identity without copying.
func (q *Query) SnapshotID() string { return q.snap.SnapshotID }

// HasObserved reports whether the snapshot carries any observation-backed
// (Provenance == observed) relationship, so a host advertises the observed
// capability from real published data rather than a hardcoded flag.
func (q *Query) HasObserved() bool {
	for _, rel := range q.snap.Relationships {
		if rel.Provenance == ProvenanceObserved {
			return true
		}
	}
	return false
}

// Meta is the completeness envelope attached to every query answer. SnapshotID
// lets a caller prove that several answers came from the same system view.
type Meta struct {
	SchemaVersion string        `json:"schemaVersion"`
	SnapshotID    string        `json:"snapshotId"`
	AsOf          time.Time     `json:"asOf"`
	Completeness  Completeness  `json:"completeness"`
	Limitations   []Limitation  `json:"limitations,omitempty"`
	Sources       []SourceState `json:"sources,omitempty"`
	// LimitationsTruncated / SourcesTruncated report that the snapshot holds more
	// than the envelope carries. An envelope that silently drops entries would be
	// the one dishonest field in a completeness envelope.
	LimitationsTruncated bool `json:"limitationsTruncated,omitempty"`
	SourcesTruncated     bool `json:"sourcesTruncated,omitempty"`
	// ExternalCatalogs are the catalogs that classified external systems, with
	// their digest and how far their signature was checked.
	ExternalCatalogs []ExternalCatalogRef `json:"externalCatalogs,omitempty"`
	// Clusters and ClustersTruncated are ProductMeta's.
	Clusters          []ClusterSummary `json:"clusters,omitempty" doc:"The status of each cluster the answer knows, least healthy first when truncated. Absent when no cluster is known."`
	ClustersTruncated bool             `json:"clustersTruncated,omitempty" doc:"The snapshot knows more clusters than the answer carries."`
}

// meta applies the SAME hard caps as productMeta. Limitations are emitted per
// record in several build paths -- one per ambiguous revision link, one per
// non-canonical compliance value -- so an ordinary multi-source disagreement puts
// one limitation per target in the snapshot, and an unbounded envelope would then
// be most of every answer. These answers also go straight to an MCP tool result,
// where that is a context bomb rather than merely bytes.
func (q *Query) meta() Meta {
	srcs, srcTrunc := boundSources(cloneSources(q.snap.Sources))
	lims, limTrunc := BoundLimitations(cloneLimitations(q.snap.Limitations))
	clusters, clusterTrunc := clusterSummaries(q.snap)
	return Meta{
		SchemaVersion:        q.snap.SchemaVersion,
		SnapshotID:           q.snap.SnapshotID,
		AsOf:                 q.snap.GeneratedAt,
		Completeness:         q.snap.Completeness,
		Limitations:          lims,
		Sources:              srcs,
		LimitationsTruncated: limTrunc,
		SourcesTruncated:     srcTrunc,
		ExternalCatalogs:     slices.Clone(q.snap.ExternalCatalogs),
		Clusters:             clusters,
		ClustersTruncated:    clusterTrunc,
	}
}

// NotFoundError is returned when a requested identity is absent from the
// snapshot. Absence under partial completeness is NOT proof the thing does not
// exist — callers should consult Meta.Completeness.
type NotFoundError struct {
	Kind string
	ID   string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q not found in the fleet snapshot", e.Kind, e.ID)
}

// AmbiguousError is returned when an identity matches more than one record.
type AmbiguousError struct {
	Kind    string
	ID      string
	Matches []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s %q is ambiguous: matches %s", e.Kind, e.ID, strings.Join(e.Matches, ", "))
}

// TargetKeyAmbiguousError is returned when a target key does not name one
// target for certain: a 3-part key that names targets in more than one
// cluster, one that matches a single target while a source is incomplete, or a
// 4-part key whose only match has no known cluster. Candidates are the targets
// the caller may see, sorted by key, each labelled for a person to choose from;
// every candidate key resolves on its own.
type TargetKeyAmbiguousError struct {
	Key        string
	Candidates []EntityRef
	// Reason says why the key does not resolve, for a person; empty means the
	// key names targets in more than one cluster.
	Reason string
}

// Message is Reason, or the several-clusters reason when Reason is empty.
func (e *TargetKeyAmbiguousError) Message() string {
	return cmp.Or(e.Reason, "this target key names targets in more than one cluster; choose one")
}

func (e *TargetKeyAmbiguousError) Error() string {
	var b strings.Builder
	b.WriteString("ambiguous target key: " + e.Message() + "; candidates:")
	for _, c := range e.Candidates {
		b.WriteString("\n  " + c.Label + "   " + c.Key)
	}
	return b.String()
}

// ParseLabels reads key=value pairs into SearchFilter.Labels. A pair with no
// key or no =, or a key repeated with another value, is an InvalidQueryError:
// dropping it would widen the answer past what the caller asked for.
func ParseLabels(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, p := range pairs {
		k, val, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, &InvalidQueryError{Field: "label", Value: p, Reason: "want key=value"}
		}
		if prev, dup := out[k]; dup && prev != val {
			return nil, &InvalidQueryError{Field: "label", Value: p, Reason: fmt.Sprintf("key %q already filters on %q", k, prev)}
		}
		out[k] = val
	}
	return out, nil
}

// SearchFilter constrains a service search. The zero value matches everything
// (subject to the default result bound).
//
// Predicates fall into two groups. SERVICE-LEVEL predicates (Status, Source) and
// REVISION-EXISTENTIAL predicates (Text, Owner, Workload, HasCapability,
// HasDependency) match when ANY revision of the service qualifies — there is no
// hidden "representative"/latest revision. TARGET-CORRELATED predicates
// (Labels, Scope, Compliance, and — when a target predicate is present —
// ReadyOnly/NotReady) must all hold for the SAME target and its LINKED revision,
// so "production" and "not ready" can never come from two different targets.
type SearchFilter struct {
	Text           string            // substring over service name and any declared owner
	Owner          string            // any revision's owner matches this team, DRI or contact value
	BusinessDomain string            // the service's business domain, NoBusinessDomain for none or UnknownBusinessDomain
	Offering       string            // the service provides this platform offering
	Labels         map[string]string // all pairs must match one target's labels
	Scope          string            // a target of the service has this scope
	Status         string            // service aggregate status (canonical value)
	Compliance     string            // a target has this compliance (canonical value)
	Source         string            // service observed from this source
	Workload       string            // some revision declares this workload
	HasCapability  bool              // some revision declares a capability
	HasDependency  bool              // some revision declares a dependency
	ReadyOnly      bool              // operationally ready (correlated to target when a target predicate is set)
	NotReady       bool              // not operationally ready (correlated likewise)
	Template       string            // TemplateOnly keeps only golden paths, TemplateExclude leaves them out
	Declaration    string            // DeclarationInferred keeps only inferred services, DeclarationDeclared leaves them out
	Limit          int               // 0 → DefaultSearchLimit, capped at MaxSearchLimit
	Offset         int
}

// ServiceHit is a bounded search-result row for a logical service. Key is the
// domain-qualified identity; Name/Domain are the display split. A consumer keys
// rows, routes and follow-up lookups on Key so two same-named services in
// different domains stay distinct — never on Name alone.
type ServiceHit struct {
	Key            ServiceKey `json:"key"`
	Name           string     `json:"name"`
	Domain         string     `json:"domain,omitempty"`
	BusinessDomain string     `json:"businessDomain,omitempty"`
	// BusinessDomainUnknown: Pacto cannot tell the service's business domain.
	BusinessDomainUnknown bool `json:"businessDomainUnknown,omitempty"`
	// Offering is the platform offering the service provides and Summary its
	// one-line purpose.
	Offering      string            `json:"offering,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	Owner         contract.Owner    `json:"owner,omitzero"`
	Status        string            `json:"status,omitempty"`
	RevisionCount int               `json:"revisionCount"`
	TargetCount   int               `json:"targetCount"`
	Sources       []string          `json:"sources,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	// Template marks a golden path (see ServiceRecord.Template).
	Template bool `json:"template,omitempty"`
	// NameInferred is where Name comes from when no revision declares it.
	NameInferred *contract.Inferred `json:"nameInferred,omitempty"`
	// Declaration says whether a contract describes the service (see
	// ServiceRecord.Declaration).
	Declaration string `json:"declaration,omitempty" enum:"declared,inferred"`
}

// SearchResult is a bounded page of service hits.
type SearchResult struct {
	Meta     Meta         `json:"meta"`
	Total    int          `json:"total"`
	Count    int          `json:"count"`
	Services []ServiceHit `json:"services"`
	// Externals are, for a search on text alone (no other filter, first
	// page), at most MaxSearchExternals external systems whose identity,
	// label or alias holds the text; ExternalsTotal counts every match. An
	// external has no owner, status or compliance, so any other filter, or a
	// later page, lists none.
	Externals      []EntityRef `json:"externals,omitempty" doc:"For a search on text alone (no other filter, first page), the external systems whose identity, label or alias holds the text, bounded"`
	ExternalsTotal int         `json:"externalsTotal,omitempty" doc:"How many external systems match the text"`
}

// Search returns logical services matching the filter, deterministically sorted
// by name and bounded. It returns an [InvalidQueryError] for a malformed filter
// (unknown status/compliance, negative limit/offset) rather than silently
// defaulting.
func (q *Query) Search(f SearchFilter) (*SearchResult, error) {
	if err := validateSearchFilter(f); err != nil {
		return nil, err
	}
	var matched []*ServiceRecord
	for _, key := range q.sortedServiceKeys() {
		if s := q.snap.Services[key]; q.serviceMatches(s, f) {
			matched = append(matched, s)
		}
	}
	pg := Paginate(matched, f.Limit, f.Offset, DefaultSearchLimit, MaxSearchLimit)
	res := &SearchResult{Meta: q.meta(), Total: len(matched), Count: len(pg.Items), Services: make([]ServiceHit, 0, len(pg.Items))}
	for _, s := range pg.Items {
		res.Services = append(res.Services, hitFromService(s))
	}
	if textOnly(f) {
		res.Externals, res.ExternalsTotal = q.searchExternals(f.Text)
		if f.Text != "" {
			res.Meta = q.externalMeta() // the external systems matched may be incomplete
		}
	}
	return res, nil
}

// textOnly reports whether f filters on text alone, on the first page: the
// only search an external system can answer.
func textOnly(f SearchFilter) bool {
	rest := f
	rest.Text, rest.Limit = "", 0
	if len(rest.Labels) == 0 {
		rest.Labels = nil
	}
	return reflect.DeepEqual(rest, SearchFilter{})
}

// validateSearchFilter rejects malformed filter values with a typed error.
func validateSearchFilter(f SearchFilter) error {
	if err := validatePaging(f.Limit, f.Offset); err != nil {
		return err
	}
	return validateFields([]fieldCheck{
		{"status", f.Status, "not a canonical status", ValidStatus},
		{"compliance", f.Compliance, "not a canonical status", ValidStatus},
		{"template", f.Template, "not a template filter (only, exclude)", validTemplateFilter},
		{"declaration", f.Declaration, "not a declaration (declared, inferred)", validDeclarationFilter},
	})
}

// validatePaging rejects a negative offset or limit; zero limit means default.
func validatePaging(limit, offset int) error {
	if offset < 0 {
		return &InvalidQueryError{Field: "offset", Value: fmt.Sprint(offset), Reason: "must be >= 0"}
	}
	if limit < 0 {
		return &InvalidQueryError{Field: "limit", Value: fmt.Sprint(limit), Reason: "must be >= 0"}
	}
	return nil
}

// fieldCheck is one optional filter field: empty passes, otherwise ok decides.
type fieldCheck struct {
	field, value, reason string
	ok                   func(string) bool
}

// validateFields returns the first set field that ok rejects.
func validateFields(checks []fieldCheck) error {
	for _, c := range checks {
		if c.value != "" && !c.ok(c.value) {
			return &InvalidQueryError{Field: c.field, Value: c.value, Reason: c.reason}
		}
	}
	return nil
}

// sortedServiceKeys returns all logical-service keys in deterministic order, so
// search is stable and includes every domain (not just the default one).
func (q *Query) sortedServiceKeys() []ServiceKey {
	keys := make([]ServiceKey, 0, len(q.snap.Services))
	for k := range q.snap.Services {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func hitFromService(s *ServiceRecord) ServiceHit {
	return ServiceHit{
		Key: s.Key, Name: s.Name, Domain: s.Domain, BusinessDomain: s.BusinessDomain, BusinessDomainUnknown: s.BusinessDomainUnknown,
		Offering: s.Offering, Summary: s.Summary, Owner: cloneOwner(s.Owner), Status: s.Status,
		RevisionCount: len(s.Revisions), TargetCount: len(s.Targets),
		Sources: append([]string(nil), s.Sources...), Labels: cloneStringMap(s.Labels), Template: s.Template,
		NameInferred: clonePtr(s.NameInferred), Declaration: cmp.Or(s.Declaration, DeclarationDeclared),
	}
}

// cloneStringMap returns a copy of m (nil for a nil map) so a returned hit's
// labels cannot alias snapshot-owned state.
func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// serviceMatches evaluates the filter without ever selecting a single
// representative revision: service-level and revision-existential predicates are
// checked independently, and target-correlated predicates are checked against
// the same target and its linked revision.
func (q *Query) serviceMatches(s *ServiceRecord, f SearchFilter) bool {
	checks := []bool{
		q.matchText(s, f.Text),
		q.matchOwner(s, f.Owner),
		f.BusinessDomain == "" || q.serviceInBusinessDomain(string(s.Key), f.BusinessDomain),
		matchEq(f.Status, s.Status),
		matchEq(f.Offering, s.Offering),
		templateMatches(f.Template, s.Template),
		declarationMatches(f.Declaration, EntityRef{Declaration: s.Declaration}),
		matchSource(s, f.Source),
		q.anyRevisionWorkload(s, f.Workload),
		q.anyRevisionHasCapability(s, f.HasCapability),
		q.anyRevisionHasDependency(s, f.HasDependency),
		q.matchTargetCorrelated(s, f),
	}
	for _, ok := range checks {
		if !ok {
			return false
		}
	}
	return true
}

// matchTargetCorrelated enforces section 7.1: when a target predicate (label, scope or
// compliance) is present, the readiness predicate must hold for that SAME target
// and its linked revision. With no target predicate, readiness applies
// existentially across the service's revisions.
func (q *Query) matchTargetCorrelated(s *ServiceRecord, f SearchFilter) bool {
	hasTargetPredicate := len(f.Labels) > 0 || f.Scope != "" || f.Compliance != ""
	if !hasTargetPredicate {
		if !f.ReadyOnly && !f.NotReady {
			return true
		}
		return q.anyRevisionReadiness(s, f.ReadyOnly, f.NotReady)
	}
	for _, tk := range s.Targets {
		t := q.snap.Targets[tk]
		if !labelsMatch(t.Labels, f.Labels) {
			continue
		}
		if f.Scope != "" && t.Scope != f.Scope {
			continue
		}
		if f.Compliance != "" && t.Compliance != f.Compliance {
			continue
		}
		if (f.ReadyOnly || f.NotReady) && !readinessMatches(q.snap.Revisions[t.ContractRevision], f.ReadyOnly, f.NotReady) {
			continue
		}
		return true
	}
	return false
}

func (q *Query) matchText(s *ServiceRecord, text string) bool {
	if text == "" {
		return true
	}
	lower := strings.ToLower(text)
	return strings.Contains(strings.ToLower(s.Name), lower) || strings.Contains(strings.ToLower(s.Summary), lower) || q.matchOwner(s, text)
}

// matchOwner is free-text owner SEARCH over a service's declarations;
// matchOwnerKey is exact canonical owner IDENTITY. They share one walk
// ([Query.ownerClaims]) and differ only in the predicate, which is the whole point:
// with owners `team-a` and `team-a-platform` in one fleet the two questions have
// different answers, and every surface has to be explicit about which it is asking.
func (q *Query) matchOwner(s *ServiceRecord, owner string) bool {
	return owner == "" || q.ownerClaims(s, func(o contract.Owner) bool { return o.MatchesFilter(owner) })
}

func (q *Query) matchOwnerKey(s *ServiceRecord, key string) bool {
	return key == "" || q.ownerClaims(s, func(o contract.Owner) bool { return o.IsKey(key) })
}

// ownerClaims is THE ownership question, asked once and answered the same way by
// every surface that filters, aggregates or drills down by owner: a service is
// associated with an owner when one of the revisions of its CURRENT claims
// ([currentOwnerRevisions]: the newest version that declares an owner and the
// revisions its targets run now) declares one that satisfies pred. So owner=x
// lists exactly the services the ownership ranking counts under x, and a team
// that handed a service over no longer finds it under its name (its owner page
// still lists the revisions it declared). ServiceRecord.Owner is only the
// summary deriveOwner reads off the newest revision; asking it instead would
// hand a service two teams dispute now to one of them and hide it from the
// other. "Consistently owned by x" is this predicate plus OwnershipConsistent,
// never a narrower reading of the predicate itself.
func (q *Query) ownerClaims(s *ServiceRecord, pred func(contract.Owner) bool) bool {
	if s == nil {
		return false
	}
	revs, t := currentOwnerRevisions(q.snap, s)
	return slices.ContainsFunc(revs, func(r *ContractRevision) bool {
		o, _ := claimOwner(r, t)
		return pred(o)
	})
}

func matchEq(want, have string) bool { return want == "" || want == have }

func matchSource(s *ServiceRecord, source string) bool {
	return source == "" || containsStr(s.Sources, source)
}

func (q *Query) anyRevisionWorkload(s *ServiceRecord, workload string) bool {
	if workload == "" {
		return true
	}
	return q.anyRevision(s, func(r *ContractRevision) bool {
		return r.Contract != nil && r.Contract.Workload == workload
	})
}

func (q *Query) anyRevisionHasCapability(s *ServiceRecord, want bool) bool {
	if !want {
		return true
	}
	return q.anyRevision(s, func(r *ContractRevision) bool {
		return r.Contract != nil && len(r.Contract.Capabilities) > 0
	})
}

func (q *Query) anyRevisionHasDependency(s *ServiceRecord, want bool) bool {
	if !want {
		return true
	}
	return q.anyRevision(s, func(r *ContractRevision) bool {
		return r.Contract != nil && len(r.Contract.Dependencies) > 0
	})
}

func (q *Query) anyRevisionReadiness(s *ServiceRecord, readyOnly, notReady bool) bool {
	return q.anyRevision(s, func(r *ContractRevision) bool {
		return readinessMatches(r, readyOnly, notReady)
	})
}

// anyRevision reports whether any revision of the service satisfies pred.
func (q *Query) anyRevision(s *ServiceRecord, pred func(*ContractRevision) bool) bool {
	for _, rk := range s.Revisions {
		if pred(q.snap.Revisions[rk]) {
			return true
		}
	}
	return false
}

func readinessMatches(rev *ContractRevision, readyOnly, notReady bool) bool {
	return (!readyOnly || readinessPasses(rev)) && (!notReady || !readinessPasses(rev))
}

func labelsMatch(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
			return false
		}
	}
	return true
}

func readinessPasses(rev *ContractRevision) bool {
	return rev != nil && rev.Readiness != nil && rev.Readiness.Passing
}

// containsStr reports whether v is in s. Generic over comparable so it serves
// both bare-string membership and domain-qualified ServiceKey membership.
func containsStr[T comparable](s []T, v T) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// joinKeys renders a path of service keys into a single sortable string.
func joinKeys(ks []ServiceKey) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = string(k)
	}
	return strings.Join(parts, "\x00")
}

// RevisionCapabilities attributes tools and skills to the exact revision that
// declares them, so a logical-service answer never presents one revision's tools
// as universal.
type RevisionCapabilities struct {
	Revision RevisionKey   `json:"revision"`
	Version  string        `json:"version,omitempty"`
	Tools    []ToolSummary `json:"tools,omitempty"`
	Skills   []string      `json:"skills,omitempty"`
}

// ServiceView is the detailed answer for one logical service. Tools and skills
// are grouped per revision (never flattened to a single "representative").
type ServiceView struct {
	Meta         Meta                   `json:"meta"`
	Service      *ServiceRecord         `json:"service"`
	Revisions    []*ContractRevision    `json:"revisions"`
	Targets      []*TargetRecord        `json:"targets"`
	Dependencies []Relationship         `json:"dependencies"`
	Dependents   []ServiceKey           `json:"dependents"`
	Capabilities []RevisionCapabilities `json:"capabilities,omitempty"`
	// ReplacedFrom is set when the service was asked for by the key of an
	// inferred service it replaced.
	ReplacedFrom   *Replacement  `json:"replacedFrom,omitempty" doc:"Set when the service was asked for by the key of an inferred service it replaced: that replacement"`
	Replaces       []Replacement `json:"replaces,omitempty" doc:"The inferred services this service replaced, at most 200"`
	ReplacesTotal  int           `json:"replacesTotal,omitempty" doc:"How many inferred services this service replaced"`
	ReplacesHidden int           `json:"replacesHidden,omitempty" doc:"Replacements of this service whose workload you may not see"`
	// Fields says where each field of the current revision comes from; nil
	// unless it carries a declaration or the service replaced an inferred
	// service through one (fieldsOf).
	Fields *FieldsPreview `json:"fields,omitempty" doc:"Where each field of the current revision comes from: declared, accepted, attested or inferred"`
}

// GetService returns the logical service, its revisions and targets, its declared
// dependency edges (across all revisions), its dependents, and per-revision tools
// and skills. It returns a [NotFoundError] when the service is absent.
func (q *Query) GetService(name string) (*ServiceView, error) {
	s, replaced, err := q.resolveServiceOrReplaced(name)
	if err != nil {
		return nil, err
	}
	view := &ServiceView{Meta: q.meta(), Service: cloneService(s), Dependencies: []Relationship{}, ReplacedFrom: replaced}
	view.Replaces, view.ReplacesTotal, view.ReplacesHidden = q.replacesOf(s.Key)
	view.Fields = q.fieldsOf(s)
	for _, rk := range s.Revisions {
		rev := cloneRevision(q.snap.Revisions[rk])
		view.Revisions = append(view.Revisions, rev)
		if len(rev.Tools) > 0 || len(rev.Skills) > 0 {
			view.Capabilities = append(view.Capabilities, RevisionCapabilities{
				Revision: rev.Key, Version: rev.Version, Tools: rev.Tools, Skills: rev.Skills,
			})
		}
	}
	for _, tk := range s.Targets {
		view.Targets = append(view.Targets, cloneTarget(q.snap.Targets[tk]))
	}
	// A golden path's edges live in templateEdges, so its Dependencies still list
	// what it uses.
	for rel := range q.snap.declaredEdges() {
		// Declared dependency edges only, matching this field's documented meaning
		// (configuration references live in the contract's configurations);
		// observed edges live in the separate observed adjacency index (and the
		// graph's observed layer), so they never masquerade as declared intent here.
		if rel.FromService == s.Key && rel.Type == RelationshipDependency && rel.Provenance == ProvenanceDeclared {
			view.Dependencies = append(view.Dependencies, *rel)
		}
	}
	// Dependents includes configuration-reference consumers (see [ConsumesEdge]).
	view.Dependents = append([]ServiceKey(nil), q.snap.reverseDeps[s.Key]...)
	return view, nil
}

// TargetView is the detailed answer for one operational target.
type TargetView struct {
	Meta     Meta              `json:"meta"`
	Target   *TargetRecord     `json:"target"`
	Revision *ContractRevision `json:"revision,omitempty"`
}

// GetTarget returns a target by exact key, or (failing that) by unique name. It
// returns a [NotFoundError] when nothing matches and an [AmbiguousError] when a
// name matches more than one target. A key in the other cluster form resolves
// first (see resolveTargetKey): a 3-part key that names targets in several
// clusters returns a [TargetKeyAmbiguousError]. The answer always carries the
// canonical key.
func (q *Query) GetTarget(id string) (*TargetView, error) {
	if t, err := q.resolveTargetKey(id); t != nil || err != nil {
		if err != nil {
			return nil, err
		}
		return q.targetView(t), nil
	}
	if t := q.snap.Targets[TargetKey(id)]; t != nil {
		return q.targetView(t), nil
	}
	var matches []*TargetRecord
	for _, t := range q.snap.Targets {
		if t.Name == id {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 0:
		return nil, &NotFoundError{Kind: "target", ID: id}
	case 1:
		return q.targetView(matches[0]), nil
	default:
		keys := make([]string, len(matches))
		for i, m := range matches {
			keys[i] = string(m.Key)
		}
		sort.Strings(keys)
		return nil, &AmbiguousError{Kind: "target", ID: id, Matches: keys}
	}
}

// resolveTargetKey resolves a key whose cluster form differs from the
// snapshot's. In a snapshot keyed by cluster, a 3-part key gathers every
// target with that scope, kind and name, clustered or not (see
// resolveLegacyKey). In a snapshot that is not, a 4-part key resolves to the
// 3-part target in that cluster (see resolveForwardKey). It returns nil, nil
// when neither applies. It reads only q's snapshot, so a restricted viewer's
// candidates are targets it may see.
func (q *Query) resolveTargetKey(id string) (*TargetRecord, error) {
	cluster, scope, kind, name, ok := ParseTargetKey(TargetKey(id))
	switch {
	case !ok:
		return nil, nil
	case cluster == "" && q.snap.clusterKeyed:
		return q.resolveLegacyKey(id, scope, kind, name)
	case cluster != "" && !q.snap.clusterKeyed:
		return q.resolveForwardKey(id, cluster, scope, kind, name)
	}
	return nil, nil
}

// resolveLegacyKey resolves a 3-part key in a snapshot keyed by cluster: one
// match resolves while every source that reports targets is available;
// several matches, or one while such a source is incomplete, are a
// [TargetKeyAmbiguousError]. An incomplete source of another kind (a registry,
// telemetry) is the same one [targetPlacementKnown] ignores: it never holds a
// target, so a published 3-part href keeps resolving while it is down.
func (q *Query) resolveLegacyKey(id, scope, kind, name string) (*TargetRecord, error) {
	var matches []*TargetRecord
	for _, t := range q.snap.Targets {
		if t.Scope == scope && t.Kind == kind && t.Name == name {
			matches = append(matches, t)
		}
	}
	switch {
	case len(matches) > 1:
		return nil, targetKeyAmbiguous(id, "", matches)
	case len(matches) == 1 && !targetPlacementKnown(q.snap):
		// A target source that failed or listed only part of its records may
		// hold this name in another cluster, so one match is not proof of one.
		return nil, targetKeyAmbiguous(id, "this target key matches one target, but a source is incomplete and may hold the same target in another cluster; choose one", matches)
	case len(matches) == 1:
		return matches[0], nil
	}
	return nil, nil
}

// resolveForwardKey resolves a 4-part key in a snapshot that is not keyed by
// cluster to the 3-part target in that cluster, or to the clusterless one for
// [UnknownClusterKeyPart]. A clusterless target under a real cluster's key
// may or may not be in that cluster, so it is a [TargetKeyAmbiguousError]
// with that one candidate.
func (q *Query) resolveForwardKey(id, cluster, scope, kind, name string) (*TargetRecord, error) {
	t := q.snap.Targets[NewTargetKey(scope, kind, name)]
	switch {
	case t == nil:
		return nil, nil
	case t.Cluster == cluster, t.Cluster == "" && cluster == UnknownClusterKeyPart:
		return t, nil
	case t.Cluster == "":
		return nil, targetKeyAmbiguous(id, "the target with this name has no known cluster, so it may not be in the cluster this key names; choose it only if it is", []*TargetRecord{t})
	}
	return nil, nil
}

// sourcesIncomplete reports whether any source that counts against an answer
// is not fully available: an advisory source never does.
func (q *Query) sourcesIncomplete() bool {
	return slices.ContainsFunc(q.snap.Sources, SourceState.degraded)
}

// targetKeyAmbiguous lists matches as the candidates of key, sorted by key,
// with reason (see [TargetKeyAmbiguousError.Reason]). A clusterless candidate
// says its cluster is unknown.
func targetKeyAmbiguous(key, reason string, matches []*TargetRecord) *TargetKeyAmbiguousError {
	e := &TargetKeyAmbiguousError{Key: key, Reason: reason}
	for _, t := range matches {
		ref := targetEntityRef(t)
		if t.Cluster == "" {
			ref.Label += " (cluster unknown)"
		}
		e.Candidates = append(e.Candidates, ref)
	}
	sort.Slice(e.Candidates, func(a, b int) bool { return e.Candidates[a].Key < e.Candidates[b].Key })
	return e
}

func (q *Query) targetView(t *TargetRecord) *TargetView {
	view := &TargetView{Meta: q.meta(), Target: cloneTarget(t)}
	if t.ContractRevision != "" {
		view.Revision = cloneRevision(q.snap.Revisions[t.ContractRevision])
	}
	return view
}

// Direction selects dependency vs dependent traversal.
type Direction string

const (
	DirectionDependencies Direction = "dependencies"
	DirectionDependents   Direction = "dependents"
)

// InvalidQueryError is returned for a malformed query argument (a bad direction,
// negative depth or offset, an unknown status). Adapters map it to a 4xx / usage
// error rather than silently defaulting.
type InvalidQueryError struct {
	Field  string
	Value  string
	Reason string
}

func (e *InvalidQueryError) Error() string {
	return fmt.Sprintf("invalid %s %q: %s", e.Field, e.Value, e.Reason)
}

// GraphQuery configures a fleet graph traversal. The perspective is explicit: a
// Target uses that target's exact linked revision; a Revision uses that revision;
// a bare Service aggregates across all its revisions (Aggregated is set in the
// result) and never silently picks "latest".
type GraphQuery struct {
	Service    string
	Revision   RevisionKey
	Target     string
	Direction  Direction // defaults to dependencies
	Transitive bool
	MaxDepth   int // 0 → unlimited (still cycle-safe)
	// MaxNodes caps the answer. 0 takes MaxGraphNodes and a larger value is clamped
	// to it, so no caller — HTTP, MCP or CLI — can ask for an unbounded traversal.
	MaxNodes int
	// IncludeObserved also follows observed runtime calls, so the walk reaches
	// services no contract connects. It is opt-in: impact reads the dependents of
	// a graph as declared consumers and must not gain observed-only callers.
	IncludeObserved bool
}

// GraphNode is one reached node with its depth and the path taken to reach it.
// Key is the node's domain-qualified identity; Name is its unqualified display
// name; Path is the domain-qualified chain of keys from the root to this node.
type GraphNode struct {
	Key   ServiceKey   `json:"key"`
	Name  string       `json:"name"`
	Depth int          `json:"depth"`
	Path  []ServiceKey `json:"path"`
	// Provenance is how the edge that first reached the node is known: declared,
	// observed or declared+observed, or inferred for a consumer of an external
	// system known only from its configuration or a runtime call one of whose
	// ends no contract describes (never a gate's evidence).
	Provenance string `json:"provenance" enum:"declared,observed,declared+observed,inferred"`
	// NameInferred is where Name comes from when no revision declares it.
	NameInferred *contract.Inferred `json:"nameInferred,omitempty"`
}

// GraphResult is a deterministic, cycle-safe traversal answer. Aggregated is true
// when the query was service-scoped over more than one revision (so edges are a
// union across revisions rather than a single revision's exact graph).
type GraphResult struct {
	Meta Meta       `json:"meta"`
	Root ServiceKey `json:"root"`
	// External is the external system a graph rooted at one walks the
	// consumers of; Root is then empty.
	External   ExternalKey    `json:"external,omitempty" doc:"The external system a graph rooted at external:<identity> walks the consumers of; root is then empty"`
	Revision   RevisionKey    `json:"revision,omitempty"`
	Aggregated bool           `json:"aggregated"`
	Direction  Direction      `json:"direction"`
	Nodes      []GraphNode    `json:"nodes"`
	Edges      []Relationship `json:"edges"`
	Cycles     [][]ServiceKey `json:"cycles,omitempty"`
	Unresolved []Relationship `json:"unresolved,omitempty"`
	// Truncated reports that the traversal hit the node cap and stopped. The answer
	// is then a deterministic prefix of the reachable graph, not the whole of it,
	// and a consumer that reads it as complete would understate a blast radius.
	Truncated bool `json:"truncated,omitempty"`
	// ReplacedFrom is set when the root was asked for by the key of an
	// inferred service it replaced.
	ReplacedFrom *Replacement `json:"replacedFrom,omitempty" doc:"Set when the root was asked for by the key of an inferred service it replaced: that replacement"`
}

// Graph traverses dependencies or dependents from an explicit root. It returns
// every reached node (with depth and path), every traversed edge (typed, with
// revision provenance, required/compatibility and resolution), unresolved edges
// at every depth, and cycles. Direct-only (Transitive=false) returns immediate
// neighbors; transitive traversal is cycle-safe and honors MaxDepth.
//
// The walk is breadth-first, so a node's Depth is its SHORTEST distance from the
// root and its Path is a shortest path: a consumer that depends on the root
// directly is depth 1 even when it also reaches it through another dependent.
// Nodes are ordered by ascending depth; within a depth, by discovery order (the
// order of the parent that reached them first, then the sorted neighbor key).
// Path is the first shortest path in that order. The node cap keeps a prefix of
// this order, so a truncated answer holds the nearest nodes.
func (q *Query) Graph(gq GraphQuery) (*GraphResult, error) {
	dir, maxNodes, err := validateGraphQuery(gq)
	if err != nil {
		return nil, err
	}
	if key, ok := ParseExternalSubject(gq.Service); ok && gq.Target == "" && gq.Revision == "" {
		return q.graphExternal(gq, key, maxNodes)
	}
	rootSvc, scopeRev, aggregated, replaced, err := q.resolveGraphScope(gq)
	if err != nil {
		return nil, err
	}

	res := &GraphResult{Meta: q.meta(), Root: rootSvc, Revision: scopeRev, Aggregated: aggregated, Direction: dir, Nodes: []GraphNode{}, ReplacedFrom: replaced}
	nodeSet, cycles := q.walkGraph(res, rootSvc, gq, maxNodes, func(key ServiceKey) []graphStep {
		return q.graphNeighbours(key, rootSvc, scopeRev, dir, gq.IncludeObserved)
	})
	res.Cycles = cycles
	res.Edges, res.Unresolved = q.graphEdges(nodeSet, rootSvc, dir, scopeRev, gq.IncludeObserved)
	if s := q.snap.Services[rootSvc]; s != nil && s.Inferred() {
		limitationFirst(&res.Meta, Limitation{
			Code: LimitationServiceInferred, Source: s.Workload.Source,
			Message: "no contract Pacto read describes " + s.Name + ", so the graph knows none of its declared dependencies or dependents; with observed calls included it shows the calls sources saw, labelled inferred",
		})
	}
	return res, nil
}

// limitationFirst marks m partial and puts l first, so the cap keeps it.
func limitationFirst(m *Meta, l Limitation) {
	m.Completeness = CompletenessPartial
	var trunc bool
	m.Limitations, trunc = BoundLimitations(append([]Limitation{l}, m.Limitations...))
	m.LimitationsTruncated = m.LimitationsTruncated || trunc
}

// walkGraph is the breadth-first walk of [Query.Graph] from root over
// neighbors: it appends every reached node to res (marking it truncated at
// maxNodes) and returns the reached set and the cycles.
func (q *Query) walkGraph(res *GraphResult, root ServiceKey, gq GraphQuery, maxNodes int, neighbors func(ServiceKey) []graphStep) (map[ServiceKey]bool, [][]ServiceKey) {
	// expanded is the set of nodes whose neighbors the walk follows: the root, and
	// with Transitive every reached node above MaxDepth. The cycle pass below
	// follows exactly the same set.
	expanded := map[ServiceKey]bool{root: true}
	nodeSet := map[ServiceKey]bool{}
	queue := []GraphNode{{Key: root, Depth: 0, Path: []ServiceKey{root}}}
bfs:
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, step := range neighbors(cur.Key) {
			next := step.Key
			if next == root || nodeSet[next] {
				continue // already reached at a depth no greater than this one
			}
			// The cap is charged before the node is admitted. Neighbors are in sorted
			// key order (the dependency indexes are sorted at build), so the kept
			// prefix is deterministic.
			if len(res.Nodes) >= maxNodes {
				res.Truncated = true
				break bfs
			}
			nodeSet[next] = true
			_, name := ParseServiceKey(next)
			n := GraphNode{Key: next, Name: name, Depth: cur.Depth + 1, Path: append(append([]ServiceKey(nil), cur.Path...), next), Provenance: step.Provenance}
			if s := q.snap.Services[next]; s != nil {
				n.NameInferred = clonePtr(s.NameInferred)
			}
			res.Nodes = append(res.Nodes, n)
			if gq.Transitive && (gq.MaxDepth == 0 || n.Depth < gq.MaxDepth) {
				expanded[next] = true
				queue = append(queue, n)
			}
		}
	}

	cycles := graphCycles(root, func(key ServiceKey) []ServiceKey {
		steps := neighbors(key)
		keys := make([]ServiceKey, len(steps))
		for i, s := range steps {
			keys[i] = s.Key
		}
		return keys
	}, expanded)
	return nodeSet, cycles
}

// graphStep is one neighbour of a graph node and how the edge to it is known.
type graphStep struct {
	Key        ServiceKey
	Provenance string
}

// graphNeighbours returns a node's neighbours in sorted key order. Declared
// neighbours come from the dependency indexes; with observed set, observed
// runtime calls and inferred relationships (runtime calls with an end no
// contract describes, labelled inferred) are merged in. A revision- or target-rooted dependencies walk
// keeps its root declared-only: the root's declared edges are exact to that
// revision and runtime observation is service-level, so merging it would claim
// the revision makes calls no one saw it make. A dependents walk is
// service-level whatever the root, so it follows observed callers too.
func (q *Query) graphNeighbours(key, rootSvc ServiceKey, scopeRev RevisionKey, dir Direction, observed bool) []graphStep {
	var declared []ServiceKey
	switch {
	case dir == DirectionDependents:
		declared = q.snap.reverseDeps[key]
	case key == rootSvc && scopeRev != "":
		declared = q.snap.forwardDepsByRevision[scopeRev]
		observed = false
	default:
		declared = q.snap.forwardDeps[key]
	}
	prov := make(map[ServiceKey]string, len(declared))
	for _, k := range declared {
		prov[k] = ProvenanceDeclared
	}
	if observed {
		for _, k := range q.adjacent(key, dir, false, true, false) {
			if prov[k] == ProvenanceDeclared {
				prov[k] = ProvenanceDeclaredObserved
			} else {
				prov[k] = ProvenanceObserved
			}
		}
		// An inferred relationship has an end no contract describes, so it
		// never shares a pair with a declared or observed edge; were it to, the
		// stronger label wins.
		for _, k := range q.adjacent(key, dir, false, false, true) {
			if prov[k] == "" {
				prov[k] = ProvenanceInferred
			}
		}
	}
	steps := make([]graphStep, 0, len(prov))
	for k, p := range prov {
		steps = append(steps, graphStep{Key: k, Provenance: p})
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].Key < steps[j].Key })
	return steps
}

// graphCycles finds cycles with a depth-first walk over the expanded set: a
// breadth-first tree has no back edges to read cycles from (two siblings that
// call each other never return to a common ancestor). Each cycle is the walk's
// path from the root closed by the repeated key. It visits only nodes the
// traversal already admitted, so it is bounded by the same cap.
func graphCycles(root ServiceKey, neighbors func(ServiceKey) []ServiceKey, expanded map[ServiceKey]bool) [][]ServiceKey {
	var cycles [][]ServiceKey
	visited := map[ServiceKey]bool{}
	var walk func(key ServiceKey, onPath []ServiceKey)
	walk = func(key ServiceKey, onPath []ServiceKey) {
		for _, next := range neighbors(key) {
			if containsStr(onPath, next) {
				cycles = append(cycles, append(append([]ServiceKey(nil), onPath...), next))
				continue
			}
			if visited[next] || !expanded[next] {
				continue
			}
			visited[next] = true
			walk(next, append(append([]ServiceKey(nil), onPath...), next))
		}
	}
	walk(root, []ServiceKey{root})
	sort.Slice(cycles, func(i, j int) bool {
		return joinKeys(cycles[i]) < joinKeys(cycles[j])
	})
	return cycles
}

// validateGraphQuery checks the bounds a caller supplies and resolves the node
// cap actually applied: zero means "no preference", so it takes the ceiling, and
// a request above the ceiling is clamped rather than refused — a graph query is
// answerable at the ceiling, and the answer says it was truncated.
func validateGraphQuery(gq GraphQuery) (Direction, int, error) {
	dir, err := validateDirection(gq.Direction)
	if err != nil {
		return "", 0, err
	}
	if gq.MaxDepth < 0 {
		return "", 0, &InvalidQueryError{Field: "maxDepth", Value: fmt.Sprint(gq.MaxDepth), Reason: "must be >= 0"}
	}
	if gq.MaxNodes < 0 {
		return "", 0, &InvalidQueryError{Field: "maxNodes", Value: fmt.Sprint(gq.MaxNodes), Reason: "must be >= 0"}
	}
	maxNodes := gq.MaxNodes
	if maxNodes == 0 || maxNodes > MaxGraphNodes {
		maxNodes = MaxGraphNodes
	}
	return dir, maxNodes, nil
}

func validateDirection(d Direction) (Direction, error) {
	switch d {
	case "", DirectionDependencies:
		return DirectionDependencies, nil
	case DirectionDependents:
		return DirectionDependents, nil
	default:
		return "", &InvalidQueryError{Field: "direction", Value: string(d), Reason: "must be dependencies or dependents"}
	}
}

// resolveGraphScope determines the root service, the scoping revision (empty for
// an aggregated service query), whether the answer aggregates revisions and the
// replacement followed when the service was asked for by a replaced key.
func (q *Query) resolveGraphScope(gq GraphQuery) (rootKey ServiceKey, scopeRev RevisionKey, aggregated bool, replaced *Replacement, err error) {
	switch {
	case gq.Target != "":
		tv, e := q.GetTarget(gq.Target)
		if e != nil {
			return "", "", false, nil, e
		}
		return tv.Target.ServiceKey, tv.Target.ContractRevision, false, nil, nil
	case gq.Revision != "":
		rev := q.snap.Revisions[gq.Revision]
		if rev == nil {
			return "", "", false, nil, &NotFoundError{Kind: "revision", ID: string(gq.Revision)}
		}
		return rev.ServiceKey, gq.Revision, false, nil, nil
	default:
		s, replaced, err := q.resolveServiceOrReplaced(gq.Service)
		if err != nil {
			return "", "", false, nil, err
		}
		return s.Key, "", len(s.Revisions) > 1, replaced, nil
	}
}

// graphEdges collects the typed dependency and resolved configuration-reference
// edges spanned by the traversal (root + reached nodes) and, separately, the
// unresolved dependencies. Root edges honor the revision scope; downstream edges
// are service-level. An edge to an external system follows graphProvenance,
// the rule the external graph draws its consumers by.
func (q *Query) graphEdges(nodeSet map[ServiceKey]bool, rootSvc ServiceKey, dir Direction, scopeRev RevisionKey, includeObserved bool) (edges, unresolved []Relationship) {
	// Inferred relationships are runtime calls, so they come with the observed
	// ones only; impact's declared walk never sees them.
	defer func() {
		if !includeObserved {
			return
		}
		for _, rel := range q.snap.InferredRelationships {
			if edgeInScope(rel, nodeSet, rootSvc, dir, scopeRev) {
				edges = append(edges, rel)
			}
		}
	}()
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency && !ConsumesEdge(rel) {
			continue
		}
		if !edgeInScope(rel, nodeSet, rootSvc, dir, scopeRev) || (rel.ToExternal != "" && !graphProvenance(rel.Provenance, includeObserved)) {
			continue
		}
		edges = append(edges, rel)
		// A joined external is not a gap: no fleet service is expected.
		if !rel.Resolved && rel.ToExternal == "" {
			unresolved = append(unresolved, rel)
		}
	}
	return edges, unresolved
}

func edgeInScope(rel Relationship, nodeSet map[ServiceKey]bool, rootSvc ServiceKey, dir Direction, scopeRev RevisionKey) bool {
	if dir == DirectionDependents {
		return rel.ToService != "" && (rel.ToService == rootSvc || nodeSet[rel.ToService])
	}
	if rel.FromService == rootSvc {
		return scopeRev == "" || rel.FromRevision == scopeRev
	}
	return nodeSet[rel.FromService]
}

// StatusQuery selects which "needs attention" categories to report.
type StatusQuery struct {
	NeedsAttention   bool // shorthand: every category below
	NonCompliant     bool
	Unknown          bool
	Invalid          bool
	StaleEvidence    bool
	MissingReadiness bool
	UnresolvedDeps   bool
	// UnsatisfiedRanges reports declared dependencies whose provider is in the
	// fleet at a version outside the consumer's compatibility range.
	UnsatisfiedRanges bool
	// Lifecycle reports services that use a deprecated or past-sunset provider.
	Lifecycle bool
	// Unowned reports services no source knows an owner for.
	Unowned bool
	// Undeclared reports runtime calls no contract declares and callers with no
	// contract.
	Undeclared bool
	// GoldenPathDeployed reports running services that declare service.template.
	GoldenPathDeployed bool
	Limit              int
}

// StatusItem is one attention-worthy finding.
type StatusItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// StatusResult is a bounded, deterministically ordered list of attention items.
type StatusResult struct {
	Meta Meta `json:"meta"`
	// Total is how many items matched before the limit cut the list; Truncated
	// reports that Items holds fewer than Total.
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated,omitempty"`
	Items     []StatusItem `json:"items"`
	// Providers are at most MaxProviders rows, reported before inferred and
	// least healthy first.
	Providers          []Provider `json:"providers,omitempty" doc:"What Pacto detected in each source, with evidence, what it uses each for and its status"`
	ProvidersTruncated bool       `json:"providersTruncated,omitempty"`
}

// Status reports the entities needing attention in the selected categories. It is
// a PROJECTION of collectAttention, not a second implementation: this list and the
// product backlog answer the same question, and while each derived its own items
// they drifted: this one reported an unresolved dependency as a "relationship"
// named by an arrow-joined pseudo-edge, an entity no other endpoint can resolve,
// and it counted observed edges the backlog correctly excludes. One producer means
// a rule can only be changed in the place both readers see it.
func (q *Query) Status(sq StatusQuery) *StatusResult {
	want := map[string]bool{
		categoryNonCompliant: sq.NonCompliant,
		categoryUnknown:      sq.Unknown,
		categoryStale:        sq.StaleEvidence,
		categoryInvalid:      sq.Invalid,
		categoryReadiness:    sq.MissingReadiness,
		categoryUnresolved:   sq.UnresolvedDeps,
		categoryOutOfRange:   sq.UnsatisfiedRanges,
		categoryLifecycle:    sq.Lifecycle,
		categoryUndeclared:   sq.Undeclared,
		categoryUnowned:      sq.Unowned,
		categoryGoldenPath:   sq.GoldenPathDeployed,
		// No flag selects provisioning alone: NeedsAttention and the empty
		// query include it, so Status stays a projection of collectAttention.
		categoryProvisioning: false,
		// Nor does any flag select health or knowledge alone, for the same reason.
		categoryHealth:    false,
		categoryKnowledge: false,
		categoryCluster:   false,
	}
	// No category selected means every category: an empty query from the CLI, an
	// MCP client or the dashboard asks "what needs attention", never "nothing".
	none := true
	for _, on := range want {
		none = none && !on
	}
	for c := range want {
		want[c] = want[c] || sq.NeedsAttention || none
	}
	type ranked struct {
		sev int
		StatusItem
	}
	var all []ranked
	for _, it := range q.collectAttention() {
		if !want[it.Category] {
			continue
		}
		all = append(all, ranked{severityOrder[it.Severity], StatusItem{
			Kind: string(it.Entity.Kind), Name: it.Entity.Key, Code: it.Code, Reason: it.Reason,
		}})
	}

	// Most severe first, so a truncated list keeps its errors. Reason is the last
	// key because it is the only field that separates two unresolved dependencies
	// of the same service: they share a Kind, a Name and a Code, and sort.Slice is
	// not stable, so without it the truncated list would vary between runs on the
	// same snapshot.
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.sev != b.sev {
			return a.sev < b.sev
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Reason < b.Reason
	})
	items := make([]StatusItem, len(all))
	for i, r := range all {
		items[i] = r.StatusItem
	}
	limit := sq.Limit
	if limit <= 0 {
		limit = DefaultStatusLimit
	}
	total := len(items)
	if total > limit {
		items = items[:limit]
	}
	providers, providersTruncated := boundProviders(q.snap.Providers)
	return &StatusResult{Meta: q.meta(), Total: total, Truncated: total > limit, Items: items, Providers: providers, ProvidersTruncated: providersTruncated}
}

// AssertionRef identifies the declared assertion a reason is about.
type AssertionRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Reason is one deterministic, structured explanation for a state. A client (or
// agent) turns these into prose; Pacto embeds no LLM.
type Reason struct {
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	Assertion  *AssertionRef `json:"assertion,omitempty"`
	Source     string        `json:"source,omitempty"`
	ObservedAt *time.Time    `json:"observedAt,omitempty"`
}

// ExplainResult is the deterministic explanation of a subject's state.
type ExplainResult struct {
	Meta    Meta     `json:"meta"`
	Subject string   `json:"subject"`
	Kind    string   `json:"kind"`
	Status  string   `json:"status"`
	Reasons []Reason `json:"reasons"`
	// Fields and ReplacesHidden are set for a service, as in [ServiceView].
	Fields         *FieldsPreview `json:"fields,omitempty" doc:"Where each field of the service's current revision comes from"`
	ReplacesHidden int            `json:"replacesHidden,omitempty" doc:"Replacements of this service whose workload you may not see"`
}

// ReasonReplacesInferred is the reason a service gives for each inferred
// service it replaced.
const ReasonReplacesInferred = "REPLACES_INFERRED"

// Explain produces deterministic reasons for a subject's state. A subject
// with the external: prefix is an external system; any other is resolved as a
// service name first, then a target key/name. It never asserts absence or
// compliance beyond the observed evidence.
func (q *Query) Explain(subject string) (*ExplainResult, error) {
	if _, ok := ParseExternalSubject(subject); ok {
		return q.explainExternal(subject)
	}
	s, serr := q.resolveService(subject)
	if serr == nil {
		return q.explainService(s), nil
	}
	if _, amb := serr.(*AmbiguousError); amb {
		return nil, serr
	}
	tv, err := q.GetTarget(subject)
	if err != nil {
		if _, ok := err.(*NotFoundError); ok {
			return nil, &NotFoundError{Kind: "subject", ID: subject}
		}
		return nil, err
	}
	return q.explainTarget(tv.Target), nil
}

func (q *Query) explainService(s *ServiceRecord) *ExplainResult {
	// The canonical ServiceKey, matching explainTarget's TargetKey: ExplainResult
	// carries no domain field, so a bare name would name a DIFFERENT service (with a
	// different status) in any fleet holding two domains' worth of the same name.
	res := &ExplainResult{Meta: q.meta(), Subject: string(s.Key), Kind: "service", Status: s.Status, Reasons: []Reason{}, Fields: q.fieldsOf(s)}
	var replaces []Replacement
	replaces, _, res.ReplacesHidden = q.replacesOf(s.Key)
	for _, r := range replaces {
		res.Reasons = append(res.Reasons, Reason{Code: ReasonReplacesInferred, Message: r.Line(q.snap.GeneratedAt), Source: cmp.Or(string(r.Revision), string(r.Target))})
	}
	if s.Inferred() {
		res.Reasons = append(res.Reasons, Reason{
			Code: LimitationServiceInferred, Source: s.Workload.Source, ObservedAt: copyTime(s.Workload.ObservedAt),
			Message: "no contract Pacto read describes this workload; it never satisfies a dependency or clears a gate",
		})
		for _, l := range inferredLimitations(s) {
			res.Reasons = append(res.Reasons, Reason{Code: l.Code, Message: l.Message, Source: l.Source})
		}
	}
	for _, tk := range s.Targets {
		res.Reasons = append(res.Reasons, targetReasons(q.snap.Targets[tk])...)
	}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		// Dependency edges only: the reasons below judge resolution and ranges.
		if rel.FromService != s.Key || rel.Type != RelationshipDependency {
			continue
		}
		switch {
		case rel.missing():
			res.Reasons = append(res.Reasons, Reason{
				Code: LimitationUnresolvedDep, Message: "declared dependency is not resolved in the fleet",
				Assertion: &AssertionRef{Kind: "dependency", Name: rel.To},
			})
		case rel.RangeStatus == RangeUnsatisfied:
			res.Reasons = append(res.Reasons, Reason{
				Code:      LimitationRangeUnsatisfied,
				Message:   "the provider version in the fleet is outside the declared range " + rel.Compatibility,
				Assertion: &AssertionRef{Kind: "dependency", Name: rel.To},
			})
		}
	}
	sortReasons(res.Reasons)
	return res
}

func (q *Query) explainTarget(t *TargetRecord) *ExplainResult {
	res := &ExplainResult{Meta: q.meta(), Subject: string(t.Key), Kind: "target", Status: t.Compliance, Reasons: targetReasons(t)}
	sortReasons(res.Reasons)
	return res
}

// targetReasons derives structured reasons from a target's findings, evidence
// presence, and freshness.
func targetReasons(t *TargetRecord) []Reason {
	var out []Reason
	for _, f := range t.Findings {
		r := Reason{Code: string(f.Code), Message: f.Message, Source: t.Source,
			Assertion: &AssertionRef{Kind: f.Subject.Kind, Name: f.Subject.Name}}
		out = append(out, r)
	}
	if t.EvidenceAt == nil && t.Compliance == StatusUnknown {
		out = append(out, Reason{Code: LimitationEvidenceMissing, Message: "no evidence has been observed for this target", Source: t.Source})
	}
	if t.Stale {
		out = append(out, Reason{Code: LimitationSourceStale, Message: "the most recent evidence is older than the freshness window", Source: t.Source, ObservedAt: copyTime(t.EvidenceAt)})
	}
	return out
}

// copyTime returns a fresh pointer to a copy of t so a returned reason never
// aliases snapshot-owned time state.
func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func sortReasons(rs []Reason) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Code != rs[j].Code {
			return rs[i].Code < rs[j].Code
		}
		return rs[i].Message < rs[j].Message
	})
}
