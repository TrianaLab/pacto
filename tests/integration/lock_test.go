//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/lock"
)

// readLock parses the pacto.lock written next to a local contract dir.
func readLock(t *testing.T, dir string) *lock.Lock {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, lock.FileName))
	if err != nil {
		t.Fatalf("read pacto.lock: %v", err)
	}
	l, err := lock.Parse(data)
	if err != nil {
		t.Fatalf("parse pacto.lock: %v", err)
	}
	return l
}

// reference finds the entry for one DECLARATION: the (kind, name) declared by
// the contract whose content identity is from ("" is the root).
// lock.RootReference covers from == "" for production callers; a test that walks
// deeper into the closure needs the general form.
func reference(l *lock.Lock, from, kind, name string) (*lock.Reference, bool) {
	want := lock.Occurrence{From: from, Kind: kind, Name: name}
	for i := range l.References {
		if r := &l.References[i]; r.Occurrence() == want {
			return r, true
		}
	}
	return nil, false
}

// depServiceContract renders a leaf or intermediate service contract whose
// optional single dependency points at another OCI bundle in the registry.
// When depRef is empty the service is a leaf. The proto interface keeps the
// contract structurally valid so it can be pushed via `pacto push`.
func depServiceContract(name, version, depName, depRef string) string {
	deps := ""
	if depRef != "" {
		deps = fmt.Sprintf(`
dependencies:
  - name: %s
    ref: %s
    required: true
    compatibility: "^1.0.0"
`, depName, depRef)
	}
	return fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: %s
  version: %s
  owner:
    team: platform
interfaces:
  - name: api
    type: grpc
    visibility: internal
    ref: interfaces/api.json
%s
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, name, version, deps)
}

// writeDepBundle writes a contract dir for a (possibly dependent) service.
func writeDepBundle(t *testing.T, name, version, depName, depRef string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	return writeBundleDir(t, dir, depServiceContract(name, version, depName, depRef), map[string]string{
		"api.json": fmt.Sprintf(grpcSpecTemplate, name, name),
	})
}

// pushDepBundle pushes a (possibly dependent) service bundle to the registry.
func pushDepBundle(t *testing.T, reg *testRegistry, repo, version, depName, depRef string) {
	t.Helper()
	dir := writeDepBundle(t, repo, version, depName, depRef)
	ref := fmt.Sprintf("oci://%s/%s:%s", reg.host, repo, version)
	if _, err := runCommand(t, reg, "push", ref, "-p", dir); err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
}

// depRefDecl is one declared dependency line for a multi-dependency root: a name,
// the raw ref string (any form: bare oci://repo, oci://repo:tag, oci://repo@sha256:,
// or a local ../path) and an optional semver compatibility constraint.
type depRefDecl struct {
	name   string
	ref    string
	compat string // emitted as `compatibility:` when non-empty
}

// multiDepContract renders a structurally-valid service contract declaring an
// arbitrary set of dependencies in arbitrary ref forms. It complements the
// single-dependency depServiceContract helper for the adversarial N-dep and
// mixed-ref-form scenarios.
func multiDepContract(name, version string, deps []depRefDecl) string {
	var b strings.Builder
	fmt.Fprintf(&b, `pactoVersion: "2.0"
service:
  name: %s
  version: %s
  owner:
    team: platform
interfaces:
  - name: api
    type: grpc
    visibility: internal
    ref: interfaces/api.json
`, name, version)
	if len(deps) > 0 {
		b.WriteString("dependencies:\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "  - name: %s\n    ref: %s\n    required: true\n", d.name, d.ref)
			if d.compat != "" {
				fmt.Fprintf(&b, "    compatibility: %q\n", d.compat)
			}
		}
	}
	b.WriteString(`workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
	return b.String()
}

// writeMultiDepBundle writes a root contract dir declaring the given deps.
func writeMultiDepBundle(t *testing.T, name, version string, deps []depRefDecl) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	return writeBundleDir(t, dir, multiDepContract(name, version, deps), map[string]string{
		"api.json": fmt.Sprintf(grpcSpecTemplate, name, name),
	})
}

// depDigest returns the current manifest digest (e.g. "sha256:...") of a pushed
// OCI bundle, used to construct already-digest-pinned dependency refs.
func depDigest(t *testing.T, reg *testRegistry, repo, tag string) string {
	t.Helper()
	digest, err := reg.client.Resolve(context.Background(), reg.host+"/"+repo+":"+tag)
	if err != nil {
		t.Fatalf("resolve digest for %s:%s: %v", repo, tag, err)
	}
	return digest
}

// TestLockCapturesTransitiveClosure proves `pacto lock` pins the FULL transitive
// dependency closure: root -> svc-a -> svc-b. Both svc-a and the transitive
// svc-b must appear with non-empty digests, and svc-a must record svc-b as a
// dependency edge. This is the user's "several dependency jumps" case.
func TestLockCapturesTransitiveClosure(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// svc-b: leaf. svc-a depends on svc-b.
	pushDepBundle(t, reg, "svc-b", "1.0.0", "", "")
	pushDepBundle(t, reg, "svc-a", "1.0.0", "svc-b", "oci://"+reg.host+"/svc-b:1.0.0")

	// Local root depends on svc-a (which transitively pulls in svc-b).
	rootDir := writeDepBundle(t, "root", "1.0.0", "svc-a", "oci://"+reg.host+"/svc-a:1.0.0")

	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("lock failed: %v", err)
	}

	l := readLock(t, rootDir)

	a, ok := l.Dependency("svc-a")
	if !ok {
		t.Fatalf("expected svc-a in lock, got %+v", l.Dependencies)
	}
	b, ok := l.Dependency("svc-b")
	if !ok {
		t.Fatalf("expected transitive svc-b in lock, got %+v", l.Dependencies)
	}
	if a.Digest == "" {
		t.Errorf("svc-a digest empty: %+v", a)
	}
	if b.Digest == "" {
		t.Errorf("svc-b digest empty: %+v", b)
	}
	if !contains(a.DependsOn, "svc-b") {
		t.Errorf("expected svc-a.dependsOn to contain svc-b, got %v", a.DependsOn)
	}
}

// policyRefContract renders a policy-provider contract that either declares a
// local policy schema (leaf) or references another OCI policy bundle (a
// reference jump: a policy that references a policy).
func policyRefContract(name, version, refName, ref string) string {
	if ref == "" {
		return fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: %s
  version: %s
policies:
  - name: default
    schema: policy/schema.json
`, name, version)
	}
	return fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: %s
  version: %s
policies:
  - name: %s
    ref: %s
`, name, version, refName, ref)
}

// TestLockCapturesReferenceJumps proves the lock pins the TRANSITIVE config/
// policy reference closure across a reference jump: root -> policy-p -> policy-q.
// policy-q is a leaf provider; policy-p references policy-q; the local root
// references policy-p. Both policy-p AND policy-q must be pinned in the lock's
// references with a digest and version. This is the user's key requirement.
func TestLockCapturesReferenceJumps(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// policy-q: a leaf policy provider with a trivially-satisfied schema.
	qDir := writeBundleDirWithPolicy(t, filepath.Join(t.TempDir(), "policy-q"),
		policyRefContract("policy-q", "1.0.0", "", ""), `{"type":"object"}`)
	if _, err := runCommand(t, reg, "push", "oci://"+reg.host+"/policy-q:1.0.0", "-p", qDir); err != nil {
		t.Fatalf("push policy-q: %v", err)
	}

	// policy-p references policy-q (a reference jump). Push it after policy-q so
	// the recursive policy-ref resolution at push time succeeds.
	pDir := writeBundleDir(t, filepath.Join(t.TempDir(), "policy-p"),
		policyRefContract("policy-p", "1.0.0", "q", "oci://"+reg.host+"/policy-q:1.0.0"), nil)
	if _, err := runCommand(t, reg, "push", "oci://"+reg.host+"/policy-p:1.0.0", "-p", pDir); err != nil {
		t.Fatalf("push policy-p: %v", err)
	}

	// Local root references policy-p.
	rootDir := writeBundleDir(t, filepath.Join(t.TempDir(), "ref-root"),
		policyRefContract("ref-root", "1.0.0", "p", "oci://"+reg.host+"/policy-p:1.0.0"), nil)

	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("lock failed: %v", err)
	}

	l := readLock(t, rootDir)

	// Direct reference: policy-p, declared by the ROOT.
	p, ok := l.RootReference("policy", "p")
	if !ok {
		t.Fatalf("expected policy-p reference in lock, got %+v", l.References)
	}
	if p.Digest == "" || p.Version == "" {
		t.Errorf("policy-p reference missing digest/version: %+v", p)
	}

	// Each entry records the contract that DECLARED it, so the jump is proven
	// directly rather than by elimination -- but the elimination still has to hold,
	// or the "transitive" entry could be one the root declared itself.
	rootYAML, err := os.ReadFile(filepath.Join(rootDir, "pacto.yaml"))
	if err != nil {
		t.Fatalf("read root pacto.yaml: %v", err)
	}
	if strings.Contains(string(rootYAML), "policy-q") {
		t.Fatal("root must not reference policy-q directly; the test would not prove the jump")
	}

	// Transitive reference jump: policy-q must also be pinned, and it must be
	// attributed to policy-p -- the occurrence it was reached through -- not to the
	// root. `pacto lock` records that as From, so the walk is now verifiable from
	// the lockfile alone.
	q, ok := reference(l, p.DestinationID(), "policy", "q")
	if !ok {
		t.Fatalf("expected TRANSITIVE policy-q reference declared by policy-p (reference jump), got %+v", l.References)
	}
	if _, isRoots := l.RootReference("policy", "q"); isRoots {
		t.Error("policy-q must not be attributed to the root: the root never declared it")
	}
	if q.Digest == "" || q.Version == "" {
		t.Errorf("policy-q reference missing digest/version: %+v", q)
	}
}

// TestLockVerifyHardFailsOnTransitiveDrift proves go.sum-style hard-fail catches
// a tampered TRANSITIVE dependency. root -> svc-a -> svc-b is locked, then svc-b
// is re-pushed at the SAME tag with different content (new digest). `pacto graph`
// must fail with LOCK_DIGEST_MISMATCH; `pacto lock --update` then re-pins.
func TestLockVerifyHardFailsOnTransitiveDrift(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	pushDepBundle(t, reg, "drift-b", "1.0.0", "", "")
	pushDepBundle(t, reg, "drift-a", "1.0.0", "drift-b", "oci://"+reg.host+"/drift-b:1.0.0")
	rootDir := writeDepBundle(t, "drift-root", "1.0.0", "drift-a", "oci://"+reg.host+"/drift-a:1.0.0")

	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("initial lock failed: %v", err)
	}
	before := readLock(t, rootDir)
	bBefore, ok := before.Dependency("drift-b")
	if !ok || bBefore.Digest == "" {
		t.Fatalf("expected locked drift-b digest, got %+v", before.Dependencies)
	}

	// Re-push drift-b at the SAME tag with DIFFERENT content (extra metadata
	// produces a new manifest digest for the tag).
	tamperDir := writeDepBundle(t, "drift-b", "1.0.0", "", "")
	if err := os.WriteFile(filepath.Join(tamperDir, "interfaces", "extra.proto"),
		[]byte(fmt.Sprintf(protoTemplate, "drift", "DriftExtra")), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(t, reg, "push", "oci://"+reg.host+"/drift-b:1.0.0", "-p", tamperDir, "--force"); err != nil {
		t.Fatalf("re-push drift-b: %v", err)
	}

	// graph must hard-fail on the tampered transitive dependency.
	out, err := runCommand(t, reg, "graph", rootDir)
	if err == nil {
		t.Fatalf("expected graph to hard-fail on transitive drift, got success:\n%s", out)
	}
	assertContains(t, out+err.Error(), "LOCK_DIGEST_MISMATCH")

	// `pacto lock --update` re-pins the closure; drift-b's digest changes.
	if _, err := runCommand(t, reg, "lock", "--update", rootDir); err != nil {
		t.Fatalf("lock --update failed: %v", err)
	}
	after := readLock(t, rootDir)
	bAfter, ok := after.Dependency("drift-b")
	if !ok {
		t.Fatalf("expected drift-b after update, got %+v", after.Dependencies)
	}
	if bAfter.Digest == bBefore.Digest {
		t.Errorf("expected drift-b digest to change after re-pin, still %s", bAfter.Digest)
	}

	// After re-pinning, graph passes again.
	if _, err := runCommand(t, reg, "graph", rootDir); err != nil {
		t.Fatalf("graph after re-pin should pass: %v", err)
	}
}

// TestLockStaleWhenDependencyAdded proves a stale lock (pacto.yaml gained a dep
// not in the lock) hard-fails with LOCK_STALE, and that re-running `pacto lock`
// reconciles so subsequent commands pass.
func TestLockStaleWhenDependencyAdded(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	pushDepBundle(t, reg, "stale-a", "1.0.0", "", "")
	pushDepBundle(t, reg, "stale-c", "1.0.0", "", "")

	// Root depends only on stale-a; lock it.
	rootDir := writeDepBundle(t, "stale-root", "1.0.0", "stale-a", "oci://"+reg.host+"/stale-a:1.0.0")
	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("initial lock failed: %v", err)
	}

	// Add a second, already-pushed dependency (stale-c) to pacto.yaml without
	// re-locking — the lock is now stale.
	twoDeps := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: stale-root
  version: 1.0.0
  owner:
    team: platform
interfaces:
  - name: api
    type: grpc
    visibility: internal
    ref: interfaces/api.json
dependencies:
  - name: stale-a
    ref: oci://%s/stale-a:1.0.0
    required: true
    compatibility: "^1.0.0"
  - name: stale-c
    ref: oci://%s/stale-c:1.0.0
    required: true
    compatibility: "^1.0.0"
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host, reg.host)
	if err := os.WriteFile(filepath.Join(rootDir, "pacto.yaml"), []byte(twoDeps), 0644); err != nil {
		t.Fatal(err)
	}

	out, err := runCommand(t, reg, "graph", rootDir)
	if err == nil {
		t.Fatalf("expected graph to hard-fail on stale lock, got success:\n%s", out)
	}
	assertContains(t, out+err.Error(), "LOCK_STALE")

	// Re-locking reconciles; graph then passes and stale-c is pinned.
	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("reconciling lock failed: %v", err)
	}
	if _, err := runCommand(t, reg, "graph", rootDir); err != nil {
		t.Fatalf("graph after reconcile should pass: %v", err)
	}
	l := readLock(t, rootDir)
	if _, ok := l.Dependency("stale-c"); !ok {
		t.Errorf("expected stale-c pinned after reconcile, got %+v", l.Dependencies)
	}
}

// TestLockPinsFiveDependenciesPlusTransitive proves `pacto lock` pins a wide
// closure: a root with N=5 direct OCI dependencies, one of which (leaf-1) has its
// own transitive dependency (leaf-tx) so the full closure is 6. Every direct dep
// AND the transitive must appear in the lock with a non-empty digest, and the
// total dependency count must equal the closure size. Adversarial scenario C.
func TestLockPinsFiveDependenciesPlusTransitive(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// leaf-tx is the transitive leaf. leaf-1 depends on it; leaf-2..5 are leaves.
	pushDepBundle(t, reg, "leaf-tx", "1.0.0", "", "")
	pushDepBundle(t, reg, "leaf-1", "1.0.0", "leaf-tx", "oci://"+reg.host+"/leaf-tx:1.0.0")
	for _, n := range []string{"leaf-2", "leaf-3", "leaf-4", "leaf-5"} {
		pushDepBundle(t, reg, n, "1.0.0", "", "")
	}

	deps := make([]depRefDecl, 0, 5)
	for _, n := range []string{"leaf-1", "leaf-2", "leaf-3", "leaf-4", "leaf-5"} {
		deps = append(deps, depRefDecl{name: n, ref: "oci://" + reg.host + "/" + n + ":1.0.0", compat: "^1.0.0"})
	}
	rootDir := writeMultiDepBundle(t, "wide-root", "1.0.0", deps)

	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	l := readLock(t, rootDir)

	// All 5 direct deps + the transitive must be pinned with a digest.
	want := []string{"leaf-1", "leaf-2", "leaf-3", "leaf-4", "leaf-5", "leaf-tx"}
	for _, name := range want {
		e, ok := l.Dependency(name)
		if !ok {
			t.Fatalf("expected %s in lock, got %+v", name, l.Dependencies)
		}
		if e.Digest == "" {
			t.Errorf("%s digest empty: %+v", name, e)
		}
	}
	// The closure is exactly these 6 entries (deduped, flat).
	if len(l.Dependencies) != len(want) {
		t.Errorf("expected %d locked dependencies, got %d: %+v", len(want), len(l.Dependencies), l.Dependencies)
	}
	// leaf-1 must record the transitive edge.
	one, _ := l.Dependency("leaf-1")
	if !contains(one.DependsOn, "leaf-tx") {
		t.Errorf("expected leaf-1.dependsOn to contain leaf-tx, got %v", one.DependsOn)
	}
}

// TestLockMixedDependencyRefForms proves `pacto lock` handles every dependency
// ref form in a single root: a bare repo with a compatibility constraint, an
// explicit :tag, an already @sha256:-pinned ref, and a LOCAL ../sibling. OCI
// entries get a digest; the pre-pinned entry round-trips the SAME digest; the
// local entry gets source=local + contentHash (no digest). `pacto graph` then
// passes against the consistent lock. Adversarial scenario D.
func TestLockMixedDependencyRefForms(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Push three OCI leaves at 1.0.0; mix-bare resolves via constraint.
	pushDepBundle(t, reg, "mix-bare", "1.0.0", "", "")
	pushDepBundle(t, reg, "mix-tag", "1.0.0", "", "")
	pushDepBundle(t, reg, "mix-pinned", "1.0.0", "", "")
	pinnedDigest := depDigest(t, reg, "mix-pinned", "1.0.0")

	// A local sibling dependency lives next to the root.
	parent := t.TempDir()
	siblingDir := filepath.Join(parent, "mix-local")
	writeBundleDir(t, siblingDir, depServiceContract("mix-local", "1.0.0", "", ""), map[string]string{
		"api.json": fmt.Sprintf(grpcSpecTemplate, "mix-local", "MixLocal"),
	})

	rootDir := filepath.Join(parent, "mix-root")
	deps := []depRefDecl{
		{name: "mix-bare", ref: "oci://" + reg.host + "/mix-bare", compat: "^1.0.0"},
		{name: "mix-tag", ref: "oci://" + reg.host + "/mix-tag:1.0.0"},
		{name: "mix-pinned", ref: "oci://" + reg.host + "/mix-pinned@" + pinnedDigest},
		{name: "mix-local", ref: "../mix-local"},
	}
	writeBundleDir(t, rootDir, multiDepContract("mix-root", "1.0.0", deps), map[string]string{
		"api.json": fmt.Sprintf(grpcSpecTemplate, "mix-root", "MixRoot"),
	})

	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	l := readLock(t, rootDir)

	// All three OCI forms resolve to a digest.
	for _, name := range []string{"mix-bare", "mix-tag", "mix-pinned"} {
		e, ok := l.Dependency(name)
		if !ok {
			t.Fatalf("expected %s in lock, got %+v", name, l.Dependencies)
		}
		if e.Source != "oci" {
			t.Errorf("%s source = %q, want oci", name, e.Source)
		}
		if e.Digest == "" {
			t.Errorf("%s digest empty: %+v", name, e)
		}
	}
	// The already-pinned ref round-trips the exact same digest.
	pinned, _ := l.Dependency("mix-pinned")
	if pinned.Digest != pinnedDigest {
		t.Errorf("pre-pinned digest changed: locked %q, want %q", pinned.Digest, pinnedDigest)
	}
	// The local sibling is recorded as source=local with a contentHash, no digest.
	local, ok := l.Dependency("mix-local")
	if !ok {
		t.Fatalf("expected mix-local in lock, got %+v", l.Dependencies)
	}
	if local.Source != "local" {
		t.Errorf("mix-local source = %q, want local", local.Source)
	}
	if local.ContentHash == "" {
		t.Errorf("mix-local contentHash empty: %+v", local)
	}
	if local.Digest != "" {
		t.Errorf("mix-local should have no digest, got %q", local.Digest)
	}

	// The lock is consistent: graph passes against it.
	if _, err := runCommand(t, reg, "graph", rootDir); err != nil {
		t.Fatalf("graph against mixed-form lock should pass: %v", err)
	}
}

// TestLockDependencyShippingOwnLockIsIgnored proves a dependency that ships its
// OWN pacto.lock does NOT influence the root's closure: only the root's lock is
// authoritative. dep-X is pushed WITH a (bogus) pacto.lock re-included via
// `!pacto.lock`; dep-Y is pushed without one. The root depends on both. `pacto
// lock` succeeds, pins BOTH from the root's own fresh resolution, and dep-X's
// pinned digest is the freshly-resolved manifest digest — independent of whatever
// dep-X's shipped lock claimed. Adversarial scenario F.
func TestLockDependencyShippingOwnLockIsIgnored(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// dep-X bundle ships its OWN pacto.lock. Locks are embedded in pushed bundles
	// by default, so we just generate a valid lock (push runs verifyLockIfPresent).
	// The point of the scenario: the root resolves dep-x's digest independently and
	// never consults dep-x's shipped lock.
	xDir := writeDepBundle(t, "dep-x", "1.0.0", "", "")
	if _, err := runCommand(t, reg, "lock", xDir); err != nil {
		t.Fatalf("lock dep-x: %v", err)
	}
	// Confirm the lock was generated.
	if _, err := os.Stat(filepath.Join(xDir, lock.FileName)); err != nil {
		t.Fatalf("expected dep-x to ship a pacto.lock: %v", err)
	}
	if _, err := runCommand(t, reg, "push", "oci://"+reg.host+"/dep-x:1.0.0", "-p", xDir); err != nil {
		t.Fatalf("push dep-x: %v", err)
	}
	// Verify the shipped lock survived the OCI round-trip (it really travels with
	// the artifact, so the root COULD have consulted it — but must not).
	pullDir := filepath.Join(t.TempDir(), "dep-x-pulled")
	if _, err := runCommand(t, reg, "pull", "oci://"+reg.host+"/dep-x:1.0.0", "-o", pullDir); err != nil {
		t.Fatalf("pull dep-x: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pullDir, lock.FileName)); err != nil {
		t.Fatalf("expected shipped pacto.lock to survive push/pull: %v", err)
	}
	pushDepBundle(t, reg, "dep-y", "1.0.0", "", "")

	// The root's own resolution of dep-x yields this digest — the authoritative one.
	freshXDigest := depDigest(t, reg, "dep-x", "1.0.0")

	rootDir := writeMultiDepBundle(t, "own-lock-root", "1.0.0", []depRefDecl{
		{name: "dep-x", ref: "oci://" + reg.host + "/dep-x:1.0.0", compat: "^1.0.0"},
		{name: "dep-y", ref: "oci://" + reg.host + "/dep-y:1.0.0", compat: "^1.0.0"},
	})

	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("lock failed: %v", err)
	}
	l := readLock(t, rootDir)

	// Both deps are pinned from the ROOT's own resolution.
	x, ok := l.Dependency("dep-x")
	if !ok {
		t.Fatalf("expected dep-x in root lock, got %+v", l.Dependencies)
	}
	if _, ok := l.Dependency("dep-y"); !ok {
		t.Fatalf("expected dep-y in root lock, got %+v", l.Dependencies)
	}
	// dep-x's pin is the root's freshly-resolved manifest digest — computed by the
	// root from its own resolution, NOT copied from dep-x's shipped lock. Only the
	// root lock is authoritative over the root closure.
	if x.Digest != freshXDigest {
		t.Errorf("dep-x pinned %q, want freshly-resolved %q (shipped lock must not influence the root)", x.Digest, freshXDigest)
	}
	// The root closure is exactly {dep-x, dep-y}; dep-x's shipped lock added nothing.
	if len(l.Dependencies) != 2 {
		t.Errorf("expected exactly 2 root dependencies, got %d: %+v", len(l.Dependencies), l.Dependencies)
	}
}

// TestLockAbsentIsPassthrough proves the feature is OPT-IN / backward compatible:
// a root with dependencies but NO pacto.lock has no enforcement, so `pacto graph`
// and `pacto validate` both SUCCEED. Adversarial scenario G.
func TestLockAbsentIsPassthrough(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	pushDepBundle(t, reg, "passthru-a", "1.0.0", "", "")
	pushDepBundle(t, reg, "passthru-b", "1.0.0", "", "")
	rootDir := writeMultiDepBundle(t, "passthru-root", "1.0.0", []depRefDecl{
		{name: "passthru-a", ref: "oci://" + reg.host + "/passthru-a:1.0.0", compat: "^1.0.0"},
		{name: "passthru-b", ref: "oci://" + reg.host + "/passthru-b:1.0.0", compat: "^1.0.0"},
	})

	// Sanity: no lock file is present.
	if _, err := os.Stat(filepath.Join(rootDir, lock.FileName)); !os.IsNotExist(err) {
		t.Fatalf("expected no pacto.lock, stat err = %v", err)
	}

	// With no lock, both commands succeed (no enforcement).
	if out, err := runCommand(t, reg, "graph", rootDir); err != nil {
		t.Fatalf("graph without lock should pass: %v\n%s", err, out)
	}
	if out, err := runCommand(t, reg, "validate", rootDir); err != nil {
		t.Fatalf("validate without lock should pass: %v\n%s", err, out)
	}
}

// TestLockStaleWhenDependencyRemoved proves removing a dependency from
// pacto.yaml without re-locking makes the lock stale and hard-fails with
// LOCK_STALE. Set up a root with TWO already-pushed OCI dependencies, lock
// both, then REMOVE one dependency from the contract without relocking. graph
// must fail, then `pacto lock` reconciles and subsequent graph passes.
func TestLockStaleWhenDependencyRemoved(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	pushDepBundle(t, reg, "remove-a", "1.0.0", "", "")
	pushDepBundle(t, reg, "remove-b", "1.0.0", "", "")

	// Root depends on both remove-a and remove-b; lock it.
	rootDir := writeMultiDepBundle(t, "remove-root", "1.0.0", []depRefDecl{
		{name: "remove-a", ref: "oci://" + reg.host + "/remove-a:1.0.0", compat: "^1.0.0"},
		{name: "remove-b", ref: "oci://" + reg.host + "/remove-b:1.0.0", compat: "^1.0.0"},
	})
	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("initial lock failed: %v", err)
	}

	// Verify both deps are in the lock.
	before := readLock(t, rootDir)
	if _, ok := before.Dependency("remove-a"); !ok {
		t.Fatalf("expected remove-a in lock, got %+v", before.Dependencies)
	}
	if _, ok := before.Dependency("remove-b"); !ok {
		t.Fatalf("expected remove-b in lock, got %+v", before.Dependencies)
	}

	// REMOVE remove-b from pacto.yaml, leaving only remove-a. The lock is now stale.
	oneDep := fmt.Sprintf(`pactoVersion: "2.0"
service:
  name: remove-root
  version: 1.0.0
  owner:
    team: platform
interfaces:
  - name: api
    type: grpc
    visibility: internal
    ref: interfaces/api.json
dependencies:
  - name: remove-a
    ref: oci://%s/remove-a:1.0.0
    required: true
    compatibility: "^1.0.0"
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`, reg.host)
	if err := os.WriteFile(filepath.Join(rootDir, "pacto.yaml"), []byte(oneDep), 0644); err != nil {
		t.Fatal(err)
	}

	// graph must hard-fail on stale lock (lock has remove-b, contract does not).
	out, err := runCommand(t, reg, "graph", rootDir)
	if err == nil {
		t.Fatalf("expected graph to hard-fail on stale lock, got success:\n%s", out)
	}
	assertContains(t, out+err.Error(), "LOCK_STALE")

	// Re-locking reconciles; graph then passes and remove-b is gone.
	if _, err := runCommand(t, reg, "lock", rootDir); err != nil {
		t.Fatalf("reconciling lock failed: %v", err)
	}
	if _, err := runCommand(t, reg, "graph", rootDir); err != nil {
		t.Fatalf("graph after reconcile should pass: %v", err)
	}
	after := readLock(t, rootDir)
	if _, ok := after.Dependency("remove-b"); ok {
		t.Errorf("expected remove-b to be absent after reconcile, got %+v", after.Dependencies)
	}
	if _, ok := after.Dependency("remove-a"); !ok {
		t.Errorf("expected remove-a to remain after reconcile, got %+v", after.Dependencies)
	}
}

// TestLockEmbeddedInPushedBundle proves pacto.lock is embedded in the pushed
// bundle and survives the OCI round-trip. A bundle with pacto.yaml, an interface
// file and a valid pacto.lock is pushed to the registry, then pulled to a fresh
// directory. The pulled tree must contain all three files: pacto.yaml, the
// interface and the lock.
func TestLockEmbeddedInPushedBundle(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)

	// Create a bundle dir with a minimal contract, an interface file and a lock.
	dir := filepath.Join(t.TempDir(), "lock-embed")
	contractYAML := depServiceContract("lock-embed", "1.0.0", "", "")
	writeBundleDir(t, dir, contractYAML, map[string]string{
		"api.json": fmt.Sprintf(grpcSpecTemplate, "lock-embed", "LockEmbed"),
	})

	// Generate a valid pacto.lock for this bundle (empty dependencies, but valid).
	// It must be at the CURRENT schema version: push verifies the lock, and a lock
	// written before reference-occurrence identity is stale by definition.
	lockContent := `lockVersion: 3
pacto:
  version: 1.4.0
root:
  name: lock-embed
  version: 1.0.0
dependencies: []
references: []
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.lock"), []byte(lockContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Push the bundle to the registry.
	ref := "oci://" + reg.host + "/lock-embed:1.0.0"
	if _, err := runCommand(t, reg, "push", ref, "-p", dir); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Pull to a fresh directory.
	pullDir := filepath.Join(t.TempDir(), "lock-embed-pulled")
	if _, err := runCommand(t, reg, "pull", ref, "-o", pullDir); err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	// Assert all three files survived the round-trip.
	for _, file := range []string{"pacto.yaml", "interfaces/api.json", "pacto.lock"} {
		path := filepath.Join(pullDir, filepath.FromSlash(file))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s in pulled tree: %v", file, err)
		}
	}
}

// contains reports whether s is in xs.
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
