package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
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

// TestEmbeddedFixturesAreIndexed is the fixture-drift tripwire for the demo. The
// browser demo's whole story — the operational graph, version history, diffs and
// impact — is built by buildDemoFleet from THIS index, so a republish that drops
// a service or a version silently empties product surfaces downstream. Asserting
// the index directly fails at the fixture, where the cause is legible, instead of
// at a Playwright assertion three layers away.
func TestEmbeddedFixturesAreIndexed(t *testing.T) {
	src := bundlesFS(t)

	if len(src.names) < 10 {
		t.Fatalf("expected the demo's full fleet, got %d services: %v", len(src.names), src.names)
	}
	if !slices.IsSorted(src.names) {
		t.Errorf("service names must be sorted, got %v", src.names)
	}
	// The last five carry the behavior and environment features: lifecycle,
	// guarantees and refusals, compatibilityEpoch, a $ref enum and environments.
	for _, name := range []string{"pacto-demo", "frontend", "payments-service", "orders-service",
		"inventory-service", "pricing-service", "shipping-rates", "catalog-service", "storefront-web"} {
		if !slices.Contains(src.names, name) {
			t.Errorf("expected service %q in the embedded fleet, got %v", name, src.names)
		}
	}

	// payments-service is the demo's change-analysis subject: impact compares two
	// of these revisions, so the version history has to survive a republish.
	for _, ver := range []string{"1.0.0", "1.1.0", "1.2.1", "2.0.1", "2.1.0", "2.1.1"} {
		entry := src.byName["payments-service"][ver]
		if entry == nil {
			t.Fatalf("payments-service %s missing from the embedded index", ver)
		}
		if entry.hash == "" {
			t.Errorf("payments-service %s has no content hash", ver)
		}
		if entry.bundle == nil || entry.bundle.FS == nil {
			t.Errorf("payments-service %s bundle is not rooted at its own directory", ver)
		}
	}
}

// plainPactoYAML is a minimal valid contract, enough to be indexed.
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

// TestEmbedSourceIndexesArbitraryFS pins the indexing rules in isolation from the
// committed bundles: one entry per service+version, unparseable files skipped
// rather than fatal, and the bundle FS rooted at the contract's own directory so
// sibling files (schemas, policies, locks) travel with it.
func TestEmbedSourceIndexesArbitraryFS(t *testing.T) {
	src, err := NewEmbedSource(fstest.MapFS{
		"svc-plain/pacto.yaml":      {Data: []byte(plainPactoYAML)},
		"svc-plain/extra.json":      {Data: []byte(`{}`)},
		"broken/pacto.yaml":         {Data: []byte("not: [valid")},
		"nested/deep/notpacto.yaml": {Data: []byte(plainPactoYAML)},
	})
	if err != nil {
		t.Fatalf("NewEmbedSource: %v", err)
	}
	if got := src.names; len(got) != 1 || got[0] != "svc-plain" {
		t.Fatalf("indexed services = %v, want [svc-plain]", got)
	}
	entry := src.byName["svc-plain"]["1.0.0"]
	if entry == nil {
		t.Fatal("svc-plain 1.0.0 not indexed")
	}
	if _, err := entry.bundle.FS.Open("extra.json"); err != nil {
		t.Errorf("bundle FS should be rooted at the contract directory: %v", err)
	}
}

func TestDemoFleetUndeclaredVersionRef(t *testing.T) {
	if got := requestedRef("oci://demo.pacto.local/", "newsletter-service", ""); got != "oci://demo.pacto.local/newsletter-service" {
		t.Fatalf("undeclared version ref = %q", got)
	}
	if got := requestedRef("oci://demo.pacto.local/", "ledger", "1.0.0"); got != "oci://demo.pacto.local/ledger:1.0.0" {
		t.Fatalf("declared version ref = %q", got)
	}
}

// newsletterCompletion is what discovery gives for the demo's newsletter-service
// folder, read with no committed record and as its own repository: never
// RepoRoot, which would find the pacto checkout.
func newsletterCompletion(t *testing.T) *contract.InferredRecord {
	t.Helper()
	src := filepath.Join("bundles", "newsletter-service")
	dir := filepath.Join(t.TempDir(), "newsletter-service")
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, contract.InferredRecordFile)); err != nil {
		t.Fatalf("the demo holds no committed record: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	declared, err := contract.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := discover.CompleteIn(dir, dir, declared, contract.InferredInputs{})
	if err != nil || got == nil {
		t.Fatalf("CompleteIn = %v, %v", got, err)
	}
	return got
}

// TestDemoInferredRecordMatchesCompletion pins the one committed inferred
// record in the demo to what discovery gives for its folder. Only the fields
// and findings are compared: the inputs name the CLI version, which every
// release changes.
func TestDemoInferredRecordMatchesCompletion(t *testing.T) {
	src := filepath.Join("bundles", "newsletter-service")
	got := newsletterCompletion(t)
	committed, err := contract.ReadInferredRecord(os.DirFS(src))
	if err != nil || committed == nil {
		t.Fatalf("committed record = %v, %v", committed, err)
	}
	if !reflect.DeepEqual(committed.Fields, got.Fields) || !reflect.DeepEqual(committed.Findings, got.Findings) {
		t.Errorf("committed record differs from completion; regenerate it:\n%s", got.Marshal())
	}
	if err := contract.VerifyInferredRecord(os.DirFS(src), committed); err != nil {
		t.Errorf("committed record does not match the bundle: %v", err)
	}
}

// TestDemoNewsletterDiscovery pins what the demo shows of a folder Pacto
// reads: the port the Dockerfile exposes, one owner read from the catalog file
// that outranks CODEOWNERS and the team label, the environments its values
// files name, the OpenAPI file at any path and the unlocked chart dependency,
// and never a value of the chart's env.
func TestDemoNewsletterDiscovery(t *testing.T) {
	got := newsletterCompletion(t)
	field := func(name string) contract.Inferred {
		t.Helper()
		i := slices.IndexFunc(got.Fields, func(f contract.Inferred) bool { return f.Field == name })
		if i < 0 {
			t.Fatalf("no %s in\n%s", name, got.Marshal())
		}
		return got.Fields[i]
	}
	if f := field("ports[8080]"); f.Source != "Dockerfile:12" {
		t.Errorf("ports[8080] = %+v, want source Dockerfile:12", f)
	}
	owner := field("service.owner")
	if owner.Source != "catalog-info.yaml:7" || owner.Class != contract.InferredClassOwnerFile || owner.Value != "team-growth" {
		t.Errorf("service.owner = %+v", owner)
	}
	if want := []string{"CODEOWNERS:4", "deploy/deployment.yaml:6"}; !slices.Equal(owner.Outranks, want) {
		t.Errorf("service.owner outranks %v, want %v", owner.Outranks, want)
	}
	for _, env := range []string{"staging", "production"} {
		if f := field("environments[" + env + "].file"); f.Source != "chart/values-"+env+".yaml" || f.Confidence != contract.ConfidencePossible {
			t.Errorf("environments[%s].file = %+v", env, f)
		}
	}
	if f := field("interfaces[api]"); f.Value != "openapi api/openapi.v1.yaml" {
		t.Errorf("interfaces[api] = %+v", f)
	}
	if !slices.ContainsFunc(got.Findings, func(f contract.InferredFinding) bool {
		return f.Code == discover.CodeChartDependencyUnlocked && strings.Contains(f.Message, "'redis'")
	}) {
		t.Errorf("no unlocked redis finding in %+v", got.Findings)
	}
	if out := string(got.Marshal()); strings.Contains(out, "demo-placeholder-password") {
		t.Errorf("the record holds the SMTP_PASSWORD value:\n%s", out)
	}
}
