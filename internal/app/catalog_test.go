package app

// The catalog adapter is the only place pkg/catalog's model meets a real
// registry and a real filesystem, so it is proved against both: the in-process
// OCI registries from the resolution matrix harness and temporary directories on
// disk. Nothing here is mocked that could be real.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/trianalab/pacto/v3/pkg/catalog"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// ── local bundle fixtures ─────────────────────────────────────────────────────

// catBundleDir writes a real contract bundle into dir and returns dir.
func catBundleDir(t *testing.T, dir, name, version string, deps ...mxDep) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), mxRenderYAML(name, version, deps), 0o644); err != nil {
		t.Fatalf("write pacto.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), []byte("openapi: '3.0.0'\n"), 0o644); err != nil {
		t.Fatalf("write openapi.yaml: %v", err)
	}
	return dir
}

func catResolve(t *testing.T, r catalog.Resolver, ref, base, constraint string) (catalog.Resolution, error) {
	t.Helper()
	return r.Resolve(context.Background(), catalog.ResolveRequest{Ref: ref, Base: base, Constraint: constraint})
}

func catCode(t *testing.T, err error) string {
	t.Helper()
	var re *catalog.ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("error %v is not a *catalog.ResolveError; the catalog would reduce it to a generic reason", err)
	}
	return re.Code
}

// ── registry resolution ───────────────────────────────────────────────────────

func TestCatalogResolverPinsARegistryTagToItsDigest(t *testing.T) {
	host := mxPlainRegistry(t)
	svc, client := mxService(authn.DefaultKeychain)
	mxPush(t, client, host, "acme/payments", "1.0.0", mxBundle(t, "payments", "1.0.0"))

	res, err := catResolve(t, svc.CatalogResolver(), "oci://"+host+"/acme/payments:1.0.0", "", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want, err := client.Resolve(context.Background(), host+"/acme/payments:1.0.0")
	if err != nil {
		t.Fatalf("Resolve digest: %v", err)
	}
	if res.Content.Scheme != catalog.SchemeOCI || res.Content.Digest != want {
		t.Errorf("content = %+v, want the registry's own digest %s", res.Content, want)
	}
	// The tag is provenance; the digest-pinned reference is what the tag meant at
	// construction time, and the two are kept apart.
	if got := host + "/acme/payments@" + want; res.ResolvedRef != got {
		t.Errorf("resolvedRef = %q, want %q", res.ResolvedRef, got)
	}
	if res.Domain != host+"/acme" {
		t.Errorf("domain = %q, want the registry and org", res.Domain)
	}
	if res.Base != catalogOCIBase {
		t.Errorf("base = %q, want the registry sentinel", res.Base)
	}
	if res.Contract.Service.Name != "payments" {
		t.Errorf("contract = %+v, want the pulled bundle's contract", res.Contract.Service)
	}
}

// A reference naming no version is answered by the constraint that declared it,
// which is the whole reason the constraint travels with the request.
func TestCatalogResolverAppliesTheDeclaringConstraintToABareReference(t *testing.T) {
	host := mxPlainRegistry(t)
	svc, client := mxService(authn.DefaultKeychain)
	for _, v := range []string{"1.0.0", "1.5.0", "2.0.0"} {
		mxPush(t, client, host, "acme/lib", v, mxBundle(t, "lib", v))
	}
	r := svc.CatalogResolver()

	for _, tc := range []struct{ constraint, want string }{
		{"^1.0.0", "1.5.0"},
		{"^2.0.0", "2.0.0"},
	} {
		res, err := catResolve(t, r, "oci://"+host+"/acme/lib", "", tc.constraint)
		if err != nil {
			t.Fatalf("Resolve under %s: %v", tc.constraint, err)
		}
		if res.Contract.Service.Version != tc.want {
			t.Errorf("%s selected %s, want %s", tc.constraint, res.Contract.Service.Version, tc.want)
		}
	}
}

func TestCatalogResolverNeedsARegistryClientForARegistryReference(t *testing.T) {
	svc := &Service{}
	_, err := catResolve(t, svc.CatalogResolver(), "oci://ghcr.io/acme/api:1.0.0", "", "")
	if got := catCode(t, err); got != catalog.ReasonUnavailable {
		t.Errorf("code = %s, want %s", got, catalog.ReasonUnavailable)
	}
}

// ── local resolution ──────────────────────────────────────────────────────────

// Identity is the bundle's content, so the same declared service and version in
// two directories is one revision or two according to the bytes, never according
// to the name.
func TestCatalogResolverIdentifiesALocalBundleByItsContent(t *testing.T) {
	root := t.TempDir()
	same := catBundleDir(t, filepath.Join(root, "copy"), "api", "1.0.0")
	original := catBundleDir(t, filepath.Join(root, "original"), "api", "1.0.0")
	differing := catBundleDir(t, filepath.Join(root, "differing"), "api", "1.0.0")
	if err := os.WriteFile(filepath.Join(differing, "openapi.yaml"), []byte("openapi: '3.1.0'\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	r := (&Service{}).CatalogResolver()

	a, err := catResolve(t, r, original, "", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	b, err := catResolve(t, r, same, "", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	c, err := catResolve(t, r, differing, "", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if a.Content.Scheme != catalog.SchemeLocal {
		t.Errorf("scheme = %q, want local", a.Content.Scheme)
	}
	if a.Content != b.Content {
		t.Errorf("byte-identical bundles got different identities: %s vs %s", a.Content, b.Content)
	}
	if a.Content == c.Content {
		t.Errorf("bundles differing in content share identity %s", a.Content)
	}
	if a.Base != original {
		t.Errorf("base = %q, want the resolved directory %q", a.Base, original)
	}
	if a.ResolvedRef != "" {
		t.Errorf("resolvedRef = %q, want none: a local identity is already immutable", a.ResolvedRef)
	}
}

// Two roots can declare the same relative reference and mean different things.
// The declaring base is what decides, never whichever root was walked first.
func TestCatalogResolverResolvesARelativeReferenceAgainstItsDeclarer(t *testing.T) {
	root := t.TempDir()
	a := catBundleDir(t, filepath.Join(root, "a"), "a", "1.0.0")
	b := catBundleDir(t, filepath.Join(root, "b"), "b", "1.0.0")
	catBundleDir(t, filepath.Join(a, "shared"), "shared", "1.0.0")
	catBundleDir(t, filepath.Join(b, "shared"), "shared", "2.0.0")
	r := (&Service{}).CatalogResolver()

	fromA, err := catResolve(t, r, "./shared", a, "^1.0.0")
	if err != nil {
		t.Fatalf("Resolve from a: %v", err)
	}
	fromB, err := catResolve(t, r, "./shared", b, "^1.0.0")
	if err != nil {
		t.Fatalf("Resolve from b: %v", err)
	}
	if fromA.Contract.Service.Version != "1.0.0" || fromB.Contract.Service.Version != "2.0.0" {
		t.Errorf("./shared resolved to %s from a and %s from b, want 1.0.0 and 2.0.0",
			fromA.Contract.Service.Version, fromB.Contract.Service.Version)
	}
}

func TestCatalogResolverAcceptsAnAbsoluteAndAFileScopedReference(t *testing.T) {
	dir := catBundleDir(t, filepath.Join(t.TempDir(), "svc"), "svc", "1.0.0")
	r := (&Service{}).CatalogResolver()

	abs, err := catResolve(t, r, dir, "", "")
	if err != nil {
		t.Fatalf("Resolve absolute: %v", err)
	}
	scoped, err := catResolve(t, r, "file://"+dir, "", "")
	if err != nil {
		t.Fatalf("Resolve file://: %v", err)
	}
	if abs.Content != scoped.Content {
		t.Errorf("the same directory got two identities: %s vs %s", abs.Content, scoped.Content)
	}
}

func TestCatalogResolverResolvesARelativeRootAgainstTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	catBundleDir(t, filepath.Join(root, "svc"), "svc", "1.0.0")
	t.Chdir(root)

	res, err := catResolve(t, (&Service{}).CatalogResolver(), "./svc", "", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Contract.Service.Name != "svc" {
		t.Errorf("contract = %+v, want the bundle under the working directory", res.Contract.Service)
	}
}

// A contract that arrived over the network must not be able to choose which
// local files the catalog reads, so a local reference declared by a registry
// bundle fails closed whether it is relative or absolute.
func TestCatalogResolverRefusesALocalReferenceDeclaredByARegistryBundle(t *testing.T) {
	dir := catBundleDir(t, filepath.Join(t.TempDir(), "secret"), "secret", "1.0.0")
	r := (&Service{}).CatalogResolver()

	for _, ref := range []string{"../../../etc", "./neighbour", dir, "file://" + dir} {
		_, err := catResolve(t, r, ref, catalogOCIBase, "")
		if got := catCode(t, err); got != catalog.ReasonInvalidReference {
			t.Errorf("%q from a registry bundle gave %s, want %s", ref, got, catalog.ReasonInvalidReference)
		}
	}
}

func TestCatalogResolverReportsLocalFailuresByCategory(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "absent")
	unparseable := catBundleDir(t, filepath.Join(root, "broken"), "broken", "1.0.0")
	if err := os.WriteFile(filepath.Join(unparseable, "pacto.yaml"), []byte("\tnot: [yaml"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	notADir := filepath.Join(root, "file.txt")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := (&Service{}).CatalogResolver()

	for _, tc := range []struct{ name, ref, want string }{
		{"the path does not exist", missing, catalog.ReasonNotFound},
		{"the path is not a bundle directory", notADir, catalog.ReasonNotFound},
		{"the contract does not parse", unparseable, catalog.ReasonInvalidContract},
		{"the reference is empty", "", catalog.ReasonInvalidReference},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := catResolve(t, r, tc.ref, "", "")
			if got := catCode(t, err); got != tc.want {
				t.Errorf("code = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCatalogResolverFailsClosedWhenTheBundleCannotBeHashed(t *testing.T) {
	dir := catBundleDir(t, filepath.Join(t.TempDir(), "svc"), "svc", "1.0.0")
	sub := filepath.Join(dir, "docs")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	_, err := catResolve(t, (&Service{}).CatalogResolver(), dir, "", "")
	if got := catCode(t, err); got != catalog.ReasonInvalidContract {
		t.Errorf("code = %s, want %s: a bundle that cannot be hashed has no identity", got, catalog.ReasonInvalidContract)
	}
}

func TestCatalogResolverFailsClosedWhenARootPathCannotBeMadeAbsolute(t *testing.T) {
	orig := absPathFn
	absPathFn = func(string) (string, error) { return "", errors.New("no working directory") }
	t.Cleanup(func() { absPathFn = orig })

	_, err := catResolve(t, (&Service{}).CatalogResolver(), "./svc", "", "")
	if got := catCode(t, err); got != catalog.ReasonInvalidReference {
		t.Errorf("code = %s, want %s", got, catalog.ReasonInvalidReference)
	}
}

// ── failure categorisation ────────────────────────────────────────────────────

// catStubStore returns a fixed error from every registry call, for the failure
// shapes an in-process registry cannot produce.
type catStubStore struct{ err error }

func (s catStubStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", s.err
}
func (s catStubStore) Pull(context.Context, string) (*contract.Bundle, error) { return nil, s.err }
func (s catStubStore) Resolve(context.Context, string) (string, error)        { return "sha256:x", nil }
func (s catStubStore) ListTags(context.Context, string) ([]string, error)     { return nil, s.err }

func TestCatalogResolverCategorisesRegistryFailures(t *testing.T) {
	authHost := mxAuthRegistry(t, "u", "p")
	goodHost := mxPlainRegistry(t)
	_, goodClient := mxService(authn.DefaultKeychain)
	mxPush(t, goodClient, goodHost, "acme/lib", "1.0.0", mxBundle(t, "lib", "1.0.0"))

	deadHost, stop := mxClosableRegistry(t)
	stop()

	anonymous, _ := mxService(mxHostKeychain{}) // no credentials for any host
	plain, _ := mxService(authn.DefaultKeychain)

	for _, tc := range []struct {
		name string
		svc  *Service
		ref  string
		want string
	}{
		{"the registry rejects the credentials", anonymous, "oci://" + authHost + "/acme/api:1.0.0", catalog.ReasonAuthFailed},
		{"the tag does not exist", plain, "oci://" + goodHost + "/acme/lib:9.9.9", catalog.ReasonNotFound},
		{"no tag satisfies the constraint", plain, "oci://" + goodHost + "/acme/lib", catalog.ReasonNotFound},
		{"the reference does not parse", plain, "oci://" + goodHost + "/AC ME/api:1.0.0", catalog.ReasonInvalidReference},
		{"the registry is unreachable", plain, "oci://" + deadHost + "/acme/api:1.0.0", catalog.ReasonUnavailable},
		{"the artifact is not a bundle", &Service{BundleStore: catStubStore{err: &oci.InvalidBundleError{Ref: "x"}}}, "oci://ghcr.io/acme/api:1.0.0", catalog.ReasonInvalidContract},
		{"the failure has no known shape", &Service{BundleStore: catStubStore{err: errors.New("something happened")}}, "oci://ghcr.io/acme/api:1.0.0", catalog.ReasonUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraint := ""
			if tc.want == catalog.ReasonNotFound && !strings.Contains(tc.ref, ":9.9.9") {
				constraint = "^7.0.0" // a constraint no published tag satisfies
			}
			_, err := catResolve(t, tc.svc.CatalogResolver(), tc.ref, "", constraint)
			if got := catCode(t, err); got != tc.want {
				t.Fatalf("code = %s, want %s (from %v)", got, tc.want, err)
			}
			// Whatever the registry said, none of it reaches the catalog.
			for _, leak := range []string{authHost, goodHost, deadHost, "acme", "401", "something happened"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("the sanitized message %q leaked %q", err.Error(), leak)
				}
			}
		})
	}
}

func TestCatalogResolverReportsCancellationAsCancellation(t *testing.T) {
	host := mxPlainRegistry(t)
	svc, client := mxService(authn.DefaultKeychain)
	mxPush(t, client, host, "acme/lib", "1.0.0", mxBundle(t, "lib", "1.0.0"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.CatalogResolver().Resolve(ctx, catalog.ResolveRequest{Ref: "oci://" + host + "/acme/lib:1.0.0"})
	if got := catCode(t, err); got != catalog.ReasonCancelled {
		t.Errorf("code = %s, want %s (from %v)", got, catalog.ReasonCancelled, err)
	}
}

// ── the whole thing, over a real registry and real directories ────────────────

// One local root and one registry root that share a transitive dependency: one
// canonical revision, both provenance paths, both roots. This is the acceptance
// case the fake proves in pkg/catalog, proved again against real resolution.
func TestCatalogOverARealRegistryAndRealDirectories(t *testing.T) {
	host := mxPlainRegistry(t)
	svc, client := mxService(authn.DefaultKeychain)
	mxPush(t, client, host, "acme/lib", "1.0.0", mxBundle(t, "lib", "1.0.0"))
	libRef := "oci://" + host + "/acme/lib:1.0.0"
	mxPush(t, client, host, "acme/app", "1.0.0",
		mxBundle(t, "app", "1.0.0", mxDep{name: "lib", ref: libRef, compat: "^1.0.0"}))

	workspace := t.TempDir()
	local := catBundleDir(t, filepath.Join(workspace, "portal"), "portal", "1.0.0",
		mxDep{name: "lib", ref: libRef, compat: "^1.0.0"},
		mxDep{name: "widgets", ref: "./widgets", compat: "^1.0.0"})
	catBundleDir(t, filepath.Join(local, "widgets"), "widgets", "1.0.0")

	c, err := catalog.Build(context.Background(), catalog.Request{
		Roots:    []string{local, "oci://" + host + "/acme/app:1.0.0"},
		Resolver: svc.CatalogResolver(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if c.Meta().Completeness != catalog.CompletenessComplete {
		t.Fatalf("completeness = %s, limitations %+v, want a complete catalog",
			c.Meta().Completeness, c.Meta().Limitations)
	}
	if n := len(c.Revisions()); n != 4 {
		t.Fatalf("revisions = %d, want portal, widgets, app and one shared lib", n)
	}
	var lib catalog.Revision
	for _, r := range c.Revisions() {
		if r.Service.Name == "lib" {
			lib = r
		}
	}
	if lib.Content.Scheme != catalog.SchemeOCI {
		t.Fatalf("lib = %+v, want one revision identified by its registry digest", lib)
	}
	if len(lib.Roots) != 2 || len(lib.Paths) != 2 {
		t.Errorf("lib roots = %v, paths = %+v, want one revision reachable from both roots", lib.Roots, lib.Paths)
	}
	if lib.Rank != catalog.RankDirect {
		t.Errorf("lib rank = %s, want direct: it is declared by a root", lib.Rank)
	}
	for _, r := range c.Roots() {
		if !r.Resolved {
			t.Errorf("root %+v did not resolve", r)
		}
	}
}

// The same bundle published into two registry namespaces. Both repositories
// carry the identical manifest digest -- that is what mirroring is -- so content
// alone cannot say which service a revision belongs to, and the answer must not
// depend on which root was asked for first.
func TestCatalogKeepsMirroredRegistryContentAsTwoServices(t *testing.T) {
	host := mxPlainRegistry(t)
	svc, client := mxService(authn.DefaultKeychain)
	mxPush(t, client, host, "acme/lib", "1.0.0", mxBundle(t, "lib", "1.0.0"))
	libRef := "oci://" + host + "/acme/lib:1.0.0"
	api := mxBundle(t, "api", "1.0.0", mxDep{name: "lib", ref: libRef, compat: "^1.0.0"})
	mxPush(t, client, host, "alpha/api", "1.0.0", api)
	mxPush(t, client, host, "beta/api", "1.0.0", api)

	alphaDigest, err := client.Resolve(context.Background(), host+"/alpha/api:1.0.0")
	if err != nil {
		t.Fatalf("resolve alpha: %v", err)
	}
	betaDigest, err := client.Resolve(context.Background(), host+"/beta/api:1.0.0")
	if err != nil {
		t.Fatalf("resolve beta: %v", err)
	}
	if alphaDigest != betaDigest {
		t.Fatalf("the two repositories hold different digests (%s vs %s); the fixture is not a mirror and would prove nothing",
			alphaDigest, betaDigest)
	}

	alphaRef, betaRef := "oci://"+host+"/alpha/api:1.0.0", "oci://"+host+"/beta/api:1.0.0"
	buildRoots := func(roots ...string) *catalog.Catalog {
		t.Helper()
		c, err := catalog.Build(context.Background(), catalog.Request{Roots: roots, Resolver: svc.CatalogResolver()})
		if err != nil {
			t.Fatalf("Build%v: %v", roots, err)
		}
		if c.Meta().Completeness != catalog.CompletenessComplete {
			t.Fatalf("completeness = %s, limitations %+v, want complete", c.Meta().Completeness, c.Meta().Limitations)
		}
		return c
	}

	c := buildRoots(alphaRef, betaRef)
	assertMirroredPair(t, c, alphaDigest, []string{host + "/alpha", host + "/beta"})

	// Asking in the other order is the same question.
	reversed := buildRoots(betaRef, alphaRef)
	if got, want := reversed.Meta().CatalogID, c.Meta().CatalogID; got != want {
		t.Errorf("catalogId = %s asking beta first, %s asking alpha first; root order is not catalog truth", got, want)
	}
}

// assertMirroredPair checks the whole shape one mirrored bundle produces: two
// revisions for the one digest, one per publishing domain, declaring the shared
// dependency separately.
func assertMirroredPair(t *testing.T, c *catalog.Catalog, mirrored string, wantDomains []string) {
	t.Helper()
	var domains []string
	for _, r := range c.Revisions() {
		if r.Content.Digest == mirrored {
			domains = append(domains, r.Service.Domain)
		}
	}
	if !slices.Equal(domains, wantDomains) {
		t.Errorf("the mirrored digest belongs to services %v, want %v: one revision per publishing domain", domains, wantDomains)
	}
	if n := len(c.Revisions()); n != 3 {
		t.Errorf("revisions = %d, want both mirrors plus the shared lib", n)
	}
	if cf := c.Conflicts(); len(cf) != 0 {
		t.Errorf("two mirrors of one bundle are two services, not a disagreement: %+v", cf)
	}
	decls := map[catalog.DeclarationID]bool{}
	for _, e := range c.Edges() {
		decls[e.Declaration] = true
	}
	if len(c.Edges()) != 2 || len(decls) != 2 {
		t.Errorf("edges = %+v, want one per mirror from two distinct declarations", c.Edges())
	}
	for _, r := range c.Revisions() {
		if r.Service.Name != "lib" {
			continue
		}
		if len(r.Paths) != 2 || slices.Equal(r.Paths[0].Steps, r.Paths[1].Steps) {
			t.Errorf("lib paths = %+v, want a distinct step naming each mirror that declared it", r.Paths)
		}
	}
}

// A registry bundle declaring a local path is a partial catalog with a named
// gap, never a silent read of whatever that path points at.
func TestCatalogRefusesALocalPathDeclaredByARegistryRoot(t *testing.T) {
	host := mxPlainRegistry(t)
	svc, client := mxService(authn.DefaultKeychain)
	mxPush(t, client, host, "acme/app", "1.0.0",
		mxBundle(t, "app", "1.0.0", mxDep{name: "escape", ref: "../../../etc", compat: "^1.0.0"}))

	c, err := catalog.Build(context.Background(), catalog.Request{
		Roots:    []string{"oci://" + host + "/acme/app:1.0.0"},
		Resolver: svc.CatalogResolver(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if c.Meta().Completeness != catalog.CompletenessPartial {
		t.Errorf("completeness = %s, want partial: the declaration is refused, not absent", c.Meta().Completeness)
	}
	un := c.Unresolved()
	if len(un) != 1 || un[0].Reason.Code != catalog.ReasonInvalidReference {
		t.Fatalf("unresolved = %+v, want the refused local declaration reported", un)
	}
	if len(c.Revisions()) != 1 {
		t.Errorf("revisions = %+v, want only the root: nothing outside the registry was read", c.Revisions())
	}
}
