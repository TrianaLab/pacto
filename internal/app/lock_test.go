package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/internal/testutil"
	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/lock"
	"github.com/trianalab/pacto/pkg/oci"
)

// writeRoot writes a minimal local root contract declaring one OCI dep and
// returns the directory.
func writeRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	yaml := "pactoVersion: \"1.0\"\nservice:\n  name: root\n  version: \"2.1.0\"\ndependencies:\n  - name: auth\n    ref: oci://ghcr.io/acme/auth\n    compatibility: ^1.0.0\n    required: true\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// authStore returns a mock store that serves an "auth" bundle resolving to the
// given digest.
func authStore(digest string) *testutil.MockBundleStore {
	auth := &contract.Contract{Service: contract.ServiceIdentity{Name: "auth", Version: "1.2.0"}}
	return &testutil.MockBundleStore{
		ListTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.2.0"}, nil },
		ResolveFn:  func(_ context.Context, _ string) (string, error) { return digest, nil },
		PullFn:     func(_ context.Context, _ string) (*contract.Bundle, error) { return &contract.Bundle{Contract: auth}, nil },
	}
}

func TestLockWritesFile(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	res, err := s.Lock(context.Background(), LockOptions{Path: dir})
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !res.Written {
		t.Errorf("expected Written=true")
	}
	if res.UpToDate {
		t.Errorf("did not expect UpToDate on first write")
	}
	if res.Dependencies != 1 {
		t.Errorf("expected 1 dependency, got %d", res.Dependencies)
	}
	if res.Path != filepath.Join(dir, "pacto.lock") {
		t.Errorf("unexpected lock path %q", res.Path)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pacto.lock"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	l, _ := lock.Parse(data)
	if e, ok := l.Dependency("auth"); !ok || e.Digest != "sha256:v1" {
		t.Errorf("lock missing auth digest: %+v", l)
	}
}

func TestLockResolveError(t *testing.T) {
	// Non-existent local dir -> resolveBundle fails.
	s := NewService(authStore("sha256:v1"), nil)
	_, err := s.Lock(context.Background(), LockOptions{Path: filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatalf("expected error for missing contract")
	}
}

func TestLockBuildError(t *testing.T) {
	dir := writeRoot(t)
	// Store fails to resolve the dep digest -> buildLock returns UnresolvedError.
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) { return "", errors.New("boom") },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "auth", Version: "1.2.0"}}}, nil
		},
	}
	s := NewService(store, nil)
	_, err := s.Lock(context.Background(), LockOptions{Path: dir})
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

func TestLockOCIRefRejected(t *testing.T) {
	s := NewService(authStore("sha256:v1"), nil)
	_, err := s.Lock(context.Background(), LockOptions{Path: "oci://ghcr.io/acme/root:1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "local directory") {
		t.Fatalf("expected OCI rejection error, got %v", err)
	}
}

func TestLockUpToDateNoop(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	if _, err := s.Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	// Second plain lock with a consistent lock present -> no write.
	res, err := s.Lock(context.Background(), LockOptions{Path: dir})
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if res.Written {
		t.Errorf("expected no write when lock is up to date")
	}
	if !res.UpToDate {
		t.Errorf("expected UpToDate=true")
	}
}

func TestLockUpdateRepins(t *testing.T) {
	dir := writeRoot(t)
	if _, err := NewService(authStore("sha256:v1"), nil).Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	// Registry now serves v2; --update repins.
	s := NewService(authStore("sha256:v2"), nil)
	res, err := s.Lock(context.Background(), LockOptions{Path: dir, Update: true})
	if err != nil {
		t.Fatalf("Lock --update: %v", err)
	}
	if !res.Written {
		t.Errorf("expected --update to write")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pacto.lock"))
	l, _ := lock.Parse(data)
	if e, _ := l.Dependency("auth"); e.Digest != "sha256:v2" {
		t.Errorf("expected repinned digest v2, got %q", e.Digest)
	}
}

// TestLockInconsistentMergesPins covers the "consistent lock absent / divergent,
// no --update" path: drift forces a rewrite via mergePreservingPins.
func TestLockInconsistentMergesPins(t *testing.T) {
	dir := writeRoot(t)
	if _, err := NewService(authStore("sha256:v1"), nil).Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	// Registry drift but plain lock (no --update): rewrite, mergePreservingPins
	// keeps the existing pin because the constraint is unchanged.
	s := NewService(authStore("sha256:v2"), nil)
	res, err := s.Lock(context.Background(), LockOptions{Path: dir})
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if !res.Written {
		t.Errorf("expected rewrite on divergence")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pacto.lock"))
	l, _ := lock.Parse(data)
	// Pin preserved because constraint unchanged and not named for update.
	if e, _ := l.Dependency("auth"); e.Digest != "sha256:v1" {
		t.Errorf("expected preserved pin v1, got %q", e.Digest)
	}
}

func TestLockCheckUpToDate(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	if _, err := s.Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Lock(context.Background(), LockOptions{Path: dir, Check: true})
	if err != nil {
		t.Fatalf("Lock --check: %v", err)
	}
	if !res.UpToDate || res.Written {
		t.Errorf("check on consistent lock: %+v", res)
	}
	if res.Dependencies != 1 {
		t.Errorf("expected 1 dependency reported, got %d", res.Dependencies)
	}
}

func TestLockCheckDrift(t *testing.T) {
	dir := writeRoot(t)
	if _, err := NewService(authStore("sha256:v1"), nil).Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	s := NewService(authStore("sha256:v2"), nil)
	_, err := s.Lock(context.Background(), LockOptions{Path: dir, Check: true})
	var de *lock.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("expected *lock.DriftError, got %v", err)
	}
}

func TestLockCheckMissing(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	_, err := s.Lock(context.Background(), LockOptions{Path: dir, Check: true})
	var me *lock.MissingError
	if !errors.As(err, &me) {
		t.Fatalf("expected *lock.MissingError, got %v", err)
	}
}

func TestLockMarshalError(t *testing.T) {
	// Force writeFileFn to fail so the write-error path is covered.
	dir := writeRoot(t)
	old := writeFileFn
	writeFileFn = func(_ string, _ []byte, _ os.FileMode) error { return errors.New("disk full") }
	defer func() { writeFileFn = old }()
	s := NewService(authStore("sha256:v1"), nil)
	_, err := s.Lock(context.Background(), LockOptions{Path: dir})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected write error, got %v", err)
	}
}

func TestLockMarshalFails(t *testing.T) {
	dir := writeRoot(t)
	old := marshalLockFn
	marshalLockFn = func(_ *lock.Lock) ([]byte, error) { return nil, errors.New("marshal boom") }
	defer func() { marshalLockFn = old }()
	s := NewService(authStore("sha256:v1"), nil)
	_, err := s.Lock(context.Background(), LockOptions{Path: dir})
	if err == nil || !strings.Contains(err.Error(), "marshal boom") {
		t.Fatalf("expected marshal error, got %v", err)
	}
}

func TestVerifyHardFailsOnDrift(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	if _, err := s.Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	s2 := NewService(authStore("sha256:v2"), nil)
	bundle, _ := s2.resolveBundle(context.Background(), dir)
	err := s2.verifyLockIfPresent(context.Background(), dir, bundle)
	if err == nil {
		t.Fatalf("expected drift error")
	}
	if !strings.HasPrefix(err.Error(), "LOCK_DIGEST_MISMATCH") {
		t.Errorf("want LOCK_DIGEST_MISMATCH, got %q", err.Error())
	}
}

func TestVerifyNoLockIsNoop(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	bundle, _ := s.resolveBundle(context.Background(), dir)
	if err := s.verifyLockIfPresent(context.Background(), dir, bundle); err != nil {
		t.Errorf("no lock should be a no-op, got %v", err)
	}
}

func TestVerifyOCIRefIsNoop(t *testing.T) {
	s := NewService(authStore("sha256:v1"), nil)
	if err := s.verifyLockIfPresent(context.Background(), "oci://ghcr.io/acme/root:1.0.0", &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "root"}}}); err != nil {
		t.Errorf("OCI ref should be a no-op, got %v", err)
	}
}

func TestVerifyReadLockParseError(t *testing.T) {
	dir := writeRoot(t)
	// Write a syntactically broken lock so Parse fails (not fs.ErrNotExist).
	if err := os.WriteFile(filepath.Join(dir, "pacto.lock"), []byte("lockVersion: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewService(authStore("sha256:v1"), nil)
	bundle, _ := s.resolveBundle(context.Background(), dir)
	err := s.verifyLockIfPresent(context.Background(), dir, bundle)
	if err == nil || !strings.Contains(err.Error(), "unsupported lockVersion") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestVerifyBuildError(t *testing.T) {
	dir := writeRoot(t)
	// Valid lock present, but fresh build fails (digest resolution errors).
	if _, err := NewService(authStore("sha256:v1"), nil).Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	store := &testutil.MockBundleStore{
		ResolveFn: func(_ context.Context, _ string) (string, error) { return "", errors.New("boom") },
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			return &contract.Bundle{Contract: &contract.Contract{Service: contract.ServiceIdentity{Name: "auth", Version: "1.2.0"}}}, nil
		},
	}
	s := NewService(store, nil)
	bundle, _ := s.resolveBundle(context.Background(), dir)
	err := s.verifyLockIfPresent(context.Background(), dir, bundle)
	var ue *lock.UnresolvedError
	if !errors.As(err, &ue) {
		t.Fatalf("expected *lock.UnresolvedError, got %v", err)
	}
}

// --- compareLocks unit coverage ---

func depLock(name, source, digest, contentHash, constraint string) *lock.Lock {
	return &lock.Lock{
		LockVersion: lock.CurrentLockVersion,
		Dependencies: []lock.Entry{
			{Name: name, Source: source, Digest: digest, ContentHash: contentHash, Constraint: constraint},
		},
	}
}

func TestCompareLocksConsistent(t *testing.T) {
	a := depLock("auth", "oci", "sha256:x", "", "^1.0.0")
	b := depLock("auth", "oci", "sha256:x", "", "^1.0.0")
	if err := compareLocks(a, b); err != nil {
		t.Errorf("expected consistent, got %v", err)
	}
}

func TestCompareLocksDepCountChanged(t *testing.T) {
	a := depLock("auth", "oci", "sha256:x", "", "^1.0.0")
	b := &lock.Lock{} // empty
	err := compareLocks(a, b)
	var se *lock.StaleError
	if !errors.As(err, &se) {
		t.Fatalf("expected *lock.StaleError, got %v", err)
	}
}

func TestCompareLocksNewDependency(t *testing.T) {
	existing := depLock("auth", "oci", "sha256:x", "", "^1.0.0")
	// fresh has the same count but a different name -> "new dependency".
	fresh := depLock("db", "oci", "sha256:y", "", "^2.0.0")
	err := compareLocks(existing, fresh)
	var se *lock.StaleError
	if !errors.As(err, &se) {
		t.Fatalf("expected *lock.StaleError, got %v", err)
	}
}

func TestCompareLocksLocalDrift(t *testing.T) {
	existing := depLock("auth", "local", "", "hash-old", "")
	fresh := depLock("auth", "local", "", "hash-new", "")
	err := compareLocks(existing, fresh)
	var ld *lock.LocalDriftError
	if !errors.As(err, &ld) {
		t.Fatalf("expected *lock.LocalDriftError, got %v", err)
	}
}

func TestCompareLocksLocalConsistent(t *testing.T) {
	existing := depLock("auth", "local", "", "hash", "")
	fresh := depLock("auth", "local", "", "hash", "")
	if err := compareLocks(existing, fresh); err != nil {
		t.Errorf("expected consistent local, got %v", err)
	}
}

func TestCompareLocksReferenceByRefDrift(t *testing.T) {
	existing := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/cfg", Digest: "sha256:old"},
	}}
	fresh := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/cfg", Digest: "sha256:new"},
	}}
	err := compareLocks(existing, fresh)
	var de *lock.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("expected *lock.DriftError, got %v", err)
	}
}

// TestCompareLocksReferenceNameCollision proves references are matched by Ref,
// not (kind,name): two refs with the SAME name but different Ref do not collide.
func TestCompareLocksReferenceNameCollision(t *testing.T) {
	existing := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/a", Digest: "sha256:a"},
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/b", Digest: "sha256:b"},
	}}
	fresh := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/a", Digest: "sha256:a"},
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/b", Digest: "sha256:b"},
	}}
	if err := compareLocks(existing, fresh); err != nil {
		t.Errorf("same-name distinct-ref references should be consistent, got %v", err)
	}
}

func TestCompareLocksReferenceLocalDrift(t *testing.T) {
	existing := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "local", Path: "./cfg", ContentHash: "h-old"},
	}}
	fresh := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "local", Path: "./cfg", ContentHash: "h-new"},
	}}
	err := compareLocks(existing, fresh)
	var ld *lock.LocalDriftError
	if !errors.As(err, &ld) {
		t.Fatalf("expected *lock.LocalDriftError for local ref, got %v", err)
	}
}

func TestCompareLocksReferenceLocalConsistent(t *testing.T) {
	existing := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "local", Path: "./cfg", ContentHash: "h"},
	}}
	fresh := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "local", Path: "./cfg", ContentHash: "h"},
	}}
	if err := compareLocks(existing, fresh); err != nil {
		t.Errorf("expected consistent local ref, got %v", err)
	}
}

// TestCompareLocksReferenceCountChanged covers the reference-count StaleError:
// both locks have equal dependency counts but a different number of references.
func TestCompareLocksReferenceCountChanged(t *testing.T) {
	existing := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/cfg", Digest: "sha256:x"},
	}}
	fresh := &lock.Lock{} // same dep count (0), fewer references
	err := compareLocks(existing, fresh)
	var se *lock.StaleError
	if !errors.As(err, &se) {
		t.Fatalf("expected *lock.StaleError for reference count change, got %v", err)
	}
}

// TestCompareLocksReferenceNew covers the membership mismatch when counts match
// but a fresh reference's identity is absent from the existing set.
func TestCompareLocksReferenceNew(t *testing.T) {
	existing := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/old", Digest: "sha256:x"},
	}}
	fresh := &lock.Lock{References: []lock.Reference{
		{Kind: "config", Name: "cfg", Source: "oci", Ref: "oci://r/new", Digest: "sha256:x"},
	}}
	err := compareLocks(existing, fresh)
	var se *lock.StaleError
	if !errors.As(err, &se) {
		t.Fatalf("expected *lock.StaleError for new reference, got %v", err)
	}
}

// --- mergePreservingPins unit coverage ---

func TestMergePreservingPins(t *testing.T) {
	existing := &lock.Lock{Dependencies: []lock.Entry{
		{Name: "keep", Source: "oci", Constraint: "^1.0.0", Version: "1.0.0", Digest: "sha256:keep"},
		{Name: "forced", Source: "oci", Constraint: "^1.0.0", Version: "1.0.0", Digest: "sha256:forced-old"},
		{Name: "changed", Source: "oci", Constraint: "^1.0.0", Version: "1.0.0", Digest: "sha256:changed-old"},
		{Name: "local", Source: "local", Constraint: "", ContentHash: "h-old"},
	}}
	fresh := &lock.Lock{Dependencies: []lock.Entry{
		{Name: "keep", Source: "oci", Constraint: "^1.0.0", Version: "9.9.9", Digest: "sha256:keep-new"},
		{Name: "forced", Source: "oci", Constraint: "^1.0.0", Version: "2.0.0", Digest: "sha256:forced-new"},
		{Name: "changed", Source: "oci", Constraint: "^2.0.0", Version: "2.0.0", Digest: "sha256:changed-new"},
		{Name: "local", Source: "local", Constraint: "", ContentHash: "h-new"},
		{Name: "new", Source: "oci", Constraint: "^1.0.0", Version: "1.0.0", Digest: "sha256:new"},
	}}
	out := mergePreservingPins(existing, fresh, []string{"forced"})

	get := func(name string) lock.Entry {
		e, ok := out.Dependency(name)
		if !ok {
			t.Fatalf("missing %s", name)
		}
		return *e
	}
	// keep: constraint unchanged, not forced -> preserve old pin.
	if e := get("keep"); e.Digest != "sha256:keep" || e.Version != "1.0.0" {
		t.Errorf("keep should preserve old pin: %+v", e)
	}
	// forced: named for update -> take fresh.
	if e := get("forced"); e.Digest != "sha256:forced-new" {
		t.Errorf("forced should be repinned: %+v", e)
	}
	// changed: constraint changed -> take fresh.
	if e := get("changed"); e.Digest != "sha256:changed-new" {
		t.Errorf("changed constraint should repin: %+v", e)
	}
	// local: constraint unchanged -> preserve old contentHash.
	if e := get("local"); e.ContentHash != "h-old" {
		t.Errorf("local should preserve old hash: %+v", e)
	}
	// new: not in existing -> take fresh.
	if e := get("new"); e.Digest != "sha256:new" {
		t.Errorf("new should be fresh: %+v", e)
	}
	// fresh must be untouched (out is a copy).
	if fresh.Dependencies[0].Digest != "sha256:keep-new" {
		t.Errorf("mergePreservingPins mutated fresh: %+v", fresh.Dependencies[0])
	}
}

// --- readLockFile coverage ---

func TestReadLockFileNotExist(t *testing.T) {
	_, err := readLockFile(filepath.Join(t.TempDir(), "pacto.lock"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestReadLockFileParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacto.lock")
	if err := os.WriteFile(path, []byte("lockVersion: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readLockFile(path)
	if err == nil {
		t.Fatalf("expected parse error")
	}
}

// --- lockCode coverage ---

func TestLockCode(t *testing.T) {
	if got := lockCode(&lock.DriftError{Name: "auth", Locked: "a", Current: "b"}); got != "LOCK_DIGEST_MISMATCH" {
		t.Errorf("lockCode drift: got %q", got)
	}
	if got := lockCode(errors.New("no colon here")); got != "LOCK_ERROR" {
		t.Errorf("lockCode fallback: got %q", got)
	}
}

// --- command wiring ---

// stalLockNextTo writes a v1 lock next to the contract, then returns a service
// whose registry serves a different digest (v2) so verification drifts.
func staleLockNextTo(t *testing.T, dir string) *Service {
	t.Helper()
	if _, err := NewService(authStore("sha256:v1"), nil).Lock(context.Background(), LockOptions{Path: dir}); err != nil {
		t.Fatal(err)
	}
	return NewService(authStore("sha256:v2"), nil)
}

func TestGraphVerifiesLock(t *testing.T) {
	dir := writeRoot(t)
	s := staleLockNextTo(t, dir)
	_, err := s.Graph(context.Background(), GraphOptions{Path: dir})
	var de *lock.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("Graph should hard-fail on drift, got %v", err)
	}
}

func TestGraphNoLockPasses(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	if _, err := s.Graph(context.Background(), GraphOptions{Path: dir}); err != nil {
		t.Fatalf("Graph with no lock should pass, got %v", err)
	}
}

func TestDiffVerifiesLockOldSide(t *testing.T) {
	dir := writeRoot(t)
	s := staleLockNextTo(t, dir)
	_, err := s.Diff(context.Background(), DiffOptions{OldPath: dir, NewPath: dir})
	var de *lock.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("Diff should hard-fail on old-side drift, got %v", err)
	}
}

func TestDiffVerifiesLockNewSide(t *testing.T) {
	oldDir := writeRoot(t)
	newDir := writeRoot(t)
	// Only the NEW side has a stale lock; old side is clean.
	if _, err := NewService(authStore("sha256:v1"), nil).Lock(context.Background(), LockOptions{Path: newDir}); err != nil {
		t.Fatal(err)
	}
	s := NewService(authStore("sha256:v2"), nil)
	_, err := s.Diff(context.Background(), DiffOptions{OldPath: oldDir, NewPath: newDir})
	var de *lock.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("Diff should hard-fail on new-side drift, got %v", err)
	}
}

func TestDiffNoLockPasses(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	if _, err := s.Diff(context.Background(), DiffOptions{OldPath: dir, NewPath: dir}); err != nil {
		t.Fatalf("Diff with no lock should pass, got %v", err)
	}
}

func TestValidateSurfacesLockError(t *testing.T) {
	dir := writeRoot(t)
	s := staleLockNextTo(t, dir)
	res, err := s.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("Validate returned raw error: %v", err)
	}
	if res.Valid {
		t.Fatalf("expected invalid result on drift")
	}
	if len(res.Errors) != 1 || res.Errors[0].Code != "LOCK_DIGEST_MISMATCH" {
		t.Fatalf("expected LOCK_DIGEST_MISMATCH result, got %+v", res.Errors)
	}
}

func TestValidateNoLockPasses(t *testing.T) {
	dir := writeRoot(t)
	s := NewService(authStore("sha256:v1"), nil)
	res, err := s.Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// No lock -> verification is a no-op; result is whatever validation says,
	// but it must not carry a LOCK_* error.
	for _, e := range res.Errors {
		if strings.HasPrefix(e.Code, "LOCK_") {
			t.Errorf("unexpected lock error with no lock: %+v", e)
		}
	}
}

func TestPushVerifiesLock(t *testing.T) {
	dir := writeRoot(t)
	// Need a fully valid contract for push's loadAndValidateFull. The minimal
	// root contract lacks runtime/interfaces, so push would fail validation
	// before reaching lock verification. Use a complete bundle on disk.
	full := testutil.WriteTestBundle(t)
	// Add a dependency + write a stale lock next to it.
	yaml := string(testutil.ValidPactoYAML()) + "dependencies:\n  - name: auth\n    ref: oci://ghcr.io/acme/auth\n    compatibility: ^1.0.0\n    required: true\n"
	if err := os.WriteFile(filepath.Join(full, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	s := staleLockNextTo(t, full)
	_, err := s.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/root", Path: full})
	var de *lock.DriftError
	if !errors.As(err, &de) {
		t.Fatalf("Push should hard-fail on drift, got %v", err)
	}
	_ = dir
}

func TestPushNoLockPasses(t *testing.T) {
	full := testutil.WriteTestBundle(t)
	yaml := string(testutil.ValidPactoYAML()) + "dependencies:\n  - name: auth\n    ref: oci://ghcr.io/acme/auth\n    compatibility: ^1.0.0\n    required: true\n"
	if err := os.WriteFile(filepath.Join(full, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	store := authStore("sha256:v1")
	// Push must believe the artifact does not exist yet, then succeed.
	store.ResolveFn = func(_ context.Context, ref string) (string, error) {
		if strings.Contains(ref, "/root") {
			return "", &oci.ArtifactNotFoundError{Ref: ref}
		}
		return "sha256:v1", nil
	}
	store.PushFn = func(_ context.Context, _ string, _ *contract.Bundle) (string, error) { return "sha256:pushed", nil }
	s := NewService(store, nil)
	res, err := s.Push(context.Background(), PushOptions{Ref: "oci://ghcr.io/acme/root", Path: full})
	if err != nil {
		t.Fatalf("Push with no lock should pass, got %v", err)
	}
	if res.Digest != "sha256:pushed" {
		t.Errorf("unexpected push digest %q", res.Digest)
	}
}
