package discover

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// readRepoFixture reads the folder folder of the repository fixture repo.
func readRepoFixture(t *testing.T, repo, folder string) Reading {
	t.Helper()
	r, err := ReadDir(filepath.Join(repo, folder), repo)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// envFields are the environments[<env>].file fields of r, by environment.
func envFields(r Reading) map[string]contract.Inferred {
	out := map[string]contract.Inferred{}
	for _, f := range r.Fields {
		if env, ok := strings.CutPrefix(f.Field, "environments["); ok {
			out[strings.TrimSuffix(env, "].file")] = f
		}
	}
	return out
}

func TestEnvsFromValuesFiles(t *testing.T) {
	envs := envFields(readFixture(t, "testdata/envs/values"))
	want := map[string]contract.Inferred{
		"staging":    {Field: "environments[staging].file", Value: "chart/values-staging.yaml", Source: "chart/values-staging.yaml", Confidence: contract.ConfidencePossible},
		"production": {Field: "environments[production].file", Value: "chart/values-production.yaml", Source: "chart/values-production.yaml", Confidence: contract.ConfidencePossible},
	}
	if len(envs) != len(want) {
		t.Fatalf("environments = %+v", envs)
	}
	for env, w := range want {
		if g := envs[env]; g.Field != w.Field || g.Value != w.Value || g.Source != w.Source || g.Confidence != w.Confidence || g.Declare != "" {
			t.Errorf("%s = %+v, want %+v", env, g, w)
		}
	}
}

func TestEnvsFromOverlays(t *testing.T) {
	envs := envFields(readFixture(t, "testdata/envs/overlays"))
	for _, env := range []string{"dev", "production"} {
		p := "overlays/" + env + "/kustomization.yaml"
		if g := envs[env]; g.Value != p || g.Source != p || g.Confidence != contract.ConfidencePossible {
			t.Errorf("%s = %+v", env, g)
		}
	}
	if len(envs) != 2 {
		t.Errorf("environments = %+v", envs)
	}
}

func TestEnvsPactoFileRanksFirst(t *testing.T) {
	g := envFields(readFixture(t, "testdata/envs/ranked"))["production"]
	want := []string{"chart/values-production.yaml", "overlays/production/kustomization.yaml"}
	if g.Value != "environments/production.yaml" || g.Confidence != contract.ConfidenceLikely || !slices.Equal(g.Outranks, want) {
		t.Errorf("production = %+v", g)
	}
}

func TestEnvsInvalidNameUnresolved(t *testing.T) {
	kust := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\n"
	r := readFixture(t, writeFiles(t, map[string]string{
		"chart/Chart.yaml":          "apiVersion: v2\nname: x\nversion: 1.0.0\n",
		"chart/values-Bad_Env.yaml": "a: 1\n",
		"overlays/" + strings.Repeat("a", 64) + "/kustomization.yaml": kust,
		"environments/Prod.yaml": "{}\n",
		"environments/ok.yaml":   "{}\n",
	}))
	if envs := envFields(r); len(envs) != 1 || envs["ok"].Value != "environments/ok.yaml" {
		t.Errorf("environments = %+v", envs)
	}
	got := findingsOf(r, CodeUnresolved)
	if len(got) != 3 {
		t.Fatalf("unresolved = %+v", got)
	}
	for _, f := range got {
		if !strings.HasPrefix(f.Message, f.Source+" names an environment") {
			t.Errorf("finding = %+v", f)
		}
	}
}

func TestEnvsDeclaredNotInferred(t *testing.T) {
	dir := "testdata/envs/values"
	rec, err := CompleteIn(dir, dir, &contract.Contract{Environments: []contract.Environment{{Name: "staging", File: "environments/staging.yaml"}}}, contract.InferredInputs{})
	if err != nil {
		t.Fatal(err)
	}
	names := fieldNames(rec)
	if slices.Contains(names, "environments[staging].file") || !slices.Contains(names, "environments[production].file") {
		t.Errorf("fields = %v", names)
	}
}

func TestArgoApplicationSources(t *testing.T) {
	r := readRepoFixture(t, "testdata/argo/repo", "apps/payments")
	g := envFields(r)["prod"]
	if g.Value != "overlays/prod/kustomization.yaml" || g.Confidence != contract.ConfidencePossible || !slices.Equal(g.Outranks, []string{"argo/prod.yaml:8"}) {
		t.Errorf("prod = %+v", g)
	}
}

func TestArgoApplicationValueFiles(t *testing.T) {
	r := readRepoFixture(t, "testdata/argo/repo", "apps/payments")
	g := envFields(r)["staging"]
	if g.Value != "chart/values-staging.yaml" || !slices.Equal(g.Outranks, []string{"argo/staging.yaml:11"}) {
		t.Errorf("staging = %+v", g)
	}
	// An Application alone names its environment, as the file it deploys.
	r = readFixture(t, writeFiles(t, map[string]string{"app.yaml": "apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  source:\n    path: chart\n    helm:\n      valueFiles:\n        - values-qa.yaml\n        - $values/values-qa.yaml\n"}))
	if g := envFields(r)["qa"]; g.Value != "chart/values-qa.yaml" || g.Source != "app.yaml:8" || g.Confidence != contract.ConfidenceLikely {
		t.Errorf("qa = %+v", g)
	}
	if got := findingsOf(r, CodeUnresolved); len(got) != 1 || got[0].Source != "app.yaml:9" || strings.Contains(got[0].Message, "$values") {
		t.Errorf("unresolved = %+v", got)
	}
}

func TestArgoApplicationSetUnsupported(t *testing.T) {
	r := readRepoFixture(t, "testdata/argo/repo", "apps/payments")
	got := findingsOf(r, CodeUnsupportedFile)
	if len(got) != 1 || got[0].Source != "argo/set.yaml:2" || !strings.Contains(got[0].Message, "ApplicationSet") {
		t.Errorf("unsupported = %+v", got)
	}
}

func TestFluxKustomizationPath(t *testing.T) {
	r := readRepoFixture(t, "testdata/flux/repo", "apps/payments")
	g := envFields(r)["prod"]
	if g.Value != "overlays/prod/kustomization.yaml" || !slices.Equal(g.Outranks, []string{"flux/sync.yaml:6"}) {
		t.Errorf("prod = %+v", g)
	}
}

func TestFluxHelmReleaseValuesFiles(t *testing.T) {
	r := readRepoFixture(t, "testdata/flux/repo", "apps/payments")
	envs := envFields(r)
	if g := envs["staging"]; g.Value != "chart/values-staging.yaml" || !slices.Equal(g.Outranks, []string{"flux/release.yaml:11"}) {
		t.Errorf("staging = %+v", g)
	}
	if len(envs) != 2 {
		t.Errorf("environments = %+v", envs)
	}
}

func TestGitOpsPathOutsideFolderUnresolved(t *testing.T) {
	r := readRepoFixture(t, "testdata/flux/repo", "apps/payments")
	got := findingsOf(r, CodeUnresolved)
	if len(got) != 1 || got[0].Source != "flux/other.yaml:6" || strings.Contains(got[0].Message, "apps/other") {
		t.Fatalf("unresolved = %+v", got)
	}
	// The folder is the repository: a path that climbs out of it, or an
	// absolute one, leaves it too; an object with no path names nothing.
	r = readFixture(t, writeFiles(t, map[string]string{"sync.yaml": "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ../overlays/prod\n---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: /overlays/prod\n---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec: {}\n"}))
	if got := findingsOf(r, CodeUnresolved); len(got) != 2 || len(envFields(r)) != 0 {
		t.Errorf("unresolved = %+v, fields = %+v", got, r.Fields)
	}
	for path, want := range map[string]bool{"apps/payments": true, "apps/payments/x": true, "apps/paymentsx": false, "apps": false} {
		rd := &reader{in: Input{FolderPath: "apps/payments"}}
		if _, ok := rd.inFolder(path); ok != want {
			t.Errorf("inFolder(%s) = %v", path, ok)
		}
	}
}

func TestUnsupportedFileTable(t *testing.T) {
	for row, file := range map[string]string{
		"helmfile": "helmfile.yaml", "tiltfile": "Tiltfile", "jsonnet": "main.jsonnet", "libsonnet": "lib.libsonnet", "cue": "deploy.cue",
		"graphql": "schema.graphql", "graphqls": "schema.graphqls", "gql": "query.gql", "applicationset": "appset.yaml",
		"helmcharts": "kustomization.yaml", "remote": "kustomization.yaml", "include": "compose.yaml", "extends": "compose.yaml", "envfile": "compose.yaml",
	} {
		r := readFixture(t, "testdata/unsupported/"+row)
		got := findingsOf(r, CodeUnsupportedFile)
		if len(got) != 1 || !strings.HasPrefix(got[0].Source, file) || !strings.HasPrefix(got[0].Message, got[0].Source+" ") {
			t.Errorf("%s: unsupported = %+v", row, got)
		}
	}
}
