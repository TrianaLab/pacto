package dashboard

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/lock"
)

const sampleLockYAML = `lockVersion: 1
pacto:
  version: 0.9.0
root:
  name: api
  version: 1.0.0
dependencies:
  - name: billing
    source: oci
    ref: ghcr.io/org/billing-pacto
    constraint: ^1.0.0
    version: 1.2.3
    digest: sha256:dep111
    contentHash: aaa
references:
  - kind: config
    name: shared-config
    source: oci
    ref: ghcr.io/org/shared-pacto
    version: 2.0.0
    digest: sha256:cfg222
    contentHash: bbb
  - kind: policy
    name: default
    source: oci
    ref: ghcr.io/org/policy-pacto
    version: 3.0.0
    digest: sha256:pol333
    contentHash: ccc
`

func writeLockFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, lock.FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLockFromFS_Present(t *testing.T) {
	fsys := fstest.MapFS{lock.FileName: {Data: []byte(sampleLockYAML)}}

	l, err := lockFromFS(fsys)
	if err != nil {
		t.Fatalf("lockFromFS: %v", err)
	}
	if l == nil {
		t.Fatal("expected non-nil lock")
	}
	if l.Root.Name != "api" {
		t.Errorf("expected root name 'api', got %q", l.Root.Name)
	}
	if len(l.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(l.Dependencies))
	}
}

func TestLockFromFS_Absent(t *testing.T) {
	l, err := lockFromFS(fstest.MapFS{})
	if err != nil {
		t.Fatalf("expected nil error for absent lock, got %v", err)
	}
	if l != nil {
		t.Fatalf("expected nil lock for absent file, got %+v", l)
	}
}

func TestLockFromFS_NilFS(t *testing.T) {
	l, err := lockFromFS(nil)
	if err != nil {
		t.Fatalf("expected nil error for nil FS, got %v", err)
	}
	if l != nil {
		t.Fatalf("expected nil lock for nil FS, got %+v", l)
	}
}

func TestLockFromFS_ParseError(t *testing.T) {
	// Valid YAML but wrong lockVersion → lock.Parse returns an error.
	fsys := fstest.MapFS{lock.FileName: {Data: []byte("lockVersion: 99\nroot:\n  name: x\n")}}
	_, err := lockFromFS(fsys)
	if err == nil {
		t.Fatal("expected error for unsupported lockVersion")
	}
}

func TestLockFromFS_ReadError(t *testing.T) {
	// A non-ErrNotExist read error (here: pacto.lock is a directory) is surfaced.
	fsys := fstest.MapFS{lock.FileName + "/nested": {Data: []byte("x")}}
	_, err := lockFromFS(fsys)
	if err == nil {
		t.Fatal("expected error when pacto.lock is not a regular file")
	}
}

// TestServiceDetailsFromBundle_EmbeddedLock proves the uniform lock read: an
// OCI/cache-style bundle whose in-memory FS carries pacto.lock has its pins
// surfaced (Lock + dependency LockedDigest) by ServiceDetailsFromBundle, exactly
// like a local on-disk bundle. This is what lights up drift for non-local sources.
func TestServiceDetailsFromBundle_EmbeddedLock(t *testing.T) {
	c, err := contract.Parse(bytes.NewReader([]byte(`pactoVersion: "2.0"
service:
  name: api
  version: 1.0.0
dependencies:
  - name: billing
    ref: ghcr.io/org/billing-pacto
    required: true
`)))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{lock.FileName: {Data: []byte(sampleLockYAML)}}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, FS: fsys}, "oci")

	if details.Lock == nil || !details.Lock.Present {
		t.Fatal("expected embedded lock to be applied for an OCI/cache bundle")
	}
	if len(details.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(details.Dependencies))
	}
	if details.Dependencies[0].LockedDigest != "sha256:dep111" {
		t.Errorf("billing locked digest = %q, want sha256:dep111", details.Dependencies[0].LockedDigest)
	}
}

// TestServiceDetailsFromBundle_NoEmbeddedLock proves an FS without pacto.lock
// leaves Lock nil (unchanged behavior).
func TestServiceDetailsFromBundle_NoEmbeddedLock(t *testing.T) {
	c, err := contract.Parse(bytes.NewReader([]byte("pactoVersion: \"2.0\"\nservice:\n  name: api\n  version: 1.0.0\n")))
	if err != nil {
		t.Fatal(err)
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, FS: fstest.MapFS{}}, "oci")
	if details.Lock != nil {
		t.Errorf("expected nil Lock without an embedded lockfile, got %+v", details.Lock)
	}
}

// TestServiceDetailsFromBundle_NilFSNoPanic proves a bundle with a nil FS does
// not panic and surfaces no lock.
func TestServiceDetailsFromBundle_NilFSNoPanic(t *testing.T) {
	c, err := contract.Parse(bytes.NewReader([]byte("pactoVersion: \"2.0\"\nservice:\n  name: api\n  version: 1.0.0\n")))
	if err != nil {
		t.Fatal(err)
	}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "oci")
	if details.Lock != nil {
		t.Errorf("expected nil Lock for nil FS, got %+v", details.Lock)
	}
}

// TestServiceDetailsFromBundle_MalformedEmbeddedLockIgnored proves a malformed
// embedded lock is best-effort: it is ignored (nil Lock) rather than dropping the
// whole service from the dashboard.
func TestServiceDetailsFromBundle_MalformedEmbeddedLockIgnored(t *testing.T) {
	c, err := contract.Parse(bytes.NewReader([]byte("pactoVersion: \"2.0\"\nservice:\n  name: api\n  version: 1.0.0\n")))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{lock.FileName: {Data: []byte("lockVersion: 99\nroot:\n  name: api\n")}}
	details := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, FS: fsys}, "oci")
	if details.Lock != nil {
		t.Errorf("expected nil Lock for malformed embedded lock, got %+v", details.Lock)
	}
}

func TestDriftStatus(t *testing.T) {
	tests := []struct {
		name    string
		locked  string
		runtime string
		want    string
	}{
		{"unlocked when no locked digest", "", "sha256:abc", driftUnlocked},
		{"unknown when no runtime digest", "sha256:abc", "", driftUnknown},
		{"locked when equal", "sha256:abc", "sha256:abc", driftLocked},
		{"drift when different", "sha256:abc", "sha256:def", driftDrift},
		{"locked with repo-qualified runtime", "sha256:abc", "ghcr.io/org/svc@sha256:abc", driftLocked},
		{"locked with repo-qualified locked", "ghcr.io/org/svc@sha256:abc", "sha256:abc", driftLocked},
		{"drift with repo-qualified both", "ghcr.io/org/svc@sha256:abc", "ghcr.io/org/svc@sha256:def", driftDrift},
		{"unlocked even with empty both", "", "", driftUnlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := driftStatus(tc.locked, tc.runtime); got != tc.want {
				t.Errorf("driftStatus(%q,%q) = %q, want %q", tc.locked, tc.runtime, got, tc.want)
			}
		})
	}
}

func TestDigestPart(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"sha256:abc", "sha256:abc"},
		{"ghcr.io/org/svc@sha256:abc", "sha256:abc"},
		{"", ""},
		{"ghcr.io/org/svc:1.0.0", "ghcr.io/org/svc:1.0.0"},
	}
	for _, tc := range tests {
		if got := digestPart(tc.in); got != tc.want {
			t.Errorf("digestPart(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestApplyLock_Exported(t *testing.T) {
	// ApplyLock is the exported hook other packages (the WASM demo's EmbedSource)
	// use to surface lock pins without an on-disk lockfile. It must behave exactly
	// like the internal path: set Lock and pin matching dependency entries.
	l, err := lock.Parse([]byte(sampleLockYAML))
	if err != nil {
		t.Fatal(err)
	}
	svc := &ServiceDetails{Dependencies: []DependencyInfo{{Name: "billing"}}}
	ApplyLock(svc, l)
	if svc.Lock == nil || !svc.Lock.Present {
		t.Fatal("expected Lock present after ApplyLock")
	}
	if svc.Dependencies[0].LockedDigest != "sha256:dep111" {
		t.Errorf("billing locked digest = %q, want sha256:dep111", svc.Dependencies[0].LockedDigest)
	}

	// A nil lock is a no-op (backward compatible).
	clean := &ServiceDetails{Dependencies: []DependencyInfo{{Name: "x"}}}
	ApplyLock(clean, nil)
	if clean.Lock != nil {
		t.Error("expected nil Lock when lock is nil")
	}
}

func TestApplyLock_PopulatesAllSections(t *testing.T) {
	l, err := lock.Parse([]byte(sampleLockYAML))
	if err != nil {
		t.Fatal(err)
	}
	svc := &ServiceDetails{
		Dependencies: []DependencyInfo{
			{Name: "billing"},
			{Name: "unmatched"},
		},
		Configurations: []ConfigurationInfo{
			{Name: "shared-config"},
			{Name: "no-ref-config"},
		},
		Policies: []PolicyInfo{
			{Name: "default"},
			{Name: "other"},
		},
	}
	ApplyLock(svc, l)

	if svc.Lock == nil {
		t.Fatal("expected svc.Lock to be set")
	}
	if !svc.Lock.Present {
		t.Error("expected Lock.Present = true")
	}
	if len(svc.Lock.Dependencies) != 1 {
		t.Errorf("expected 1 lock dep, got %d", len(svc.Lock.Dependencies))
	}
	if len(svc.Lock.References) != 2 {
		t.Errorf("expected 2 lock refs, got %d", len(svc.Lock.References))
	}

	// Dependency by name.
	if svc.Dependencies[0].LockedDigest != "sha256:dep111" {
		t.Errorf("billing locked digest = %q", svc.Dependencies[0].LockedDigest)
	}
	if svc.Dependencies[0].LockedVersion != "1.2.3" {
		t.Errorf("billing locked version = %q", svc.Dependencies[0].LockedVersion)
	}
	if svc.Dependencies[1].LockedDigest != "" {
		t.Error("unmatched dep must stay empty")
	}

	// Config reference by name (kind=config).
	if svc.Configurations[0].LockedDigest != "sha256:cfg222" {
		t.Errorf("shared-config locked digest = %q", svc.Configurations[0].LockedDigest)
	}
	if svc.Configurations[0].LockedVersion != "2.0.0" {
		t.Errorf("shared-config locked version = %q", svc.Configurations[0].LockedVersion)
	}
	if svc.Configurations[1].LockedDigest != "" {
		t.Error("no-ref-config must stay empty")
	}

	// Policy reference by name (kind=policy).
	if svc.Policies[0].LockedDigest != "sha256:pol333" {
		t.Errorf("default policy locked digest = %q", svc.Policies[0].LockedDigest)
	}
	if svc.Policies[0].LockedVersion != "3.0.0" {
		t.Errorf("default policy locked version = %q", svc.Policies[0].LockedVersion)
	}
	if svc.Policies[1].LockedDigest != "" {
		t.Error("other policy must stay empty")
	}
}

// TestApplyLock_PartialFields locks the behavior for dependency entries with
// partially-populated pins: empty Digest + set Version, set Digest + empty
// Version, and both empty. ApplyLock must copy whatever is present onto the
// matching DependencyInfo without panicking.
func TestApplyLock_PartialFields(t *testing.T) {
	l := &lock.Lock{
		LockVersion: lock.CurrentLockVersion,
		Dependencies: []lock.Entry{
			{Name: "ver-only", Source: "oci", Version: "1.2.3"},      // (a) empty digest, set version
			{Name: "digest-only", Source: "oci", Digest: "sha256:d"}, // (b) set digest, empty version
			{Name: "both-empty", Source: "local", ContentHash: "h"},  // (c) both empty
		},
	}
	svc := &ServiceDetails{Dependencies: []DependencyInfo{
		{Name: "ver-only"},
		{Name: "digest-only"},
		{Name: "both-empty"},
	}}
	ApplyLock(svc, l)

	if svc.Dependencies[0].LockedVersion != "1.2.3" || svc.Dependencies[0].LockedDigest != "" {
		t.Errorf("ver-only: %+v", svc.Dependencies[0])
	}
	if svc.Dependencies[1].LockedDigest != "sha256:d" || svc.Dependencies[1].LockedVersion != "" {
		t.Errorf("digest-only: %+v", svc.Dependencies[1])
	}
	if svc.Dependencies[2].LockedDigest != "" || svc.Dependencies[2].LockedVersion != "" {
		t.Errorf("both-empty: %+v", svc.Dependencies[2])
	}
}

// TestEnrichDrift_MissingLockedFields locks the behavior that a dependency with
// no LockedDigest is left with an empty DriftStatus (no "drift"/"locked"
// assertion) even when the target carries a runtime digest.
func TestEnrichDrift_MissingLockedFields(t *testing.T) {
	index := map[string]*ServiceDetails{
		"api": {
			Service:      Service{Name: "api"},
			Dependencies: []DependencyInfo{{Name: "billing"}}, // no LockedDigest
		},
		"billing": {Service: Service{Name: "billing"}, ResolvedRef: "ghcr.io/org/billing@sha256:run"},
	}
	enrichDrift(index)
	if got := index["api"].Dependencies[0].DriftStatus; got != "" {
		t.Errorf("expected empty DriftStatus for unlocked dep, got %q", got)
	}
}

func TestApplyLock_NilLock(t *testing.T) {
	svc := &ServiceDetails{Dependencies: []DependencyInfo{{Name: "x"}}}
	ApplyLock(svc, nil)
	if svc.Lock != nil {
		t.Error("expected nil Lock when lock is nil")
	}
	if svc.Dependencies[0].LockedDigest != "" {
		t.Error("expected unchanged dependency when lock is nil")
	}
}

func TestApplyLock_RootDigestFromContentHash(t *testing.T) {
	l, err := lock.Parse([]byte(sampleLockYAML))
	if err != nil {
		t.Fatal(err)
	}
	svc := &ServiceDetails{}
	ApplyLock(svc, l)
	if svc.Lock == nil {
		t.Fatal("expected Lock set")
	}
	// Sample root has no digest; RootDigest stays empty.
	if svc.Lock.RootDigest != "" {
		t.Errorf("expected empty RootDigest, got %q", svc.Lock.RootDigest)
	}
}

func TestLocalSource_GetService_WithLock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "api")
	writeLocalPactoYAML(t, dir, "api", "1.0.0")
	writeLockFile(t, dir, sampleLockYAML)

	src := NewLocalSource(root)
	details, err := src.GetService(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if details.Lock == nil {
		t.Fatal("expected Lock to be populated from disk")
	}
	if !details.Lock.Present {
		t.Error("expected Lock.Present = true")
	}
	if len(details.Lock.Dependencies) != 1 {
		t.Errorf("expected 1 lock dependency, got %d", len(details.Lock.Dependencies))
	}
}

func TestLocalSource_GetService_NoLock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "api")
	writeLocalPactoYAML(t, dir, "api", "1.0.0")

	src := NewLocalSource(root)
	details, err := src.GetService(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if details.Lock != nil {
		t.Errorf("expected nil Lock with no lockfile, got %+v", details.Lock)
	}
}

func TestLocalSource_GetServiceVersion_WithLock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "api")
	writeLocalPactoYAML(t, dir, "api", "1.0.0")
	writeLockFile(t, dir, sampleLockYAML)

	src := NewLocalSource(root)
	details, err := src.GetServiceVersion(context.Background(), Ref{Name: "api", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if details.Lock == nil {
		t.Fatal("expected Lock to be populated from disk on versioned read")
	}
}

func TestRuntimeDigest(t *testing.T) {
	tests := []struct {
		name string
		d    *ServiceDetails
		want string
	}{
		{"from resolvedRef", &ServiceDetails{ResolvedRef: "ghcr.io/org/svc@sha256:abc"}, "sha256:abc"},
		{"bare resolvedRef", &ServiceDetails{ResolvedRef: "sha256:def"}, "sha256:def"},
		{"fallback to currentRevision", &ServiceDetails{ResolvedRef: "ghcr.io/org/svc:1.0.0", CurrentRevision: "sha256:rev"}, "sha256:rev"},
		{"none", &ServiceDetails{ResolvedRef: "ghcr.io/org/svc:1.0.0"}, ""},
		{"empty", &ServiceDetails{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeDigest(tc.d); got != tc.want {
				t.Errorf("runtimeDigest = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnrichDrift(t *testing.T) {
	index := map[string]*ServiceDetails{
		"api": {
			Service: Service{Name: "api"},
			Dependencies: []DependencyInfo{
				{Name: "billing", LockedDigest: "sha256:match"},   // target runtime matches → locked
				{Name: "shipping", LockedDigest: "sha256:locked"}, // target runtime differs → drift
				{Name: "audit", LockedDigest: "sha256:noruntime"}, // target has no runtime digest → unknown
				{Name: "ghost", LockedDigest: "sha256:gone"},      // target missing from index → unknown
				{Name: "unpinned"}, // no locked digest → untouched
			},
		},
		"billing":  {Service: Service{Name: "billing"}, ResolvedRef: "ghcr.io/org/billing@sha256:match"},
		"shipping": {Service: Service{Name: "shipping"}, ResolvedRef: "ghcr.io/org/shipping@sha256:other"},
		"audit":    {Service: Service{Name: "audit"}, ResolvedRef: "ghcr.io/org/audit:1.0.0"},
	}
	enrichDrift(index)

	deps := index["api"].Dependencies
	if deps[0].DriftStatus != driftLocked {
		t.Errorf("billing drift = %q, want locked", deps[0].DriftStatus)
	}
	if deps[1].DriftStatus != driftDrift {
		t.Errorf("shipping drift = %q, want drift", deps[1].DriftStatus)
	}
	if deps[2].DriftStatus != driftUnknown {
		t.Errorf("audit drift = %q, want unknown", deps[2].DriftStatus)
	}
	if deps[3].DriftStatus != driftUnknown {
		t.Errorf("ghost drift = %q, want unknown", deps[3].DriftStatus)
	}
	if deps[4].DriftStatus != "" {
		t.Errorf("unpinned drift = %q, want empty (untouched)", deps[4].DriftStatus)
	}
}

func TestEnrichDrift_NilDetails(t *testing.T) {
	// An index entry whose target is nil must not panic and yields unknown.
	index := map[string]*ServiceDetails{
		"api": {
			Service:      Service{Name: "api"},
			Dependencies: []DependencyInfo{{Name: "x", LockedDigest: "sha256:abc"}},
		},
		"x": nil,
	}
	enrichDrift(index)
	if index["api"].Dependencies[0].DriftStatus != driftUnknown {
		t.Errorf("expected unknown for nil target, got %q", index["api"].Dependencies[0].DriftStatus)
	}
}

// cloningSource returns a fresh deep copy of each ServiceDetails on every read so
// the cached index and a detail fetch are guaranteed to be distinct objects. This
// proves the detail-view drift comes from enrichDetailDriftFromIndex and not from a
// shared pointer that enrichDrift already mutated while building the index.
type cloningSource struct {
	services []Service
	details  map[string]*ServiceDetails
}

func cloneDetails(d *ServiceDetails) *ServiceDetails {
	if d == nil {
		return nil
	}
	c := *d
	c.Dependencies = append([]DependencyInfo(nil), d.Dependencies...)
	return &c
}

func (c *cloningSource) ListServices(context.Context) ([]Service, error) {
	return append([]Service(nil), c.services...), nil
}

func (c *cloningSource) GetService(_ context.Context, name string) (*ServiceDetails, error) {
	if d, ok := c.details[name]; ok {
		return cloneDetails(d), nil
	}
	return nil, context.Canceled
}

func (c *cloningSource) GetServiceVersion(_ context.Context, ref Ref) (*ServiceDetails, error) {
	if d, ok := c.details[ref.Name]; ok {
		return cloneDetails(d), nil
	}
	return nil, context.Canceled
}

func (c *cloningSource) GetVersions(context.Context, string) ([]Version, error) {
	return []Version{{Version: "1.0.0"}}, nil
}

func (c *cloningSource) GetDiff(context.Context, Ref, Ref) (*DiffResult, error) {
	return nil, context.Canceled
}

// TestGetService_DependencyDriftReachesDetail proves Finding 2: the service-DETAIL
// response (getService), not only the index/graph, carries dependency DriftStatus.
// "api" locks "billing" to a digest that differs from billing's runtime digest
// (→ drift) and "audit" to one that matches (→ locked); the freshly-fetched detail
// must surface both. A dependency with no lock stays empty (no badge).
func TestGetService_DependencyDriftReachesDetail(t *testing.T) {
	source := &cloningSource{
		services: []Service{
			{Name: "api", Source: "local"},
			{Name: "billing", Source: "k8s"},
			{Name: "audit", Source: "k8s"},
			{Name: "free", Source: "k8s"},
		},
		details: map[string]*ServiceDetails{
			"api": {
				Service: Service{Name: "api", Version: "1.0.0", Source: "local"},
				Dependencies: []DependencyInfo{
					{Name: "billing", Ref: "ghcr.io/org/billing-pacto", LockedDigest: "sha256:locked", LockedVersion: "1.2.3"},
					{Name: "audit", Ref: "ghcr.io/org/audit-pacto", LockedDigest: "sha256:match", LockedVersion: "2.0.0"},
					{Name: "free", Ref: "ghcr.io/org/free-pacto"}, // no lock → no drift assertion
				},
			},
			"billing": {Service: Service{Name: "billing"}, ResolvedRef: "ghcr.io/org/billing@sha256:other"},
			"audit":   {Service: Service{Name: "audit"}, ResolvedRef: "ghcr.io/org/audit@sha256:match"},
			"free":    {Service: Service{Name: "free"}},
		},
	}

	srv := NewServer(source, nil)
	out, err := srv.getService(context.Background(), &ServiceNameInput{Name: "api"})
	if err != nil {
		t.Fatalf("getService: %v", err)
	}
	deps := out.Body.Dependencies
	if len(deps) != 3 {
		t.Fatalf("expected 3 dependencies, got %d", len(deps))
	}
	if deps[0].DriftStatus != driftDrift {
		t.Errorf("billing detail drift = %q, want %q", deps[0].DriftStatus, driftDrift)
	}
	if deps[1].DriftStatus != driftLocked {
		t.Errorf("audit detail drift = %q, want %q", deps[1].DriftStatus, driftLocked)
	}
	if deps[2].DriftStatus != "" {
		t.Errorf("free detail drift = %q, want empty (no lock)", deps[2].DriftStatus)
	}

	// Lock pins must also be present on the detail (carried even if the source
	// itself didn't apply them).
	if deps[0].LockedDigest != "sha256:locked" || deps[0].LockedVersion != "1.2.3" {
		t.Errorf("billing lock pins missing on detail: %+v", deps[0])
	}

	// The historical view must agree with the graph too.
	vout, err := srv.getServiceVersion(context.Background(), &serviceVersionInput{Name: "api", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("getServiceVersion: %v", err)
	}
	if vout.Body.Dependencies[0].DriftStatus != driftDrift {
		t.Errorf("billing version-detail drift = %q, want %q", vout.Body.Dependencies[0].DriftStatus, driftDrift)
	}
}

func TestEnrichDetailDriftFromIndex_EdgeCases(t *testing.T) {
	// nil details is a no-op (no panic).
	enrichDetailDriftFromIndex(nil, map[string]*ServiceDetails{})

	index := map[string]*ServiceDetails{
		"api": {
			Service: Service{Name: "api"},
			Dependencies: []DependencyInfo{
				{Name: "billing", Ref: "ghcr.io/org/billing-pacto", DriftStatus: driftDrift, LockedDigest: "sha256:l", LockedVersion: "1.0.0"},
				{Name: "audit", DriftStatus: driftLocked}, // refless: matched by name
			},
		},
	}

	// Service absent from the index → untouched.
	absent := &ServiceDetails{Service: Service{Name: "ghost"}, Dependencies: []DependencyInfo{{Name: "x", DriftStatus: ""}}}
	enrichDetailDriftFromIndex(absent, index)
	if absent.Dependencies[0].DriftStatus != "" {
		t.Errorf("absent service drift = %q, want empty", absent.Dependencies[0].DriftStatus)
	}

	// Ref match, name-fallback match, and an unmatched dependency in one detail.
	detail := &ServiceDetails{
		Service: Service{Name: "api"},
		Dependencies: []DependencyInfo{
			{Name: "billing-renamed", Ref: "ghcr.io/org/billing-pacto"}, // matched by ref despite different name
			{Name: "audit"}, // refless → matched by name
			{Name: "unknown-dep", Ref: "ghcr.io/org/unknown-pacto"}, // no match → untouched
		},
	}
	enrichDetailDriftFromIndex(detail, index)
	if detail.Dependencies[0].DriftStatus != driftDrift {
		t.Errorf("ref-matched drift = %q, want drift", detail.Dependencies[0].DriftStatus)
	}
	if detail.Dependencies[0].LockedDigest != "sha256:l" || detail.Dependencies[0].LockedVersion != "1.0.0" {
		t.Errorf("ref-matched lock pins not carried: %+v", detail.Dependencies[0])
	}
	if detail.Dependencies[1].DriftStatus != driftLocked {
		t.Errorf("name-matched drift = %q, want locked", detail.Dependencies[1].DriftStatus)
	}
	if detail.Dependencies[2].DriftStatus != "" {
		t.Errorf("unmatched drift = %q, want empty", detail.Dependencies[2].DriftStatus)
	}

	// Existing lock pins on the detail are preserved (not overwritten by the index).
	preset := &ServiceDetails{
		Service:      Service{Name: "api"},
		Dependencies: []DependencyInfo{{Name: "billing", Ref: "ghcr.io/org/billing-pacto", LockedDigest: "sha256:preset", LockedVersion: "9.9.9"}},
	}
	enrichDetailDriftFromIndex(preset, index)
	if preset.Dependencies[0].LockedDigest != "sha256:preset" || preset.Dependencies[0].LockedVersion != "9.9.9" {
		t.Errorf("preset lock pins overwritten: %+v", preset.Dependencies[0])
	}
}

func TestBuildGlobalGraph_CarriesLockPins(t *testing.T) {
	services := []Service{{Name: "api", Source: "local"}}
	index := map[string]*ServiceDetails{
		"api": {
			Service: Service{Name: "api"},
			Dependencies: []DependencyInfo{{
				Name: "billing", Ref: "billing", Required: true,
				LockedDigest: "sha256:dep111", LockedVersion: "1.2.3", DriftStatus: driftLocked,
			}},
		},
		"billing": {Service: Service{Name: "billing"}},
	}
	graph := buildGlobalGraph(services, index, nil)
	var api *GraphNodeData
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "api" {
			api = &graph.Nodes[i]
		}
	}
	if api == nil || len(api.Edges) != 1 {
		t.Fatalf("expected api node with 1 edge, got %+v", api)
	}
	e := api.Edges[0]
	if e.LockedDigest != "sha256:dep111" || e.LockedVersion != "1.2.3" || e.DriftStatus != driftLocked {
		t.Errorf("edge lock pins not carried: %+v", e)
	}
}

func TestBuildGraph_CarriesLockPins(t *testing.T) {
	index := map[string]*ServiceDetails{
		"api": {
			Service: Service{Name: "api"},
			Dependencies: []DependencyInfo{{
				Name: "billing", Ref: "billing", Required: true,
				LockedDigest: "sha256:dep111", LockedVersion: "1.2.3", DriftStatus: driftDrift,
			}},
		},
		"billing": {Service: Service{Name: "billing"}},
	}
	g := buildGraph(index["api"], index, nil)
	if g.Root == nil || len(g.Root.Dependencies) != 1 {
		t.Fatalf("expected root with 1 dependency, got %+v", g.Root)
	}
	e := g.Root.Dependencies[0]
	if e.LockedDigest != "sha256:dep111" || e.LockedVersion != "1.2.3" || e.DriftStatus != driftDrift {
		t.Errorf("graph edge lock pins not carried: %+v", e)
	}
}

func TestLocalSource_GetService_MalformedLockIgnored(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "api")
	writeLocalPactoYAML(t, dir, "api", "1.0.0")
	// A malformed lock (unsupported lockVersion) is best-effort: it is treated as
	// absent so a bad embedded lock never breaks the dashboard for that service.
	writeLockFile(t, dir, "lockVersion: 99\nroot:\n  name: api\n")

	src := NewLocalSource(root)
	details, err := src.GetService(context.Background(), "api")
	if err != nil {
		t.Fatalf("expected no error for malformed lockfile, got %v", err)
	}
	if details.Lock != nil {
		t.Errorf("expected nil Lock for malformed lockfile, got %+v", details.Lock)
	}
}

func TestLocalSource_GetServiceVersion_MalformedLockIgnored(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "api")
	writeLocalPactoYAML(t, dir, "api", "1.0.0")
	writeLockFile(t, dir, "lockVersion: 99\nroot:\n  name: api\n")

	src := NewLocalSource(root)
	details, err := src.GetServiceVersion(context.Background(), Ref{Name: "api", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("expected no error for malformed lockfile on versioned read, got %v", err)
	}
	if details.Lock != nil {
		t.Errorf("expected nil Lock for malformed lockfile, got %+v", details.Lock)
	}
}
