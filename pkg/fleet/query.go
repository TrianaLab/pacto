package fleet

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Result-bounding defaults. Queries are bounded so an agent or UI can never be
// handed an unbounded response.
const (
	DefaultSearchLimit = 100
	MaxSearchLimit     = 500
	DefaultStatusLimit = 200
)

// Query is a pure, network-free view over an immutable [FleetSnapshot]. It never
// performs I/O and never mutates the snapshot, so a single snapshot can serve
// concurrent queries. Every answer carries a [Meta] with the snapshot's as-of
// time, completeness, and limitations.
type Query struct {
	snap *FleetSnapshot
}

// NewQuery wraps a snapshot in a query view.
func NewQuery(s *FleetSnapshot) *Query { return &Query{snap: s} }

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

// Snapshot returns a deep copy of the snapshot for serialization. It never
// returns the internal snapshot pointer, so a caller cannot mutate shared state;
// the copy carries data only (its private query indexes are not rebuilt, so it
// is for reading/serializing, not for constructing a new Query).
func (q *Query) Snapshot() *FleetSnapshot { return jsonClone(q.snap) }

// SnapshotID returns the snapshot's content identity without copying.
func (q *Query) SnapshotID() string { return q.snap.SnapshotID }

// Meta is the completeness envelope attached to every query answer. SnapshotID
// lets a caller prove that several answers came from the same system view.
type Meta struct {
	SchemaVersion string        `json:"schemaVersion"`
	SnapshotID    string        `json:"snapshotId"`
	AsOf          time.Time     `json:"asOf"`
	Completeness  Completeness  `json:"completeness"`
	Limitations   []Limitation  `json:"limitations,omitempty"`
	Sources       []SourceState `json:"sources,omitempty"`
}

func (q *Query) meta() Meta {
	return Meta{
		SchemaVersion: q.snap.SchemaVersion,
		SnapshotID:    q.snap.SnapshotID,
		AsOf:          q.snap.GeneratedAt,
		Completeness:  q.snap.Completeness,
		Limitations:   q.snap.Limitations,
		Sources:       q.snap.Sources,
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

// SearchFilter constrains a service search. The zero value matches everything
// (subject to the default result bound).
//
// Predicates fall into two groups. SERVICE-LEVEL predicates (Text, Owner,
// Status, Source) and REVISION-EXISTENTIAL predicates (Workload, HasCapability,
// HasDependency) match when ANY revision of the service qualifies — there is no
// hidden "representative"/latest revision. TARGET-CORRELATED predicates
// (Labels, Scope, Compliance, and — when a target predicate is present —
// ReadyOnly/NotReady) must all hold for the SAME target and its LINKED revision,
// so "production" and "not ready" can never come from two different targets.
type SearchFilter struct {
	Text          string            // substring over service name and owner
	Owner         string            // matches team, DRI, or a contact value
	Labels        map[string]string // all pairs must match one target's labels
	Scope         string            // a target of the service has this scope
	Status        string            // service aggregate status (canonical value)
	Compliance    string            // a target has this compliance (canonical value)
	Source        string            // service observed from this source
	Workload      string            // some revision declares this workload
	HasCapability bool              // some revision declares a capability
	HasDependency bool              // some revision declares a dependency
	ReadyOnly     bool              // operationally ready (correlated to target when a target predicate is set)
	NotReady      bool              // not operationally ready (correlated likewise)
	Limit         int               // 0 → DefaultSearchLimit, capped at MaxSearchLimit
	Offset        int
}

// ServiceHit is a bounded search-result row for a logical service. Key is the
// domain-qualified identity; Name/Domain are the display split. A consumer keys
// rows, routes and follow-up lookups on Key so two same-named services in
// different domains stay distinct — never on Name alone.
type ServiceHit struct {
	Key           ServiceKey        `json:"key"`
	Name          string            `json:"name"`
	Domain        string            `json:"domain,omitempty"`
	Owner         string            `json:"owner,omitempty"`
	Status        string            `json:"status,omitempty"`
	RevisionCount int               `json:"revisionCount"`
	TargetCount   int               `json:"targetCount"`
	Sources       []string          `json:"sources,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// SearchResult is a bounded page of service hits.
type SearchResult struct {
	Meta     Meta         `json:"meta"`
	Total    int          `json:"total"`
	Count    int          `json:"count"`
	Services []ServiceHit `json:"services"`
}

// Search returns logical services matching the filter, deterministically sorted
// by name and bounded. It returns an [InvalidQueryError] for a malformed filter
// (unknown status/compliance, negative limit/offset) rather than silently
// defaulting.
func (q *Query) Search(f SearchFilter) (*SearchResult, error) {
	if err := validateSearchFilter(f); err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	keys := q.sortedServiceKeys()

	res := &SearchResult{Meta: q.meta(), Services: []ServiceHit{}}
	for _, key := range keys {
		s := q.snap.Services[key]
		if !q.serviceMatches(s, f) {
			continue
		}
		res.Total++
		if res.Total <= f.Offset || len(res.Services) >= limit {
			continue
		}
		res.Services = append(res.Services, hitFromService(s))
	}
	res.Count = len(res.Services)
	return res, nil
}

// validateSearchFilter rejects malformed filter values with a typed error.
func validateSearchFilter(f SearchFilter) error {
	if f.Offset < 0 {
		return &InvalidQueryError{Field: "offset", Value: fmt.Sprint(f.Offset), Reason: "must be >= 0"}
	}
	if f.Limit < 0 {
		return &InvalidQueryError{Field: "limit", Value: fmt.Sprint(f.Limit), Reason: "must be >= 0"}
	}
	if f.Status != "" && !ValidStatus(f.Status) {
		return &InvalidQueryError{Field: "status", Value: f.Status, Reason: "not a canonical status"}
	}
	if f.Compliance != "" && !ValidStatus(f.Compliance) {
		return &InvalidQueryError{Field: "compliance", Value: f.Compliance, Reason: "not a canonical status"}
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
		Key: s.Key, Name: s.Name, Domain: s.Domain, Owner: s.Owner.DisplayString(), Status: s.Status,
		RevisionCount: len(s.Revisions), TargetCount: len(s.Targets),
		Sources: append([]string(nil), s.Sources...), Labels: cloneStringMap(s.Labels),
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
		matchText(s, f.Text),
		matchOwner(s, f.Owner),
		matchEq(f.Status, s.Status),
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

func matchText(s *ServiceRecord, text string) bool {
	if text == "" {
		return true
	}
	text = strings.ToLower(text)
	return strings.Contains(strings.ToLower(s.Name), text) || s.Owner.MatchesFilter(text)
}

func matchOwner(s *ServiceRecord, owner string) bool {
	return owner == "" || s.Owner.MatchesFilter(owner)
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
}

// GetService returns the logical service, its revisions and targets, its declared
// dependency edges (across all revisions), its dependents, and per-revision tools
// and skills. It returns a [NotFoundError] when the service is absent.
func (q *Query) GetService(name string) (*ServiceView, error) {
	s, err := q.resolveService(name)
	if err != nil {
		return nil, err
	}
	view := &ServiceView{Meta: q.meta(), Service: cloneService(s), Dependencies: []Relationship{}}
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
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.FromService == s.Key && rel.Type == RelationshipDependency {
			view.Dependencies = append(view.Dependencies, rel)
		}
	}
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
// name matches more than one target.
func (q *Query) GetTarget(id string) (*TargetView, error) {
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
}

// GraphNode is one reached node with its depth and the path taken to reach it.
// Key is the node's domain-qualified identity; Name is its unqualified display
// name; Path is the domain-qualified chain of keys from the root to this node.
type GraphNode struct {
	Key   ServiceKey   `json:"key"`
	Name  string       `json:"name"`
	Depth int          `json:"depth"`
	Path  []ServiceKey `json:"path"`
}

// GraphResult is a deterministic, cycle-safe traversal answer. Aggregated is true
// when the query was service-scoped over more than one revision (so edges are a
// union across revisions rather than a single revision's exact graph).
type GraphResult struct {
	Meta       Meta           `json:"meta"`
	Root       ServiceKey     `json:"root"`
	Revision   RevisionKey    `json:"revision,omitempty"`
	Aggregated bool           `json:"aggregated"`
	Direction  Direction      `json:"direction"`
	Nodes      []GraphNode    `json:"nodes"`
	Edges      []Relationship `json:"edges"`
	Cycles     [][]ServiceKey `json:"cycles,omitempty"`
	Unresolved []Relationship `json:"unresolved,omitempty"`
}

// Graph traverses dependencies or dependents from an explicit root. It returns
// every reached node (with depth and path), every traversed edge (typed, with
// revision provenance, required/compatibility and resolution), unresolved edges
// at every depth, and cycles. Direct-only (Transitive=false) returns immediate
// neighbors; transitive traversal is cycle-safe and honors MaxDepth.
func (q *Query) Graph(gq GraphQuery) (*GraphResult, error) {
	dir, err := validateDirection(gq.Direction)
	if err != nil {
		return nil, err
	}
	if gq.MaxDepth < 0 {
		return nil, &InvalidQueryError{Field: "maxDepth", Value: fmt.Sprint(gq.MaxDepth), Reason: "must be >= 0"}
	}
	rootSvc, scopeRev, aggregated, err := q.resolveGraphScope(gq)
	if err != nil {
		return nil, err
	}

	res := &GraphResult{Meta: q.meta(), Root: rootSvc, Revision: scopeRev, Aggregated: aggregated, Direction: dir, Nodes: []GraphNode{}}
	neighbors := func(key ServiceKey, isRoot bool) []ServiceKey {
		if dir == DirectionDependents {
			return q.snap.reverseDeps[key]
		}
		if isRoot && scopeRev != "" {
			return q.snap.forwardDepsByRevision[scopeRev]
		}
		return q.snap.forwardDeps[key]
	}

	visited := map[ServiceKey]bool{}
	nodeSet := map[ServiceKey]bool{}
	var cycles [][]ServiceKey
	var walk func(key ServiceKey, depth int, path, onPath []ServiceKey, isRoot bool)
	walk = func(key ServiceKey, depth int, path, onPath []ServiceKey, isRoot bool) {
		for _, next := range neighbors(key, isRoot) {
			if containsStr(onPath, next) {
				cycles = append(cycles, append(append([]ServiceKey(nil), onPath...), next))
				continue
			}
			if visited[next] {
				continue // already reached and expanded; keeps traversal O(V+E)
			}
			visited[next] = true
			nodeSet[next] = true
			nextPath := append(append([]ServiceKey(nil), path...), next)
			_, name := ParseServiceKey(next)
			res.Nodes = append(res.Nodes, GraphNode{Key: next, Name: name, Depth: depth, Path: nextPath})
			if gq.Transitive && (gq.MaxDepth == 0 || depth < gq.MaxDepth) {
				walk(next, depth+1, nextPath, append(append([]ServiceKey(nil), onPath...), next), false)
			}
		}
	}
	walk(rootSvc, 1, []ServiceKey{rootSvc}, []ServiceKey{rootSvc}, true)

	sort.Slice(cycles, func(i, j int) bool {
		return joinKeys(cycles[i]) < joinKeys(cycles[j])
	})
	res.Cycles = cycles
	res.Edges, res.Unresolved = q.graphEdges(nodeSet, rootSvc, dir, scopeRev)
	return res, nil
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
// an aggregated service query) and whether the answer aggregates revisions.
func (q *Query) resolveGraphScope(gq GraphQuery) (rootKey ServiceKey, scopeRev RevisionKey, aggregated bool, err error) {
	switch {
	case gq.Target != "":
		tv, e := q.GetTarget(gq.Target)
		if e != nil {
			return "", "", false, e
		}
		return tv.Target.ServiceKey, tv.Target.ContractRevision, false, nil
	case gq.Revision != "":
		rev := q.snap.Revisions[gq.Revision]
		if rev == nil {
			return "", "", false, &NotFoundError{Kind: "revision", ID: string(gq.Revision)}
		}
		return rev.ServiceKey, gq.Revision, false, nil
	default:
		s, err := q.resolveService(gq.Service)
		if err != nil {
			return "", "", false, err
		}
		return s.Key, "", len(s.Revisions) > 1, nil
	}
}

// graphEdges collects the typed dependency edges spanned by the traversal (root +
// reached nodes) and, separately, the unresolved ones. Root edges honor the
// revision scope; downstream edges are service-level.
func (q *Query) graphEdges(nodeSet map[ServiceKey]bool, rootSvc ServiceKey, dir Direction, scopeRev RevisionKey) (edges, unresolved []Relationship) {
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type != RelationshipDependency {
			continue
		}
		if !edgeInScope(rel, nodeSet, rootSvc, dir, scopeRev) {
			continue
		}
		edges = append(edges, rel)
		if !rel.Resolved {
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
	Limit            int
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
	Meta  Meta         `json:"meta"`
	Items []StatusItem `json:"items"`
}

// Status reports services and targets needing attention across the selected
// categories. Per-category collection is delegated to small helpers.
func (q *Query) Status(sq StatusQuery) *StatusResult {
	all := sq.NeedsAttention
	var items []StatusItem
	items = append(items, q.targetStatusItems(all, sq)...)
	items = append(items, q.revisionStatusItems(all, sq)...)
	items = append(items, q.unresolvedStatusItems(all, sq)...)

	sort.Slice(items, func(i, j int) bool {
		if items[i].Code != items[j].Code {
			return items[i].Code < items[j].Code
		}
		return items[i].Name < items[j].Name
	})
	limit := sq.Limit
	if limit <= 0 {
		limit = DefaultStatusLimit
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return &StatusResult{Meta: q.meta(), Items: items}
}

func (q *Query) targetStatusItems(all bool, sq StatusQuery) []StatusItem {
	var items []StatusItem
	for _, t := range q.snap.Targets {
		if (all || sq.NonCompliant) && t.Compliance == StatusNonCompliant {
			items = append(items, StatusItem{Kind: "target", Name: string(t.Key), Code: "NON_COMPLIANT", Reason: "target has confirmed drift"})
		}
		if (all || sq.Unknown) && t.Compliance == StatusUnknown {
			items = append(items, StatusItem{Kind: "target", Name: string(t.Key), Code: "UNKNOWN", Reason: "target compliance is unknown (insufficient evidence)"})
		}
		if (all || sq.StaleEvidence) && t.Stale {
			items = append(items, StatusItem{Kind: "target", Name: string(t.Key), Code: "STALE_EVIDENCE", Reason: "target evidence is older than the freshness window"})
		}
	}
	return items
}

func (q *Query) revisionStatusItems(all bool, sq StatusQuery) []StatusItem {
	var items []StatusItem
	for _, rev := range q.snap.Revisions {
		if (all || sq.Invalid) && rev.validated && !rev.Valid {
			items = append(items, StatusItem{Kind: "revision", Name: string(rev.Key), Code: "INVALID_CONTRACT", Reason: "contract is structurally invalid"})
		}
		if (all || sq.MissingReadiness) && rev.Readiness == nil {
			items = append(items, StatusItem{Kind: "revision", Name: string(rev.Key), Code: "MISSING_READINESS", Reason: "revision declares no readiness assessment"})
		}
	}
	return items
}

func (q *Query) unresolvedStatusItems(all bool, sq StatusQuery) []StatusItem {
	if !all && !sq.UnresolvedDeps {
		return nil
	}
	var items []StatusItem
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.Type == RelationshipDependency && !rel.Resolved {
			items = append(items, StatusItem{Kind: "relationship", Name: string(rel.FromService) + "→" + rel.To, Code: "UNRESOLVED_DEPENDENCY", Reason: "declared dependency is not resolved in the fleet"})
		}
	}
	return items
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
}

// Explain produces deterministic reasons for a subject's state. The subject is
// resolved as a service name first, then a target key/name. It never asserts
// absence or compliance beyond the observed evidence.
func (q *Query) Explain(subject string) (*ExplainResult, error) {
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
	res := &ExplainResult{Meta: q.meta(), Subject: s.Name, Kind: "service", Status: s.Status, Reasons: []Reason{}}
	for _, tk := range s.Targets {
		res.Reasons = append(res.Reasons, targetReasons(q.snap.Targets[tk])...)
	}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.FromService == s.Key && rel.Type == RelationshipDependency && !rel.Resolved {
			res.Reasons = append(res.Reasons, Reason{
				Code: LimitationUnresolvedDep, Message: "declared dependency is not resolved in the fleet",
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
