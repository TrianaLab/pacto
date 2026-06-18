//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/pkg/lock"
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
	return fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: %s
  version: %s
  owner: team/platform
interfaces:
  - name: api
    type: grpc
    port: 9000
    visibility: internal
    contract: interfaces/api.proto
%s
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
`, name, version, deps)
}

// writeDepBundle writes a contract dir for a (possibly dependent) service.
func writeDepBundle(t *testing.T, name, version, depName, depRef string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	return writeBundleDir(t, dir, depServiceContract(name, version, depName, depRef), map[string]string{
		"api.proto": fmt.Sprintf(protoTemplate, name, name),
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
		return fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: %s
  version: %s
policies:
  - name: default
    schema: policy/schema.json
`, name, version)
	}
	return fmt.Sprintf(`pactoVersion: "1.0"
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

	// Direct reference: policy-p.
	p, ok := l.Reference("policy", "p")
	if !ok {
		t.Fatalf("expected policy-p reference in lock, got %+v", l.References)
	}
	if p.Digest == "" || p.Version == "" {
		t.Errorf("policy-p reference missing digest/version: %+v", p)
	}

	// The lock's References list is flat (no edge tracking). We prove the reference
	// JUMP by elimination: the root declares only policy-p; policy-q is referenced
	// solely by policy-p, so policy-q appearing in the lock means buildReferenceClosure
	// walked p -> q.
	rootYAML, err := os.ReadFile(filepath.Join(rootDir, "pacto.yaml"))
	if err != nil {
		t.Fatalf("read root pacto.yaml: %v", err)
	}
	if strings.Contains(string(rootYAML), "policy-q") {
		t.Fatal("root must not reference policy-q directly; the test would not prove the jump")
	}

	// Transitive reference jump: policy-q must also be pinned. The closure is
	// captured transitively by buildReferenceClosure; policy-q is declared under
	// name "q" inside policy-p's contract.
	q, ok := l.Reference("policy", "q")
	if !ok {
		t.Fatalf("expected TRANSITIVE policy-q reference in lock (reference jump), got %+v", l.References)
	}
	if q.Kind != "policy" {
		t.Errorf("expected policy-q kind=policy, got %q", q.Kind)
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
	twoDeps := fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: stale-root
  version: 1.0.0
  owner: team/platform
interfaces:
  - name: api
    type: grpc
    port: 9000
    visibility: internal
    contract: interfaces/api.proto
dependencies:
  - name: stale-a
    ref: oci://%s/stale-a:1.0.0
    required: true
    compatibility: "^1.0.0"
  - name: stale-c
    ref: oci://%s/stale-c:1.0.0
    required: true
    compatibility: "^1.0.0"
runtime:
  workload: service
  state:
    type: stateless
    persistence:
      scope: local
      durability: ephemeral
    dataCriticality: low
  health:
    interface: api
    path: /health
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

// contains reports whether s is in xs.
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
