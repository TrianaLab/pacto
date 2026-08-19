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
		revs:           map[RevisionID]*revState{},
		edges:          map[edgeKey]Edge{},
		edgeWork:       map[edgeWork]bool{},
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

// id is the revision this resolution IS: its service and its content. Content
// alone would fold two mirrors of one bundle into a single revision and let
// whichever root arrived first decide whose service it claimed to be.
func (p projection) id() RevisionID { return RevisionID{Service: p.service, Content: p.content} }

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
	chain      []RevisionID
}

type edgeKey struct {
	decl DeclarationID
	to   RevisionID
}

// edgeWork identifies one unit of dependency work: one declaration asking one
// question. Two routes reaching the same declaring revision ask the same
// question, so a diamond costs what it is -- one edge -- while the same
// reference declared twice, or declared from two bases, costs two.
type edgeWork struct {
	decl       DeclarationID
	base       string
	ref        string
	constraint string
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
	revs           map[RevisionID]*revState
	revOrder       []RevisionID
	edges          map[edgeKey]Edge
	edgeWork       map[edgeWork]bool
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
	id := p.id()
	if len(a.steps) > 0 {
		b.recordEdge(a, id)
	}
	// A revision already on this route closes a loop. The edge above is kept, so
	// the cycle stays visible in the graph; the walk stops instead of following
	// it, so it terminates.
	if slices.Contains(a.chain, id) {
		b.recordCycle(a.chain, id)
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
// is fetched, and permanent tests prove it by counting calls. The edge bound is
// charged earlier still, in [builder.admitEdgeWork], because a dependency the
// bound refuses must not even become an arrival.
func (b *builder) mayResolve(a arrival) (Reason, bool) {
	if len(b.revs) >= b.bounds.MaxRevisions {
		b.limit(LimitationRevisionLimit, a.ref, "the revision bound was reached; this reference was not resolved")
		return Reason{Code: ReasonBoundExceeded, Message: "the revision bound stopped this resolution"}, false
	}
	return Reason{}, true
}

// admitEdgeWork charges one declared dependency against [Bounds.MaxEdges]
// before the arrival exists, so the surplus is never queued and never fetched.
// Success and failure are charged alike: a broken declaration costs a
// resolution too, and a bound measured against RECORDED edges would never
// engage on the closure that costs the most, one whose declarations all fail.
// The same key reached again by a second route was already paid for.
func (b *builder) admitEdgeWork(k edgeWork) bool {
	if b.edgeWork[k] {
		return true
	}
	if len(b.edgeWork) >= b.bounds.MaxEdges {
		return false
	}
	b.edgeWork[k] = true
	return true
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
	b.recordUnresolved(Unresolved{
		Declaration: a.steps[len(a.steps)-1], Name: a.name, Ref: a.ref,
		Constraint: a.constraint, Required: a.required, Reason: r,
	})
}

// recordUnresolved lists one dependency gap. It is separate from
// [builder.recordFailure] because a dependency the edge bound refused has no
// arrival to fail: it was never admitted, and inventing one to report it would
// be the same mistake as walking it.
func (b *builder) recordUnresolved(u Unresolved) {
	k := unresolvedKey{decl: u.Declaration, ref: u.Ref}
	if b.unresolvedSeen[k] {
		return
	}
	b.unresolvedSeen[k] = true
	if len(b.unresolved) >= b.bounds.MaxUnresolved {
		b.limit(LimitationUnresolvedLimit, "", "the unresolved-dependency bound was reached; further failures are not listed")
		return
	}
	b.unresolved = append(b.unresolved, u)
	if u.Reason.Code != ReasonBoundExceeded { // a bound already named itself
		b.limit(LimitationUnresolvedDep, u.Ref, "a declared dependency did not resolve")
	}
}

func (b *builder) recordEdge(a arrival, to RevisionID) {
	k := edgeKey{decl: a.steps[len(a.steps)-1], to: to}
	if _, dup := b.edges[k]; dup {
		return
	}
	b.edges[k] = Edge{
		Declaration: k.decl, Name: a.name, Ref: a.ref,
		Constraint: a.constraint, Required: a.required, To: to,
	}
}

func (b *builder) recordCycle(chain []RevisionID, target RevisionID) {
	loop := slices.Clone(chain[slices.Index(chain, target):])
	// Rotate to the smallest identity so the same loop found from two entry
	// points is one cycle.
	lowest := 0
	for i := range loop {
		if compareRevisionID(loop[i], loop[lowest]) < 0 {
			lowest = i
		}
	}
	loop = append(loop[lowest:], loop[:lowest]...)
	key := ""
	for _, r := range loop {
		// Length-prefixed framing, the same the fingerprint uses, so the service
		// text a revision carries cannot forge or split a key.
		key += encode(revFields(r)...)
	}
	if _, dup := b.cycles[key]; dup {
		return
	}
	b.cycles[key] = Cycle{Revisions: loop}
}

// retain records one arrival against its revision and reports whether the walk
// should continue through it.
func (b *builder) retain(a arrival, p projection) bool {
	depth := len(a.steps)
	id := p.id()
	st, ok := b.revs[id]
	if !ok {
		st = &revState{
			rev: Revision{
				Content: p.content, Service: p.service, Version: p.version,
				PactoVersion: p.pactoVersion, Owner: p.owner, MinDepth: depth,
			},
			reqRefs: map[string]bool{}, resRefs: map[string]bool{}, roots: map[RootID]bool{},
		}
		b.revs[id] = st
		b.revOrder = append(b.revOrder, id)
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
		b.roots[a.root].Revision = id
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
	chain := append(slices.Clone(a.chain), p.id())
	// Never more arrivals than the edge bound admits, so a contract declaring a
	// million dependencies allocates the budget, not the declaration list.
	out := make([]arrival, 0, min(len(p.deps), b.bounds.MaxEdges))
	for i, d := range p.deps {
		decl := DeclarationID{From: p.id(), Index: i}
		if !b.admitEdgeWork(edgeWork{decl: decl, base: p.base, ref: d.Ref, constraint: d.Compatibility}) {
			b.limit(LimitationEdgeLimit, d.Ref, "the edge bound was reached; this dependency was not resolved")
			b.recordUnresolved(Unresolved{
				Declaration: decl, Name: d.Name, Ref: d.Ref,
				Constraint: d.Compatibility, Required: d.Required,
				Reason: Reason{Code: ReasonBoundExceeded, Message: "the edge bound stopped this resolution"},
			})
			continue
		}
		out = append(out, arrival{
			ref: d.Ref, base: p.base, root: a.root,
			name: d.Name, constraint: d.Compatibility, required: d.Required,
			steps: append(slices.Clone(a.steps), decl),
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
	c.byRevision = make(map[RevisionID]int, len(c.revisions))
	for i, r := range c.revisions {
		c.byRevision[r.ID()] = i
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
	slices.SortFunc(out, func(x, y Revision) int { return compareRevisionID(x.ID(), y.ID()) })
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
	slices.SortFunc(out, func(x, y Cycle) int {
		return slices.CompareFunc(x.Revisions, y.Revisions, compareRevisionID)
	})
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
	byVersion := map[serviceVersion]map[RevisionID]bool{}
	for _, r := range revs {
		addTo(byService, r.Service, r.Version)
		addTo(byVersion, serviceVersion{r.Service, r.Version}, r.ID())
	}
	byDecl := map[DeclarationID]map[RevisionID]bool{}
	for _, e := range edges {
		addTo(byDecl, e.Declaration, e.To)
	}

	var out []Conflict
	for svc, versions := range byService {
		if len(versions) > 1 {
			out = append(out, Conflict{Kind: ConflictVersion, Service: svc, Versions: sortedStrings(versions)})
		}
	}
	for sv, ids := range byVersion {
		if len(ids) > 1 {
			out = append(out, Conflict{Kind: ConflictContent, Service: sv.service, Version: sv.version, Revisions: sortedRevisions(ids)})
		}
	}
	for decl, ids := range byDecl {
		if len(ids) > 1 {
			out = append(out, Conflict{Kind: ConflictDeclaration, Declaration: decl, Revisions: sortedRevisions(ids)})
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

func sortedRevisions(set map[RevisionID]bool) []RevisionID {
	out := make([]RevisionID, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	slices.SortFunc(out, compareRevisionID)
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
	return compareRevisionID(a.To, b.To)
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
