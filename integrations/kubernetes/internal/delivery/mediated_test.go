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
)

// guardedKs is the Kustomization applying the payments OCIRepository,
// labelled so admission guards it, with fn applied to its spec.
func guardedKs(name, namespace string, fn func(spec map[string]any)) *unstructured.Unstructured {
	k := kustomization(name, "payments", "True", "", "", "")
	k.SetNamespace(namespace)
	k.SetLabels(map[string]string{ApplyRefLabel: "payments"})
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
			if err := e.reconcileStage("payments-prod"); err != nil {
				t.Fatal(err)
			}
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
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if m := conditionOf(e.getStage().Status.Conditions, ConditionMediated); m == nil || m.Status != statusFalse || m.Reason != "ApplyRefUnguarded" ||
		!strings.Contains(m.Message, ApplyRefLabel) {
		t.Fatalf("an unlabelled apply object is not mediated: %+v", m)
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
		"plain":   {nil, statusTrue, "PointerOnly"},
		"sources": {func(a map[string]any) { a["spec"].(map[string]any)["sources"] = []any{map[string]any{"repoURL": "x"}} }, statusFalse, "SeveralSources"},
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
	for v, want := range map[any]bool{nil: true, "": true, "x": false, false: false, 0: false} {
		if empty(v) != want {
			t.Errorf("empty(%v) = %v", v, !want)
		}
	}
	if !empty([]any{}) || empty([]any{1}) || !empty(map[string]any{}) || empty(map[string]any{"a": 1}) {
		t.Error("collections")
	}
}
