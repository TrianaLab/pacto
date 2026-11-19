package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const deliveryNote = "removed delivery (it left pactoVersion 2.5 before its release; the Stage's spec.applyRef names the applier)"

// migrateOne runs pacto migrate over a folder holding only files and returns
// its row and the pacto.yaml it leaves.
func migrateOne(t *testing.T, files map[string]string, opts MigrateOptions) (MigratedFile, string) {
	t.Helper()
	dir := writeTree(t, "", files)
	opts.Roots = []string{dir}
	f := oneRow(t, migrate(t, NewService(nil, nil), opts))
	return f, read(t, filepath.Join(dir, "pacto.yaml"))
}

func TestMigrateDropsRemovedDelivery(t *testing.T) {
	cur := contract.Current()
	head := "\xef\xbb\xbfpactoVersion: \"" + cur + "\"\r\nservice: {name: s, version: 1.0.0}\r\n"
	tail := "# the HTTP API\r\ninterfaces:\r\n  - {name: api, type: http, port: 8080}\r\n"
	block := "delivery:\r\n  # the applier moved to the Stage\r\n\r\n  applier: argocd\r\n"
	cases := map[string]string{
		"block": head + block + tail,
		"flow":  head + "delivery: {applier: argocd}\r\n" + tail,
	}
	cases["first line"] = "\xef\xbb\xbfdelivery: {applier: argocd}\r\n" + strings.TrimPrefix(head, "\xef\xbb\xbf") + tail
	for name, orig := range cases {
		t.Run(name, func(t *testing.T) {
			f, got := migrateOne(t, map[string]string{"pacto.yaml": orig}, MigrateOptions{})
			if f.Status != MigrateRewritten || f.Message != deliveryNote {
				t.Fatalf("%+v", f)
			}
			if got != head+tail {
				t.Fatalf("got %q\nwant %q", got, head+tail)
			}
			if _, err := contract.Parse(strings.NewReader(got)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrateDropsDeliveryAndRaises(t *testing.T) {
	f, got := migrateOne(t, map[string]string{"pacto.yaml": simple(prevVersion) + "delivery:\n  applier: flux\n"}, MigrateOptions{})
	want := `set pactoVersion to "` + contract.Current() + `" (was "` + prevVersion + `"); ` + deliveryNote
	if f.Status != MigrateRewritten || f.Message != want || got != simple(contract.Current()) {
		t.Fatalf("%+v\n%q", f, got)
	}
}

func TestMigrateDropsNothingItCannotCut(t *testing.T) {
	// A key that shares its line with another entry is edited by hand.
	orig := "{pactoVersion: \"" + contract.Current() + "\", service: {name: s, version: 1.0.0}, delivery: {applier: argocd}}\n"
	f, got := migrateOne(t, map[string]string{"pacto.yaml": orig}, MigrateOptions{})
	if f.Status != MigrateCurrent || got != orig {
		t.Fatalf("%+v\n%q", f, got)
	}
}

func TestMigrateKeptWritesNoDrop(t *testing.T) {
	dir := refPair(t, strictSchema, nil)
	p := filepath.Join(dir, "pacto.yaml")
	before := read(t, p) + "delivery:\n  applier: argocd\n"
	if err := os.WriteFile(p, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	f := oneRow(t, migrate(t, NewService(&mockBundleStore{}, nil), MigrateOptions{Roots: []string{dir}}))
	if f.Status != MigrateKept || read(t, p) != before {
		t.Fatalf("%+v", f)
	}
}

func TestMigrateKeepsSymlinkWithDrop(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/real.yaml": "service: {name: s, version: 1.0.0}\ndelivery: {applier: argocd}\n"})
	if err := os.Symlink("real.yaml", filepath.Join(dir, "s", "pacto.yaml")); err != nil {
		t.Fatal(err)
	}
	f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}}))
	if f.Status != MigrateKept || f.Message != "kept at "+contract.Current()+": pacto.yaml is a symbolic link; edit it by hand" {
		t.Fatalf("%+v", f)
	}
}

// buildFolder is a service whose deployment runs ghcr.io/acme/web:1.0 and
// whose image artifact declares art's build, with the extra files.
func buildFolder(version, art string, extra map[string]string) map[string]string {
	files := map[string]string{
		"pacto.yaml":             "pactoVersion: \"" + version + "\"\nservice: {name: web, version: 1.0.0}\npublish:\n  bundle: oci://ghcr.io/acme/contracts/web\n  artifacts:\n    - name: image\n      type: image\n" + art + "      ref: oci://ghcr.io/acme/web\n",
		"deploy/deployment.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n  template:\n    spec:\n      containers:\n        - {name: web, image: ghcr.io/acme/web:1.0}\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

const dockerBuild = "      # built from the folder\n      build:\n        dockerfile:\n          context: .\n"

func TestMigrateDropsBuildWhenDetectionMatches(t *testing.T) {
	for _, from := range []string{contract.Current(), prevVersion} {
		files := buildFolder(from, dockerBuild, map[string]string{"Dockerfile": "FROM scratch\n"})
		f, got := migrateOne(t, files, MigrateOptions{})
		note := "removed publish.artifacts[0].build (pacto ci --publish detects the same build)"
		want := buildFolder(contract.Current(), "      # built from the folder\n", nil)["pacto.yaml"]
		if f.Status != MigrateRewritten || !strings.HasSuffix(f.Message, note) || got != want {
			t.Fatalf("%s: %+v\ngot  %q\nwant %q", from, f, got, want)
		}
	}
}

func TestMigrateKeepsBuildWhenDetectionDiffers(t *testing.T) {
	ko := map[string]string{".ko.yaml": "builds: [{main: ./cmd/web}]\n"}
	df := map[string]string{"Dockerfile": "FROM scratch\n"}
	cases := map[string]struct {
		art   string
		extra map[string]string
		want  string
	}{
		"another tool":  {dockerBuild, ko, "detection gives ko ghcr.io/acme/web:1.0"},
		"a command":     {"      build: {command: [make, image]}\n", df, "detection gives dockerfile ghcr.io/acme/web:1.0"},
		"a platform":    {"      build:\n        dockerfile: {context: ., platforms: [linux/arm64]}\n", df, "detection gives dockerfile"},
		"another file":  {"      build:\n        dockerfile: {context: ., file: Dockerfile.prod}\n", df, "detection gives dockerfile"},
		"a finding":     {dockerBuild, map[string]string{"Dockerfile": "FROM scratch\n", ".ko.yaml": "{}\n"}, "detection reports "},
		"no detection":  {dockerBuild, map[string]string{"deploy/deployment.yaml": "{}\n"}, "detection finds no build"},
		"not a mapping": {"      build: 3\n", df, "detection gives dockerfile"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			files := buildFolder(contract.Current(), tc.art, tc.extra)
			f, got := migrateOne(t, files, MigrateOptions{})
			if f.Status != MigrateCurrent || got != files["pacto.yaml"] || !strings.Contains(f.Message, "; kept publish.artifacts[0].build: "+tc.want) || !strings.HasSuffix(f.Message, "; remove it if that is what you want") {
				t.Fatalf("%+v", f)
			}
		})
	}
}

func TestMigrateKeepsBuildItCannotCut(t *testing.T) {
	for name, art := range map[string]string{
		"flow":      "    - {name: image, type: image, ref: oci://ghcr.io/acme/web, build: {dockerfile: {context: .}}}\n",
		"first key": "    - build: {dockerfile: {context: .}}\n      name: image\n      type: image\n      ref: oci://ghcr.io/acme/web\n    - {name: chart, type: chart, ref: oci://ghcr.io/acme/charts/web}\n",
	} {
		t.Run(name, func(t *testing.T) {
			files := buildFolder(contract.Current(), "", map[string]string{"Dockerfile": "FROM scratch\n"})
			files["pacto.yaml"] = strings.Replace(files["pacto.yaml"], "    - name: image\n      type: image\n      ref: oci://ghcr.io/acme/web\n", art, 1)
			f, got := migrateOne(t, files, MigrateOptions{})
			if f.Status != MigrateCurrent || got != files["pacto.yaml"] || !strings.HasSuffix(f.Message, "; kept publish.artifacts[0].build: pacto ci --publish detects the same build; delete it by hand") {
				t.Fatalf("%+v", f)
			}
		})
	}
}

func TestMigrateKeepsBuildBelow25(t *testing.T) {
	files := buildFolder("2.4", dockerBuild, map[string]string{"Dockerfile": "FROM scratch\n"})
	f, got := migrateOne(t, files, MigrateOptions{To: "2.4"})
	if f.Status != MigrateCurrent || f.Message != `already at or past "2.4" (2.4)` || !bytes.Equal([]byte(got), []byte(files["pacto.yaml"])) {
		t.Fatalf("%+v", f)
	}
}

// TestMigrateKeepsBehindBuildDetectionDiffers: a raise that would leave a
// build detection does not reproduce adds its deprecation, so the file stays.
func TestMigrateKeepsBehindBuildDetectionDiffers(t *testing.T) {
	files := buildFolder(prevVersion, dockerBuild, map[string]string{".ko.yaml": "{}\n"})
	f, got := migrateOne(t, files, MigrateOptions{})
	if f.Status != MigrateKept || f.Finding == nil || f.Finding.Code != "PUBLISH_BUILD_DEPRECATED" || got != files["pacto.yaml"] {
		t.Fatalf("%+v", f)
	}
}
