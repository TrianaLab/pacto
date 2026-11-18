package discover

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// buildOf is Build over the folder dir, scanned by Dir.
func buildOf(t *testing.T, dir string) (BuildPlan, []contract.InferredFinding) {
	t.Helper()
	sc, err := Dir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return Build(dir, sc)
}

// buildFixture is Build over testdata/build/<name>, which must give no finding.
func buildFixture(t *testing.T, name string) BuildPlan {
	t.Helper()
	p, findings := buildOf(t, filepath.Join("testdata", "build", name))
	if len(findings) > 0 {
		t.Fatalf("%s: findings %+v", name, findings)
	}
	return p
}

func TestBuildToolSkaffoldWins(t *testing.T) {
	p := buildFixture(t, "skaffold")
	if p.Tool != ToolSkaffold || p.ToolSource != "skaffold.yaml:5" || p.Image != "ghcr.io/acme/app:1.0.0" || p.ImageSource != "deploy.yaml:10" || p.Pinned {
		t.Errorf("plan = %+v", p)
	}
}

func TestBuildToolKo(t *testing.T) {
	if p := buildFixture(t, "ko"); p.Tool != ToolKo || p.Main != "./cmd/app" || p.ToolSource != ".ko.yaml:1" {
		t.Errorf("plan = %+v", p)
	}
	// With no builds listed, ko builds the folder itself.
	dir := writeTree(t, t.TempDir(), map[string]string{".ko.yaml": "defaultBaseImage: x\n"})
	if p, _ := buildOf(t, dir); p.Tool != ToolKo || p.Main != "." {
		t.Errorf("plan = %+v", p)
	}
}

func TestBuildToolJibMaven(t *testing.T) {
	if p := buildFixture(t, "jib-maven"); p.Tool != ToolJib || p.Wrapper != "./mvnw" || p.ToolSource != "pom.xml:5" {
		t.Errorf("plan = %+v", p)
	}
	// A pom.xml that names no Jib plugin is no build tool.
	dir := writeTree(t, t.TempDir(), map[string]string{"pom.xml": "<project/>\n"})
	if p, f := buildOf(t, dir); p.Tool != "" || len(f) > 0 {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
}

func TestBuildToolJibGradle(t *testing.T) {
	if p := buildFixture(t, "jib-gradle"); p.Tool != ToolJib || p.Wrapper != "gradle" || p.ToolSource != "build.gradle.kts:3" {
		t.Errorf("plan = %+v", p)
	}
}

func TestBuildToolBuildpacks(t *testing.T) {
	if p := buildFixture(t, "buildpacks"); p.Tool != ToolBuildpacks || p.ToolSource != "project.toml:1" {
		t.Errorf("plan = %+v", p)
	}
}

func TestBuildToolDockerfile(t *testing.T) {
	if p := buildFixture(t, "dockerfile"); p.Tool != ToolDockerfile || p.ToolSource != "Dockerfile:1" {
		t.Errorf("plan = %+v", p)
	}
}

func TestBuildToolSeveralIsAFinding(t *testing.T) {
	p, findings := buildOf(t, filepath.Join("testdata", "build", "ko-and-dockerfile"))
	want := []contract.InferredFinding{{Code: CodeAmbiguous, Source: ".ko.yaml", Message: "both ko (.ko.yaml) and Dockerfile found; pass --image or declare publish"}}
	if p.Tool != "" || !reflect.DeepEqual(findings, want) {
		t.Errorf("plan = %+v, findings %+v", p, findings)
	}
	dir := writeTree(t, t.TempDir(), map[string]string{".ko.yaml": "builds: []\n", "project.toml": "", "Dockerfile": "FROM x\n"})
	if _, f := buildOf(t, dir); len(f) != 1 || f[0].Message != "ko (.ko.yaml), buildpacks (project.toml) and Dockerfile found; pass --image or declare publish" {
		t.Errorf("findings = %+v", f)
	}
}

func TestBuildToolNoneNeedsImage(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{"deploy.yaml": appManifest})
	p, findings := buildOf(t, dir)
	if p.Tool != "" || len(findings) != 1 || findings[0].Code != CodeUnresolved || findings[0].Source != p.ImageSource ||
		findings[0].Message != p.ImageSource+" names an image no Skaffold, ko, Jib, buildpacks or Dockerfile builds; pass --image or declare publish" {
		t.Errorf("plan = %+v, findings %+v", p, findings)
	}
	// No image and no tool: nothing to build, so nothing is asked.
	if p, f := buildOf(t, t.TempDir()); !reflect.DeepEqual(p, BuildPlan{}) || len(f) > 0 {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
}

func TestBuildToolPinnedImageNeedsNoBuild(t *testing.T) {
	p := buildFixture(t, "pinned")
	if !p.Pinned || p.Tool != "" || !strings.HasSuffix(p.Image, "@sha256:"+strings.Repeat("0", 64)) || p.ImageSource != "deploy.yaml:10" {
		t.Errorf("plan = %+v", p)
	}
}

func TestSkaffoldSeveralArtifactsIsAFinding(t *testing.T) {
	two := "build:\n  artifacts:\n    - image: a\n    - image: b\n"
	dir := writeTree(t, t.TempDir(), map[string]string{"skaffold.yaml": two, "Dockerfile": "FROM x\n"})
	p, findings := buildOf(t, dir)
	want := []contract.InferredFinding{{Code: CodeAmbiguous, Source: "skaffold.yaml:2",
		Message: "skaffold.yaml:2 builds 2 artifacts; Pacto builds one image per service: pass --image or declare publish"}}
	if p.Tool != "" || !reflect.DeepEqual(findings, want) {
		t.Errorf("plan = %+v, findings %+v", p, findings)
	}
	// One that does not parse lists no artifacts.
	dir = writeTree(t, t.TempDir(), map[string]string{"skaffold.yaml": "{"})
	if _, f := buildOf(t, dir); len(f) != 1 || f[0].Source != "skaffold.yaml" {
		t.Errorf("findings = %+v", f)
	}
}

func TestDeployKustomize(t *testing.T) {
	p := buildFixture(t, "kustomize")
	if want := (&DeploySource{Kind: DeployKustomize, Paths: []string{"kustomization.yaml"}, Source: "kustomization.yaml"}); !reflect.DeepEqual(p.Deploy, want) {
		t.Errorf("deploy = %+v", p.Deploy)
	}
	// The only kustomization, anywhere in the folder, deploys.
	dir := writeTree(t, t.TempDir(), map[string]string{"k8s/kustomization.yaml": "resources: [app.yaml]\n", "k8s/app.yaml": appManifest, "Dockerfile": "FROM x\n"})
	if p, f := buildOf(t, dir); len(f) > 0 || p.Deploy == nil || p.Deploy.Paths[0] != "k8s/kustomization.yaml" {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
	// The kustomization no other one names deploys, its base aside.
	dir = writeTree(t, t.TempDir(), map[string]string{
		"k8s/kustomization.yaml": "resources: [base]\n", "k8s/base/kustomization.yaml": "resources: [app.yaml]\n",
		"k8s/base/app.yaml": appManifest, "Dockerfile": "FROM x\n",
	})
	if p, f := buildOf(t, dir); len(f) > 0 || p.Deploy == nil || p.Deploy.Paths[0] != "k8s/kustomization.yaml" {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
}

func TestDeployChart(t *testing.T) {
	p := buildFixture(t, "chart")
	if want := (&DeploySource{Kind: DeployChart, Paths: []string{"chart"}, Source: "chart/Chart.yaml"}); !reflect.DeepEqual(p.Deploy, want) {
		t.Errorf("deploy = %+v", p.Deploy)
	}
	// A folder that is a chart deploys itself, its subcharts aside.
	dir := writeTree(t, t.TempDir(), map[string]string{"Chart.yaml": "apiVersion: v2\nname: x\nversion: 0.1.0\n", "Dockerfile": "FROM x\n"})
	if p, f := buildOf(t, dir); len(f) > 0 || p.Deploy == nil || p.Deploy.Source != "Chart.yaml" {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
}

func TestDeployManifests(t *testing.T) {
	p := buildFixture(t, "manifests")
	want := &DeploySource{Kind: DeployManifests, Paths: []string{"deploy/deployment.yaml", "deploy/service.yaml"}, Source: "deploy/deployment.yaml:10"}
	if !reflect.DeepEqual(p.Deploy, want) {
		t.Errorf("deploy = %+v", p.Deploy)
	}
	// With no workload image, the first manifest is the source.
	dir := writeTree(t, t.TempDir(), map[string]string{"svc.yaml": "apiVersion: v1\nkind: Service\nmetadata: {name: x}\n"})
	if p, _ := buildOf(t, dir); p.Deploy == nil || p.Deploy.Source != "svc.yaml" {
		t.Errorf("deploy = %+v", p.Deploy)
	}
}

func TestDeploySeveralSourcesIsAFinding(t *testing.T) {
	p, findings := buildOf(t, filepath.Join("testdata", "build", "chart-and-manifests"))
	want := []contract.InferredFinding{{Code: CodeAmbiguous, Source: "chart/Chart.yaml",
		Message: "both chart (chart) and manifests (deploy.yaml) found; declare publish"}}
	if p.Deploy != nil || p.Tool != ToolDockerfile || !reflect.DeepEqual(findings, want) {
		t.Errorf("plan = %+v, findings %+v", p, findings)
	}
	// Two overlays over one base and no root kustomization: one per environment.
	dir := writeTree(t, t.TempDir(), map[string]string{
		"base/kustomization.yaml": "resources: []\n", "overlays/prod/kustomization.yaml": "resources: [../../base]\n",
		"overlays/dev/kustomization.yaml": "resources: [../../base]\n", "Dockerfile": "FROM x\n",
	})
	if p, f := buildOf(t, dir); p.Deploy != nil || len(f) != 1 ||
		f[0].Message != "both kustomize (overlays/dev/kustomization.yaml) and kustomize (overlays/prod/kustomization.yaml) found; declare publish" {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
	// Two that name each other: neither is the root, so both are candidates.
	dir = writeTree(t, t.TempDir(), map[string]string{
		"a/kustomization.yaml": "resources: [../b]\n", "b/kustomization.yaml": "resources: [../a]\n", "Dockerfile": "FROM x\n",
	})
	if p, f := buildOf(t, dir); p.Deploy != nil || len(f) != 1 {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
}

func TestDeployKustomizeOutsideIsAFinding(t *testing.T) {
	p, findings := buildOf(t, filepath.Join("testdata", "build", "kustomize-outside"))
	want := []contract.InferredFinding{{Code: CodeUnresolved, Source: "kustomization.yaml:3",
		Message: "kustomization.yaml:3 names a resource outside the folder; Pacto deploys only the folder's files: declare publish"}}
	if p.Deploy != nil || !reflect.DeepEqual(findings, want) {
		t.Errorf("plan = %+v, findings %+v", p, findings)
	}
	// Through a nested kustomization, and a loop between two is read once.
	dir := writeTree(t, t.TempDir(), map[string]string{
		"kustomization.yaml": "resources: [a]\n", "a/kustomization.yaml": "resources: [../b]\n",
		"b/kustomization.yaml": "resources: [../a, ../../x]\n", "Dockerfile": "FROM x\n",
	})
	if p, f := buildOf(t, dir); p.Deploy != nil || len(f) != 1 || f[0].Source != "b/kustomization.yaml:1" {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
}

func TestBuildMissingAndUnreadableFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if p, f := Build(missing, &Scan{}); !reflect.DeepEqual(p, BuildPlan{}) || f != nil {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, f := Build(file, &Scan{}); len(f) != 1 || f[0].Code != CodeUnreadable {
		t.Errorf("findings = %+v", f)
	}
}

// TestBuildHonoursPactoignore: a build file .pactoignore leaves out is not
// read.
func TestBuildHonoursPactoignore(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{".pactoignore": "project.toml\n", "project.toml": "", "Dockerfile": "FROM x\n"})
	if p, f := buildOf(t, dir); p.Tool != ToolDockerfile || len(f) > 0 {
		t.Errorf("plan = %+v, findings %+v", p, f)
	}
}

func TestCompleteAddsPublishFieldsOnlyWithoutSection(t *testing.T) {
	dir := filepath.Join("testdata", "build", "manifests")
	rec := complete(t, dir, nil)
	if got := fieldNames(rec); !slices.Contains(got, "publish.build") || !slices.Contains(got, "publish.deploy") {
		t.Errorf("fields = %q", got)
	}
	declared := &contract.Contract{Publish: &contract.Publish{Bundle: "oci://r.io/app"}}
	if got := fieldNames(complete(t, dir, declared)); slices.ContainsFunc(got, func(f string) bool { return strings.HasPrefix(f, "publish.") }) {
		t.Errorf("fields = %q with a declared publish section", got)
	}
	// Findings, too, only without a section.
	amb := filepath.Join("testdata", "build", "ko-and-dockerfile")
	if rec := complete(t, amb, nil); !slices.ContainsFunc(rec.Findings, func(f contract.InferredFinding) bool { return f.Code == CodeAmbiguous }) {
		t.Errorf("findings = %+v", rec.Findings)
	}
	if rec := complete(t, amb, declared); slices.ContainsFunc(rec.Findings, func(f contract.InferredFinding) bool { return f.Code == CodeAmbiguous }) {
		t.Errorf("findings = %+v with a declared publish section", rec.Findings)
	}
}

func TestCompletePublishFieldsHaveSources(t *testing.T) {
	for _, tc := range []struct{ dir, build, buildSrc, deploy, deploySrc string }{
		{"manifests", "dockerfile (Dockerfile), image ghcr.io/acme/app", "Dockerfile:1",
			"manifests: deploy/deployment.yaml, deploy/service.yaml; image pinned by digest", "deploy/deployment.yaml:10"},
		{"ko", "ko (.ko.yaml), image ghcr.io/acme/app", ".ko.yaml:1", "manifests: deploy.yaml; image pinned by digest", "deploy.yaml:10"},
		{"pinned", "image supplied, not built by Pacto: ghcr.io/acme/app@sha256:" + strings.Repeat("0", 64), "deploy.yaml:10",
			"manifests: deploy.yaml; image pinned by digest", "deploy.yaml:10"},
		{"chart", "dockerfile (Dockerfile)", "Dockerfile:1", "chart: chart; image pinned by digest", "chart/Chart.yaml"},
	} {
		dir := filepath.Join("testdata", "build", tc.dir)
		rec := complete(t, dir, nil)
		got := map[string]contract.Inferred{}
		for _, f := range rec.Fields {
			got[f.Field] = f
		}
		b, d := got["publish.build"], got["publish.deploy"]
		if b.Value != tc.build || b.Source != tc.buildSrc || d.Value != tc.deploy || d.Source != tc.deploySrc ||
			b.Confidence != contract.ConfidenceLikely || b.Declare == "" || b.Declare != d.Declare {
			t.Errorf("%s: build %+v, deploy %+v", tc.dir, b, d)
		}
		var section struct{ Publish contract.Publish }
		if err := yaml.Unmarshal([]byte(b.Declare), &section); err != nil || section.Publish.Bundle != "oci://<registry>/"+tc.dir {
			t.Errorf("%s: declare %q: %v", tc.dir, b.Declare, err)
		}
		if err := contract.VerifyInferredRecord(os.DirFS(dir), rec); err != nil {
			t.Errorf("%s: %v", tc.dir, err)
		}
	}
}

func TestPublishYAML(t *testing.T) {
	cases := map[string]struct {
		plan BuildPlan
		want string
	}{
		"nothing": {BuildPlan{}, "publish:\n  bundle: oci://<registry>/x\n"},
		"no image": {BuildPlan{Tool: ToolKo, Deploy: &DeploySource{Kind: DeployKustomize}},
			"publish:\n  bundle: oci://<registry>/x\n  deploy: manifests\n  artifacts:\n    - name: image\n      type: image\n      ref: oci://<registry>/images/x\n" +
				"    - name: manifests\n      type: manifests\n      ref: oci://<registry>/deploy/x\n"},
		"chart only": {BuildPlan{Pinned: true, Deploy: &DeploySource{Kind: DeployChart}},
			"publish:\n  bundle: oci://<registry>/x\n  deploy: chart\n  artifacts:\n    - name: chart\n      type: chart\n      ref: oci://<registry>/deploy/x\n"},
	}
	for name, tc := range cases {
		if got := publishYAML(tc.plan, "x"); got != tc.want {
			t.Errorf("%s:\n%s\nwant\n%s", name, got, tc.want)
		}
	}
}
