package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v3/pkg/catalog"
	"github.com/trianalab/pacto/v3/pkg/contract"
)

var catalogBuiltAt = time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

// stubResolver scripts resolutions by reference and counts every call, so a
// test can prove not only what the surface answered but that answering it cost
// no further resolution.
type stubResolver struct {
	mu     sync.Mutex
	script map[string]catalog.Resolution
	calls  int
}

func newStubResolver() *stubResolver {
	return &stubResolver{script: map[string]catalog.Resolution{}}
}

func (s *stubResolver) Resolve(_ context.Context, req catalog.ResolveRequest) (catalog.Resolution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	res, ok := s.script[req.Ref]
	if !ok {
		return catalog.Resolution{}, &catalog.ResolveError{
			Code: catalog.ReasonNotFound, Message: "the registry holds nothing matching the reference",
		}
	}
	return res, nil
}

func (s *stubResolver) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// catDigest derives a real content address from a seed, so fixtures name
// content by meaning while identity stays a validated digest.
func catDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func catDep(name, ref string) contract.Dependency {
	return contract.Dependency{Name: name, Ref: ref, Required: true, Compatibility: "^1.0.0"}
}

func catContract(name, version string, deps ...contract.Dependency) *contract.Contract {
	return &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "platform"}},
		Dependencies: deps,
	}
}

// publish scripts a registry resolution: a digest-pinned resolved reference and
// a domain-qualified service, exactly as the app adapter produces them.
func (s *stubResolver) publish(ref, domain, name, version string, deps ...contract.Dependency) *stubResolver {
	dgst := catDigest(ref)
	s.script[ref] = catalog.Resolution{
		Contract:    catContract(name, version, deps...),
		Domain:      domain,
		Content:     catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: dgst},
		ResolvedRef: ref + "@" + dgst,
		Base:        "oci://",
	}
	return s
}

// publishLocal scripts a local resolution: a content hash, no domain and no
// resolved reference, exactly as the app adapter produces them.
func (s *stubResolver) publishLocal(path, name, version string, deps ...contract.Dependency) *stubResolver {
	s.script[path] = catalog.Resolution{
		Contract: catContract(name, version, deps...),
		Content:  catalog.ContentID{Scheme: catalog.SchemeLocal, Digest: catDigest(path)},
		Base:     "/abs" + path,
	}
	return s
}

func (s *stubResolver) publishAt(ref string, id catalog.RevisionID, version string, deps ...contract.Dependency) *stubResolver {
	s.script[ref] = catalog.Resolution{
		Contract:    catContract(id.Service.Name, version, deps...),
		Domain:      id.Service.Domain,
		Content:     id.Content,
		ResolvedRef: ref + "@" + id.Content.Digest,
		Base:        "oci://",
	}
	return s
}

func buildCatalogFixture(t *testing.T, r *stubResolver, roots ...string) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.Build(context.Background(), catalog.Request{
		Roots: roots, Resolver: r, Clock: func() time.Time { return catalogBuiltAt },
	})
	if err != nil {
		t.Fatalf("catalog.Build: %v", err)
	}
	return cat
}

// platformCatalog is the shared fixture: two roots (one registry, one local)
// whose closures share one revision, reached by a diamond from the registry
// root and directly from the local root, with one dependency that never
// resolves so the whole answer is partial.
func platformCatalog(t *testing.T) (*catalog.Catalog, *stubResolver) {
	t.Helper()
	r := newStubResolver().
		publish("oci://reg.example/platform", "reg.example", "platform", "1.0.0",
			catDep("orders", "oci://reg.example/orders"),
			catDep("payments", "oci://reg.example/payments")).
		publish("oci://reg.example/orders", "reg.example", "orders", "1.2.0",
			catDep("shared", "oci://reg.example/shared")).
		publish("oci://reg.example/payments", "reg.example", "payments", "2.0.0",
			catDep("shared", "oci://reg.example/shared")).
		publish("oci://reg.example/shared", "reg.example", "shared", "3.0.0",
			catDep("gone", "oci://reg.example/gone")).
		publishLocal("./edge", "edge", "0.1.0",
			catDep("shared", "oci://reg.example/shared"))
	return buildCatalogFixture(t, r, "oci://reg.example/platform", "./edge"), r
}

func sharedRevisionID() catalog.RevisionID {
	return catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "reg.example", Name: "shared"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("oci://reg.example/shared")},
	}
}

// catalogSession connects a real MCP client to server over an in-memory
// transport, so every assertion below travels the protocol rather than calling
// a handler directly.
func catalogSession(t *testing.T, server *mcpsdk.Server) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "catalog-test", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func readResourceText(t *testing.T, s *mcpsdk.ClientSession, uri string) string {
	t.Helper()
	res, err := s.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("ReadResource(%s): %v", uri, err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("ReadResource(%s): got %d contents, want 1", uri, len(res.Contents))
	}
	c := res.Contents[0]
	if c.URI != uri {
		t.Errorf("content URI = %q, want %q", c.URI, uri)
	}
	if c.MIMEType != "application/json" {
		t.Errorf("content MIME type = %q, want application/json", c.MIMEType)
	}
	return c.Text
}

func readJSON[T any](t *testing.T, s *mcpsdk.ClientSession, uri string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(readResourceText(t, s, uri)), &v); err != nil {
		t.Fatalf("decoding %s: %v", uri, err)
	}
	return v
}

// overviewWire and closureWire mirror the projection a client actually parses.
type overviewWire struct {
	Meta  catalog.Meta   `json:"meta"`
	Roots []catalog.Root `json:"roots"`
}

type closureWire struct {
	Revisions  []catalog.Revision   `json:"revisions"`
	Edges      []catalog.Edge       `json:"edges"`
	Unresolved []catalog.Unresolved `json:"unresolved"`
	Conflicts  []catalog.Conflict   `json:"conflicts"`
	Cycles     []catalog.Cycle      `json:"cycles"`
}

type revisionAnswerWire struct {
	Found        bool                 `json:"found"`
	Completeness catalog.Completeness `json:"completeness"`
	Requested    catalog.RevisionID   `json:"requested"`
	Revision     *catalog.Revision    `json:"revision"`
}

func callRevisionTool(t *testing.T, s *mcpsdk.ClientSession, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "pacto_catalog_revision", Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(pacto_catalog_revision): %v", err)
	}
	return res
}

func toolText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("got %d content items, want 1", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("got %T content, want *mcpsdk.TextContent", res.Content[0])
	}
	return tc.Text
}

func revisionAnswer(t *testing.T, s *mcpsdk.ClientSession, args map[string]any) revisionAnswerWire {
	t.Helper()
	res := callRevisionTool(t, s, args)
	if res.IsError {
		t.Fatalf("pacto_catalog_revision reported an error: %s", toolText(t, res))
	}
	var got revisionAnswerWire
	if err := json.Unmarshal([]byte(toolText(t, res)), &got); err != nil {
		t.Fatalf("decoding revision answer: %v", err)
	}
	return got
}

func resourceURIs(t *testing.T, s *mcpsdk.ClientSession) []string {
	t.Helper()
	res, err := s.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	var uris []string
	for _, r := range res.Resources {
		uris = append(uris, r.URI)
	}
	return uris
}

// assertResourcesAreSelfDescribing checks that a client listing the surface
// learns what each resource is without reading it.
func assertResourcesAreSelfDescribing(t *testing.T, s *mcpsdk.ClientSession) {
	t.Helper()
	res, err := s.ListResources(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	for _, r := range res.Resources {
		if r.MIMEType != "application/json" {
			t.Errorf("resource %s MIME type = %q, want application/json", r.URI, r.MIMEType)
		}
		if r.Name == "" || r.Description == "" {
			t.Errorf("resource %s must be self-describing, got name %q description %q", r.URI, r.Name, r.Description)
		}
	}
}

// assertCatalogToolSurface checks that catalog mode adds exactly one tool,
// keeps the authoring tools and never borrows the operational ones.
func assertCatalogToolSurface(t *testing.T, names map[string]bool) {
	t.Helper()
	if !names["pacto_catalog_revision"] {
		t.Errorf("tools = %v, want pacto_catalog_revision", slices.Sorted(maps.Keys(names)))
	}
	for _, authoring := range []string{"pacto_create", "pacto_edit", "pacto_check", "pacto_schema"} {
		if !names[authoring] {
			t.Errorf("tools = %v, want the authoring tool %s to stay registered", slices.Sorted(maps.Keys(names)), authoring)
		}
	}
	// One tool, not a redundant second projection of the whole catalog.
	catalogTools := 0
	for n := range names {
		if strings.HasPrefix(n, "pacto_fleet_") || n == "pacto_impact" {
			t.Errorf("catalog mode registered the operational tool %s; the catalog is not the fleet", n)
		}
		if strings.HasPrefix(n, "pacto_catalog") {
			catalogTools++
		}
	}
	if catalogTools != 1 {
		t.Errorf("got %d pacto_catalog* tools, want exactly 1", catalogTools)
	}
}

func TestCatalogServerExposesTheWholeDiscoverySurfaceAndNothingElse(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))

	uris := resourceURIs(t, s)
	slices.Sort(uris)
	want := []string{"pacto://catalog", "pacto://catalog/closure"}
	if !slices.Equal(uris, want) {
		t.Errorf("resources = %v, want %v", uris, want)
	}
	assertResourcesAreSelfDescribing(t, s)

	// No templates: a revision identity is four structured fields, and encoding
	// them into one URI is the ambiguity the tool exists to avoid.
	tmpl, err := s.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	if len(tmpl.ResourceTemplates) != 0 {
		t.Errorf("got %d resource templates, want none", len(tmpl.ResourceTemplates))
	}

	assertCatalogToolSurface(t, toolNames(t, s))
}

func TestAuthoringAndFleetServersExposeNoCatalogSurface(t *testing.T) {
	t.Parallel()
	for name, server := range map[string]*mcpsdk.Server{
		"authoring": NewServer(nil, "v-test"),
		"fleet":     NewFleetServer("v-test", buildFleetQuery(t), nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := catalogSession(t, server)
			if uris := resourceURIs(t, s); len(uris) != 0 {
				t.Errorf("%s server exposes resources %v, want none", name, uris)
			}
			if toolNames(t, s)["pacto_catalog_revision"] {
				t.Errorf("%s server registered pacto_catalog_revision", name)
			}
		})
	}
}

// assertSessionMeta checks the metadata that says which frozen session an
// answer came from and under which bounds it was built.
func assertSessionMeta(t *testing.T, m catalog.Meta, cat *catalog.Catalog, wantRoots int) {
	t.Helper()
	if m.SchemaVersion != catalog.SchemaVersion {
		t.Errorf("schemaVersion = %q, want %q", m.SchemaVersion, catalog.SchemaVersion)
	}
	if m.CatalogID != cat.Meta().CatalogID || m.CatalogID == "" {
		t.Errorf("catalogId = %q, want the session's %q", m.CatalogID, cat.Meta().CatalogID)
	}
	if !m.GeneratedAt.Equal(catalogBuiltAt) {
		t.Errorf("generatedAt = %s, want %s", m.GeneratedAt, catalogBuiltAt)
	}
	if m.RequestedRoots != wantRoots {
		t.Errorf("requestedRoots = %d, want %d", m.RequestedRoots, wantRoots)
	}
	if m.Bounds.MaxRevisions != catalog.DefaultMaxRevisions {
		t.Errorf("bounds.maxRevisions = %d, want the effective default %d", m.Bounds.MaxRevisions, catalog.DefaultMaxRevisions)
	}
}

// assertRootIdentitiesSurvive checks the three-way separation on both kinds of
// root: the requested reference, the immutable reference it resolved to and
// the content identity are different things, and none stands in for another.
func assertRootIdentitiesSurvive(t *testing.T, registry, local catalog.Root) {
	t.Helper()
	wantResolved := "oci://reg.example/platform@" + catDigest("oci://reg.example/platform")
	if registry.ResolvedRef != wantResolved {
		t.Errorf("resolvedRef = %q, want %q", registry.ResolvedRef, wantResolved)
	}
	if registry.Revision.Content.Scheme != catalog.SchemeOCI || registry.Revision.Service.Domain != "reg.example" {
		t.Errorf("registry root identity = %+v, want a domain-qualified oci revision", registry.Revision)
	}
	if local.ResolvedRef != "" {
		t.Errorf("local resolvedRef = %q, want empty: local content is its own immutable identity", local.ResolvedRef)
	}
	if local.Revision.Content.Scheme != catalog.SchemeLocal || local.Revision.Service.Domain != "" {
		t.Errorf("local root identity = %+v, want an undomained local revision", local.Revision)
	}
}

func TestCatalogResourceReportsMetadataBoundsAndEveryRequestedRoot(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	got := readJSON[overviewWire](t, s, "pacto://catalog")

	assertSessionMeta(t, got.Meta, cat, 2)
	// A dependency that never resolved makes the whole answer partial, and the
	// limitation says which kind of gap it was.
	if got.Meta.Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %q, want partial", got.Meta.Completeness)
	}
	if !slices.ContainsFunc(got.Meta.Limitations, func(l catalog.Limitation) bool {
		return l.Code == catalog.LimitationUnresolvedDep
	}) {
		t.Errorf("limitations = %+v, want one coded %s", got.Meta.Limitations, catalog.LimitationUnresolvedDep)
	}

	if len(got.Roots) != 2 {
		t.Fatalf("got %d roots, want 2", len(got.Roots))
	}
	registry, local := got.Roots[0], got.Roots[1]
	if registry.RequestedRef != "oci://reg.example/platform" || local.RequestedRef != "./edge" {
		t.Errorf("requested refs = %q and %q, want them preserved verbatim in request order",
			registry.RequestedRef, local.RequestedRef)
	}
	if !registry.Resolved || !local.Resolved {
		t.Fatalf("both roots resolve in this fixture, got %+v and %+v", registry, local)
	}
	assertRootIdentitiesSurvive(t, registry, local)
}

func TestCatalogResourceKeepsAnUnresolvedRootVisibleWithItsReason(t *testing.T) {
	t.Parallel()
	r := newStubResolver().publish("oci://reg.example/platform", "reg.example", "platform", "1.0.0")
	cat := buildCatalogFixture(t, r, "oci://reg.example/platform", "oci://reg.example/missing")
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	got := readJSON[overviewWire](t, s, "pacto://catalog")

	if got.Meta.Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %q, want partial: an unresolved root is not an empty catalog", got.Meta.Completeness)
	}
	if got.Meta.RequestedRoots != 2 || len(got.Roots) != 2 {
		t.Fatalf("requestedRoots = %d with %d roots, want both roots reported", got.Meta.RequestedRoots, len(got.Roots))
	}
	missing := got.Roots[1]
	if missing.Resolved {
		t.Fatalf("root %+v resolved, want it reported unresolved", missing)
	}
	if missing.RequestedRef != "oci://reg.example/missing" {
		t.Errorf("unresolved requestedRef = %q, want it preserved", missing.RequestedRef)
	}
	if missing.Reason.Code != catalog.ReasonNotFound || missing.Reason.Message == "" {
		t.Errorf("reason = %+v, want a structured NOT_FOUND with a message", missing.Reason)
	}
	if !slices.ContainsFunc(got.Meta.Limitations, func(l catalog.Limitation) bool {
		return l.Code == catalog.LimitationRootUnresolved && l.Ref == "oci://reg.example/missing"
	}) {
		t.Errorf("limitations = %+v, want ROOT_UNRESOLVED naming the root", got.Meta.Limitations)
	}
}

// assertEdgesKeepStructuredIdentity checks that every resolved edge keeps the
// declaration apart from the revision it reached, so one revision declared by
// many contracts stays attributable.
func assertEdgesKeepStructuredIdentity(t *testing.T, edges []catalog.Edge) {
	t.Helper()
	for _, e := range edges {
		if e.Declaration.From.Content.Digest == "" || e.To.Content.Digest == "" {
			t.Errorf("edge %+v lost a structured identity", e)
		}
	}
}

// assertTheGoneDependency checks the one dependency the platform fixture never
// resolves: named, classified and attributed to the revision that declared it.
func assertTheGoneDependency(t *testing.T, unresolved []catalog.Unresolved) {
	t.Helper()
	if len(unresolved) != 1 {
		t.Fatalf("got %d unresolved dependencies, want 1", len(unresolved))
	}
	gap := unresolved[0]
	if gap.Ref != "oci://reg.example/gone" || gap.Name != "gone" || gap.Reason.Code != catalog.ReasonNotFound {
		t.Errorf("unresolved = %+v, want the named declaration with a structured reason", gap)
	}
	if gap.Declaration.From != sharedRevisionID() {
		t.Errorf("unresolved declared by %+v, want %+v", gap.Declaration.From, sharedRevisionID())
	}
}

func TestCatalogClosureResourceProjectsRevisionsEdgesAndGaps(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	got := readJSON[closureWire](t, s, "pacto://catalog/closure")

	if len(got.Revisions) != len(cat.Revisions()) || len(got.Revisions) != 5 {
		t.Fatalf("got %d revisions, want the session's %d (platform, orders, payments, shared, edge)",
			len(got.Revisions), len(cat.Revisions()))
	}
	if len(got.Edges) != len(cat.Edges()) || len(got.Edges) != 5 {
		t.Errorf("got %d edges, want the session's %d", len(got.Edges), len(cat.Edges()))
	}
	assertEdgesKeepStructuredIdentity(t, got.Edges)
	assertTheGoneDependency(t, got.Unresolved)
	// Authoritative empty is still a fact, and reads as [] rather than null.
	if got.Conflicts == nil || got.Cycles == nil {
		t.Errorf("conflicts and cycles must be present and empty, got %v and %v", got.Conflicts, got.Cycles)
	}
	if len(got.Conflicts) != 0 || len(got.Cycles) != 0 {
		t.Errorf("got %d conflicts and %d cycles, want none in this fixture", len(got.Conflicts), len(got.Cycles))
	}
}

// findRevision returns the projected revision with this identity.
func findRevision(t *testing.T, revs []catalog.Revision, id catalog.RevisionID) catalog.Revision {
	t.Helper()
	for _, r := range revs {
		if r.ID() == id {
			return r
		}
	}
	t.Fatalf("revision %+v is missing from %d projected revisions", id, len(revs))
	return catalog.Revision{}
}

// rankTally counts projected revisions by rank.
func rankTally(revs []catalog.Revision) map[catalog.Rank]int {
	tally := map[catalog.Rank]int{}
	for _, r := range revs {
		tally[r.Rank]++
	}
	return tally
}

func TestCatalogClosureKeepsSharedRevisionsRanksAndEveryRetainedPath(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	got := readJSON[closureWire](t, s, "pacto://catalog/closure")

	shared := findRevision(t, got.Revisions, sharedRevisionID())
	if !shared.Shared() || !slices.Equal(shared.Roots, []catalog.RootID{0, 1}) {
		t.Errorf("shared roots = %v, want both requested roots", shared.Roots)
	}
	// Two diamond branches under the registry root, one direct declaration under
	// the local root: three retained routes, none collapsed.
	if len(shared.Paths) != 3 {
		t.Errorf("got %d retained paths, want 3: %+v", len(shared.Paths), shared.Paths)
	}
	if shared.PathsTruncated {
		t.Errorf("paths were truncated in a fixture well inside the bounds")
	}
	// Rank comes from the shortest retained path, and the longer ones survive.
	if shared.Rank != catalog.RankDirect || shared.MinDepth != 1 {
		t.Errorf("rank = %q at depth %d, want direct at 1", shared.Rank, shared.MinDepth)
	}
	if !slices.ContainsFunc(shared.Paths, func(p catalog.Path) bool { return len(p.Steps) == 2 }) {
		t.Errorf("the transitive diamond routes were lost: %+v", shared.Paths)
	}
	if !slices.Equal(shared.RequestedRefs, []string{"oci://reg.example/shared"}) {
		t.Errorf("requestedRefs = %v, want the declared reference", shared.RequestedRefs)
	}
	if !slices.Equal(shared.ResolvedRefs, []string{"oci://reg.example/shared@" + catDigest("oci://reg.example/shared")}) {
		t.Errorf("resolvedRefs = %v, want the digest-pinned reference", shared.ResolvedRefs)
	}

	ranks := rankTally(got.Revisions)
	if ranks[catalog.RankRoot] != 2 || ranks[catalog.RankDirect] != 3 {
		t.Errorf("ranks = %v, want 2 root and 3 direct", ranks)
	}
}

func TestCatalogClosureKeepsConflictsAndCyclesVisible(t *testing.T) {
	t.Parallel()
	loopA := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "reg.example", Name: "a"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("a")},
	}
	loopB := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "reg.example", Name: "b"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("b")},
	}
	r := newStubResolver().
		publishAt("oci://reg.example/a", loopA, "1.0.0", catDep("b", "oci://reg.example/b")).
		publishAt("oci://reg.example/b", loopB, "1.0.0", catDep("a", "oci://reg.example/a")).
		// Two registry references publishing the same service at two versions.
		publish("oci://reg.example/dup:1", "reg.example", "dup", "1.0.0").
		publish("oci://reg.example/dup:2", "reg.example", "dup", "2.0.0")
	cat := buildCatalogFixture(t, r, "oci://reg.example/a", "oci://reg.example/dup:1", "oci://reg.example/dup:2")

	s := catalogSession(t, NewCatalogServer("v-test", cat))
	got := readJSON[closureWire](t, s, "pacto://catalog/closure")

	if len(got.Cycles) != 1 || len(got.Cycles[0].Revisions) != 2 {
		t.Fatalf("cycles = %+v, want the a<->b loop", got.Cycles)
	}
	if !slices.Contains(got.Cycles[0].Revisions, loopA) || !slices.Contains(got.Cycles[0].Revisions, loopB) {
		t.Errorf("cycle = %+v, want both loop members by full identity", got.Cycles[0].Revisions)
	}
	if !slices.ContainsFunc(got.Conflicts, func(c catalog.Conflict) bool {
		return c.Kind == catalog.ConflictVersion && c.Service.Name == "dup"
	}) {
		t.Errorf("conflicts = %+v, want the dup version conflict left unresolved", got.Conflicts)
	}
}

func TestCatalogRevisionToolAnswersByStructuredIdentity(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	id := sharedRevisionID()

	got := revisionAnswer(t, s, map[string]any{
		"domain": id.Service.Domain, "name": id.Service.Name,
		"scheme": string(id.Content.Scheme), "digest": id.Content.Digest,
	})
	if !got.Found || got.Revision == nil {
		t.Fatalf("answer = %+v, want the shared revision", got)
	}
	if got.Requested != id {
		t.Errorf("requested = %+v, want it echoed as %+v", got.Requested, id)
	}
	if got.Revision.ID() != id {
		t.Errorf("revision identity = %+v, want %+v", got.Revision.ID(), id)
	}
	if got.Revision.Version != "3.0.0" || len(got.Revision.Paths) != 3 {
		t.Errorf("revision = %+v, want the full projection including every retained path", got.Revision)
	}
	// Every answer carries the standing of the knowledge it came from.
	if got.Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %q, want the session's partial", got.Completeness)
	}

	// A local revision is addressable by the same structured identity, with no
	// domain and the local content scheme.
	local := revisionAnswer(t, s, map[string]any{
		"name": "edge", "scheme": "local", "digest": catDigest("./edge"),
	})
	if !local.Found || local.Revision == nil || local.Revision.Rank != catalog.RankRoot {
		t.Fatalf("local answer = %+v, want the local root revision", local)
	}
}

func TestCatalogRevisionToolCannotConfuseHostileIdentities(t *testing.T) {
	t.Parallel()
	// Every pair below collides the moment identity is joined with a delimiter.
	slashLeft := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "acme/team", Name: "api"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("slash-left")},
	}
	slashRight := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "acme", Name: "team/api"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("slash-right")},
	}
	colon := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "acme:api", Name: "sha256:beef"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("colon")},
	}
	percent := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "acme%2F", Name: "api%3Fq=1"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("percent")},
	}
	unicode := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "acme", Name: "api\u2044\u00e9\U0001F600"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("unicode")},
	}
	// Same service, two contents: identity is the pair, never either half.
	mirrorA := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "acme", Name: "mirror"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("mirror-a")},
	}
	mirrorB := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "acme", Name: "mirror"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("mirror-b")},
	}
	ids := map[string]catalog.RevisionID{
		"slash-left": slashLeft, "slash-right": slashRight, "colon": colon,
		"percent": percent, "unicode": unicode, "mirror-a": mirrorA, "mirror-b": mirrorB,
	}

	r := newStubResolver()
	var roots []string
	for _, key := range slices.Sorted(maps.Keys(ids)) {
		ref := "oci://reg.example/" + key
		r.publishAt(ref, ids[key], "1.0.0")
		roots = append(roots, ref)
	}
	cat := buildCatalogFixture(t, r, roots...)
	s := catalogSession(t, NewCatalogServer("v-test", cat))

	for key, id := range ids {
		got := revisionAnswer(t, s, map[string]any{
			"domain": id.Service.Domain, "name": id.Service.Name,
			"scheme": string(id.Content.Scheme), "digest": id.Content.Digest,
		})
		if !got.Found || got.Revision == nil {
			t.Fatalf("%s: answer = %+v, want a hit", key, got)
		}
		if got.Revision.ID() != id {
			t.Errorf("%s: resolved to %+v, want %+v", key, got.Revision.ID(), id)
		}
	}

	// Recombining halves of two different identities must miss, not collide.
	crossed := revisionAnswer(t, s, map[string]any{
		"domain": slashLeft.Service.Domain, "name": slashRight.Service.Name,
		"scheme": string(slashLeft.Content.Scheme), "digest": slashLeft.Content.Digest,
	})
	if crossed.Found {
		t.Errorf("a crossed identity resolved to %+v, want a miss", crossed.Revision)
	}
}

func TestCatalogRevisionToolReportsAMissAsBoundedKnowledge(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))

	absent := catalog.RevisionID{
		Service: catalog.ServiceID{Domain: "reg.example", Name: "nobody"},
		Content: catalog.ContentID{Scheme: catalog.SchemeOCI, Digest: catDigest("nobody")},
	}
	got := revisionAnswer(t, s, map[string]any{
		"domain": absent.Service.Domain, "name": absent.Service.Name,
		"scheme": string(absent.Content.Scheme), "digest": absent.Content.Digest,
	})
	if got.Found || got.Revision != nil {
		t.Fatalf("answer = %+v, want a miss", got)
	}
	if got.Requested != absent {
		t.Errorf("requested = %+v, want it echoed as %+v", got.Requested, absent)
	}
	// The catalog is partial, so a miss is "not known here", not "does not
	// exist". The completeness travels with the answer that needs it.
	if got.Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %q, want partial to accompany the miss", got.Completeness)
	}
}

func TestCatalogRevisionToolRejectsSomethingThatIsNotAContentIdentity(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))

	for name, args := range map[string]map[string]any{
		"a tag is not a digest":    {"name": "shared", "scheme": "oci", "digest": "1.0.0"},
		"an unknown scheme":        {"name": "shared", "scheme": "tag", "digest": catDigest("x")},
		"a missing content digest": {"name": "shared", "scheme": "oci"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := callRevisionTool(t, s, args)
			if !res.IsError {
				t.Errorf("got %s, want an error result", toolText(t, res))
			}
		})
	}
}

func TestCatalogAnswersAreDeterministicAndCannotMutateTheSession(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	id := sharedRevisionID()
	args := map[string]any{
		"domain": id.Service.Domain, "name": id.Service.Name,
		"scheme": string(id.Content.Scheme), "digest": id.Content.Digest,
	}

	first := readResourceText(t, s, "pacto://catalog/closure")
	firstOverview := readResourceText(t, s, "pacto://catalog")
	firstRevision := toolText(t, callRevisionTool(t, s, args))

	// Mutate everything a caller can reach: what the session hands out, and what
	// a previous protocol answer decoded into.
	revs := cat.Revisions()
	revs[0].Rank = "tampered"
	revs[0].Paths = nil
	roots := cat.Roots()
	roots[0].RequestedRef = "tampered"
	meta := cat.Meta()
	meta.Completeness = catalog.CompletenessComplete
	meta.Limitations = nil
	decoded := readJSON[closureWire](t, s, "pacto://catalog/closure")
	decoded.Revisions[0].Rank = "tampered"
	decoded.Edges = nil

	if second := readResourceText(t, s, "pacto://catalog/closure"); second != first {
		t.Errorf("the closure changed between reads:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if second := readResourceText(t, s, "pacto://catalog"); second != firstOverview {
		t.Errorf("the catalog overview changed between reads:\nfirst:\n%s\nsecond:\n%s", firstOverview, second)
	}
	if second := toolText(t, callRevisionTool(t, s, args)); second != firstRevision {
		t.Errorf("the revision answer changed between calls:\nfirst:\n%s\nsecond:\n%s", firstRevision, second)
	}
}

func TestCatalogQueriesResolveNothingAfterTheSessionIsBuilt(t *testing.T) {
	t.Parallel()
	cat, r := platformCatalog(t)
	built := r.count()
	if built == 0 {
		t.Fatalf("the fixture resolved nothing")
	}
	s := catalogSession(t, NewCatalogServer("v-test", cat))

	readResourceText(t, s, "pacto://catalog")
	readResourceText(t, s, "pacto://catalog/closure")
	id := sharedRevisionID()
	revisionAnswer(t, s, map[string]any{
		"domain": id.Service.Domain, "name": id.Service.Name,
		"scheme": string(id.Content.Scheme), "digest": id.Content.Digest,
	})
	revisionAnswer(t, s, map[string]any{
		"domain": "reg.example", "name": "nobody", "scheme": "oci", "digest": catDigest("nobody"),
	})

	if after := r.count(); after != built {
		t.Errorf("the resolver was called %d times after construction (%d -> %d); protocol queries must be pure and network-free",
			after-built, built, after)
	}
}

func TestReadingAnUnregisteredCatalogURIIsNotFound(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	_, err := s.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "pacto://catalog/secrets"})
	if err == nil {
		t.Fatal("reading an unregistered URI succeeded, want a not-found error")
	}
}

func TestJSONResourceReportsAMarshalFailure(t *testing.T) {
	t.Parallel()
	// json.MarshalIndent fails for channels.
	if _, err := jsonResource("pacto://catalog", make(chan int)); err == nil {
		t.Error("expected an error for an unmarshalable value")
	}
}

func TestCatalogInstructionsSeparateDiscoveryFromTheFleet(t *testing.T) {
	t.Parallel()
	cat, _ := platformCatalog(t)
	s := catalogSession(t, NewCatalogServer("v-test", cat))
	got := s.InitializeResult().Instructions
	for _, want := range []string{"pacto://catalog", "pacto_catalog_revision", "authorization", "partial"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions must mention %q, got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "pacto_create") {
		t.Errorf("catalog mode must keep the authoring instructions, got:\n%s", got)
	}
}
