//go:build integration
// +build integration

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package integration contains integration tests that verify the full OCI pull
// and version resolution flow using an in-process OCI registry.
//
// These tests are tagged with `integration` and excluded from the default test run.
// Run them with: go test -tags=integration ./test/integration/ -v
package integration

import (
	"context"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/oci"

	"github.com/trianalab/pacto-operator/internal/loader"
)

// testOCIEnv bundles an in-process OCI registry and a configured loader for tests.
type testOCIEnv struct {
	server *httptest.Server
	host   string
	client *oci.Client
	loader *loader.Loader
}

func newTestOCIEnv(t *testing.T) *testOCIEnv {
	t.Helper()
	reg := registry.New()
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	client := oci.NewClient(authn.DefaultKeychain, oci.WithNameOptions(name.Insecure))

	return &testOCIEnv{
		server: srv,
		host:   host,
		client: client,
		loader: loader.New(),
	}
}

func (e *testOCIEnv) ref(repo, tag string) string {
	return e.host + "/" + repo + ":" + tag
}

func (e *testOCIEnv) repoRef(repo string) string {
	return e.host + "/" + repo
}

func (e *testOCIEnv) pushBundle(t *testing.T, repo, tag string, bundle *contract.Bundle) string {
	t.Helper()
	ref := e.ref(repo, tag)
	digest, err := e.client.Push(context.Background(), ref, bundle)
	if err != nil {
		t.Fatalf("failed to push bundle to %s: %v", ref, err)
	}
	return digest
}

func newBundle(svcName, version string) *contract.Bundle {
	port := 8080
	pactoYAML := []byte(fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: %s
  version: "%s"
interfaces:
  - name: api
    type: http
    port: 8080
`, svcName, version))

	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service: contract.ServiceIdentity{
				Name:    svcName,
				Version: version,
			},
			Interfaces: []contract.Interface{
				{Name: "api", Type: "http", Port: &port},
			},
		},
		RawYAML: pactoYAML,
		FS: fstest.MapFS{
			"pacto.yaml": &fstest.MapFile{Data: pactoYAML},
			"docs":       &fstest.MapFile{Mode: fs.ModeDir | 0755},
		},
	}
}

func newBundleWithRuntime(svcName, version, workload string) *contract.Bundle {
	port := 8080
	graceful := 30
	pactoYAML := []byte(fmt.Sprintf(`pactoVersion: "1.0"
service:
  name: %s
  version: "%s"
interfaces:
  - name: api
    type: http
    port: 8080
runtime:
  workload: %s
  state:
    type: stateless
  lifecycle:
    upgradeStrategy: rolling
    gracefulShutdownSeconds: 30
`, svcName, version, workload))

	return &contract.Bundle{
		Contract: &contract.Contract{
			PactoVersion: "1.0",
			Service: contract.ServiceIdentity{
				Name:    svcName,
				Version: version,
			},
			Interfaces: []contract.Interface{
				{Name: "api", Type: "http", Port: &port},
			},
			Runtime: &contract.Runtime{
				Workload: workload,
				State:    contract.State{Type: "stateless"},
				Lifecycle: &contract.Lifecycle{
					UpgradeStrategy:         "rolling",
					GracefulShutdownSeconds: &graceful,
				},
			},
		},
		RawYAML: pactoYAML,
		FS: fstest.MapFS{
			"pacto.yaml": &fstest.MapFile{Data: pactoYAML},
			"docs":       &fstest.MapFile{Mode: fs.ModeDir | 0755},
		},
	}
}

// --- Pull tests ---

func TestOCI_Pull_SingleTag(t *testing.T) {
	env := newTestOCIEnv(t)
	env.pushBundle(t, "org/my-svc", "1.0.0", newBundle("my-svc", "1.0.0"))

	result, err := env.loader.Load(context.Background(), env.ref("org/my-svc", "1.0.0"), "", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.Contract.Service.Name != "my-svc" {
		t.Errorf("expected service name my-svc, got %s", result.Contract.Service.Name)
	}
	if result.Contract.Service.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", result.Contract.Service.Version)
	}
	if result.ResolvedRef == "" {
		t.Error("expected ResolvedRef to be populated")
	}
}

func TestOCI_Pull_LatestResolution(t *testing.T) {
	env := newTestOCIEnv(t)

	// Push multiple versions
	env.pushBundle(t, "org/versioned-svc", "1.0.0", newBundle("versioned-svc", "1.0.0"))
	env.pushBundle(t, "org/versioned-svc", "1.1.0", newBundle("versioned-svc", "1.1.0"))
	env.pushBundle(t, "org/versioned-svc", "2.0.0", newBundle("versioned-svc", "2.0.0"))
	env.pushBundle(t, "org/versioned-svc", "1.5.0", newBundle("versioned-svc", "1.5.0"))

	// Load without explicit tag — should resolve to highest semver (2.0.0)
	result, err := env.loader.Load(context.Background(), env.repoRef("org/versioned-svc"), "", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.Contract.Service.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0 (highest semver), got %s", result.Contract.Service.Version)
	}
	if !strings.Contains(result.ResolvedRef, "2.0.0") {
		t.Errorf("expected ResolvedRef to contain 2.0.0, got %s", result.ResolvedRef)
	}
}

func TestOCI_Pull_PinnedTag(t *testing.T) {
	env := newTestOCIEnv(t)

	env.pushBundle(t, "org/pinned-svc", "1.0.0", newBundle("pinned-svc", "1.0.0"))
	env.pushBundle(t, "org/pinned-svc", "2.0.0", newBundle("pinned-svc", "2.0.0"))

	// Load with explicit tag — should use 1.0.0 even though 2.0.0 exists
	result, err := env.loader.Load(context.Background(), env.ref("org/pinned-svc", "1.0.0"), "", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.Contract.Service.Version != "1.0.0" {
		t.Errorf("expected pinned version 1.0.0, got %s", result.Contract.Service.Version)
	}
}

func TestOCI_Pull_NonexistentRef(t *testing.T) {
	env := newTestOCIEnv(t)

	_, err := env.loader.Load(context.Background(), env.ref("org/nonexistent", "1.0.0"), "", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent ref")
	}
}

func TestOCI_Pull_ContractWithRuntime(t *testing.T) {
	env := newTestOCIEnv(t)
	env.pushBundle(t, "org/runtime-svc", "1.0.0", newBundleWithRuntime("runtime-svc", "1.0.0", "service"))

	result, err := env.loader.Load(context.Background(), env.ref("org/runtime-svc", "1.0.0"), "", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.Contract.Runtime == nil {
		t.Fatal("expected runtime section in contract")
	}
	if result.Contract.Runtime.Workload != "service" {
		t.Errorf("expected workload=service, got %s", result.Contract.Runtime.Workload)
	}
	if result.Contract.Runtime.Lifecycle == nil || result.Contract.Runtime.Lifecycle.UpgradeStrategy != "rolling" {
		t.Error("expected lifecycle.upgradeStrategy=rolling")
	}
}

// --- ListTags tests ---

func TestOCI_ListTags(t *testing.T) {
	env := newTestOCIEnv(t)

	env.pushBundle(t, "org/tagged-svc", "1.0.0", newBundle("tagged-svc", "1.0.0"))
	env.pushBundle(t, "org/tagged-svc", "1.1.0", newBundle("tagged-svc", "1.1.0"))
	env.pushBundle(t, "org/tagged-svc", "2.0.0", newBundle("tagged-svc", "2.0.0"))

	tags, err := env.loader.ListTags(context.Background(), env.repoRef("org/tagged-svc"), nil)
	if err != nil {
		t.Fatalf("ListTags failed: %v", err)
	}

	if len(tags) != 3 {
		t.Errorf("expected 3 tags, got %d: %v", len(tags), tags)
	}

	// Tags should be semver-filtered (non-semver tags like "latest" would be excluded)
	for _, tag := range tags {
		if !strings.Contains(tag, ".") {
			t.Errorf("expected semver tags only, got %q", tag)
		}
	}
}

func TestOCI_ListTags_NonexistentRepo(t *testing.T) {
	env := newTestOCIEnv(t)

	_, err := env.loader.ListTags(context.Background(), env.repoRef("org/nonexistent"), nil)
	if err == nil {
		t.Fatal("expected error for nonexistent repo")
	}
}

func TestOCI_ListTags_StripsTagFromRef(t *testing.T) {
	env := newTestOCIEnv(t)

	env.pushBundle(t, "org/strip-svc", "1.0.0", newBundle("strip-svc", "1.0.0"))

	// ListTags should work even if the ref includes a tag
	tags, err := env.loader.ListTags(context.Background(), env.ref("org/strip-svc", "1.0.0"), nil)
	if err != nil {
		t.Fatalf("ListTags failed: %v", err)
	}

	if len(tags) == 0 {
		t.Fatal("expected at least 1 tag")
	}
}

// --- Cache behavior tests ---

func TestOCI_Pull_CacheServesRepeatedCalls(t *testing.T) {
	env := newTestOCIEnv(t)
	env.pushBundle(t, "org/cached-svc", "1.0.0", newBundle("cached-svc", "1.0.0"))

	ref := env.ref("org/cached-svc", "1.0.0")

	// First call
	r1, err := env.loader.Load(context.Background(), ref, "", nil)
	if err != nil {
		t.Fatalf("first Load failed: %v", err)
	}

	// Second call should serve from cache (same result, no error)
	r2, err := env.loader.Load(context.Background(), ref, "", nil)
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}

	if r1.Contract.Service.Name != r2.Contract.Service.Name {
		t.Errorf("cached result should match: %s != %s",
			r1.Contract.Service.Name, r2.Contract.Service.Name)
	}
}

// --- Inline vs OCI precedence ---

func TestOCI_Load_InlineTakesPrecedenceOverOCI(t *testing.T) {
	env := newTestOCIEnv(t)
	env.pushBundle(t, "org/should-not-use", "1.0.0", newBundle("oci-svc", "1.0.0"))

	inlineContract := `pactoVersion: "1.0"
service:
  name: inline-svc
  version: 9.9.9
`

	// When both inline and OCI are provided, inline wins
	result, err := env.loader.Load(context.Background(), env.ref("org/should-not-use", "1.0.0"), inlineContract, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.Contract.Service.Name != "inline-svc" {
		t.Errorf("expected inline-svc (inline takes precedence), got %s", result.Contract.Service.Name)
	}
	if result.ResolvedRef != "" {
		t.Errorf("expected empty ResolvedRef for inline, got %s", result.ResolvedRef)
	}
}

// --- Digest tracking ---

func TestOCI_Pull_DigestPopulated(t *testing.T) {
	env := newTestOCIEnv(t)
	pushDigest := env.pushBundle(t, "org/digest-svc", "1.0.0", newBundle("digest-svc", "1.0.0"))

	result, err := env.loader.Load(context.Background(), env.ref("org/digest-svc", "1.0.0"), "", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.ResolvedDigest == "" {
		t.Fatal("expected ResolvedDigest to be populated")
	}
	if !strings.HasPrefix(result.ResolvedDigest, "sha256:") {
		t.Errorf("expected digest to start with sha256:, got %s", result.ResolvedDigest)
	}
	if result.ResolvedDigest != pushDigest {
		t.Errorf("expected digest %s from push, got %s", pushDigest, result.ResolvedDigest)
	}
}

func TestOCI_Pull_ForcePushChangesDigest(t *testing.T) {
	env := newTestOCIEnv(t)

	// Push v1 content under tag 1.0.0
	digest1 := env.pushBundle(t, "org/force-push-svc", "1.0.0", newBundle("force-push-svc", "1.0.0"))

	// Pull and verify initial digest
	result1, err := env.loader.Load(context.Background(), env.ref("org/force-push-svc", "1.0.0"), "", nil)
	if err != nil {
		t.Fatalf("first Load failed: %v", err)
	}
	if result1.ResolvedDigest != digest1 {
		t.Fatalf("first pull digest mismatch: push=%s pull=%s", digest1, result1.ResolvedDigest)
	}

	// Force-push: overwrite the same tag with different content
	differentBundle := newBundle("force-push-svc", "1.0.0-modified")
	digest2 := env.pushBundle(t, "org/force-push-svc", "1.0.0", differentBundle)

	if digest1 == digest2 {
		t.Fatal("expected different digests after force-push, got same")
	}

	// Create a fresh loader to avoid cache
	freshLoader := loader.New()
	result2, err := freshLoader.Load(context.Background(), env.ref("org/force-push-svc", "1.0.0"), "", nil)
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}
	if result2.ResolvedDigest != digest2 {
		t.Fatalf("second pull digest mismatch: push=%s pull=%s", digest2, result2.ResolvedDigest)
	}

	// The key assertion: digest changed after force-push
	if result1.ResolvedDigest == result2.ResolvedDigest {
		t.Fatal("expected different digests after force-push, but they match")
	}

	t.Logf("Force-push detected: old=%s new=%s", result1.ResolvedDigest, result2.ResolvedDigest)
}

func TestOCI_Pull_InlineHasNoDigest(t *testing.T) {
	env := newTestOCIEnv(t)
	_ = env // just to use the env setup

	inlineContract := `pactoVersion: "1.0"
service:
  name: inline-svc
  version: 1.0.0
`

	result, err := env.loader.Load(context.Background(), "", inlineContract, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if result.ResolvedDigest != "" {
		t.Errorf("expected empty ResolvedDigest for inline, got %s", result.ResolvedDigest)
	}
}

// --- RawYAML populated ---

func TestOCI_Pull_RawYAMLPopulated(t *testing.T) {
	env := newTestOCIEnv(t)
	env.pushBundle(t, "org/raw-svc", "1.0.0", newBundle("raw-svc", "1.0.0"))

	result, err := env.loader.Load(context.Background(), env.ref("org/raw-svc", "1.0.0"), "", nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(result.RawYAML) == 0 {
		t.Error("expected RawYAML to be populated for OCI pulls")
	}

	// RawYAML should contain valid YAML with service info
	raw := string(result.RawYAML)
	if !strings.Contains(raw, "raw-svc") {
		t.Errorf("expected RawYAML to contain service name, got: %s", raw)
	}
}
