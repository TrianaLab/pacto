package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// argoApp is an Argo CD Application whose source.helm is helm (nil for
// none) and whose source.path is p.
func argoApp(helm map[string]any, p string) map[string]any {
	src := map[string]any{}
	if helm != nil {
		src["helm"] = helm
	}
	if p != "" {
		src["path"] = p
	}
	return map[string]any{"spec": map[string]any{"source": src}}
}

func valueFiles(files ...any) map[string]any { return map[string]any{"valueFiles": files} }

func wantSelection(t *testing.T, name string, got ValuesSelection, file, form, problem string) {
	t.Helper()
	if got.File != file || got.Form != form || !strings.Contains(got.Problem, problem) || (problem == "") != (got.Problem == "") {
		t.Errorf("%s: want %q %q %q, got %+v", name, file, form, problem, got)
	}
}

func TestSelectedValuesArgoOneFile(t *testing.T) {
	got := SelectedValues(argoRef, argoApp(valueFiles("values-production.yaml"), ""), nil, "prod", "production")
	wantSelection(t, "one file", got, "values-production.yaml", FormHelmValues, "")
}

func TestSelectedValuesArgoDotSlash(t *testing.T) {
	got := SelectedValues(argoRef, argoApp(valueFiles("./values-production.yaml"), ""), nil, "prod", "production")
	wantSelection(t, "dot slash", got, "values-production.yaml", FormHelmValues, "")
}

func TestSelectedValuesArgoOtherEnvironment(t *testing.T) {
	got := SelectedValues(argoRef, argoApp(valueFiles("values-staging.yaml"), ""), nil, "prod", "production")
	wantSelection(t, "other environment", got, "values-staging.yaml", FormHelmValues,
		"Application payments selects values-staging.yaml, and the Stage delivers environment production")
}

func TestSelectedValuesArgoTwoFiles(t *testing.T) {
	got := SelectedValues(argoRef, argoApp(valueFiles("values-production.yaml", "extra.yaml"), ""), nil, "prod", "production")
	wantSelection(t, "two files", got, "values-production.yaml, extra.yaml", FormHelmValues,
		"Application payments selects values-production.yaml, extra.yaml, and the Stage delivers environment production")
}

func TestSelectedValuesArgoParentPath(t *testing.T) {
	got := SelectedValues(argoRef, argoApp(valueFiles("../values-production.yaml"), ""), nil, "prod", "production")
	wantSelection(t, "parent path", got, "../values-production.yaml", FormHelmValues, "selects ../values-production.yaml")
	got = SelectedValues(argoRef, argoApp(valueFiles(7), ""), nil, "prod", "production")
	wantSelection(t, "not a string", got, otherValuesFile, FormHelmValues, "selects "+otherValuesFile+", and the Stage")
	got = SelectedValues(argoRef, argoApp(valueFiles("values-.yaml"), ""), nil, "prod", "")
	wantSelection(t, "no environment", got, "values-.yaml", FormHelmValues, "Application payments selects values-.yaml, and the Stage names no environment")
	got = SelectedValues(argoRef, argoApp(valueFiles(), ""), nil, "prod", "production")
	wantSelection(t, "empty", got, "", "", "")
}

// A valueFiles entry that is a URL can carry credentials or an internal host
// a reader of the Stage has no grant on, so it never reaches File or Problem;
// a long list is counted, never listed in full.
func TestSelectedValuesArgoHidesRemoteValueFiles(t *testing.T) {
	secret := "https://deploy:ghp_XXXX@git.internal/payments/values-prod.yaml?ref=main"
	for _, f := range []any{secret, "/etc/values.yaml", "git.internal:values.yaml", strings.Repeat("a", 254)} {
		got := SelectedValues(argoRef, argoApp(valueFiles(f), ""), nil, "prod", "production")
		if strings.Contains(got.File+got.Problem, "ghp_") || strings.Contains(got.File+got.Problem, "internal") || got.File != otherValuesFile {
			t.Errorf("%v: %+v", f, got)
		}
	}
	got := SelectedValues(argoRef, argoApp(valueFiles("a.yaml", "b.yaml", "c.yaml", "d.yaml", secret), ""), nil, "prod", "production")
	wantSelection(t, "many", got, "a.yaml, b.yaml, c.yaml, 2 more", FormHelmValues, "selects a.yaml, b.yaml, c.yaml, 2 more")
}

// A Kustomization in another namespace that applies the Stage's
// OCIRepository is one Mediated counts, so its overlay is judged too.
func TestSelectedValuesKustomizeOtherNamespace(t *testing.T) {
	k := kustomizationOf("flux-system", "payments", ApplierFlux, "prod", "payments")
	k.Spec.Path = "overlays/staging"
	got := SelectedValues(ApplyRef{Name: "payments"}, nil, []KustomizationView{k}, "prod", "production")
	wantSelection(t, "other namespace", got, "overlays/staging", FormKustomizeOverlay, "Kustomization payments builds overlays/staging")
}

func TestSelectedValuesArgoOtherHelmKey(t *testing.T) {
	helm := valueFiles("values-production.yaml")
	helm["parameters"] = []any{map[string]any{"name": "image.tag", "value": "x"}}
	app := argoApp(helm, "")
	wantSelection(t, "other helm key", SelectedValues(argoRef, app, nil, "prod", "production"), "values-production.yaml", FormHelmValues, "")
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"}, Spec: StageSpec{Environment: "production", ApplyRef: argoRef}}
	tg := ManifestTargetOf(stage, app, nil, nil)
	if strings.Join(tg.Unread, "|") != "Application payments transforms the artifact: spec.source.helm" || tg.ValuesFile != "" {
		t.Fatalf("a parameters key keeps the transform: %+v", tg)
	}
	tg = ManifestTargetOf(stage, argoApp(valueFiles("values-production.yaml"), ""), nil, nil)
	if len(tg.Unread) != 0 || tg.ValuesFile != "values-production.yaml" || tg.Environment != "production" {
		t.Fatalf("only valueFiles is no transform: %+v", tg)
	}
	tg = ManifestTargetOf(stage, argoApp(valueFiles("values-staging.yaml"), ""), nil, nil)
	if strings.Join(tg.Unread, "|") != "Application payments transforms the artifact: spec.source.helm|"+
		"Application payments selects values-staging.yaml, and the Stage delivers environment production" || tg.ValuesFile != "" {
		t.Fatalf("another environment's file keeps the transform and says why: %+v", tg)
	}
	tg = ManifestTargetOf(stage, argoApp(nil, "deploy/overlays/production"), nil, nil)
	if len(tg.Unread) != 0 || tg.ValuesFile != "deploy/overlays/production" || tg.Path != "deploy/overlays/production" {
		t.Fatalf("an Argo CD overlay of the environment: %+v", tg)
	}
}

func TestSelectedValuesKustomizeOverlay(t *testing.T) {
	k := kustomizationOf("prod", "payments", ApplierFlux, "", "payments")
	k.Spec.Path = "./overlays/production"
	got := SelectedValues(ApplyRef{Name: "payments"}, nil, []KustomizationView{k}, "prod", "production")
	wantSelection(t, "overlay", got, "overlays/production", FormKustomizeOverlay, "")
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"}, Spec: StageSpec{Environment: "production", ApplyRef: ApplyRef{Name: "payments"}}}
	if tg := ManifestTargetOf(stage, nil, []KustomizationView{k}, nil); tg.ValuesFile != "overlays/production" || len(tg.Unread) != 0 {
		t.Fatalf("the Kustomization builds the environment's overlay: %+v", tg)
	}
	got = SelectedValues(argoRef, argoApp(nil, "overlays/production"), nil, "prod", "production")
	wantSelection(t, "argo overlay", got, "overlays/production", FormKustomizeOverlay, "")
}

func TestSelectedValuesKustomizeOtherOverlay(t *testing.T) {
	k := kustomizationOf("prod", "payments", ApplierFlux, "", "payments")
	k.Spec.Path = "overlays/staging"
	// Kustomizations that do not apply the Stage's OCIRepository never count.
	foreign := kustomizationOf("prod", "other", ApplierFlux, "", "another")
	foreign.Spec.Path = "overlays/dev"
	got := SelectedValues(ApplyRef{Name: "payments"}, nil, []KustomizationView{k, foreign}, "prod", "production")
	wantSelection(t, "other overlay", got, "overlays/staging", FormKustomizeOverlay,
		"Kustomization payments builds overlays/staging, and the Stage delivers environment production")
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"}, Spec: StageSpec{Environment: "production", ApplyRef: ApplyRef{Name: "payments"}}}
	if tg := ManifestTargetOf(stage, nil, []KustomizationView{k}, nil); tg.ValuesFile != "" || strings.Join(tg.Unread, "|") != got.Problem {
		t.Fatalf("another environment's overlay is unread: %+v", tg)
	}
	got = SelectedValues(argoRef, argoApp(nil, "overlays/staging"), nil, "prod", "production")
	wantSelection(t, "argo other overlay", got, "overlays/staging", FormKustomizeOverlay,
		"Application payments builds overlays/staging, and the Stage delivers environment production")
}

func TestSelectedValuesRootPathNone(t *testing.T) {
	k := kustomizationOf("prod", "payments", ApplierFlux, "", "payments")
	k.Spec.Path = "deploy"
	wantSelection(t, "deploy path", SelectedValues(ApplyRef{Name: "payments"}, nil, []KustomizationView{k}, "prod", "production"), "", "", "")
	wantSelection(t, "root", SelectedValues(ApplyRef{Name: "payments"}, nil, nil, "prod", "production"), "", "", "")
	wantSelection(t, "argo root", SelectedValues(argoRef, argoApp(nil, "."), nil, "prod", "production"), "", "", "")
	wantSelection(t, "argo no source", SelectedValues(argoRef, nil, nil, "prod", "production"), "", "", "")
}

func TestSelectedValuesHelmReleaseNone(t *testing.T) {
	hr := map[string]any{"spec": map[string]any{"values": map[string]any{"a": 1}}}
	wantSelection(t, "helmrelease", SelectedValues(ApplyRef{Kind: ApplierHelmRelease, Name: "payments"}, hr, nil, "prod", "production"), "", "", "")
}

const prodValues = "replicas: 3\nimage:\n  tag: v1\n"

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(h[:])
}

func chartWith(t *testing.T, extra map[string]string) []byte {
	t.Helper()
	files := map[string]string{"app/Chart.yaml": "name: app\n", "app/templates/cm.yaml": obj("v1", "ConfigMap", "c", "")}
	for n, c := range extra {
		files["app/"+n] = c
	}
	return tgz(t, files)
}

func TestInspectChartReadsValuesFile(t *testing.T) {
	target := ManifestTarget{Namespace: "prod", Environment: "production", ValuesFile: "values-production.yaml", valuesChart: true}
	in := InspectArtifact(HelmChartMediaType, chartWith(t, map[string]string{"values-production.yaml": prodValues}), target, BuiltinScope)
	v := wantVerdict(t, "values", in, Pass, "every one of the 1 objects")
	if v.Citation["valuesFile"] != "values-production.yaml" || v.Citation["valuesDigest"] != sha(prodValues) ||
		in.Values["replicas"] != float64(3) || in.ValuesDigest != sha(prodValues) {
		t.Fatalf("the values file is read and cited: %+v %+v", v, in)
	}
	in = InspectArtifact(HelmChartMediaType, chartWith(t, map[string]string{"values-production.yaml": "a: [\n"}), target, BuiltinScope)
	v = wantVerdict(t, "bad yaml", in, Unknown, "values-production.yaml in this version is not valid YAML")
	if v.Citation["valuesFile"] != "" || in.Values != nil {
		t.Fatalf("an unreadable values file is never cited: %+v", v)
	}
}

func TestMissingValuesFileWaitsForPerson(t *testing.T) {
	target := ManifestTarget{Namespace: "prod", Environment: "production", ValuesFile: "values-production.yaml", valuesChart: true}
	in := InspectArtifact(HelmChartMediaType, chartWith(t, map[string]string{"values-staging.yaml": prodValues}), target, BuiltinScope)
	v := wantVerdict(t, "missing", in, Unknown, "values-production.yaml is not in the chart of this version, so Argo CD cannot render it for production")
	if !strings.Contains(v.Citation["unread"], "values-production.yaml") || v.Citation["valuesFile"] != "" {
		t.Fatalf("unread names the file: %+v", v)
	}
	// A layer Pacto cannot read never cites a values file either.
	in = InspectArtifact(HelmChartMediaType, []byte("not a tar"), target, BuiltinScope)
	if v := in.Verdict(); v.Result != Unknown || v.Citation["valuesFile"] != "" {
		t.Fatalf("an unreadable layer asks a person: %+v", v)
	}
	// A helm values file on an artifact that is no chart is unread.
	in = InspectArtifact(ArgoCDContentMediaType, tgz(t, map[string]string{"a.yaml": obj("v1", "ConfigMap", "c", ""), "values-production.yaml": prodValues}), target, BuiltinScope)
	wantVerdict(t, "not a chart", in, Unknown, "the applier selects values-production.yaml, and this version is not a Helm chart")
}

func TestHelmReleaseChartEnvironmentFileAsksPerson(t *testing.T) {
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"},
		Spec: StageSpec{Environment: "production", ApplyRef: ApplyRef{Kind: ApplierHelmRelease, Name: "payments"}}}
	target := ManifestTargetOf(stage, map[string]any{}, nil, nil)
	in := InspectArtifact(HelmChartMediaType, chartWith(t, map[string]string{"values-production.yaml": prodValues}), target, BuiltinScope)
	wantVerdict(t, "helmrelease", in, Unknown, "the chart carries values-production.yaml, and HelmRelease payments takes its chart from an OCIRepository, "+
		"which selects no values file inside the chart, so production would run on values.yaml")
}

func TestHelmReleaseChartWithoutEnvironmentFilePasses(t *testing.T) {
	stage := StageObject{Metadata: ObjectMeta{Namespace: "prod"},
		Spec: StageSpec{Environment: "production", ApplyRef: ApplyRef{Kind: ApplierHelmRelease, Name: "payments"}}}
	target := ManifestTargetOf(stage, map[string]any{}, nil, nil)
	in := InspectArtifact(HelmChartMediaType, chartWith(t, map[string]string{"values.yaml": prodValues, "values-staging.yaml": prodValues}), target, BuiltinScope)
	if v := wantVerdict(t, "no environment file", in, Pass, "every one of the 1 objects"); v.Citation["valuesFile"] != "" {
		t.Fatalf("a HelmRelease selects no values file: %+v", v)
	}
}

func TestInspectOverlayRecordsValues(t *testing.T) {
	target := ManifestTarget{Namespace: "prod", Environment: "production", Path: "overlays/production", ValuesFile: "overlays/production"}
	in := InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"overlays/production/a.yaml": obj("v1", "ConfigMap", "c", "")}), target, BuiltinScope)
	v := wantVerdict(t, "overlay", in, Pass, "every one of the 1 objects")
	if v.Citation["valuesFile"] != "overlays/production" || v.Citation["valuesDigest"] != "" || in.ValuesDigest != "" {
		t.Fatalf("an overlay is cited with no digest: %+v", v)
	}
	in = InspectArtifact(FluxContentMediaType, tgz(t, map[string]string{"overlays/staging/a.yaml": obj("v1", "ConfigMap", "c", "")}), target, BuiltinScope)
	if v := wantVerdict(t, "missing overlay", in, Unknown, "holds nothing under overlays/production"); v.Citation["valuesFile"] != "" {
		t.Fatalf("a missing overlay is never cited: %+v", v)
	}
}
