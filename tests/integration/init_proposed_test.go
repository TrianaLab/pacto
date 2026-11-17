//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestInitProposedRangesValidate: pacto init writes the dependency it found
// with compatibility unknown and the proposal from --fleet in a comment, and
// the written contract validates.
func TestInitProposedRangesValidate(t *testing.T) {
	t.Parallel()
	// Parallel tests change the working directory, so the fixture is found
	// from this file.
	_, self, _, _ := runtime.Caller(0)
	dir := filepath.Join(t.TempDir(), "invoices")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(filepath.Dir(self), "..", "..", "internal", "pactify", "testdata", "invoices"))); err != nil {
		t.Fatal(err)
	}
	fleet := filepath.Join(t.TempDir(), "ledger")
	if err := os.MkdirAll(fleet, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fleet, "pacto.yaml"), []byte("pactoVersion: \"2.1\"\nservice: {name: ledger, version: 2.3.1}\npublish: {bundle: oci://reg.example/finance/ledger, sign: {key: k}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runCommand(t, nil, "init", "--fleet", filepath.Dir(fleet), dir); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runCommand(t, nil, "validate", dir); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	written, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(written), "compatibility: unknown   # proposed ^2.3.0, from its contract in --fleet at 2.3.1")
	assertContains(t, string(written), `pactoVersion: "2.5"`)
}
