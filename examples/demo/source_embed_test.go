package main

import (
	"os"
	"slices"
	"testing"
	"testing/fstest"
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
	for _, name := range []string{"pacto-demo", "frontend", "payments-service", "orders-service"} {
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
