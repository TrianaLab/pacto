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
const ignoreContract = `pactoVersion: "2.0"
service:
  name: ignore-svc
  version: 1.0.0
  owner:
    team: platform
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: interfaces/openapi.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
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

// TestPactoignoreCannotDropPactoYAML proves the `alwaysKeep` guard: pacto.yaml is
// NEVER ignorable. Even when .pactoignore explicitly lists `pacto.yaml` AND the
// catch-all `*.yaml`, `pacto pack` still packages pacto.yaml. This is by design —
// erroring on the listing would break legitimate `*.yaml` ignores of OTHER files,
// so the ignore is a protected no-op for pacto.yaml only. Adversarial scenario A.
func TestPactoignoreCannotDropPactoYAML(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "protect-svc")
	if err := os.MkdirAll(filepath.Join(dir, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	// Contract uses a non-YAML (.json) interface so the catch-all `*.yaml` ignore
	// would otherwise have nothing legitimate to protect except pacto.yaml itself.
	contractYAML := depServiceContract("protect-svc", "1.0.0", "", "")
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(contractYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "interfaces", "api.json"),
		[]byte(fmt.Sprintf(grpcSpecTemplate, "protect", "Protect")), 0644); err != nil {
		t.Fatal(err)
	}
	// The adversarial .pactoignore: try to drop pacto.yaml directly and via glob.
	if err := os.WriteFile(filepath.Join(dir, ".pactoignore"), []byte("pacto.yaml\n*.yaml\n"), 0644); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "protect-svc.tar.gz")
	out, err := runCommand(t, nil, "pack", dir, "-o", archive)
	if err != nil {
		t.Fatalf("pack failed: %v\noutput: %s", err, out)
	}

	// pacto.yaml MUST survive the ignore — it is never ignorable (alwaysKeep guard).
	verifyArchiveContains(t, archive, "pacto.yaml")
}

// TestPactoignoreIgnoringReferencedFileFails proves the real failure case: when
// .pactoignore excludes a file the CONTRACT references (the interface file), the
// file is missing from the filtered FS, so `pacto validate` reports FILE_NOT_FOUND
// (and exits non-zero) and `pacto pack` fails too. You cannot ship a bundle whose
// referenced files were ignored. Adversarial scenario B.
func TestPactoignoreIgnoringReferencedFileFails(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ignore-ref-svc")
	if err := os.MkdirAll(filepath.Join(dir, "interfaces"), 0755); err != nil {
		t.Fatal(err)
	}
	// ignoreContract references interfaces/openapi.yaml.
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(ignoreContract), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "interfaces", "openapi.yaml"),
		[]byte(fmt.Sprintf(openapiTemplate, "ignore-svc", "1.0.0")), 0644); err != nil {
		t.Fatal(err)
	}
	// Ignore the EXACT referenced interface file.
	if err := os.WriteFile(filepath.Join(dir, ".pactoignore"),
		[]byte("interfaces/openapi.yaml\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// validate must fail and report the missing referenced file.
	out, err := runCommand(t, nil, "validate", dir)
	if err == nil {
		t.Fatalf("expected validate to fail when referenced file is ignored, got success:\n%s", out)
	}
	assertContains(t, out, "FILE_NOT_FOUND")

	// pack must also fail (cannot package a bundle missing a referenced file).
	archive := filepath.Join(t.TempDir(), "ignore-ref-svc.tar.gz")
	if _, perr := runCommand(t, nil, "pack", dir, "-o", archive); perr == nil {
		t.Fatal("expected pack to fail when referenced file is ignored, got success")
	}
}

// TestPactoignoreIgnoredDirHidesReferencedFile proves directory-level ignore
// patterns (e.g., `api/`) correctly hide files in that directory even when the
// contract references them. This exercises the ancestor filtering fix: any file
// whose parent directory is ignored is also treated as ignored, so validate and
// pack fail with FILE_NOT_FOUND. Scenario C: directory pattern ignoring a file.
func TestPactoignoreIgnoredDirHidesReferencedFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ignore-dir-svc")
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0755); err != nil {
		t.Fatal(err)
	}

	// Contract references api/openapi.yaml.
	contractYAML := `pactoVersion: "2.0"
service:
  name: ignore-dir-svc
  version: 1.0.0
  owner:
    team: platform
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: api/openapi.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(contractYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "openapi.yaml"),
		[]byte(fmt.Sprintf(openapiTemplate, "ignore-dir-svc", "1.0.0")), 0644); err != nil {
		t.Fatal(err)
	}
	// Ignore the DIRECTORY containing the referenced interface file.
	if err := os.WriteFile(filepath.Join(dir, ".pactoignore"),
		[]byte("api/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// validate must fail and report the missing referenced file.
	out, err := runCommand(t, nil, "validate", dir)
	if err == nil {
		t.Fatalf("expected validate to fail when referenced file's directory is ignored, got success:\n%s", out)
	}
	assertContains(t, out, "FILE_NOT_FOUND")

	// pack must also fail (cannot package a bundle missing a referenced file).
	archive := filepath.Join(t.TempDir(), "ignore-dir-svc.tar.gz")
	if _, perr := runCommand(t, nil, "pack", dir, "-o", archive); perr == nil {
		t.Fatal("expected pack to fail when referenced file's directory is ignored, got success")
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

// TestPactoignoreIgnoredNestedDirHidesReferencedFile proves ancestor filtering
// works at depth > 1: a contract references deeply/nested/dir/openapi.yaml, the
// file exists there, and .pactoignore contains `deeply/` (a top-level dir pattern).
// Both validate and pack must fail because the referenced file is excluded.
func TestPactoignoreIgnoredNestedDirHidesReferencedFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "ignore-nested-svc")
	if err := os.MkdirAll(filepath.Join(dir, "deeply", "nested", "dir"), 0755); err != nil {
		t.Fatal(err)
	}

	// Contract references deeply/nested/dir/openapi.yaml.
	contractYAML := `pactoVersion: "2.0"
service:
  name: ignore-nested-svc
  version: 1.0.0
  owner:
    team: platform
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: deeply/nested/dir/openapi.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(contractYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deeply", "nested", "dir", "openapi.yaml"),
		[]byte(fmt.Sprintf(openapiTemplate, "ignore-nested-svc", "1.0.0")), 0644); err != nil {
		t.Fatal(err)
	}
	// Ignore the TOP-LEVEL directory containing the deeply nested file.
	if err := os.WriteFile(filepath.Join(dir, ".pactoignore"),
		[]byte("deeply/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// validate must fail and report the missing referenced file.
	out, err := runCommand(t, nil, "validate", dir)
	if err == nil {
		t.Fatalf("expected validate to fail when referenced file's ancestor directory is ignored, got success:\n%s", out)
	}
	assertContains(t, out, "FILE_NOT_FOUND")

	// pack must also fail (cannot package a bundle missing a referenced file).
	archive := filepath.Join(t.TempDir(), "ignore-nested-svc.tar.gz")
	if _, perr := runCommand(t, nil, "pack", dir, "-o", archive); perr == nil {
		t.Fatal("expected pack to fail when referenced file's ancestor directory is ignored, got success")
	}
}

// TestPactoignoreAnchoredDirPatternDoesNotMatchSubdir proves an anchored
// directory pattern (/build/) does NOT match a same-named nested directory
// (sub/build/). A contract references sub/build/api.yaml, and .pactoignore
// contains `/build/` (anchored to root). The referenced file is KEPT, so
// validate passes and pack succeeds with the file in the archive.
func TestPactoignoreAnchoredDirPatternDoesNotMatchSubdir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "anchored-svc")
	if err := os.MkdirAll(filepath.Join(dir, "sub", "build"), 0755); err != nil {
		t.Fatal(err)
	}

	// Contract references sub/build/api.yaml.
	contractYAML := `pactoVersion: "2.0"
service:
  name: anchored-svc
  version: 1.0.0
  owner:
    team: platform
interfaces:
  - name: api
    type: openapi
    visibility: internal
    ref: sub/build/api.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(contractYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "build", "api.yaml"),
		[]byte(fmt.Sprintf(openapiTemplate, "anchored-svc", "1.0.0")), 0644); err != nil {
		t.Fatal(err)
	}
	// Anchored pattern: /build/ matches only <root>/build/, not sub/build/.
	if err := os.WriteFile(filepath.Join(dir, ".pactoignore"),
		[]byte("/build/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// validate must pass (the referenced file is kept).
	out, err := runCommand(t, nil, "validate", dir)
	if err != nil {
		t.Fatalf("expected validate to pass when anchored pattern does not match subdir, got error: %v\n%s", err, out)
	}

	// pack must succeed and the referenced file must be in the archive.
	archive := filepath.Join(t.TempDir(), "anchored-svc.tar.gz")
	if _, err := runCommand(t, nil, "pack", dir, "-o", archive); err != nil {
		t.Fatalf("pack failed: %v", err)
	}
	verifyArchiveContains(t, archive, "sub/build/api.yaml")
}
