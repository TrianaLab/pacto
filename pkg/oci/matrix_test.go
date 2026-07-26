package oci_test

// This file is the exhaustive, hermetic OCI resolution matrix for the CACHE and
// VERSION dimensions. Every case runs against real in-process OCI registries
// (go-containerregistry registry + net/http/httptest), including at least one
// registry behind HTTP Basic auth, so the suite is fully CI-runnable with no
// external Docker or network. The multi-registry graph / order-independence
// dimensions live in internal/app/resolution_matrix_test.go (they exercise the
// real dependency fetcher + lock builder).

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// ── hermetic registry harness ────────────────────────────────────────────────

// plainRegistry starts an unauthenticated in-process OCI registry and returns
// its host (127.0.0.1:PORT — always a host:port reference).
func plainRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// closableRegistry is a plain registry plus an explicit stop func, so a test can
// take the registry offline to model an unreachable / interrupted registry.
func closableRegistry(t *testing.T) (host string, stop func()) {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	var once sync.Once
	stop = func() { once.Do(srv.Close) }
	t.Cleanup(stop)
	return strings.TrimPrefix(srv.URL, "http://"), stop
}

// authRegistry starts an in-process OCI registry behind HTTP Basic auth. Requests
// without the exact credentials get a 401 + WWW-Authenticate: Basic challenge,
// which go-containerregistry's transport answers with the keychain's basic creds.
func authRegistry(t *testing.T, user, pass string) string {
	t.Helper()
	reg := registry.New()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="pacto-test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		reg.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// hostKeychain resolves credentials strictly per registry host, so it also proves
// credentials are host-scoped: a host with no entry resolves anonymous.
type hostKeychain struct{ creds map[string]authn.AuthConfig }

func (k hostKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	if c, ok := k.creds[r.RegistryStr()]; ok {
		return authn.FromConfig(c), nil
	}
	return authn.Anonymous, nil
}

func insecureClient(kc authn.Keychain) *oci.Client {
	return oci.NewClient(kc, oci.WithNameOptions(name.Insecure))
}

// depSpec is one rendered dependency line for a synthesized pacto.yaml.
type depSpec struct {
	name, ref, compat string
	required          bool
}

// renderPactoYAML produces a minimal, parseable pacto.yaml with the given
// identity and dependency edges.
func renderPactoYAML(name, version string, deps []depSpec) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "pactoVersion: \"2.0\"\nservice:\n  name: %s\n  version: \"%s\"\n", name, version)
	b.WriteString("interfaces:\n  - name: api\n    type: openapi\n    ref: openapi.yaml\n    visibility: public\n")
	if len(deps) > 0 {
		b.WriteString("dependencies:\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "  - name: %s\n    ref: %s\n    required: %t\n    compatibility: \"%s\"\n",
				d.name, d.ref, d.required, d.compat)
		}
	}
	b.WriteString("workload: service\nstate:\n  type: stateless\n  persistence:\n    scope: local\n    durability: ephemeral\n  dataCriticality: low\n")
	return []byte(b.String())
}

// makeBundle builds an in-memory bundle from a rendered pacto.yaml. The FS carries
// the pacto.yaml (Pull reconstructs the contract from it) plus a marker file so
// two bundles with the same identity but different markers get distinct digests.
func makeBundle(t *testing.T, name, version, marker string, deps ...depSpec) *contract.Bundle {
	t.Helper()
	y := renderPactoYAML(name, version, deps)
	c, err := contract.Parse(bytes.NewReader(y))
	if err != nil {
		t.Fatalf("contract.Parse(%s): %v", name, err)
	}
	return &contract.Bundle{
		Contract: c,
		RawYAML:  y,
		FS: fstest.MapFS{
			"pacto.yaml":   &fstest.MapFile{Data: y},
			"openapi.yaml": &fstest.MapFile{Data: []byte("openapi: '3.0.0'\ninfo:\n  title: T\n  version: '1.0.0'\npaths: {}\n")},
			"marker.txt":   &fstest.MapFile{Data: []byte(marker)},
		},
	}
}

// pushTag pushes a bundle to host/repo:tag and fails the test on error.
func pushTag(t *testing.T, client *oci.Client, host, repo, tag string, b *contract.Bundle) {
	t.Helper()
	if _, err := client.Push(context.Background(), host+"/"+repo+":"+tag, b); err != nil {
		t.Fatalf("push %s/%s:%s: %v", host, repo, tag, err)
	}
}

// countingProxy delegates to a real BundleStore while counting calls, so cache
// behavior (inner pulls avoided / forced) is asserted against real registries.
type countingProxy struct {
	inner    oci.BundleStore
	pulls    atomic.Int32
	resolves atomic.Int32
	lists    atomic.Int32
}

func (p *countingProxy) Push(ctx context.Context, ref string, b *contract.Bundle) (string, error) {
	return p.inner.Push(ctx, ref, b)
}
func (p *countingProxy) Pull(ctx context.Context, ref string) (*contract.Bundle, error) {
	p.pulls.Add(1)
	return p.inner.Pull(ctx, ref)
}
func (p *countingProxy) Resolve(ctx context.Context, ref string) (string, error) {
	p.resolves.Add(1)
	return p.inner.Resolve(ctx, ref)
}
func (p *countingProxy) ListTags(ctx context.Context, repo string) ([]string, error) {
	p.lists.Add(1)
	return p.inner.ListTags(ctx, repo)
}

// useTempCacheDir points the disk cache at a fresh temp dir for the test and
// returns the resolved home so multiple CachedStores can share the same disk
// (modeling separate process invocations against one on-disk cache).
func useTempCacheDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old := oci.SetUserHomeDirFn(func() (string, error) { return dir, nil })
	t.Cleanup(func() { oci.SetUserHomeDirFn(old) })
}

// ── CACHE MATRIX ─────────────────────────────────────────────────────────────

// Cold cache: the first resolve misses memory and disk and pulls from the
// registry, then persists to disk.
func TestMatrix_Cache_ColdMissPullsAndPersists(t *testing.T) {
	useTempCacheDir(t)
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "m"))

	proxy := &countingProxy{inner: client}
	res := oci.NewResolver(oci.NewCachedStore(proxy))

	b, err := res.Resolve(context.Background(), host+"/svc/a:1.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("cold Resolve: %v", err)
	}
	if b.Contract.Service.Name != "a" {
		t.Errorf("name = %q, want a", b.Contract.Service.Name)
	}
	if got := proxy.pulls.Load(); got != 1 {
		t.Errorf("cold cache: inner pulls = %d, want 1", got)
	}
}

// Warm cache: repeated resolves in the same process serve from the in-memory
// cache without any further inner pulls.
func TestMatrix_Cache_WarmInMemoryServesWithoutInnerPull(t *testing.T) {
	useTempCacheDir(t)
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "m"))

	proxy := &countingProxy{inner: client}
	res := oci.NewResolver(oci.NewCachedStore(proxy))
	ref := host + "/svc/a:1.0.0"

	for i := range 4 {
		if _, err := res.Resolve(context.Background(), ref, oci.RemoteAllowed); err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
	}
	if got := proxy.pulls.Load(); got != 1 {
		t.Errorf("warm cache: inner pulls = %d, want 1", got)
	}
}

// Warm disk cache across "processes": a second CachedStore (fresh memory) sharing
// the same on-disk cache serves from disk even after the registry is offline.
func TestMatrix_Cache_WarmDiskServesOffline(t *testing.T) {
	useTempCacheDir(t)
	host, stop := closableRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "m"))
	ref := host + "/svc/a:1.0.0"

	// Process 1: populate the disk cache.
	if _, err := oci.NewResolver(oci.NewCachedStore(client)).Resolve(context.Background(), ref, oci.RemoteAllowed); err != nil {
		t.Fatalf("process 1 Resolve: %v", err)
	}

	// Registry offline; process 2 (fresh memory, same disk) must still resolve.
	stop()
	proxy := &countingProxy{inner: client}
	b, err := oci.NewResolver(oci.NewCachedStore(proxy)).Resolve(context.Background(), ref, oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("offline disk Resolve: %v", err)
	}
	if b.Contract.Service.Name != "a" {
		t.Errorf("name = %q, want a", b.Contract.Service.Name)
	}
	if got := proxy.pulls.Load(); got != 0 {
		t.Errorf("warm disk: inner pulls = %d, want 0 (served from disk)", got)
	}
}

// --no-cache (DisableCache) skips disk reads: with the disk warm but the registry
// offline, resolution MUST fail because the disk is not consulted.
func TestMatrix_Cache_NoCacheSkipsDiskReads(t *testing.T) {
	useTempCacheDir(t)
	host, stop := closableRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "m"))
	ref := host + "/svc/a:1.0.0"

	// Warm the disk cache.
	if _, err := oci.NewResolver(oci.NewCachedStore(client)).Resolve(context.Background(), ref, oci.RemoteAllowed); err != nil {
		t.Fatalf("warm Resolve: %v", err)
	}

	stop() // registry offline
	cs := oci.NewCachedStore(client)
	cs.DisableCache() // --no-cache: disk reads skipped
	if _, err := oci.NewResolver(cs).Resolve(context.Background(), ref, oci.RemoteAllowed); err == nil {
		t.Fatal("expected failure: --no-cache must skip disk and the registry is offline")
	}
}

// Tag moves to a new digest: --no-cache in a fresh process re-fetches the NEW
// content, never the stale cached bundle.
func TestMatrix_Cache_NoCacheRefetchesAfterTagMoves(t *testing.T) {
	useTempCacheDir(t)
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	ref := host + "/svc/moving:stable"

	// v1 published + cached to disk (warm process).
	pushTag(t, client, host, "svc/moving", "stable", makeBundle(t, "moving", "1.0.0", "v1"))
	if _, err := oci.NewResolver(oci.NewCachedStore(client)).Resolve(context.Background(), ref, oci.RemoteAllowed); err != nil {
		t.Fatalf("warm Resolve: %v", err)
	}

	// Tag moved to a new digest (v2).
	pushTag(t, client, host, "svc/moving", "stable", makeBundle(t, "moving", "2.0.0", "v2"))

	cs := oci.NewCachedStore(client)
	cs.DisableCache()
	b, err := oci.NewResolver(cs).Resolve(context.Background(), ref, oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("--no-cache Resolve after tag move: %v", err)
	}
	if b.Contract.Service.Version != "2.0.0" {
		t.Errorf("--no-cache served version %q, want fresh 2.0.0 (stale content)", b.Contract.Service.Version)
	}
}

// A version tag is treated as immutable: a warm disk cache keyed by an immutable
// tag returns byte-identical content across processes. This is the assumption the
// offline by-ref cache relies on; freshness for moving tags is opt-in via --no-cache.
func TestMatrix_Cache_ImmutableTagStableAcrossProcesses(t *testing.T) {
	useTempCacheDir(t)
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.2.3", makeBundle(t, "a", "1.2.3", "immutable"))
	ref := host + "/svc/a:1.2.3"

	first, err := oci.NewResolver(oci.NewCachedStore(client)).Resolve(context.Background(), ref, oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("process 1 Resolve: %v", err)
	}
	second, err := oci.NewResolver(oci.NewCachedStore(client)).Resolve(context.Background(), ref, oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("process 2 Resolve: %v", err)
	}
	m1, _ := fs.ReadFile(first.FS, "marker.txt")
	m2, _ := fs.ReadFile(second.FS, "marker.txt")
	if !bytes.Equal(m1, m2) || string(m1) != "immutable" {
		t.Errorf("immutable tag content diverged: %q vs %q", m1, m2)
	}
}

// Two distinct tags pointing at one pushed bundle resolve to the same manifest
// digest and identical content.
func TestMatrix_Cache_TwoTagsOneDigest(t *testing.T) {
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	b := makeBundle(t, "a", "1.0.0", "shared")
	pushTag(t, client, host, "svc/a", "1.0.0", b)
	pushTag(t, client, host, "svc/a", "stable", b)

	ctx := context.Background()
	d1, err := client.Resolve(ctx, host+"/svc/a:1.0.0")
	if err != nil {
		t.Fatalf("Resolve tag 1.0.0: %v", err)
	}
	d2, err := client.Resolve(ctx, host+"/svc/a:stable")
	if err != nil {
		t.Fatalf("Resolve tag stable: %v", err)
	}
	if d1 != d2 {
		t.Errorf("two tags -> two digests: %s vs %s", d1, d2)
	}
}

// A digest-pinned reference is immutable: it resolves to identical content even
// after the human-readable tag is moved to a different digest.
func TestMatrix_Version_DigestPinIsImmutable(t *testing.T) {
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "pinned"))

	ctx := context.Background()
	digest, err := client.Resolve(ctx, host+"/svc/a:1.0.0")
	if err != nil {
		t.Fatalf("Resolve digest: %v", err)
	}
	pinnedRef := host + "/svc/a@" + digest

	// Move the tag to entirely different content.
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "9.9.9", "moved"))

	res := oci.NewResolver(oci.NewCachedStore(client))
	b, err := res.Resolve(ctx, pinnedRef, oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("Resolve digest-pinned ref: %v", err)
	}
	if b.Contract.Service.Version != "1.0.0" {
		t.Errorf("digest pin resolved version %q, want immutable 1.0.0", b.Contract.Service.Version)
	}
	m, _ := fs.ReadFile(b.FS, "marker.txt")
	if string(m) != "pinned" {
		t.Errorf("digest pin marker = %q, want pinned", m)
	}
}

// Same repo path + tag on two different registries are distinct identities: the
// full host-qualified ref is what identifies an artifact.
func TestMatrix_Version_SameNameDifferentRegistries(t *testing.T) {
	hostA := plainRegistry(t)
	hostB := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, hostA, "org/svc", "1.0.0", makeBundle(t, "svc", "1.0.0", "from-A"))
	pushTag(t, client, hostB, "org/svc", "1.0.0", makeBundle(t, "svc", "1.0.0", "from-B"))

	ctx := context.Background()
	res := oci.NewResolver(oci.NewCachedStore(client))
	ba, err := res.Resolve(ctx, hostA+"/org/svc:1.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("Resolve from A: %v", err)
	}
	bb, err := res.Resolve(ctx, hostB+"/org/svc:1.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("Resolve from B: %v", err)
	}
	ma, _ := fs.ReadFile(ba.FS, "marker.txt")
	mb, _ := fs.ReadFile(bb.FS, "marker.txt")
	if string(ma) != "from-A" || string(mb) != "from-B" {
		t.Errorf("distinct identity failed: A=%q B=%q", ma, mb)
	}
}

// Concurrent COLD resolution of the same artifact is race-safe: every goroutine
// gets valid, identical content (validated under -race).
func TestMatrix_Cache_ConcurrentColdResolveSameArtifact(t *testing.T) {
	useTempCacheDir(t)
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "concurrent"))
	ref := host + "/svc/a:1.0.0"

	res := oci.NewResolver(oci.NewCachedStore(client))
	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	names := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := res.Resolve(context.Background(), ref, oci.RemoteAllowed)
			errs[i] = err
			if err == nil {
				names[i] = b.Contract.Service.Name
			}
		}()
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if names[i] != "a" {
			t.Fatalf("goroutine %d: name = %q, want a", i, names[i])
		}
	}
}

// Concurrent WARM reads never re-pull: after one priming pull, N concurrent
// resolves all serve from the in-memory cache.
func TestMatrix_Cache_ConcurrentWarmReadsSingleInnerPull(t *testing.T) {
	useTempCacheDir(t)
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "warm"))
	ref := host + "/svc/a:1.0.0"

	proxy := &countingProxy{inner: client}
	res := oci.NewResolver(oci.NewCachedStore(proxy))
	if _, err := res.Resolve(context.Background(), ref, oci.RemoteAllowed); err != nil {
		t.Fatalf("prime Resolve: %v", err)
	}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = res.Resolve(context.Background(), ref, oci.RemoteAllowed)
		}()
	}
	wg.Wait()
	if got := proxy.pulls.Load(); got != 1 {
		t.Errorf("warm concurrent: inner pulls = %d, want 1", got)
	}
}

// Interrupted resolution then retry: a cancelled context fails the first attempt
// and poisons nothing; a retry with a fresh context re-hits the registry and
// succeeds. The two inner pulls prove the failed attempt cached no stale entry.
func TestMatrix_Cache_InterruptedThenRetry(t *testing.T) {
	useTempCacheDir(t)
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "retry"))
	ref := host + "/svc/a:1.0.0"

	proxy := &countingProxy{inner: client}
	res := oci.NewResolver(oci.NewCachedStore(proxy))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := res.Resolve(cancelled, ref, oci.RemoteAllowed); err == nil {
		t.Fatal("expected failure on cancelled context")
	}

	b, err := res.Resolve(context.Background(), ref, oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("retry Resolve: %v", err)
	}
	if b.Contract.Service.Name != "a" {
		t.Errorf("retry name = %q, want a", b.Contract.Service.Name)
	}
	if got := proxy.pulls.Load(); got != 2 {
		t.Errorf("interrupted+retry: inner pulls = %d, want 2 (nothing cached on failure)", got)
	}
}

// ── VERSION MATRIX ───────────────────────────────────────────────────────────

// Multiple versions satisfying a range: the highest matching version is selected,
// deterministically across repeated resolves.
func TestMatrix_Version_RangeSelectsHighestDeterministically(t *testing.T) {
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "2.0.0"} {
		pushTag(t, client, host, "svc/a", v, makeBundle(t, "a", v, "m"))
	}
	res := oci.NewResolver(oci.NewCachedStore(client))
	ref := host + "/svc/a"

	for i := range 5 {
		b, err := res.ResolveConstrained(context.Background(), ref, "^1.0.0", oci.RemoteAllowed)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if b.Contract.Service.Version != "1.2.0" {
			t.Fatalf("iteration %d: version = %q, want 1.2.0 (highest in ^1.0.0)", i, b.Contract.Service.Version)
		}
	}
}

// Pre-release versions: a stable constraint excludes pre-releases, while an
// unconstrained resolve orders a pre-release below its final release.
func TestMatrix_Version_PreRelease(t *testing.T) {
	host := plainRegistry(t)
	client := insecureClient(authn.DefaultKeychain)
	for _, v := range []string{"1.0.0", "1.1.0", "2.0.0-rc.1"} {
		pushTag(t, client, host, "svc/a", v, makeBundle(t, "a", v, "m"))
	}
	res := oci.NewResolver(oci.NewCachedStore(client))
	ref := host + "/svc/a"
	ctx := context.Background()

	// Stable constraint must not select the 2.0.0-rc.1 pre-release.
	b, err := res.ResolveConstrained(ctx, ref, "^1.0.0", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("constrained Resolve: %v", err)
	}
	if b.Contract.Service.Version != "1.1.0" {
		t.Errorf("^1.0.0 selected %q, want 1.1.0 (pre-release excluded)", b.Contract.Service.Version)
	}

	// Unconstrained: highest overall, and 2.0.0-rc.1 sorts above 1.1.0.
	b, err = res.ResolveConstrained(ctx, ref, "", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("unconstrained Resolve: %v", err)
	}
	if b.Contract.Service.Version != "2.0.0-rc.1" {
		t.Errorf("unconstrained selected %q, want 2.0.0-rc.1", b.Contract.Service.Version)
	}
}

// A bare host:port reference (no tag) resolves via ListTags — the port colon is
// not mistaken for a tag separator.
func TestMatrix_Version_HostPortBareRefResolves(t *testing.T) {
	host := plainRegistry(t) // 127.0.0.1:PORT
	if !strings.Contains(host, ":") {
		t.Fatalf("expected host:port, got %q", host)
	}
	client := insecureClient(authn.DefaultKeychain)
	pushTag(t, client, host, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "m"))
	pushTag(t, client, host, "svc/a", "1.1.0", makeBundle(t, "a", "1.1.0", "m"))

	res := oci.NewResolver(oci.NewCachedStore(client))
	b, err := res.ResolveConstrained(context.Background(), host+"/svc/a", "", oci.RemoteAllowed)
	if err != nil {
		t.Fatalf("bare host:port Resolve: %v", err)
	}
	if b.Contract.Service.Version != "1.1.0" {
		t.Errorf("host:port bare ref selected %q, want 1.1.0", b.Contract.Service.Version)
	}
}

// Mixed authenticated + unauthenticated registries resolve through one host-scoped
// keychain: the auth'd host uses its credentials, the plain host resolves anonymous.
func TestMatrix_Version_MixedAuthAndPlain(t *testing.T) {
	plain := plainRegistry(t)
	auth := authRegistry(t, "robot", "s3cret")
	kc := hostKeychain{creds: map[string]authn.AuthConfig{
		auth: {Username: "robot", Password: "s3cret"},
	}}
	client := insecureClient(kc)
	pushTag(t, client, plain, "svc/plain", "1.0.0", makeBundle(t, "plain", "1.0.0", "p"))
	pushTag(t, client, auth, "svc/auth", "1.0.0", makeBundle(t, "auth", "1.0.0", "a"))

	res := oci.NewResolver(oci.NewCachedStore(client))
	ctx := context.Background()
	if b, err := res.Resolve(ctx, plain+"/svc/plain:1.0.0", oci.RemoteAllowed); err != nil || b.Contract.Service.Name != "plain" {
		t.Fatalf("plain resolve: b=%v err=%v", b, err)
	}
	if b, err := res.Resolve(ctx, auth+"/svc/auth:1.0.0", oci.RemoteAllowed); err != nil || b.Contract.Service.Name != "auth" {
		t.Fatalf("auth resolve: b=%v err=%v", b, err)
	}
}

// Credentials are host-scoped: the keychain holds creds only for registry #1, so
// registry #2 (different creds) rejects with an AuthenticationError.
func TestMatrix_Version_HostScopedCredentials(t *testing.T) {
	auth1 := authRegistry(t, "u1", "p1")
	auth2 := authRegistry(t, "u2", "p2")
	kc := hostKeychain{creds: map[string]authn.AuthConfig{
		auth1: {Username: "u1", Password: "p1"},
	}}
	client := insecureClient(kc)
	pushTag(t, client, auth1, "svc/a", "1.0.0", makeBundle(t, "a", "1.0.0", "m"))

	res := oci.NewResolver(oci.NewCachedStore(client))
	ctx := context.Background()
	if _, err := res.Resolve(ctx, auth1+"/svc/a:1.0.0", oci.RemoteAllowed); err != nil {
		t.Fatalf("auth1 (scoped creds) should resolve: %v", err)
	}
	// auth2 has no creds in the keychain -> anonymous -> rejected.
	_, err := res.Resolve(ctx, auth2+"/svc/a:1.0.0", oci.RemoteAllowed)
	if err == nil {
		t.Fatal("expected auth failure on registry without scoped credentials")
	}
}
