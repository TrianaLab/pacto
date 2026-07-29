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

// Snapshot returns the underlying immutable snapshot.
func (q *Query) Snapshot() *FleetSnapshot { return q.snap }

// Meta is the completeness envelope attached to every query answer.
type Meta struct {
	AsOf         time.Time     `json:"asOf"`
	Completeness Completeness  `json:"completeness"`
	Limitations  []Limitation  `json:"limitations,omitempty"`
	Sources      []SourceState `json:"sources,omitempty"`
}

func (q *Query) meta() Meta {
	return Meta{
		AsOf:         q.snap.GeneratedAt,
		Completeness: q.snap.Completeness,
		Limitations:  q.snap.Limitations,
		Sources:      q.snap.Sources,
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
type SearchFilter struct {
	Text          string            // substring over name and owner
	Owner         string            // matches team, DRI, or a contact value
	Labels        map[string]string // all pairs must match a service label
	Status        string            // service aggregate status
	Compliance    string            // at least one target has this compliance
	Source        string            // service observed from this source
	Workload      string            // representative revision workload
	HasCapability bool              // representative revision declares a capability
	HasDependency bool              // representative revision declares a dependency
	ReadyOnly     bool              // representative revision readiness passes
	NotReady      bool              // representative revision readiness missing or failing
	Limit         int               // 0 → DefaultSearchLimit, capped at MaxSearchLimit
	Offset        int
}

// ServiceHit is a bounded search-result row for a logical service.
type ServiceHit struct {
	Name          string            `json:"name"`
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
// by name and bounded.
func (q *Query) Search(f SearchFilter) *SearchResult {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	names := q.sortedServiceNames()

	res := &SearchResult{Meta: q.meta(), Services: []ServiceHit{}}
	for _, name := range names {
		s := q.snap.Services[NewServiceKey(name)]
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
	return res
}

func (q *Query) sortedServiceNames() []string {
	names := make([]string, 0, len(q.snap.Services))
	for _, s := range q.snap.Services {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

func hitFromService(s *ServiceRecord) ServiceHit {
	return ServiceHit{
		Name: s.Name, Owner: s.Owner.DisplayString(), Status: s.Status,
		RevisionCount: len(s.Revisions), TargetCount: len(s.Targets),
		Sources: s.Sources, Labels: s.Labels,
	}
}

// serviceMatches evaluates every filter predicate. Each predicate is a small,
// independently testable helper that folds in its own empty-filter short-circuit,
// so this function stays a single loop with low cyclomatic complexity.
func (q *Query) serviceMatches(s *ServiceRecord, f SearchFilter) bool {
	rev := representativeRevision(q.snap, s)
	checks := []bool{
		matchText(s, f.Text),
		matchOwner(s, f.Owner),
		labelsMatch(s.Labels, f.Labels),
		matchEq(f.Status, s.Status),
		q.matchCompliance(s, f.Compliance),
		matchSource(s, f.Source),
		matchWorkload(rev, f.Workload),
		matchHasCapability(rev, f.HasCapability),
		matchHasDependency(rev, f.HasDependency),
		matchReadiness(rev, f.ReadyOnly, f.NotReady),
	}
	for _, ok := range checks {
		if !ok {
			return false
		}
	}
	return true
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

func (q *Query) matchCompliance(s *ServiceRecord, compliance string) bool {
	return compliance == "" || q.anyTargetCompliance(s, compliance)
}

func matchWorkload(rev *ContractRevision, workload string) bool {
	return workload == "" || (rev != nil && rev.Contract != nil && rev.Contract.Workload == workload)
}

func matchHasCapability(rev *ContractRevision, want bool) bool {
	return !want || (rev != nil && rev.Contract != nil && len(rev.Contract.Capabilities) > 0)
}

func matchHasDependency(rev *ContractRevision, want bool) bool {
	return !want || (rev != nil && rev.Contract != nil && len(rev.Contract.Dependencies) > 0)
}

func matchReadiness(rev *ContractRevision, readyOnly, notReady bool) bool {
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

func (q *Query) anyTargetCompliance(s *ServiceRecord, compliance string) bool {
	for _, tk := range s.Targets {
		if q.snap.Targets[tk].Compliance == compliance {
			return true
		}
	}
	return false
}

func readinessPasses(rev *ContractRevision) bool {
	return rev != nil && rev.Readiness != nil && rev.Readiness.Passing
}

func containsStr(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}

// ServiceView is the detailed answer for one logical service.
type ServiceView struct {
	Meta         Meta                `json:"meta"`
	Service      *ServiceRecord      `json:"service"`
	Revisions    []*ContractRevision `json:"revisions"`
	Targets      []*TargetRecord     `json:"targets"`
	Dependencies []Relationship      `json:"dependencies"`
	Dependents   []string            `json:"dependents"`
	Tools        []ToolSummary       `json:"tools,omitempty"`
	Skills       []string            `json:"skills,omitempty"`
}

// GetService returns the logical service, its revisions and targets, its
// declared dependency edges, its dependents, and its representative revision's
// tools and skills. It returns a [NotFoundError] when the service is absent.
func (q *Query) GetService(name string) (*ServiceView, error) {
	s := q.snap.Services[NewServiceKey(name)]
	if s == nil {
		return nil, &NotFoundError{Kind: "service", ID: name}
	}
	view := &ServiceView{Meta: q.meta(), Service: s, Dependencies: []Relationship{}}
	for _, rk := range s.Revisions {
		view.Revisions = append(view.Revisions, q.snap.Revisions[rk])
	}
	for _, tk := range s.Targets {
		view.Targets = append(view.Targets, q.snap.Targets[tk])
	}
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.From == name && rel.Type == "dependency" {
			view.Dependencies = append(view.Dependencies, rel)
		}
	}
	view.Dependents = append([]string(nil), q.snap.reverseDeps[name]...)
	if rev := representativeRevision(q.snap, s); rev != nil {
		view.Tools = rev.Tools
		view.Skills = rev.Skills
	}
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
	view := &TargetView{Meta: q.meta(), Target: t}
	if t.ContractRevision != "" {
		view.Revision = q.snap.Revisions[t.ContractRevision]
	}
	return view
}

// Direction selects dependency vs dependent traversal.
type Direction string

const (
	DirectionDependencies Direction = "dependencies"
	DirectionDependents   Direction = "dependents"
)

// GraphQuery configures a fleet graph traversal.
type GraphQuery struct {
	Service    string
	Direction  Direction // defaults to dependencies
	Transitive bool
	MaxDepth   int // 0 → unlimited (still cycle-safe)
}

// GraphNode is one reached node with its depth and the path taken to reach it.
type GraphNode struct {
	Name  string   `json:"name"`
	Depth int      `json:"depth"`
	Path  []string `json:"path"`
}

// GraphResult is a deterministic, cycle-safe traversal answer.
type GraphResult struct {
	Meta       Meta           `json:"meta"`
	Root       string         `json:"root"`
	Direction  Direction      `json:"direction"`
	Nodes      []GraphNode    `json:"nodes"`
	Edges      []Relationship `json:"edges,omitempty"`
	Cycles     [][]string     `json:"cycles,omitempty"`
	Unresolved []string       `json:"unresolved,omitempty"`
}

// Graph traverses dependencies or dependents from a root service. Direct-only
// (Transitive=false) returns the immediate neighbors; transitive traversal is
// cycle-safe and honors MaxDepth. Neighbors are visited in deterministic order.
func (q *Query) Graph(gq GraphQuery) (*GraphResult, error) {
	if q.snap.Services[NewServiceKey(gq.Service)] == nil {
		return nil, &NotFoundError{Kind: "service", ID: gq.Service}
	}
	dir := gq.Direction
	if dir == "" {
		dir = DirectionDependencies
	}
	adj := q.snap.forwardDeps
	if dir == DirectionDependents {
		adj = q.snap.reverseDeps
	}

	res := &GraphResult{Meta: q.meta(), Root: gq.Service, Direction: dir, Nodes: []GraphNode{}}
	visited := map[string]bool{}
	var cycles [][]string
	var walk func(name string, depth int, path, onPath []string)
	walk = func(name string, depth int, path, onPath []string) {
		for _, next := range adj[name] {
			if containsStr(onPath, next) {
				// Back-edge to a node on the current path: a cycle. Recorded, not
				// traversed, so recursion is always bounded.
				cycles = append(cycles, append(append([]string(nil), onPath...), next))
				continue
			}
			if visited[next] {
				continue // already reached and expanded; keeps traversal O(V+E)
			}
			visited[next] = true
			nextPath := append(append([]string(nil), path...), next)
			res.Nodes = append(res.Nodes, GraphNode{Name: next, Depth: depth, Path: nextPath})
			if gq.Transitive && (gq.MaxDepth == 0 || depth < gq.MaxDepth) {
				walk(next, depth+1, nextPath, append(append([]string(nil), onPath...), next))
			}
		}
	}
	walk(gq.Service, 1, []string{gq.Service}, []string{gq.Service})

	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], "\x00") < strings.Join(cycles[j], "\x00")
	})
	res.Cycles = cycles
	if dir == DirectionDependencies {
		res.Unresolved = q.unresolvedDeps(gq.Service)
	}
	res.Edges = q.edgesFor(gq.Service, dir)
	return res, nil
}

// unresolvedDeps lists the root service's declared dependencies that no service
// in the fleet resolves.
func (q *Query) unresolvedDeps(name string) []string {
	var out []string
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if rel.From == name && rel.Type == "dependency" && !rel.Resolved {
			out = append(out, rel.To)
		}
	}
	return out
}

func (q *Query) edgesFor(name string, dir Direction) []Relationship {
	var out []Relationship
	for i := range q.snap.Relationships {
		rel := q.snap.Relationships[i]
		if dir == DirectionDependencies && rel.From == name {
			out = append(out, rel)
		}
		if dir == DirectionDependents && rel.ResolvedService == name {
			out = append(out, rel)
		}
	}
	return out
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
		if (all || sq.Invalid) && rev.bundle != nil && rev.bundle.RawYAML != nil && !rev.Valid {
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
		if rel.Type == "dependency" && !rel.Resolved {
			items = append(items, StatusItem{Kind: "relationship", Name: rel.From + "→" + rel.To, Code: "UNRESOLVED_DEPENDENCY", Reason: "declared dependency is not resolved in the fleet"})
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
	if s := q.snap.Services[NewServiceKey(subject)]; s != nil {
		return q.explainService(s), nil
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
		if rel.From == s.Name && rel.Type == "dependency" && !rel.Resolved {
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
		out = append(out, Reason{Code: LimitationSourceStale, Message: "the most recent evidence is older than the freshness window", Source: t.Source, ObservedAt: t.EvidenceAt})
	}
	return out
}

func sortReasons(rs []Reason) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Code != rs[j].Code {
			return rs[i].Code < rs[j].Code
		}
		return rs[i].Message < rs[j].Message
	})
}
