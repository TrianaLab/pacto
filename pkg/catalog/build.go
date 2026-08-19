package catalog

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Build rejects a request it cannot answer at all, rather than answering it
// emptily. Everything it CAN answer partially, it answers partially.
var (
	// ErrNoRoots means no root was supplied. The catalog is bounded by an
	// explicit root set; it never infers one and never scans a registry for one.
	ErrNoRoots = errors.New("catalog: at least one root is required")
	// ErrNoResolver means no resolver was supplied.
	ErrNoResolver = errors.New("catalog: a resolver is required")
)

// Request is one catalog construction.
type Request struct {
	// Roots are the requested root references, in caller order. The slice is
	// copied, so mutating it afterwards cannot reach the catalog.
	Roots []string
	// Resolver turns a reference into an immutable revision.
	Resolver Resolver
	// Bounds is the work budget. Zero fields take their defaults.
	Bounds Bounds
	// Clock is injected so generation time is a parameter rather than a hidden
	// read of the wall clock. It defaults to time.Now.
	Clock func() time.Time
}

// Build resolves every root and walks their dependency closure exactly once,
// then freezes the result. After it returns, the catalog performs no I/O: a tag
// that moves in a registry does not move the catalog.
func Build(ctx context.Context, req Request) (*Catalog, error) {
	if req.Resolver == nil {
		return nil, ErrNoResolver
	}
	roots := slices.Clone(req.Roots)
	if len(roots) == 0 {
		return nil, ErrNoRoots
	}
	clock := req.Clock
	if clock == nil {
		clock = time.Now
	}
	b := &builder{
		resolver:       req.Resolver,
		bounds:         req.Bounds.effective(),
		memo:           map[memoKey]memoEntry{},
		revs:           map[ContentID]*revState{},
		edges:          map[edgeKey]Edge{},
		cycles:         map[string]Cycle{},
		unresolvedSeen: map[unresolvedKey]bool{},
		limitSeen:      map[limitKey]bool{},
	}
	b.walk(ctx, roots)
	return b.finish(clock(), len(roots)), nil
}

// memoKey is what "resolve once" is keyed on: everything that can change the
// answer, and nothing that cannot. A relative reference means different things
// from different declaring bases, and a reference naming no version resolves
// differently under different constraints, so both qualify it. The same triple
// is never resolved twice, so a mutable tag is read once.
type memoKey struct{ Base, Ref, Constraint string }

// projection is everything the catalog keeps from one resolution. The contract
// pointer and any filesystem view are deliberately dropped here.
type projection struct {
	content      ContentID
	service      ServiceID
	version      string
	pactoVersion string
	owner        contract.Owner
	resolvedRef  string
	base         string
	deps         []contract.Dependency
}

type memoEntry struct {
	rev    projection
	reason Reason
	ok     bool
}

// arrival is one route reaching one reference. The walk is over routes, not
// over revisions, because a diamond's two branches are two facts about the
// revision at the bottom, and only [Bounds.MaxPaths] decides how many are kept.
type arrival struct {
	ref        string
	base       string
	root       RootID
	name       string
	constraint string
	required   bool
	steps      []DeclarationID
	chain      []ContentID
}

type edgeKey struct {
	decl DeclarationID
	to   ContentID
}

type unresolvedKey struct {
	decl DeclarationID
	ref  string
}

type limitKey struct{ code, ref string }

// revState accumulates one revision while the walk is still running.
type revState struct {
	rev     Revision
	reqRefs map[string]bool
	resRefs map[string]bool
	roots   map[RootID]bool
}

type builder struct {
	resolver Resolver
	bounds   Bounds

	memo           map[memoKey]memoEntry
	revs           map[ContentID]*revState
	revOrder       []ContentID
	edges          map[edgeKey]Edge
	cycles         map[string]Cycle
	roots          []Root
	unresolved     []Unresolved
	unresolvedSeen map[unresolvedKey]bool
	limits         []Limitation
	limitSeen      map[limitKey]bool
	limitOverflow  bool
}

func (b *builder) walk(ctx context.Context, roots []string) {
	if len(roots) > b.bounds.MaxRoots {
		b.limit(LimitationRootLimit, "", "more roots were requested than the root bound allows; the surplus was never resolved")
		roots = roots[:b.bounds.MaxRoots]
	}
	queue := make([]arrival, 0, len(roots))
	for i, ref := range roots {
		b.roots = append(b.roots, Root{ID: RootID(i), RequestedRef: ref})
		queue = append(queue, arrival{ref: ref, root: RootID(i)})
	}
	// Breadth first, so the first arrival at a revision is its shortest route and
	// its rank is settled without depending on which root came first.
	for len(queue) > 0 {
		if ctx.Err() != nil {
			b.limit(LimitationCancelled, "", "construction ended before the closure was fully walked")
			return
		}
		a := queue[0]
		queue = append(queue[1:], b.step(ctx, a)...)
	}
}

func (b *builder) step(ctx context.Context, a arrival) []arrival {
	entry, resolved := b.resolveArrival(ctx, a)
	if !resolved {
		return nil
	}
	if !entry.ok {
		b.recordFailure(a, entry.reason)
		return nil
	}
	p := entry.rev
	if len(a.steps) > 0 {
		b.recordEdge(a, p.content)
	}
	// A revision already on this route closes a loop. The edge above is kept, so
	// the cycle stays visible in the graph; the walk stops instead of following
	// it, so it terminates.
	if slices.Contains(a.chain, p.content) {
		b.recordCycle(a.chain, p.content)
		return nil
	}
	if !b.retain(a, p) {
		return nil
	}
	return b.expand(a, p)
}

// resolveArrival answers what a reference resolves to, calling the resolver at
// most once per (base, reference). The second return is false when a bound
// refused the work; the caller records that as unresolved-because-bounded.
func (b *builder) resolveArrival(ctx context.Context, a arrival) (memoEntry, bool) {
	if a.ref == "" {
		return memoEntry{reason: Reason{Code: ReasonInvalidReference, Message: "the reference is empty"}}, true
	}
	key := memoKey{Base: a.base, Ref: a.ref, Constraint: a.constraint}
	if e, hit := b.memo[key]; hit {
		return e, true // already paid for; a repeat costs no work and no network
	}
	if reason, ok := b.mayResolve(a); !ok {
		b.recordFailure(a, reason)
		return memoEntry{}, false
	}
	e := b.callResolver(ctx, a)
	b.memo[key] = e
	return e, true
}

// mayResolve applies the bounds that must stop work BEFORE it happens. Refusing
// here is what makes the bounds real: the resolver is never called, so nothing
// is fetched, and permanent tests prove it by counting calls.
func (b *builder) mayResolve(a arrival) (Reason, bool) {
	if len(b.revs) >= b.bounds.MaxRevisions {
		b.limit(LimitationRevisionLimit, a.ref, "the revision bound was reached; this reference was not resolved")
		return Reason{Code: ReasonBoundExceeded, Message: "the revision bound stopped this resolution"}, false
	}
	if len(a.steps) > 0 && len(b.edges) >= b.bounds.MaxEdges {
		b.limit(LimitationEdgeLimit, a.ref, "the edge bound was reached; this dependency was not resolved")
		return Reason{Code: ReasonBoundExceeded, Message: "the edge bound stopped this resolution"}, false
	}
	return Reason{}, true
}

func (b *builder) callResolver(ctx context.Context, a arrival) memoEntry {
	res, err := b.resolver.Resolve(ctx, ResolveRequest{Ref: a.ref, Base: a.base, Constraint: a.constraint})
	if err != nil {
		return memoEntry{reason: reasonFrom(err)}
	}
	p, reason := project(res)
	if reason.Code != "" {
		return memoEntry{reason: reason}
	}
	return memoEntry{rev: p, ok: true}
}

// project validates what the resolver returned and keeps only durable values.
// A resolution without a usable contract, or without a real content identity,
// is a failure: a name, a version or a tag is never accepted as identity.
func project(res Resolution) (projection, Reason) {
	if res.Contract == nil || res.Contract.Service.Name == "" {
		return projection{}, Reason{Code: ReasonInvalidContract, Message: "the resolver returned no usable contract"}
	}
	if _, err := NewContentID(res.Content.Scheme, res.Content.Digest); err != nil {
		return projection{}, Reason{Code: ReasonInvalidIdentity, Message: "the resolver returned no immutable content identity"}
	}
	owner := res.Contract.Service.Owner
	owner.Contacts = slices.Clone(owner.Contacts)
	return projection{
		content:      res.Content,
		service:      ServiceID{Domain: res.Domain, Name: res.Contract.Service.Name},
		version:      res.Contract.Service.Version,
		pactoVersion: res.Contract.PactoVersion,
		owner:        owner,
		resolvedRef:  res.ResolvedRef,
		base:         res.Base,
		deps:         slices.Clone(res.Contract.Dependencies),
	}, Reason{}
}

// recordFailure files a gap where it belongs: a root keeps its own reason, a
// dependency becomes an Unresolved entry. Neither is ever dropped.
func (b *builder) recordFailure(a arrival, r Reason) {
	bounded := r.Code == ReasonBoundExceeded
	if len(a.steps) == 0 {
		b.roots[a.root].Reason = r
		if !bounded { // the bound already named itself
			b.limit(LimitationRootUnresolved, a.ref, "the requested root did not resolve")
		}
		return
	}
	k := unresolvedKey{decl: a.steps[len(a.steps)-1], ref: a.ref}
	if b.unresolvedSeen[k] {
		return
	}
	b.unresolvedSeen[k] = true
	if len(b.unresolved) >= b.bounds.MaxUnresolved {
		b.limit(LimitationUnresolvedLimit, "", "the unresolved-dependency bound was reached; further failures are not listed")
		return
	}
	b.unresolved = append(b.unresolved, Unresolved{
		Declaration: k.decl, Name: a.name, Ref: a.ref,
		Constraint: a.constraint, Required: a.required, Reason: r,
	})
	if !bounded {
		b.limit(LimitationUnresolvedDep, a.ref, "a declared dependency did not resolve")
	}
}

func (b *builder) recordEdge(a arrival, to ContentID) {
	k := edgeKey{decl: a.steps[len(a.steps)-1], to: to}
	if _, dup := b.edges[k]; dup {
		return
	}
	if len(b.edges) >= b.bounds.MaxEdges {
		b.limit(LimitationEdgeLimit, a.ref, "the edge bound was reached; this dependency edge was not recorded")
		return
	}
	b.edges[k] = Edge{
		Declaration: k.decl, Name: a.name, Ref: a.ref,
		Constraint: a.constraint, Required: a.required, To: to,
	}
}

func (b *builder) recordCycle(chain []ContentID, target ContentID) {
	loop := slices.Clone(chain[slices.Index(chain, target):])
	// Rotate to the smallest identity so the same loop found from two entry
	// points is one cycle.
	lowest := 0
	for i := range loop {
		if compareContentID(loop[i], loop[lowest]) < 0 {
			lowest = i
		}
	}
	loop = append(loop[lowest:], loop[:lowest]...)
	key := ""
	for _, c := range loop {
		// Both halves of String are constrained (closed enum, validated digest),
		// so this key holds no user text and cannot be forged by a name.
		key += c.String() + "|"
	}
	if _, dup := b.cycles[key]; dup {
		return
	}
	b.cycles[key] = Cycle{Contents: loop}
}

// retain records one arrival against its revision and reports whether the walk
// should continue through it.
func (b *builder) retain(a arrival, p projection) bool {
	depth := len(a.steps)
	st, ok := b.revs[p.content]
	if !ok {
		st = &revState{
			rev: Revision{
				Content: p.content, Service: p.service, Version: p.version,
				PactoVersion: p.pactoVersion, Owner: p.owner, MinDepth: depth,
			},
			reqRefs: map[string]bool{}, resRefs: map[string]bool{}, roots: map[RootID]bool{},
		}
		b.revs[p.content] = st
		b.revOrder = append(b.revOrder, p.content)
	}
	// Provenance that does not depend on which route arrived first: every
	// reference that reached this content, and every root that can reach it.
	st.reqRefs[a.ref] = true
	if p.resolvedRef != "" {
		st.resRefs[p.resolvedRef] = true
	}
	st.roots[a.root] = true
	st.rev.MinDepth = min(st.rev.MinDepth, depth)
	if depth == 0 {
		b.roots[a.root].Resolved = true
		b.roots[a.root].Content = p.content
		b.roots[a.root].ResolvedRef = p.resolvedRef
	}
	if len(st.rev.Paths) >= b.bounds.MaxPaths {
		st.rev.PathsTruncated = true
		b.limit(LimitationPathLimit, a.ref, "the retained-path bound was reached for this revision; further routes were not walked")
		return false
	}
	st.rev.Paths = append(st.rev.Paths, Path{Root: a.root, Steps: slices.Clone(a.steps)})
	return true
}

func (b *builder) expand(a arrival, p projection) []arrival {
	if len(p.deps) == 0 {
		return nil
	}
	next := len(a.steps) + 1
	if next > b.bounds.MaxDepth {
		b.limit(LimitationDepthLimit, a.ref, "the depth bound stopped the walk; the deeper closure was not resolved")
		return nil
	}
	if next > b.bounds.MaxPathLength {
		b.limit(LimitationPathLengthLimit, a.ref, "the path-length bound stopped the walk; longer routes were not resolved")
		return nil
	}
	chain := append(slices.Clone(a.chain), p.content)
	out := make([]arrival, 0, len(p.deps))
	for i, d := range p.deps {
		out = append(out, arrival{
			ref: d.Ref, base: p.base, root: a.root,
			name: d.Name, constraint: d.Compatibility, required: d.Required,
			steps: append(slices.Clone(a.steps), DeclarationID{From: p.content, Index: i}),
			chain: chain,
		})
	}
	return out
}

func (b *builder) limit(code, ref, msg string) {
	k := limitKey{code: code, ref: ref}
	if b.limitSeen[k] {
		return
	}
	b.limitSeen[k] = true
	if len(b.limits) >= b.bounds.MaxLimitations {
		b.limitOverflow = true
		return
	}
	b.limits = append(b.limits, Limitation{Code: code, Ref: ref, Message: msg})
}

func (b *builder) finish(now time.Time, requested int) *Catalog {
	c := &Catalog{
		roots:      b.roots,
		revisions:  b.buildRevisions(),
		edges:      b.buildEdges(),
		unresolved: b.unresolved,
		cycles:     b.buildCycles(),
	}
	slices.SortFunc(c.unresolved, compareUnresolved)
	c.byContent = make(map[ContentID]int, len(c.revisions))
	for i, r := range c.revisions {
		c.byContent[r.Content] = i
	}
	c.conflicts = b.detectConflicts(c.revisions, c.edges)

	limits := b.finalLimitations()
	completeness := CompletenessComplete
	if len(limits) > 0 {
		// Anything that produced a limitation -- an unresolved root, an unresolved
		// dependency, a bound that stopped work -- is missing knowledge, not
		// absent knowledge. Partial, never empty and never complete.
		completeness = CompletenessPartial
	}
	c.meta = Meta{
		SchemaVersion:  SchemaVersion,
		GeneratedAt:    now,
		Completeness:   completeness,
		Bounds:         b.bounds,
		RequestedRoots: requested,
		Limitations:    limits,
	}
	c.meta.CatalogID = fingerprint(c)
	return c
}

func (b *builder) buildRevisions() []Revision {
	out := make([]Revision, 0, len(b.revOrder))
	for _, id := range b.revOrder {
		st := b.revs[id]
		r := st.rev
		r.RequestedRefs = sortedStrings(st.reqRefs)
		r.ResolvedRefs = sortedStrings(st.resRefs)
		r.Roots = sortedRoots(st.roots)
		r.Rank = rankForDepth(r.MinDepth)
		slices.SortFunc(r.Paths, comparePath)
		out = append(out, r)
	}
	slices.SortFunc(out, func(x, y Revision) int { return compareContentID(x.Content, y.Content) })
	return out
}

func (b *builder) buildEdges() []Edge {
	out := make([]Edge, 0, len(b.edges))
	for _, e := range b.edges {
		out = append(out, e)
	}
	slices.SortFunc(out, compareEdge)
	return out
}

func (b *builder) buildCycles() []Cycle {
	out := make([]Cycle, 0, len(b.cycles))
	for _, cy := range b.cycles {
		out = append(out, cy)
	}
	slices.SortFunc(out, func(x, y Cycle) int { return slices.CompareFunc(x.Contents, y.Contents, compareContentID) })
	return out
}

func (b *builder) finalLimitations() []Limitation {
	if b.limitOverflow {
		b.limits = append(b.limits[:b.bounds.MaxLimitations-1], Limitation{
			Code:    LimitationLimitationLimit,
			Message: "the limitation bound was reached; further distinct limitations are not listed",
		})
	}
	slices.SortFunc(b.limits, compareLimitation)
	return b.limits
}

// detectConflicts reports disagreement without resolving it. Three shapes are
// possible and each is a different question, so none is folded into another.
func (b *builder) detectConflicts(revs []Revision, edges []Edge) []Conflict {
	byService := map[ServiceID]map[string]bool{}
	type serviceVersion struct {
		service ServiceID
		version string
	}
	byVersion := map[serviceVersion]map[ContentID]bool{}
	for _, r := range revs {
		addTo(byService, r.Service, r.Version)
		addTo(byVersion, serviceVersion{r.Service, r.Version}, r.Content)
	}
	byDecl := map[DeclarationID]map[ContentID]bool{}
	for _, e := range edges {
		addTo(byDecl, e.Declaration, e.To)
	}

	var out []Conflict
	for svc, versions := range byService {
		if len(versions) > 1 {
			out = append(out, Conflict{Kind: ConflictVersion, Service: svc, Versions: sortedStrings(versions)})
		}
	}
	for sv, contents := range byVersion {
		if len(contents) > 1 {
			out = append(out, Conflict{Kind: ConflictContent, Service: sv.service, Version: sv.version, Contents: sortedContents(contents)})
		}
	}
	for decl, contents := range byDecl {
		if len(contents) > 1 {
			out = append(out, Conflict{Kind: ConflictDeclaration, Declaration: decl, Contents: sortedContents(contents)})
		}
	}
	slices.SortFunc(out, compareConflict)
	if len(out) > b.bounds.MaxConflicts {
		out = out[:b.bounds.MaxConflicts]
		b.limit(LimitationConflictLimit, "", "the conflict bound was reached; further conflicts are not listed")
	}
	return out
}

func addTo[K comparable, V comparable](m map[K]map[V]bool, k K, v V) {
	if m[k] == nil {
		m[k] = map[V]bool{}
	}
	m[k][v] = true
}

func sortedStrings(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func sortedContents(set map[ContentID]bool) []ContentID {
	out := make([]ContentID, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	slices.SortFunc(out, compareContentID)
	return out
}

func sortedRoots(set map[RootID]bool) []RootID {
	out := make([]RootID, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}

func compareEdge(a, b Edge) int {
	if v := compareDeclaration(a.Declaration, b.Declaration); v != 0 {
		return v
	}
	return compareContentID(a.To, b.To)
}

func compareUnresolved(a, b Unresolved) int {
	if v := compareDeclaration(a.Declaration, b.Declaration); v != 0 {
		return v
	}
	if v := cmp.Compare(a.Ref, b.Ref); v != 0 {
		return v
	}
	return cmp.Compare(a.Name, b.Name)
}

func compareLimitation(a, b Limitation) int {
	if v := cmp.Compare(a.Code, b.Code); v != 0 {
		return v
	}
	return cmp.Compare(a.Ref, b.Ref)
}

func compareConflict(a, b Conflict) int {
	if v := cmp.Compare(a.Kind, b.Kind); v != 0 {
		return v
	}
	if v := compareServiceID(a.Service, b.Service); v != 0 {
		return v
	}
	if v := cmp.Compare(a.Version, b.Version); v != 0 {
		return v
	}
	return compareDeclaration(a.Declaration, b.Declaration)
}

// itoa keeps numeric fingerprint fields textual and length-prefixed like every
// other field, so no numeric field can ever run into its neighbour.
func itoa(n int) string { return strconv.Itoa(n) }
