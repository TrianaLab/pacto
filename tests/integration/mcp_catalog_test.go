//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/trianalab/pacto/v3/pkg/catalog"
)

// The catalog discovery E2E drives the shipped binary over stdio, exactly as an
// agent host would: `pacto mcp --root ... --root ...`, spoken to with a real MCP
// client. Nothing below reaches into the process; every claim is made from what
// crossed the protocol.

// repoRoot is resolved once, while the working directory is still this
// package's own. Sibling tests in this suite chdir into fixtures (see inDir),
// so resolving a relative path later would follow whichever test happens to
// hold the directory at that moment.
var repoRoot = func() string {
	root, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	return root
}()

// buildPactoBinary compiles the real CLI so the test exercises the shipped
// entry point — the same wiring of keychain, insecure-registry policy and disk
// cache that a user gets — rather than an in-process approximation of it.
func buildPactoBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pacto")
	build := exec.Command("go", "build", "-o", bin, "./cmd/pacto")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building pacto: %v\n%s", err, out)
	}
	return bin
}

// catalogStdioSession starts the binary in catalog mode over stdio and speaks
// MCP to it. It returns the initialized session, the child process and the
// child's stderr, which is only read after the process has exited.
func catalogStdioSession(t *testing.T, bin, cacheDir string, args ...string) (*mcpsdk.ClientSession, *exec.Cmd, *strings.Builder) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"mcp"}, args...)...)
	// Every root is passed absolute, so the child needs no particular working
	// directory — and must not inherit one a sibling test is about to remove.
	cmd.Dir = t.TempDir()
	// A private cache: the child must not read or write the developer's own
	// ~/.cache/pacto, and the test must be able to see what it left behind.
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cacheDir)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "catalog-e2e", Version: "1.0"}, nil)
	session, err := client.Connect(context.Background(), &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connecting to `pacto mcp %s`: %v\nstderr:\n%s", strings.Join(args, " "), err, stderr)
	}
	return session, cmd, stderr
}

// readCatalogResource reads one resource and returns its raw text, so
// byte-for-byte comparisons stay possible.
func readCatalogResource(t *testing.T, session *mcpsdk.ClientSession, uri string) string {
	t.Helper()
	res, err := session.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("ReadResource(%s): %v", uri, err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("ReadResource(%s) returned %d contents, want 1", uri, len(res.Contents))
	}
	if got := res.Contents[0].MIMEType; got != "application/json" {
		t.Errorf("ReadResource(%s) mime type = %q, want application/json", uri, got)
	}
	return res.Contents[0].Text
}

func decodeJSON[T any](t *testing.T, text string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("decoding %T: %v\npayload:\n%s", v, err, text)
	}
	return v
}

type e2eOverview struct {
	Meta  catalog.Meta   `json:"meta"`
	Roots []catalog.Root `json:"roots"`
}

type e2eClosure struct {
	Revisions  []catalog.Revision   `json:"revisions"`
	Edges      []catalog.Edge       `json:"edges"`
	Unresolved []catalog.Unresolved `json:"unresolved"`
	Conflicts  []catalog.Conflict   `json:"conflicts"`
	Cycles     []catalog.Cycle      `json:"cycles"`
}

// catalogContract renders a contract that declares the given dependency refs.
func catalogContract(name, version string, deps ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `pactoVersion: "2.0"

service:
  name: %s
  version: %s
  owner:
    team: platform

interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml

configurations:
  - name: default
    schema: configuration/schema.json
    required: true
`, name, version)
	if len(deps) > 0 {
		b.WriteString("\ndependencies:\n")
		for i, ref := range deps {
			fmt.Fprintf(&b, "  - name: dep%d\n    ref: %s\n    required: true\n    compatibility: \"^1.0.0\"\n", i, ref)
		}
	}
	b.WriteString(`
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
	return b.String()
}

// writeCatalogFixture writes a bundle directory for a catalog service.
func writeCatalogFixture(t *testing.T, name, version string, deps ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	return writeBundleDir(t, dir, catalogContract(name, version, deps...), map[string]string{
		"openapi.yaml": fmt.Sprintf(openapiTemplate, name, version),
	})
}

func findRevision(revs []catalog.Revision, name string) (catalog.Revision, bool) {
	for _, r := range revs {
		if r.Service.Name == name {
			return r, true
		}
	}
	return catalog.Revision{}, false
}

// TestMCPCatalogDiscoveryOverStdio drives the whole Phase 12 surface end to end
// against a live registry: a registry root and a local root, a revision shared
// by both, a dependency that cannot resolve, a tag moved while the server runs,
// and a clean shutdown.
func TestMCPCatalogDiscoveryOverStdio(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	bin := buildPactoBinary(t)
	ctx := context.Background()

	sharedRef := "oci://" + reg.host + "/catalog-shared:1.0.0"
	ordersRef := "oci://" + reg.host + "/catalog-orders:1.0.0"
	platformRef := "oci://" + reg.host + "/catalog-platform:1.0.0"
	// Never published. Its absence is knowledge, and the catalog has to say so.
	absentRef := "oci://" + reg.host + "/catalog-absent:1.9.9"

	// platform ─┬─────────────► shared      (direct)
	//           └─► orders ───► shared      (transitive: the diamond)
	// orders ───► catalog-absent            (unresolvable)
	// edge (local) ─► shared                (the second root reaching shared)
	for _, push := range []struct {
		ref string
		dir string
	}{
		{sharedRef, writeCatalogFixture(t, "catalog-shared", "1.0.0")},
		{ordersRef, writeCatalogFixture(t, "catalog-orders", "1.0.0", sharedRef, absentRef)},
		{platformRef, writeCatalogFixture(t, "catalog-platform", "1.0.0", ordersRef, sharedRef)},
	} {
		if _, err := runCommand(t, reg, "push", push.ref, "-p", push.dir); err != nil {
			t.Fatalf("push %s: %v", push.ref, err)
		}
	}
	edgeDir := writeCatalogFixture(t, "catalog-edge", "1.0.0", sharedRef)

	frozenSharedDigest, err := reg.client.Resolve(ctx, strings.TrimPrefix(sharedRef, "oci://"))
	if err != nil {
		t.Fatalf("resolving %s: %v", sharedRef, err)
	}

	cacheDir := t.TempDir()
	session, child, stderr := catalogStdioSession(t, bin, cacheDir, "--root", platformRef, "--root", edgeDir)
	closed := false
	defer func() {
		if !closed {
			_ = session.Close()
		}
	}()

	// --- protocol discovery -------------------------------------------------
	init := session.InitializeResult()
	if init.Capabilities == nil || init.Capabilities.Resources == nil || init.Capabilities.Tools == nil {
		t.Fatalf("server capabilities = %+v, want resources and tools advertised", init.Capabilities)
	}
	if !strings.Contains(init.Instructions, "pacto://catalog") {
		t.Errorf("instructions do not point at the catalog resource:\n%s", init.Instructions)
	}

	resources, err := session.ListResources(ctx, &mcpsdk.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	var uris []string
	for _, r := range resources.Resources {
		uris = append(uris, r.URI)
	}
	slices.Sort(uris)
	if want := []string{"pacto://catalog", "pacto://catalog/closure"}; !slices.Equal(uris, want) {
		t.Fatalf("resources = %v, want exactly %v", uris, want)
	}

	templates, err := session.ListResourceTemplates(ctx, &mcpsdk.ListResourceTemplatesParams{})
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	if len(templates.ResourceTemplates) != 0 {
		// A template would have to encode a service name and a digest into a URI.
		t.Errorf("resource templates = %+v, want none: identity is never encoded into a URI", templates.ResourceTemplates)
	}

	tools, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var catalogTools []string
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "pacto_catalog") {
			catalogTools = append(catalogTools, tool.Name)
		}
		if strings.HasPrefix(tool.Name, "pacto_fleet") {
			t.Errorf("catalog mode exposed the fleet tool %s", tool.Name)
		}
	}
	if !slices.Equal(catalogTools, []string{"pacto_catalog_revision"}) {
		t.Errorf("catalog tools = %v, want exactly [pacto_catalog_revision]", catalogTools)
	}

	// --- what the catalog says ----------------------------------------------
	overviewText := readCatalogResource(t, session, "pacto://catalog")
	overview := decodeJSON[e2eOverview](t, overviewText)

	if overview.Meta.SchemaVersion == "" || overview.Meta.CatalogID == "" || overview.Meta.GeneratedAt.IsZero() {
		t.Errorf("meta = %+v, want a schema version, a catalog id and a generation time", overview.Meta)
	}
	if overview.Meta.Bounds.MaxRoots == 0 || overview.Meta.RequestedRoots != 2 {
		t.Errorf("meta bounds/roots = %+v / %d, want the applied bounds and both roots",
			overview.Meta.Bounds, overview.Meta.RequestedRoots)
	}
	// One dependency cannot resolve, so the whole answer is partial. That is not
	// the same as an empty catalog and not the same as a complete one.
	if overview.Meta.Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %q, want partial", overview.Meta.Completeness)
	}
	if !slices.ContainsFunc(overview.Meta.Limitations, func(l catalog.Limitation) bool {
		return l.Code == catalog.LimitationUnresolvedDep
	}) {
		t.Errorf("limitations = %+v, want UNRESOLVED_DEPENDENCY", overview.Meta.Limitations)
	}

	if len(overview.Roots) != 2 {
		t.Fatalf("roots = %+v, want two", overview.Roots)
	}
	ociRoot, localRoot := overview.Roots[0], overview.Roots[1]
	if ociRoot.RequestedRef != platformRef || !ociRoot.Resolved {
		t.Fatalf("registry root = %+v, want %s resolved", ociRoot, platformRef)
	}
	// Requested ref, resolved ref and content identity are three different
	// things, and all three survive the protocol.
	if !strings.Contains(ociRoot.ResolvedRef, "@sha256:") || strings.Contains(ociRoot.ResolvedRef, ":1.0.0") {
		t.Errorf("resolvedRef = %q, want the tag replaced by a digest pin", ociRoot.ResolvedRef)
	}
	if ociRoot.Revision.Content.Scheme != catalog.SchemeOCI {
		t.Errorf("registry root scheme = %q, want oci", ociRoot.Revision.Content.Scheme)
	}
	if ociRoot.Revision.Service.Domain == "" {
		t.Errorf("registry root service = %+v, want a domain-qualified identity", ociRoot.Revision.Service)
	}
	if localRoot.RequestedRef != edgeDir || !localRoot.Resolved {
		t.Fatalf("local root = %+v, want %s resolved", localRoot, edgeDir)
	}
	if localRoot.Revision.Content.Scheme != catalog.SchemeLocal || localRoot.ResolvedRef != "" {
		t.Errorf("local root = %+v, want a local content identity and no registry ref", localRoot)
	}

	closureText := readCatalogResource(t, session, "pacto://catalog/closure")
	closure := decodeJSON[e2eClosure](t, closureText)

	if len(closure.Revisions) != 4 {
		t.Fatalf("revisions = %d, want platform, orders, shared and edge", len(closure.Revisions))
	}
	if len(closure.Edges) != 4 {
		t.Errorf("edges = %d, want platform→orders, platform→shared, orders→shared, edge→shared", len(closure.Edges))
	}

	shared, ok := findRevision(closure.Revisions, "catalog-shared")
	if !ok {
		t.Fatalf("catalog-shared missing from %+v", closure.Revisions)
	}
	// Reached from both roots, by three routes: it is one revision, not three.
	if !shared.Shared() || len(shared.Roots) != 2 {
		t.Errorf("shared roots = %v, want both requested roots", shared.Roots)
	}
	if len(shared.Paths) != 3 || shared.PathsTruncated {
		t.Errorf("shared paths = %+v, want all three retained routes", shared.Paths)
	}
	// Direct from platform and transitive through orders: the shorter route names
	// the rank, and the longer one is still kept.
	if shared.Rank != catalog.RankDirect || shared.MinDepth != 1 {
		t.Errorf("shared rank/minDepth = %q/%d, want direct/1", shared.Rank, shared.MinDepth)
	}
	if shared.Content.Digest != frozenSharedDigest {
		t.Errorf("shared digest = %q, want the digest the tag resolved to (%q)", shared.Content.Digest, frozenSharedDigest)
	}
	if !slices.Contains(shared.RequestedRefs, sharedRef) {
		t.Errorf("shared requestedRefs = %v, want the tag that was asked for", shared.RequestedRefs)
	}

	if len(closure.Unresolved) != 1 {
		t.Fatalf("unresolved = %+v, want the one dependency that could not resolve", closure.Unresolved)
	}
	gap := closure.Unresolved[0]
	if gap.Ref != absentRef || gap.Reason.Code != catalog.ReasonNotFound {
		t.Errorf("unresolved = %+v, want %s reported as %s", gap, absentRef, catalog.ReasonNotFound)
	}
	if gap.Declaration.From.Service.Name != "catalog-orders" {
		t.Errorf("unresolved declaration = %+v, want it attributed to catalog-orders", gap.Declaration)
	}

	// --- identity lookup ----------------------------------------------------
	lookup, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "pacto_catalog_revision",
		Arguments: map[string]any{
			"name":   shared.Service.Name,
			"domain": shared.Service.Domain,
			"scheme": string(shared.Content.Scheme),
			"digest": shared.Content.Digest,
		},
	})
	if err != nil {
		t.Fatalf("CallTool(pacto_catalog_revision): %v", err)
	}
	answer := decodeJSON[struct {
		Found    bool              `json:"found"`
		Revision *catalog.Revision `json:"revision"`
	}](t, mcpResultText(t, lookup))
	if !answer.Found || answer.Revision == nil || answer.Revision.Content != shared.Content {
		t.Errorf("revision lookup = %+v, want the shared revision by its full identity", answer)
	}

	// --- the tag moves; the session does not ---------------------------------
	movedDir := writeCatalogFixture(t, "catalog-shared", "1.0.1")
	if _, err := runCommand(t, reg, "push", sharedRef, "-p", movedDir, "--force"); err != nil {
		t.Fatalf("moving the tag: %v", err)
	}
	movedDigest, err := reg.client.Resolve(ctx, strings.TrimPrefix(sharedRef, "oci://"))
	if err != nil {
		t.Fatalf("re-resolving the moved tag: %v", err)
	}
	if movedDigest == frozenSharedDigest {
		t.Fatalf("the tag did not actually move: still %s", movedDigest)
	}

	// Delete the local root and stop the registry too. A server that resolved
	// anything per request now has nothing to resolve against; a frozen session
	// is unaffected, byte for byte.
	if err := os.RemoveAll(edgeDir); err != nil {
		t.Fatal(err)
	}
	reg.server.Close()

	if got := readCatalogResource(t, session, "pacto://catalog"); got != overviewText {
		t.Errorf("pacto://catalog changed after the tag moved:\n%s", got)
	}
	if got := readCatalogResource(t, session, "pacto://catalog/closure"); got != closureText {
		t.Errorf("pacto://catalog/closure changed after the tag moved:\n%s", got)
	}

	// --- shutdown ------------------------------------------------------------
	if err := session.Close(); err != nil {
		t.Errorf("closing the session: %v", err)
	}
	closed = true
	if child.ProcessState == nil {
		t.Fatal("the child process was still running after the session closed")
	}
	if child.ProcessState.Sys().(interface{ Signaled() bool }).Signaled() {
		t.Errorf("the child had to be signalled to stop; stderr:\n%s", stderr)
	}

	// The child used the cache it was given and nothing else.
	if entries, err := os.ReadDir(filepath.Join(cacheDir, "pacto")); err != nil || len(entries) == 0 {
		t.Errorf("private cache %s is empty (%v); the child did not use the configured cache", cacheDir, err)
	}
}

// TestMCPCatalogEmptyRootFailsClosed proves the shipped binary refuses to serve an
// empty catalog: absent knowledge must never be published as authoritative.
func TestMCPCatalogEmptyRootFailsClosed(t *testing.T) {
	t.Parallel()
	bin := buildPactoBinary(t)
	out, err := exec.Command(bin, "mcp", "--root", "").CombinedOutput()
	if err == nil {
		t.Fatalf("`pacto mcp --root ''` succeeded, want a failure:\n%s", out)
	}
	if !strings.Contains(string(out), "--root") {
		t.Errorf("output does not explain the failure:\n%s", out)
	}
}
