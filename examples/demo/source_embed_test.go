package main

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/dashboard"
)

// bundlesFS loads the same bundles the wasm binary embeds. Bundles live in
// ./bundles relative to this package, so go test (cwd = package dir) finds them.
func bundlesFS(t *testing.T) *EmbedSource {
	t.Helper()
	src, err := NewEmbedSource(os.DirFS("bundles"))
	if err != nil {
		t.Fatalf("NewEmbedSource: %v", err)
	}
	return src
}

func TestListServices(t *testing.T) {
	src := bundlesFS(t)
	svcs, err := src.ListServices(context.Background())
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) < 10 {
		t.Fatalf("expected the demo's full fleet, got %d services", len(svcs))
	}
	want := map[string]bool{"pacto-demo": false, "frontend": false, "payments-service": false}
	for _, s := range svcs {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected service %q in fleet", name)
		}
	}
}

func TestGetServiceReturnsLatest(t *testing.T) {
	src := bundlesFS(t)
	d, err := src.GetService(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if d.Version != "2.1.0" {
		t.Errorf("payments-service current version = %q, want 2.1.0", d.Version)
	}
}

func TestGetVersionsDescendingWithClassification(t *testing.T) {
	src := bundlesFS(t)
	vs, err := src.GetVersions(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}
	wantOrder := []string{"2.1.0", "2.0.0", "1.2.0", "1.1.0", "1.0.0"}
	if len(vs) != len(wantOrder) {
		t.Fatalf("got %d versions, want %d", len(vs), len(wantOrder))
	}
	for i, w := range wantOrder {
		if vs[i].Version != w {
			t.Errorf("version[%d] = %q, want %q", i, vs[i].Version, w)
		}
		if vs[i].ContractHash == "" {
			t.Errorf("version %q missing contract hash", vs[i].Version)
		}
	}
	for _, v := range vs {
		if v.Version == "2.0.0" && v.Classification != "BREAKING" {
			t.Errorf("2.0.0 classification = %q, want BREAKING", v.Classification)
		}
	}
}

func TestGetDiffBreaking(t *testing.T) {
	src := bundlesFS(t)
	d, err := src.GetDiff(context.Background(),
		dashboard.Ref{Name: "payments-service", Version: "1.2.0"},
		dashboard.Ref{Name: "payments-service", Version: "2.0.0"})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if d.Classification != "BREAKING" {
		t.Errorf("classification = %q, want BREAKING", d.Classification)
	}
	var sawChargesRemoved bool
	for _, c := range d.Changes {
		if c.Path == "openapi.paths[/charges]" {
			sawChargesRemoved = true
		}
	}
	if !sawChargesRemoved {
		t.Errorf("expected removal of /charges path among %d changes", len(d.Changes))
	}
}

func TestGetDiffDefaultsToLatest(t *testing.T) {
	src := bundlesFS(t)
	d, err := src.GetDiff(context.Background(),
		dashboard.Ref{Name: "payments-service", Version: "1.0.0"},
		dashboard.Ref{Name: "payments-service", Version: ""})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if d.To.Version != "2.1.0" {
		t.Errorf("to version = %q, want resolved latest 2.1.0", d.To.Version)
	}
}

// TestReadinessShowcase pins the two readiness fixtures: payments-service 2.1.0
// fails its gate (Score 70 < minScore 80) and orders-service 1.2.0 passes. Dates
// are durable sentinels, so these hold regardless of when the test runs.
func TestReadinessShowcase(t *testing.T) {
	src := bundlesFS(t)

	pay, err := src.GetService(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetService(payments-service): %v", err)
	}
	if pay.Readiness == nil {
		t.Fatal("payments-service 2.1.0 should expose readiness")
	}
	if pay.Readiness.MinScore != 80 {
		t.Errorf("payments minScore = %d, want 80", pay.Readiness.MinScore)
	}
	if pay.Readiness.Score != 70 {
		t.Errorf("payments score = %d, want 70", pay.Readiness.Score)
	}
	if pay.Readiness.Passing {
		t.Error("payments readiness gate should FAIL (70 < 80)")
	}

	ord, err := src.GetService(context.Background(), "orders-service")
	if err != nil {
		t.Fatalf("GetService(orders-service): %v", err)
	}
	if ord.Readiness == nil || !ord.Readiness.Passing {
		t.Errorf("orders-service 1.2.0 readiness should PASS, got %+v", ord.Readiness)
	}
}

func TestEmbedSource_GetServiceVersion(t *testing.T) {
	src := bundlesFS(t)
	details, err := src.GetServiceVersion(context.Background(), dashboard.Ref{Name: "payments-service", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if details == nil {
		t.Fatal("expected non-nil details")
	}
}

// TestEmbedSource_RealBundlesCarryLocks pins the committed demo locks: a
// TestEmbedSource_SurfacesCapabilitiesAndSkills guards that the demo actually
// demonstrates the agent-capabilities feature: services with an http/OpenAPI
// interface expose derived capability tools, and payments-service ships a skill.
func TestEmbedSource_SurfacesCapabilitiesAndSkills(t *testing.T) {
	src := bundlesFS(t)

	pay, err := src.GetService(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetService(payments-service): %v", err)
	}
	if len(pay.Tools) == 0 {
		t.Fatal("payments-service should expose derived capability tools from its OpenAPI")
	}
	var refund bool
	for _, s := range pay.Skills {
		if s.Name == "refund_customer.md" {
			refund = true
			if s.Content == "" {
				t.Error("refund_customer.md skill content should be populated")
			}
		}
	}
	if !refund {
		t.Errorf("payments-service should surface the refund_customer.md skill, got %v", pay.Skills)
	}
}

// dep-bearing bundle (payments-service) surfaces its lock with pinned digests,
// while a leaf bundle (postgresql) has no lock. Guards both the committed lock
// data and EmbedSource's embedded-lock reading against regressions.
func TestEmbedSource_RealBundlesCarryLocks(t *testing.T) {
	src := bundlesFS(t)

	pay, err := src.GetService(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetService(payments-service): %v", err)
	}
	if pay.Lock == nil || !pay.Lock.Present {
		t.Fatal("payments-service should carry an embedded lock")
	}
	// Every declared dependency should be pinned to a sha256 content hash.
	if len(pay.Dependencies) == 0 {
		t.Fatal("payments-service should declare dependencies")
	}
	for _, d := range pay.Dependencies {
		if d.LockedDigest == "" {
			t.Errorf("dependency %q not pinned by the committed lock", d.Name)
		}
	}

	leaf, err := src.GetService(context.Background(), "postgresql")
	if err != nil {
		t.Fatalf("GetService(postgresql): %v", err)
	}
	if leaf.Lock != nil {
		t.Errorf("leaf bundle postgresql must have no lock, got %+v", leaf.Lock)
	}
}

// lockedPactoYAML is a minimal valid contract declaring one dependency, paired
// with lockedLockYAML below so EmbedSource can surface the pin.
const lockedPactoYAML = `pactoVersion: "2.0"
service:
  name: svc-locked
  version: 1.0.0
  owner:
    team: demo
    contacts:
      - type: email
        value: demo@example.com
        purpose: support
dependencies:
  - name: dep-a
    ref: oci://ghcr.io/demo/dep-a
    required: true
    compatibility: "^1.0.0"
`

const lockedLockYAML = `lockVersion: 1
pacto:
  version: 0.0.0
root:
  name: svc-locked
  version: 1.0.0
dependencies:
  - name: dep-a
    source: oci
    ref: oci://ghcr.io/demo/dep-a
    constraint: ^1.0.0
    version: 1.4.2
    digest: sha256:deadbeef
`

const plainPactoYAML = `pactoVersion: "2.0"
service:
  name: svc-plain
  version: 1.0.0
  owner:
    team: demo
    contacts:
      - type: email
        value: demo@example.com
        purpose: support
`

// TestEmbedSource_SurfacesEmbeddedLock proves a bundle with an embedded pacto.lock
// surfaces lock pins, and one without leaves Lock nil — the demo's offline lock
// awareness in isolation, independent of the committed demo bundles.
func TestEmbedSource_SurfacesEmbeddedLock(t *testing.T) {
	fsys := fstest.MapFS{
		"svc-locked/pacto.yaml": {Data: []byte(lockedPactoYAML)},
		"svc-locked/pacto.lock": {Data: []byte(lockedLockYAML)},
		"svc-plain/pacto.yaml":  {Data: []byte(plainPactoYAML)},
	}
	src, err := NewEmbedSource(fsys)
	if err != nil {
		t.Fatalf("NewEmbedSource: %v", err)
	}

	// Locked service: Lock present, dependency pin surfaced.
	locked, err := src.GetService(context.Background(), "svc-locked")
	if err != nil {
		t.Fatalf("GetService(svc-locked): %v", err)
	}
	if locked.Lock == nil || !locked.Lock.Present {
		t.Fatal("expected Lock present for svc-locked")
	}
	if len(locked.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(locked.Dependencies))
	}
	if locked.Dependencies[0].LockedDigest != "sha256:deadbeef" {
		t.Errorf("dep-a locked digest = %q, want sha256:deadbeef", locked.Dependencies[0].LockedDigest)
	}
	if locked.Dependencies[0].LockedVersion != "1.4.2" {
		t.Errorf("dep-a locked version = %q, want 1.4.2", locked.Dependencies[0].LockedVersion)
	}
	// No k8s runtime in the demo, so no drift assertion is made.
	if locked.Dependencies[0].DriftStatus != "" {
		t.Errorf("expected empty DriftStatus offline, got %q", locked.Dependencies[0].DriftStatus)
	}

	// Same pins on the versioned read path.
	lockedV, err := src.GetServiceVersion(context.Background(), dashboard.Ref{Name: "svc-locked", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("GetServiceVersion(svc-locked): %v", err)
	}
	if lockedV.Lock == nil || lockedV.Dependencies[0].LockedDigest != "sha256:deadbeef" {
		t.Error("expected lock pins on versioned read")
	}

	// Plain service: no lockfile → nil Lock, unchanged behavior.
	plain, err := src.GetService(context.Background(), "svc-plain")
	if err != nil {
		t.Fatalf("GetService(svc-plain): %v", err)
	}
	if plain.Lock != nil {
		t.Errorf("expected nil Lock for svc-plain, got %+v", plain.Lock)
	}
}
