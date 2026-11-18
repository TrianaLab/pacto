package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// declaredSection writes a 2.5 payments contract at 1.0.0 whose publish
// section lists artifacts (YAML flow items, one per line) and signs with a
// key outside the folder, and files.
func declaredSection(t *testing.T, host string, artifacts []string, files map[string]string) string {
	t.Helper()
	keys := t.TempDir()
	keyFiles(t, keys)
	y := "pactoVersion: \"2.5\"\nservice:\n  name: payments\n  version: 1.0.0\npublish:\n  bundle: oci://" + host + "/acme/pacto/payments\n"
	for i, a := range artifacts {
		if i == 0 {
			y += "  artifacts:\n"
		}
		y += "    - " + a + "\n"
	}
	y += "  sign: {key: \"" + filepath.Join(keys, "cosign.key") + "\", transparencyLog: false}\n"
	dir := sectionless(t, "", "", files)
	writeFiles(t, dir, map[string]string{"pacto.yaml": y})
	return dir
}

// TestArtifactWithoutBuildIsDetected: a 2.5 image artifact without build is
// built with the folder's tool, a manifests artifact without build is
// assembled by Pacto with that image pinned, and a folder showing two tools
// refuses the artifact with the finding.
func TestArtifactWithoutBuildIsDetected(t *testing.T) {
	host, reg := publishRegistry(t)
	arts := []string{
		`{name: api, type: image, ref: "oci://` + host + `/acme/payments"}`,
		`{name: deploy, type: manifests, ref: "oci://` + host + `/acme/deploy/payments"}`,
	}
	files := map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment(host + "/acme/payments:1.0")}
	r := &buildRun{}
	res, err := publishService(&fakeRun{}, reg).withRun(r).Publish(t.Context(), PublishOptions{Path: declaredSection(t, host, arts, files), Revision: "r1"})
	if err != nil || res.Digests["api"] == "" || res.Digests["deploy"] == "" {
		t.Fatalf("%v %+v", err, res)
	}
	if !slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "docker buildx build") }) {
		t.Fatalf("calls %q", r.calls)
	}
	files[".ko.yaml"] = "builds:\n  - main: ./cmd/api\n"
	_, err = publishService(&fakeRun{}, reg).withRun(&buildRun{}).Publish(t.Context(), PublishOptions{Path: declaredSection(t, host, arts, files), Revision: "r1"})
	if err == nil || !strings.HasPrefix(err.Error(), "artifact api declares no build, and Pacto cannot detect one: finding: ") || !strings.Contains(err.Error(), "ko (.ko.yaml)") {
		t.Fatal(err)
	}
}

// TestDetectBuildsRefusals: detection never guesses, so each part it cannot
// tell refuses that artifact by name.
func TestDetectBuildsRefusals(t *testing.T) {
	host := "r.io"
	img := `{name: api, type: image, ref: "oci://r.io/acme/payments"}`
	man := `{name: deploy, type: manifests, ref: "oci://r.io/acme/deploy/payments"}`
	built := `{name: web, type: image, ref: "oci://r.io/acme/web", build: {command: [./b.sh]}}`
	dep := deployment("r.io/acme/payments:1.0")
	for _, tc := range []struct {
		name  string
		arts  []string
		files map[string]string
		want  string
	}{
		{"no tool", []string{img}, map[string]string{"k8s/deploy.yaml": dep}, "artifact api declares no build, and Pacto cannot detect one: finding: k8s/deploy.yaml"},
		{"no tool, no workload", []string{img}, nil, "artifact api declares no build, and Pacto cannot detect one: the folder shows no build tool"},
		{"two images", []string{img, built}, map[string]string{"Dockerfile": "FROM scratch\n"}, "artifact api declares no build, and Pacto detects the build of one image per folder: declare build on each of api, web"},
		{"no deploy source", []string{man}, nil, "artifact deploy declares no build, and Pacto cannot detect one: the folder holds no manifests, kustomization or chart"},
		{"wrong kind", []string{`{name: deploy, type: chart, ref: "oci://r.io/acme/c"}`}, map[string]string{"k8s/deploy.yaml": dep}, "artifact deploy declares no build, and is a chart, but the folder deploys with a manifests: declare build"},
		{"image after deploy", []string{man, built}, map[string]string{"k8s/deploy.yaml": dep}, "artifact deploy pins the image web, so declare web before it"},
		{"secret literal", []string{man}, map[string]string{"k8s/deploy.yaml": dep, "k8s/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c}\ndata:\n  password: hunter2hunter2\n"}, "k8s/cm.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := declaredSection(t, host, tc.arts, tc.files)
			b, err := loadPublishBundle(t, dir)
			if err != nil {
				t.Fatal(err)
			}
			_, err = detectBuilds(t.Context(), dir, b.Publish, nil, (&buildRun{}).run)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v, want %q", err, tc.want)
			}
		})
	}
}

// TestDeclaredSectionWithoutArtifactsStaysContractOnly: a declared section
// with no artifacts, or with every build declared, gets nothing detected.
func TestDeclaredSectionWithoutArtifactsStaysContractOnly(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := declaredSection(t, host, nil, map[string]string{"Dockerfile": "FROM scratch\n", "k8s/deploy.yaml": deployment("r.io/acme/payments:1.0")})
	r := &buildRun{}
	res, err := publishService(&fakeRun{}, reg).withRun(r).Publish(t.Context(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil || len(res.Plan.Items) != 1 || res.Plan.Items[0].Name != publish.BundleItem || slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "docker") }) {
		t.Fatalf("%v %+v calls %q", err, res, r.calls)
	}
	b, err := loadPublishBundle(t, declaredSection(t, host, []string{`{name: web, type: image, ref: "oci://r.io/acme/web", build: {command: [./b.sh]}}`}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if d, err := detectBuilds(t.Context(), dir, b.Publish, nil, r.run); d != nil || err != nil {
		t.Fatalf("%+v %v", d, err)
	}
	unreadable := t.TempDir()
	_ = os.Mkdir(filepath.Join(unreadable, ".pactoignore"), 0o755)
	bare := &contract.Publish{Artifacts: []contract.PublishArtifact{{Name: "api", Type: contract.ArtifactImage}}}
	if _, err := detectBuilds(t.Context(), unreadable, bare, nil, r.run); err == nil {
		t.Fatal("an unreadable folder detects nothing")
	}
}

// loadPublishBundle parses the contract in dir.
func loadPublishBundle(t *testing.T, dir string) (*contract.Contract, error) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		return nil, err
	}
	return contract.Parse(bytes.NewReader(raw))
}
