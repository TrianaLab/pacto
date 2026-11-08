package pactify

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/plugin"
)

// pathWith is the golden path with extra files.
func pathWith(files map[string]string) fstest.MapFS {
	fsys := goldenPath("")
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

// TestTemplateRefusesGitFiles pins that a golden path or a plugin never plants
// a .git folder: git runs the commands its config names on the next git status.
func TestTemplateRefusesGitFiles(t *testing.T) {
	for _, name := range []string{".git/config", ".GIT/HEAD", "sub/.git", "x/.git/hooks/pre-commit"} {
		if _, _, err := fromPath(t, pathWith(map[string]string{name: "x"}), Options{}, override.Overrides{}); err == nil || !strings.Contains(err.Error(), "a file under .git") {
			t.Errorf("path %s: %v", name, err)
		}
	}
	dir := filepath.Join(t.TempDir(), "gift-cards")
	_, err := createWithPlugins(t, dir, &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc(plugin.GeneratedFile{Path: ".git/config", Content: "x"})}, false)
	if err == nil || !strings.Contains(err.Error(), "the plugin dockerfile gives .git/config") {
		t.Errorf("plugin: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refused creation wrote the folder")
	}
}

// TestTemplateDeliveryFilesArePactos pins that with --delivery the golden
// path's own delivery files never replace the Pacto resource and Stages Pacto
// plans, and the person is told which were left out.
func TestTemplateDeliveryFilesArePactos(t *testing.T) {
	fsys := goldenPath(deployPath)
	fsys["delivery/pacto.yaml"] = &fstest.MapFile{Data: []byte("the path's\n")}
	r, _ := mustFromPath(t, fsys, Options{Delivery: true}, override.Overrides{})
	if f := file(t, r, "delivery/pacto.yaml"); !strings.Contains(f.Content, "oci://registry.example/gift-cards") {
		t.Errorf("delivery/pacto.yaml is the path's: %+v", f)
	}
	wantLines(t, "Unknown", r.Unknown, "delivery/pacto.yaml: the golden path gives this file, which was left out because --delivery writes the delivery files")

	r, _ = mustFromPath(t, fsys, Options{}, override.Overrides{})
	if f := file(t, r, "delivery/pacto.yaml"); f.Content != "the path's\n" {
		t.Errorf("without --delivery the path's file is copied: %+v", f)
	}
}

// TestTemplateSigning pins that a created service never signs with the path's
// signer: publish.sign is reset to its own key, and a path that ships a
// private key is refused.
func TestTemplateSigning(t *testing.T) {
	signed := strings.Replace(pathContract, "  sign:\n    key: cosign.key\n", "  sign:\n    key: keys/platform.key\n    transparencyLog: false\n", 1)
	r, c := mustFromPath(t, goldenPath(signed), Options{}, override.Overrides{})
	if s := c.Publish.Sign; s.Key != "cosign.key" || s.TransparencyLog != nil {
		t.Errorf("sign: %+v", s)
	}
	wantLines(t, "Found", r.Found, "publish.sign: the golden path's signer was not carried over; the service signs with its own key at cosign.key")
	wantLines(t, "Next", r.Next, "put the signing key at cosign.key")

	for _, name := range []string{"cosign.key", "keys/platform.key"} {
		key := "-----BEGIN ENCRYPTED SIGSTORE PRIVATE KEY-----\nx\n"
		if _, _, err := fromPath(t, pathWith(map[string]string{name: key}), Options{}, override.Overrides{}); err == nil || !strings.Contains(err.Error(), "ships a private key at "+name) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestTemplateArtifactsInRegistry pins that every artifact moves to the
// registry given, so the first publish never pushes into the path owner's
// repositories, and each move is reported.
func TestTemplateArtifactsInRegistry(t *testing.T) {
	r, c := mustFromPath(t, goldenPath(""), Options{Registry: "ghcr.io/checkout"}, override.Overrides{})
	if arts := c.Publish.Artifacts; arts[0].Ref != "oci://ghcr.io/checkout/gift-cards" || arts[1].Ref != "oci://ghcr.io/checkout/base" {
		t.Errorf("artifacts: %+v", arts)
	}
	wantLines(t, "Found", r.Found, "publish.artifacts base: oci://ghcr.io/platform/images/base becomes oci://ghcr.io/checkout/base")
}

// TestTemplateNamesIgnoreCase pins that a name differing only in case from one
// planned is never planned again: on a case-insensitive filesystem it would
// replace the validated contract, or bring in the path's lock.
func TestTemplateNamesIgnoreCase(t *testing.T) {
	r, c := mustFromPath(t, pathWith(map[string]string{"Pacto.yaml": pathContract, "PACTO.LOCK": "x", "docs/RUNBOOK.md": "other\n"}), Options{}, override.Overrides{})
	runbooks := 0
	for _, f := range r.Files {
		if strings.EqualFold(f.Path, "pacto.yaml") && f.Path != "pacto.yaml" || strings.EqualFold(f.Path, "pacto.lock") {
			t.Errorf("planned %s", f.Path)
		}
		if strings.EqualFold(f.Path, "docs/runbook.md") {
			runbooks++
		}
	}
	if runbooks != 1 {
		t.Errorf("planned %d runbooks: %+v", runbooks, r.Files)
	}
	if c.Service.Name != "gift-cards" {
		t.Errorf("contract: %+v", c.Service)
	}
	_, err := createWithPlugins(t, "", &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc(plugin.GeneratedFile{Path: "PACTO.YAML", Content: pathContract})}, true)
	if err != nil {
		t.Fatal(err)
	}
}

// TestTemplateValidatesPluginOutput pins that what plugins return is part of
// what is validated, and that a plugin's pacto.lock is never written.
func TestTemplateValidatesPluginOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gift-cards")
	_, err := createWithPlugins(t, dir, &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc(plugin.GeneratedFile{Path: ".pactoignore", Content: "interfaces/\n"})}, false)
	if err == nil || !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("ignored interface: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refused creation wrote the folder")
	}

	r, err := createWithPlugins(t, "", &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc(plugin.GeneratedFile{Path: "pacto.lock", Content: "stale\n"})}, false)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(r.Files, func(f File) bool { return f.Path == "pacto.lock" }) {
		t.Errorf("planned the plugin's lock: %+v", r.Files)
	}
	wantLines(t, "Unknown", r.Unknown, "pacto.lock: the plugin dockerfile returned one, which was not written")
}

// TestPlanRefusesConflicts pins that a plan whose files cannot all be written
// is refused before anything is: apply would stop half way and leave a
// contract that makes running init again fail.
func TestPlanRefusesConflicts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gift-cards")
	_, err := createWithPlugins(t, dir, &Template{RunPlugins: []string{"dockerfile"}, Run: runFunc(plugin.GeneratedFile{Path: "interfaces", Content: "x"})}, false)
	if err == nil || !strings.Contains(err.Error(), "interfaces is planned both as a file and as the folder of interfaces/openapi.yaml") {
		t.Errorf("planned twice: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refused creation wrote the folder")
	}

	dir = writeTree(t, filepath.Join(t.TempDir(), "gift-cards"), map[string]string{"docs": "a file\n"})
	_, _, err = fromPath(t, goldenPath(""), Options{Dir: dir}, override.Overrides{})
	if err == nil || !strings.Contains(err.Error(), "docs/runbook.md cannot be written: docs is a file") {
		t.Errorf("on disk: %v", err)
	}
}

// TestTemplateOverlayReadsTheFolder pins that validation sees the lines a
// planned append adds to a file the folder holds, and that a kept file the
// folder cannot read refuses the creation.
func TestTemplateOverlayReadsTheFolder(t *testing.T) {
	dir := writeTree(t, filepath.Join(t.TempDir(), "gift-cards"), map[string]string{".pactoignore": "dist/"})
	r, _, err := fromPath(t, goldenPath(""), Options{Dir: dir}, override.Overrides{})
	if err != nil || file(t, r, ".pactoignore").Action != Append {
		t.Fatalf("append: %v %+v", err, r)
	}
	dir = filepath.Join(t.TempDir(), "gift-cards")
	if err := os.MkdirAll(filepath.Join(dir, "delivery", "pacto.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fromPath(t, goldenPath(deployPath), Options{Dir: dir, Delivery: true}, override.Overrides{}); err == nil || !strings.Contains(err.Error(), "reading delivery/pacto.yaml, which the folder already holds") {
		t.Errorf("unreadable: %v", err)
	}
}
