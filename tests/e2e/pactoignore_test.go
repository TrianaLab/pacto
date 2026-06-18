//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// ignoreContract is a minimal valid contract whose only interface points at
// interfaces/openapi.yaml, so the kept interface file is exercised end-to-end.
const ignoreContract = `pactoVersion: "1.0"
service:
  name: ignore-svc
  version: 1.0.0
  owner: team/platform
interfaces:
  - name: api
    type: http
    port: 8080
    visibility: internal
    contract: interfaces/openapi.yaml
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
`

const pactoignoreFile = `# test ignore file
*.secret
build/
scratch/**
`

// writeIgnoreBundle builds a bundle dir containing a .pactoignore, kept files
// (pacto.yaml, interfaces/openapi.yaml, README.md) and files that the patterns
// must drop (token.secret, build/out.bin, scratch/x/y.txt).
func writeIgnoreBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ignore-svc")

	mustWrite := func(rel, content string) {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("pacto.yaml", ignoreContract)
	mustWrite(".pactoignore", pactoignoreFile)
	mustWrite("interfaces/openapi.yaml", fmt.Sprintf(openapiTemplate, "ignore-svc", "1.0.0"))
	mustWrite("README.md", "# Ignore Service\n")
	mustWrite("token.secret", "supersecret\n")
	mustWrite("build/out.bin", "binary-artifact\n")
	mustWrite("scratch/x/y.txt", "scratch notes\n")

	return dir
}

// keptInArchive are the bundle-relative paths that must survive .pactoignore.
var keptInArchive = []string{"pacto.yaml", "interfaces/openapi.yaml", "README.md"}

// droppedFromArchive are the paths that .pactoignore (plus defaults) must drop.
var droppedFromArchive = []string{".pactoignore", "token.secret", "build/out.bin", "scratch/x/y.txt"}

// TestPactoignoreExcludesFromPack proves .pactoignore filters files at bundle
// load time so `pacto pack` excludes them: the archive keeps pacto.yaml + the
// referenced interface + README.md, and drops the ignored files (and the
// default-ignored .pactoignore itself).
func TestPactoignoreExcludesFromPack(t *testing.T) {
	t.Parallel()
	dir := writeIgnoreBundle(t)
	archive := filepath.Join(t.TempDir(), "ignore-svc.tar.gz")

	out, err := runCommand(t, nil, "pack", dir, "-o", archive)
	if err != nil {
		t.Fatalf("pack failed: %v\noutput: %s", err, out)
	}

	for _, kept := range keptInArchive {
		verifyArchiveContains(t, archive, kept)
	}
	for _, dropped := range droppedFromArchive {
		assertArchiveExcludes(t, archive, dropped)
	}
}

// TestPactoignoreSurvivesPushPull proves the filter applies through the real OCI
// round-trip: after push then pull to a fresh dir, the pulled tree keeps
// pacto.yaml + the interface file but omits the ignored files. This proves the
// exclusion happens at load, not only in local pack.
func TestPactoignoreSurvivesPushPull(t *testing.T) {
	t.Parallel()
	reg := newTestRegistry(t)
	dir := writeIgnoreBundle(t)
	ref := "oci://" + reg.host + "/ignore-svc:1.0.0"

	if _, err := runCommand(t, reg, "push", ref, "-p", dir); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	pullDir := filepath.Join(t.TempDir(), "pulled")
	if _, err := runCommand(t, reg, "pull", ref, "-o", pullDir); err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	// Kept files must be present in the pulled tree.
	for _, kept := range keptInArchive {
		if _, err := os.Stat(filepath.Join(pullDir, filepath.FromSlash(kept))); err != nil {
			t.Errorf("expected %s in pulled tree: %v", kept, err)
		}
	}
	// Ignored files must be absent after the round-trip.
	for _, dropped := range droppedFromArchive {
		if _, err := os.Stat(filepath.Join(pullDir, filepath.FromSlash(dropped))); !os.IsNotExist(err) {
			t.Errorf("expected %s to be absent from pulled tree, stat err=%v", dropped, err)
		}
	}
}
