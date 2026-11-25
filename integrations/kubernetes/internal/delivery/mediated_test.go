/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/delivery/v1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// guardedKs is the Kustomization applying the payments OCIRepository,
// labelled so admission guards it, with fn applied to its spec.
func guardedKs(name, namespace string, fn func(spec map[string]any)) *unstructured.Unstructured {
	k := kustomization(name, "payments", "True", "", "", "")
	k.SetNamespace(namespace)
	k.SetLabels(map[string]string{ApplyRefLabel: "payments"})
	k.Object["spec"].(map[string]any)["serviceAccountName"] = "payments-deployer"
	if fn != nil {
		fn(k.Object["spec"].(map[string]any))
	}
	return k
}

// TestMediated: the Stage says whether its pointer is all that decides what
// runs: one applier, labelled so admission guards it, overriding nothing.
func TestMediated(t *testing.T) {
	for name, c := range map[string]struct {
		objs   []*unstructured.Unstructured
		status string
		reason string
		msg    string
	}{
		"guarded": {[]*unstructured.Unstructured{guardedKs("payments", ns, func(s map[string]any) {
			s["images"], s["postBuild"], s["namePrefix"] = []any{}, map[string]any{}, ""
		})}, statusTrue, "PointerOnly", "only the pointer decides"},
		"no applier": {nil, statusFalse, "NoApplier", "no Kustomization applies OCIRepository payments"},
		"unlabelled": {[]*unstructured.Unstructured{kustomization("payments", "payments", "True", "", "", "")}, statusFalse, "ApplierUnguarded", "has no " + ApplyRefLabel},
		"images": {[]*unstructured.Unstructured{guardedKs("payments", ns, func(s map[string]any) {
			s["images"], s["prune"] = []any{map[string]any{"name": "app", "newTag": "evil"}}, true
		})}, statusFalse, "ApplierOverrides", "sets spec.images"},
		"suspended": {[]*unstructured.Unstructured{guardedKs("payments", ns, func(s map[string]any) {
			s["suspend"] = true
		})}, statusFalse, "ApplierOverrides", "sets spec.suspend"},
		"another cluster": {[]*unstructured.Unstructured{guardedKs("payments", ns, func(s map[string]any) {
			s["kubeConfig"], s["suspend"] = map[string]any{"secretRef": map[string]any{"name": "remote"}}, false
		})}, statusFalse, "ApplierOverrides", "sets spec.kubeConfig"},
		"cluster-admin applier": {[]*unstructured.Unstructured{guardedKs("payments", ns, func(s map[string]any) {
			delete(s, "serviceAccountName")
		})}, statusFalse, "ApplierUnscoped", "sets no serviceAccountName"},
		"two appliers": {[]*unstructured.Unstructured{guardedKs("payments", ns, nil), guardedKs("other", "elsewhere", func(s map[string]any) {
			s["sourceRef"].(map[string]any)["namespace"] = ns
		})}, statusFalse, "SeveralAppliers", "Kustomizations elsewhere/other and prod/payments all apply"},
		"another namespace's own": {[]*unstructured.Unstructured{guardedKs("payments", ns, nil), guardedKs("payments", "elsewhere", nil)}, statusTrue, "PointerOnly", ""},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "", false), verifiedRepo("payments-candidates", "latest@"+dB))
			for _, o := range c.objs {
				e.create(o)
			}
			must(t, e.reconcileStage("payments-prod"))
			m := conditionOf(e.getStage().Status.Conditions, ConditionMediated)
			if m == nil || m.Status != c.status || m.Reason != c.reason || !strings.Contains(m.Message, c.msg) {
				t.Fatalf("Mediated: %+v", m)
			}
		})
	}
	// An apply object recreated without the label (delete then create) is
	// guarded by nothing, however its applier looks.
	bare := ociRepo("payments", dA, "", false)
	bare.SetLabels(nil)
	e := newEnv(t, stageObj(dA), bare, verifiedRepo("payments-candidates", "latest@"+dB), guardedKs("payments", ns, nil))
	must(t, e.reconcileStage("payments-prod"))
	if m := conditionOf(e.getStage().Status.Conditions, ConditionMediated); m == nil || m.Status != statusFalse || m.Reason != "ApplyRefUnguarded" ||
		!strings.Contains(m.Message, ApplyRefLabel) {
		t.Fatalf("an unlabelled apply object is not mediated: %+v", m)
	}
	// An apply object that leaves files of the version out is not mediated.
	ignoring := ociRepo("payments", dA, "", false)
	_ = unstructured.SetNestedField(ignoring.Object, "deploy/", "spec", "ignore")
	e = newEnv(t, stageObj(dA), ignoring, verifiedRepo("payments-candidates", "latest@"+dB), guardedKs("payments", ns, nil))
	must(t, e.reconcileStage("payments-prod"))
	if m := conditionOf(e.getStage().Status.Conditions, ConditionMediated); m == nil || m.Reason != "ApplierOverrides" ||
		!strings.Contains(m.Message, "spec.ignore") {
		t.Fatalf("an apply object with spec.ignore is not mediated: %+v", m)
	}
	e = newEnv(t, stageObj(dA), ociRepo("payments", dA, "", false), verifiedRepo("payments-candidates", "latest@"+dB))
	e.fails["list/Kustomization"] = true
	if err := e.reconcileStage("payments-prod"); err == nil {
		t.Fatal("a failed Kustomization list must surface")
	}
}

// TestMediatedArgo: an Application that overrides what its source holds, or
// takes it from several sources, is not mediated by its targetRevision.
func TestMediatedArgo(t *testing.T) {
	for name, c := range map[string]struct {
		fn     func(app map[string]any)
		status string
		reason string
	}{
		"plain":      {nil, statusTrue, "PointerOnly"},
		"sources":    {func(a map[string]any) { a["spec"].(map[string]any)["sources"] = []any{map[string]any{"repoURL": "x"}} }, statusFalse, "SeveralSources"},
		"unlabelled": {func(a map[string]any) { delete(a["metadata"].(map[string]any), "labels") }, statusFalse, "ApplyRefUnguarded"},
		"helm values": {func(a map[string]any) {
			a["spec"].(map[string]any)["source"].(map[string]any)["helm"] = map[string]any{"values": "image: evil"}
		}, statusFalse, "ApplierOverrides"},
	} {
		t.Run(name, func(t *testing.T) {
			app := argoApp(dA, dA, "Synced", "Healthy")
			if c.fn != nil {
				c.fn(app.Object)
			}
			e := newEnv(t, argoStage(dA), app, verifiedRepo("payments-candidates", "latest@"+dB))
			if err := e.reconcileStage("payments-prod"); err != nil {
				t.Fatal(err)
			}
			if m := conditionOf(e.getStage().Status.Conditions, ConditionMediated); m == nil || m.Status != c.status || m.Reason != c.reason {
				t.Fatalf("Mediated: %+v", m)
			}
		})
	}
}

func TestEmpty(t *testing.T) {
	for v, want := range map[any]bool{nil: true, "": true, "x": false, false: true, true: false, 0: false} {
		if empty(v) != want {
			t.Errorf("empty(%v) = %v", v, !want)
		}
	}
	if !empty([]any{}) || empty([]any{1}) || !empty(map[string]any{}) || empty(map[string]any{"a": 1}) {
		t.Error("collections")
	}
}

// mediatedOf reconciles the production Stage s over objs and returns its
// Mediated condition.
func mediatedOf(t *testing.T, s *dv1.Stage, objs ...client.Object) *delivery.Condition {
	t.Helper()
	s.Spec.Environment = "production"
	e := newEnv(t, append([]client.Object{s, verifiedRepo("payments-candidates", "latest@"+dB)}, objs...)...)
	must(t, e.reconcileStage("payments-prod"))
	return conditionOf(e.getStage().Status.Conditions, ConditionMediated)
}

// argoValuesApp is the payments Application with spec.source.helm set to helm.
func argoValuesApp(helm map[string]any) *unstructured.Unstructured {
	app := argoApp(dA, dA, "Synced", "Healthy")
	app.Object["spec"].(map[string]any)["source"].(map[string]any)["helm"] = helm
	return app
}

func wantMediated(t *testing.T, m *delivery.Condition, status, reason, msg string) {
	t.Helper()
	if m == nil || m.Status != status || m.Reason != reason || !strings.Contains(m.Message, msg) {
		t.Fatalf("want %s %s %q, got %+v", status, reason, msg, m)
	}
}

// TestMediatedAcceptsEnvironmentValueFile: an Application selecting only its
// Stage's own values file is mediated by its pointer, and says so.
func TestMediatedAcceptsEnvironmentValueFile(t *testing.T) {
	m := mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{"values-production.yaml"}}))
	wantMediated(t, m, statusTrue, "PointerOnly", "with values-production.yaml for environment production")
	m = mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{"./values-production.yaml"}}))
	wantMediated(t, m, statusTrue, "PointerOnly", "with values-production.yaml")
	// Another helm key beside the one file is still an override.
	m = mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{"values-production.yaml"}, "values": "image: evil"}))
	wantMediated(t, m, statusFalse, "ApplierOverrides", "spec.source.helm.values")
	// An empty valueFiles selects nothing, as before.
	m = mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{}}))
	wantMediated(t, m, statusTrue, "PointerOnly", "admission guards it")
}

func TestMediatedRefusesOtherEnvironmentValueFile(t *testing.T) {
	m := mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{"values-staging.yaml"}}))
	wantMediated(t, m, statusFalse, "OtherEnvironmentValues",
		"Application payments selects values-staging.yaml, and the Stage delivers environment production")
}

func TestMediatedRefusesTwoValueFiles(t *testing.T) {
	m := mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{"values-production.yaml", "extra.yaml"}}))
	wantMediated(t, m, statusFalse, "OtherEnvironmentValues", "selects values-production.yaml, extra.yaml")
}

func TestMediatedRefusesParentPathValueFile(t *testing.T) {
	m := mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{"../values-production.yaml"}}))
	wantMediated(t, m, statusFalse, "OtherEnvironmentValues", "selects ../values-production.yaml")
}

// TestMediatedRefusesOtherEnvironmentOverlay: a Kustomization or an
// Application building another environment's overlay is refused; overlays
// name environments exactly, so overlays/prod is not production's.
func TestMediatedRefusesOtherEnvironmentOverlay(t *testing.T) {
	for _, p := range []string{"overlays/staging", "./overlays/prod"} {
		m := mediatedOf(t, stageObj(dA), ociRepo("payments", dA, "", false), guardedKs("payments", ns, func(s map[string]any) { s["path"] = p }))
		wantMediated(t, m, statusFalse, "OtherEnvironmentValues", "Kustomization payments builds overlays/")
	}
	app := argoApp(dA, dA, "Synced", "Healthy")
	app.Object["spec"].(map[string]any)["source"].(map[string]any)["path"] = "overlays/staging"
	wantMediated(t, mediatedOf(t, argoStage(dA), app), statusFalse, "OtherEnvironmentValues", "Application payments builds overlays/staging")
	m := mediatedOf(t, stageObj(dA), ociRepo("payments", dA, "", false), guardedKs("payments", ns, func(s map[string]any) { s["path"] = "overlays/production" }))
	wantMediated(t, m, statusTrue, "PointerOnly", "with overlays/production for environment production")
	// The overlay accepted, the Kustomization's own checks still hold.
	m = mediatedOf(t, stageObj(dA), ociRepo("payments", dA, "", false), guardedKs("payments", ns, func(s map[string]any) {
		s["path"], s["images"] = "overlays/production", []any{map[string]any{"name": "app"}}
	}))
	wantMediated(t, m, statusFalse, "ApplierOverrides", "sets spec.images")
}

// TestMediatedRefusesOtherNamespaceOverlay: the one Kustomization applying
// the Stage's OCIRepository from another namespace has its overlay judged
// and recorded, as one in the Stage's namespace does.
func TestMediatedRefusesOtherNamespaceOverlay(t *testing.T) {
	k := guardedKs("payments", "flux-system", func(s map[string]any) {
		s["sourceRef"].(map[string]any)["namespace"], s["path"] = ns, "overlays/staging"
	})
	m := mediatedOf(t, stageObj(dA), ociRepo("payments", dA, "", false), k)
	wantMediated(t, m, statusFalse, "OtherEnvironmentValues", "Kustomization payments builds overlays/staging")
}

// TestMediatedHidesRemoteValueFile: a valueFiles URL, which may carry
// credentials, never reaches the condition a Stage reader sees.
func TestMediatedHidesRemoteValueFile(t *testing.T) {
	m := mediatedOf(t, argoStage(dA), argoValuesApp(map[string]any{"valueFiles": []any{"https://deploy:ghp_XXXX@git.internal/values-prod.yaml"}}))
	wantMediated(t, m, statusFalse, "OtherEnvironmentValues", "selects a values file outside the chart")
	if strings.Contains(m.Message, "ghp_") || strings.Contains(m.Message, "git.internal") {
		t.Fatalf("leaks the URL: %+v", m)
	}
}

// TestMediatedKeepsRootPath: a path that is no overlay keeps today's result.
func TestMediatedKeepsRootPath(t *testing.T) {
	for _, p := range []string{"./", "deploy/", "overlays"} {
		m := mediatedOf(t, stageObj(dA), ociRepo("payments", dA, "", false), guardedKs("payments", ns, func(s map[string]any) { s["path"] = p }))
		wantMediated(t, m, statusTrue, "PointerOnly", "admission guards it")
		if strings.Contains(m.Message, "with ") {
			t.Fatalf("%s: no values named: %+v", p, m)
		}
	}
}

// TestMediatedHelmReleaseUnchanged: a HelmRelease selects no values file, so
// the environment changes nothing.
func TestMediatedHelmReleaseUnchanged(t *testing.T) {
	a := helmReleaseApplier{hr: helmReleaseObj("True", "", dA, dA), chart: &source{obj: chartRepo(dA)}}
	x, err1 := Cluster{}.mediated(t.Context(), ns, "production", a)
	y, err2 := Cluster{}.mediated(t.Context(), ns, "staging", a)
	if err1 != nil || err2 != nil || x != y || x.Reason != "PointerOnly" {
		t.Fatalf("%+v %+v %v %v", x, y, err1, err2)
	}
}
