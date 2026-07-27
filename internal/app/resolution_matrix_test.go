package app

// Hermetic, multi-registry OCI resolution matrix for the GRAPH + LOCK dimensions.
// Every case runs against real in-process OCI registries (go-containerregistry
// registry + httptest), including one behind HTTP Basic auth, exercising the real
// dependency fetcher (depFetcher), recursive graph resolver, and lock builder end
// to end — no external Docker or network.
//
// The load-bearing property is TestResolutionMatrix_OrderIndependence: shuffling
// the declared dependency order must never change the resolved graph or the lock.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// ── hermetic registry harness (test-local; mirrors pkg/oci matrix harness) ────

func mxPlainRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func mxClosableRegistry(t *testing.T) (host string, stop func()) {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	var once sync.Once
	stop = func() { once.Do(srv.Close) }
	t.Cleanup(stop)
	return strings.TrimPrefix(srv.URL, "http://"), stop
}

func mxAuthRegistry(t *testing.T, user, pass string) string {
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

type mxHostKeychain struct{ creds map[string]authn.AuthConfig }

func (k mxHostKeychain) Resolve(r authn.Resource) (authn.Authenticator, error) {
	if c, ok := k.creds[r.RegistryStr()]; ok {
		return authn.FromConfig(c), nil
	}
	return authn.Anonymous, nil
}

type mxDep struct{ name, ref, compat string }

func mxRenderYAML(name, version string, deps []mxDep) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "pactoVersion: \"2.0\"\nservice:\n  name: %s\n  version: \"%s\"\n", name, version)
	b.WriteString("interfaces:\n  - name: api\n    type: openapi\n    ref: openapi.yaml\n    visibility: public\n")
	if len(deps) > 0 {
		b.WriteString("dependencies:\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "  - name: %s\n    ref: %s\n    required: true\n    compatibility: \"%s\"\n", d.name, d.ref, d.compat)
		}
	}
	b.WriteString("workload: service\nstate:\n  type: stateless\n  persistence:\n    scope: local\n    durability: ephemeral\n  dataCriticality: low\n")
	return []byte(b.String())
}

func mxBundle(t *testing.T, name, version string, deps ...mxDep) *contract.Bundle {
	t.Helper()
	y := mxRenderYAML(name, version, deps)
	c, err := contract.Parse(strings.NewReader(string(y)))
	if err != nil {
		t.Fatalf("contract.Parse(%s): %v", name, err)
	}
	return &contract.Bundle{
		Contract: c,
		RawYAML:  y,
		FS: fstest.MapFS{
			"pacto.yaml":   &fstest.MapFile{Data: y},
			"openapi.yaml": &fstest.MapFile{Data: []byte("openapi: '3.0.0'\ninfo:\n  title: T\n  version: '1.0.0'\npaths: {}\n")},
		},
	}
}

func mxPush(t *testing.T, client *oci.Client, host, repo, tag string, b *contract.Bundle) {
	t.Helper()
	if _, err := client.Push(context.Background(), host+"/"+repo+":"+tag, b); err != nil {
		t.Fatalf("push %s/%s:%s: %v", host, repo, tag, err)
	}
}

// mxService builds a Service whose BundleStore is a real OCI client with the given
// keychain (insecure name parsing for http in-process registries).
func mxService(kc authn.Keychain) (*Service, *oci.Client) {
	client := oci.NewClient(kc, oci.WithNameOptions(name.Insecure))
	return &Service{BundleStore: client}, client
}

// ── order-independence proof (LOAD-BEARING) ──────────────────────────────────

// semanticGraph reduces a resolved graph to its order-independent essence: the set
// of resolved services (name@version) and the set of parent->child dependency
// relationships. The shared-vs-full-node distinction (which path won the dedup
// race) is deliberately discarded — only the relationships are semantic.
func semanticGraph(root *graph.Node) (nodes, edges []string) {
	nodeSet := map[string]bool{root.Name + "@" + root.Version: true}
	edgeSet := map[string]bool{}
	visited := map[string]bool{}
	var visit func(n *graph.Node)
	visit = func(n *graph.Node) {
		if visited[n.Name] {
			return
		}
		visited[n.Name] = true
		for _, e := range n.Dependencies {
			if e.Node == nil {
				continue
			}
			nodeSet[e.Node.Name+"@"+e.Node.Version] = true
			edgeSet[n.Name+"->"+e.Node.Name] = true
			if !e.Shared {
				visit(e.Node)
			}
		}
	}
	visit(root)
	nodes = keys(nodeSet)
	edges = keys(edgeSet)
	return nodes, edges
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// seededOrderings returns count distinct index orderings of n elements, always
// including the identity and reverse, the rest drawn from a seeded PRNG so the run
// is deterministic and reproducible.
func seededOrderings(n, count int, seed int64) [][]int {
	base := make([]int, n)
	for i := range base {
		base[i] = i
	}
	clone := func(s []int) []int { return append([]int(nil), s...) }
	rev := clone(base)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	out := [][]int{clone(base), rev}
	seen := map[string]bool{fmt.Sprint(base): true, fmt.Sprint(rev): true}
	r := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test shuffling, not security
	for len(out) < count {
		o := clone(base)
		r.Shuffle(n, func(i, j int) { o[i], o[j] = o[j], o[i] })
		if key := fmt.Sprint(o); !seen[key] {
			seen[key] = true
			out = append(out, o)
		}
	}
	return out
}

func TestResolutionMatrix_OrderIndependence(t *testing.T) {
	regA := mxPlainRegistry(t)
	regAuth := mxAuthRegistry(t, "robot", "s3cret")
	kc := mxHostKeychain{creds: map[string]authn.AuthConfig{regAuth: {Username: "robot", Password: "s3cret"}}}
	svc, client := mxService(kc)

	refD := "oci://" + regA + "/svc/d"
	refE := "oci://" + regAuth + "/svc/e"
	refA := "oci://" + regA + "/svc/a"
	refB := "oci://" + regAuth + "/svc/b"
	refC := "oci://" + regA + "/svc/c"
	const cc = "^1.0.0" // one constraint per target keeps the lock deterministic

	// Leaves. D carries a version range so semver selection is exercised too.
	mxPush(t, client, regA, "svc/d", "1.0.0", mxBundle(t, "d", "1.0.0"))
	mxPush(t, client, regA, "svc/d", "1.1.0", mxBundle(t, "d", "1.1.0"))
	mxPush(t, client, regAuth, "svc/e", "1.0.0", mxBundle(t, "e", "1.0.0"))
	// A and B form a diamond over D and E; C shares D. D and E are reached through
	// multiple paths and also declared directly on the root below.
	mxPush(t, client, regA, "svc/a", "1.0.0", mxBundle(t, "a", "1.0.0", mxDep{"d", refD, cc}, mxDep{"e", refE, cc}))
	mxPush(t, client, regAuth, "svc/b", "1.0.0", mxBundle(t, "b", "1.0.0", mxDep{"d", refD, cc}, mxDep{"e", refE, cc}))
	mxPush(t, client, regA, "svc/c", "1.0.0", mxBundle(t, "c", "1.0.0", mxDep{"d", refD, cc}))

	directDeps := []mxDep{{"a", refA, cc}, {"b", refB, cc}, {"c", refC, cc}, {"d", refD, cc}, {"e", refE, cc}}
	rootRef := "oci://" + regA + "/svc/root:1.0.0"
	ctx := context.Background()

	buildFor := func(order []int) (lockBytes []byte, nodes, edges []string) {
		deps := make([]mxDep, len(order))
		for i, idx := range order {
			deps[i] = directDeps[idx]
		}
		root := mxBundle(t, "root", "1.0.0", deps...)
		lk, err := svc.buildLock(ctx, rootRef, root, nil)
		if err != nil {
			t.Fatalf("buildLock(order=%v): %v", order, err)
		}
		data, err := lk.Marshal()
		if err != nil {
			t.Fatalf("Marshal(order=%v): %v", order, err)
		}
		res := graph.ResolveWithOptions(ctx, root.Contract, svc.newDepFetcher(rootRef), graph.ResolveOptions{IncludeReferences: true})
		n, e := semanticGraph(res.Root)
		return data, n, e
	}

	orderings := seededOrderings(len(directDeps), 24, 1)
	var wantLock []byte
	var wantNodes, wantEdges []string
	for i, order := range orderings {
		lockBytes, nodes, edges := buildFor(order)
		if i == 0 {
			wantLock, wantNodes, wantEdges = lockBytes, nodes, edges
			// Sanity: the closure is the full {a,b,c,d,e}; D resolved to 1.1.0.
			if len(wantNodes) != 6 { // root + a,b,c,d,e
				t.Fatalf("expected 6 nodes (root+5), got %d: %v", len(wantNodes), wantNodes)
			}
			if !strings.Contains(string(wantLock), "version: 1.1.0") {
				t.Fatalf("expected D resolved to 1.1.0 (semver range) in lock:\n%s", wantLock)
			}
			continue
		}
		if string(lockBytes) != string(wantLock) {
			t.Fatalf("lock differs for ordering %v:\n--- want ---\n%s\n--- got ---\n%s", order, wantLock, lockBytes)
		}
		if fmt.Sprint(nodes) != fmt.Sprint(wantNodes) {
			t.Fatalf("graph nodes differ for ordering %v:\nwant %v\ngot  %v", order, wantNodes, nodes)
		}
		if fmt.Sprint(edges) != fmt.Sprint(wantEdges) {
			t.Fatalf("graph edges differ for ordering %v:\nwant %v\ngot  %v", order, wantEdges, edges)
		}
	}
}

// ── preserved / completed behaviors ──────────────────────────────────────────

// Linear multi-registry chain across a plain and an auth'd registry.
func TestResolutionMatrix_LinearMultiRegistryChain(t *testing.T) {
	regA := mxPlainRegistry(t)
	regAuth := mxAuthRegistry(t, "u", "p")
	kc := mxHostKeychain{creds: map[string]authn.AuthConfig{regAuth: {Username: "u", Password: "p"}}}
	svc, client := mxService(kc)

	refB := "oci://" + regAuth + "/svc/b"
	refC := "oci://" + regA + "/svc/c"
	mxPush(t, client, regA, "svc/c", "1.0.0", mxBundle(t, "c", "1.0.0"))
	mxPush(t, client, regAuth, "svc/b", "1.0.0", mxBundle(t, "b", "1.0.0", mxDep{"c", refC, "^1.0.0"}))
	root := mxBundle(t, "root", "1.0.0", mxDep{"b", refB, "^1.0.0"})

	lk, err := svc.buildLock(context.Background(), "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	if _, ok := lk.Dependency("b"); !ok {
		t.Error("missing dependency b")
	}
	c, ok := lk.Dependency("c")
	if !ok {
		t.Fatal("missing dependency c")
	}
	if !strings.HasPrefix(c.Digest, "sha256:") {
		t.Errorf("c.Digest = %q, want sha256:", c.Digest)
	}
	b, _ := lk.Dependency("b")
	if len(b.DependsOn) != 1 || b.DependsOn[0] != "c" {
		t.Errorf("b.DependsOn = %v, want [c]", b.DependsOn)
	}
}

// Diamond dependency dedups to a single lock entry reached through multiple paths.
func TestResolutionMatrix_DiamondDedup(t *testing.T) {
	regA := mxPlainRegistry(t)
	svc, client := mxService(mxHostKeychain{})
	refD := "oci://" + regA + "/svc/d"
	mxPush(t, client, regA, "svc/d", "1.0.0", mxBundle(t, "d", "1.0.0"))
	mxPush(t, client, regA, "svc/a", "1.0.0", mxBundle(t, "a", "1.0.0", mxDep{"d", refD, "^1.0.0"}))
	mxPush(t, client, regA, "svc/b", "1.0.0", mxBundle(t, "b", "1.0.0", mxDep{"d", refD, "^1.0.0"}))
	root := mxBundle(t, "root", "1.0.0",
		mxDep{"a", "oci://" + regA + "/svc/a", "^1.0.0"},
		mxDep{"b", "oci://" + regA + "/svc/b", "^1.0.0"})

	lk, err := svc.buildLock(context.Background(), "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	count := 0
	for _, d := range lk.Dependencies {
		if d.Name == "d" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("diamond: dependency d appears %d times, want 1", count)
	}
}

// Cycle detection: A->B->A is reported as a cycle and fails the lock closed.
func TestResolutionMatrix_CycleDetection(t *testing.T) {
	regA := mxPlainRegistry(t)
	svc, client := mxService(mxHostKeychain{})
	refA := "oci://" + regA + "/svc/a"
	refB := "oci://" + regA + "/svc/b"
	mxPush(t, client, regA, "svc/a", "1.0.0", mxBundle(t, "a", "1.0.0", mxDep{"b", refB, "^1.0.0"}))
	mxPush(t, client, regA, "svc/b", "1.0.0", mxBundle(t, "b", "1.0.0", mxDep{"a", refA, "^1.0.0"}))
	root := mxBundle(t, "root", "1.0.0", mxDep{"a", refA, "^1.0.0"})
	ctx := context.Background()

	res := graph.Resolve(ctx, root.Contract, svc.newDepFetcher("oci://"+regA+"/svc/root:1.0.0"))
	if len(res.Cycles) == 0 {
		t.Fatal("expected a cycle to be detected")
	}
	if _, err := svc.buildLock(ctx, "oci://"+regA+"/svc/root:1.0.0", root, nil); err == nil {
		t.Fatal("expected buildLock to fail closed on a cycle")
	}
}

// A transitive-hop auth failure (no credentials for the deep registry) fails closed.
func TestResolutionMatrix_TransitiveAuthFailure(t *testing.T) {
	regA := mxPlainRegistry(t)
	regAuth := mxAuthRegistry(t, "u", "p")
	// Keychain intentionally has NO creds for regAuth.
	svc, client := mxService(mxHostKeychain{})
	authClient := oci.NewClient(mxHostKeychain{creds: map[string]authn.AuthConfig{regAuth: {Username: "u", Password: "p"}}}, oci.WithNameOptions(name.Insecure))

	refB := "oci://" + regAuth + "/svc/b"
	mxPush(t, authClient, regAuth, "svc/b", "1.0.0", mxBundle(t, "b", "1.0.0"))
	mxPush(t, client, regA, "svc/a", "1.0.0", mxBundle(t, "a", "1.0.0", mxDep{"b", refB, "^1.0.0"}))
	root := mxBundle(t, "root", "1.0.0", mxDep{"a", "oci://" + regA + "/svc/a", "^1.0.0"})

	_, err := svc.buildLock(context.Background(), "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err == nil {
		t.Fatal("expected buildLock to fail on transitive auth failure")
	}
	var unresolved *lock.UnresolvedError
	if !errors.As(err, &unresolved) {
		t.Fatalf("error = %T %v, want *lock.UnresolvedError", err, err)
	}
}

// An unreachable transitive registry fails closed.
func TestResolutionMatrix_UnreachableTransitiveRegistry(t *testing.T) {
	regA := mxPlainRegistry(t)
	regDead, stop := mxClosableRegistry(t)
	svc, client := mxService(mxHostKeychain{})
	refB := "oci://" + regDead + "/svc/b:1.0.0"
	mxPush(t, client, regA, "svc/a", "1.0.0", mxBundle(t, "a", "1.0.0", mxDep{"b", refB, "^1.0.0"}))
	root := mxBundle(t, "root", "1.0.0", mxDep{"a", "oci://" + regA + "/svc/a", "^1.0.0"})

	stop() // registry offline
	_, err := svc.buildLock(context.Background(), "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err == nil {
		t.Fatal("expected buildLock to fail on unreachable transitive registry")
	}
	var unresolved *lock.UnresolvedError
	if !errors.As(err, &unresolved) {
		t.Fatalf("error = %T %v, want *lock.UnresolvedError", err, err)
	}
}

// Artifact-not-found on a transitive hop propagates and fails closed.
func TestResolutionMatrix_ArtifactNotFoundPropagation(t *testing.T) {
	regA := mxPlainRegistry(t)
	svc, client := mxService(mxHostKeychain{})
	refGhost := "oci://" + regA + "/svc/ghost:1.0.0" // never pushed
	mxPush(t, client, regA, "svc/a", "1.0.0", mxBundle(t, "a", "1.0.0", mxDep{"ghost", refGhost, "^1.0.0"}))
	root := mxBundle(t, "root", "1.0.0", mxDep{"a", "oci://" + regA + "/svc/a", "^1.0.0"})

	_, err := svc.buildLock(context.Background(), "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err == nil {
		t.Fatal("expected buildLock to fail on artifact-not-found")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q should reference the missing ghost ref", err.Error())
	}
}

// Lock digest equality + semver range: two builds of the same closure pin the same
// digests, and a ^1.0.0 edge selects the highest matching version (1.2.0).
func TestResolutionMatrix_LockDigestEqualityAndSemverRange(t *testing.T) {
	regA := mxPlainRegistry(t)
	svc, client := mxService(mxHostKeychain{})
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0", "2.0.0"} {
		mxPush(t, client, regA, "svc/a", v, mxBundle(t, "a", v))
	}
	root := mxBundle(t, "root", "1.0.0", mxDep{"a", "oci://" + regA + "/svc/a", "^1.0.0"})
	ctx := context.Background()

	lk1, err := svc.buildLock(ctx, "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err != nil {
		t.Fatalf("buildLock #1: %v", err)
	}
	lk2, err := svc.buildLock(ctx, "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err != nil {
		t.Fatalf("buildLock #2: %v", err)
	}
	a1, _ := lk1.Dependency("a")
	a2, _ := lk2.Dependency("a")
	if a1.Version != "1.2.0" {
		t.Errorf("semver range: a.Version = %q, want 1.2.0", a1.Version)
	}
	if a1.Digest == "" || a1.Digest != a2.Digest {
		t.Errorf("lock digest equality: %q vs %q", a1.Digest, a2.Digest)
	}
}

// Concurrent graph resolution of a shared artifact through many paths dedups to a
// single fetched node (single-flight), validated under -race.
func TestResolutionMatrix_ConcurrentSharedArtifactSingleFlight(t *testing.T) {
	regA := mxPlainRegistry(t)
	svc, client := mxService(mxHostKeychain{})
	refShared := "oci://" + regA + "/svc/shared"
	mxPush(t, client, regA, "svc/shared", "1.0.0", mxBundle(t, "shared", "1.0.0"))

	// Fan-out: root -> p0..p9, each pi -> shared. Siblings resolve concurrently.
	var parents []mxDep
	for i := range 10 {
		repo := fmt.Sprintf("svc/p%d", i)
		mxPush(t, client, regA, repo, "1.0.0", mxBundle(t, fmt.Sprintf("p%d", i), "1.0.0", mxDep{"shared", refShared, "^1.0.0"}))
		parents = append(parents, mxDep{fmt.Sprintf("p%d", i), "oci://" + regA + "/" + repo, "^1.0.0"})
	}
	root := mxBundle(t, "root", "1.0.0", parents...)

	lk, err := svc.buildLock(context.Background(), "oci://"+regA+"/svc/root:1.0.0", root, nil)
	if err != nil {
		t.Fatalf("buildLock: %v", err)
	}
	count := 0
	for _, d := range lk.Dependencies {
		if d.Name == "shared" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("shared artifact appears %d times, want 1 (single-flight dedup)", count)
	}
}
