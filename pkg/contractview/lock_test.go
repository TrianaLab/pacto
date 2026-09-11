package contractview

import (
	"bytes"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

const sampleLockYAML = `lockVersion: 2
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

func TestBuildGlobalGraph_CarriesLockPins(t *testing.T) {
	services := []Service{{Name: "api", Source: "local"}}
	index := map[string]*ServiceDetails{
		"api": {
			Service: Service{Name: "api"},
			Dependencies: []DependencyInfo{{
				Name: "billing", Ref: "billing", Required: true,
				LockedDigest: "sha256:dep111", LockedVersion: "1.2.3", DriftStatus: "locked",
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
	if e.LockedDigest != "sha256:dep111" || e.LockedVersion != "1.2.3" || e.DriftStatus != "locked" {
		t.Errorf("edge lock pins not carried: %+v", e)
	}
}
