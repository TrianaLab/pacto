/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// argoApp is an Argo CD Application with an OCI source pinned to target, at
// sync revision rev with the given sync and health status.
func argoApp(target, rev, sync, health string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": "payments"},
		"spec":     map[string]any{"source": map[string]any{"repoURL": "oci://" + repo, "targetRevision": target}},
		"status": map[string]any{"sync": map[string]any{"revision": rev, "status": sync},
			"health": map[string]any{"status": health, "message": "pod crashloop"}},
	}}
	u.SetGroupVersionKind(gvkApplication)
	return u
}

func argoStage(pointer string) *dv1.Stage {
	s := stageObj(pointer)
	s.Spec.ApplyRef.Kind = delivery.ApplierArgoCD
	return s
}

// argoOp is a promote whose intent digest binds the Argo CD Stage's policy.
func argoOp(from, to string) *dv1.Operation {
	o := opObj("p1", delivery.TypePromote, from, to, bot)
	o.Spec.IntentDigest = delivery.IntentDigest(o.Spec, argoStage("").Spec)
	return o
}

func (e *env) app() *unstructured.Unstructured {
	e.t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkApplication)
	if err := e.c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "payments"}, u); err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) setApp(u *unstructured.Unstructured) {
	e.t.Helper()
	_ = e.c.Delete(context.Background(), u)
	e.create(u)
}

// TestArgoCDPromoteShipsAndVerifies: on a Stage applied by Argo CD the
// promoter writes only spec.source.targetRevision, asks for a refresh and
// confirms the move once the Application is Synced and Healthy at that digest.
func TestArgoCDPromoteShipsAndVerifies(t *testing.T) {
	e := newEnv(t, argoStage(dA), argoApp(dA, dA, "Synced", "Healthy"),
		deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	e.create(argoOp(dA, dB))
	o := e.drive("p1")
	target, _, _ := unstructured.NestedString(e.app().Object, "spec", "source", "targetRevision")
	if o.Status.Phase != delivery.PhaseRunning || target != dB || e.app().GetAnnotations()["argocd.argoproj.io/refresh"] != "normal" {
		t.Fatalf("want Running with targetRevision written and a refresh asked, got %s %s (%s)", o.Status.Phase, target, o.Status.Message)
	}
	if o = e.drive("p1"); !strings.Contains(o.Status.Message, "has not synced") {
		t.Fatalf("waits for Argo CD: %q", o.Status.Message)
	}
	e.setApp(argoApp(dB, dB, "Synced", "Progressing"))
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseRunning || !strings.Contains(o.Status.Message, "Progressing") {
		t.Fatalf("Progressing is not applied yet: %s %q", o.Status.Phase, o.Status.Message)
	}
	e.setApp(argoApp(dB, dB, "Synced", "Healthy"))
	if o = e.drive("p1"); o.Status.Phase != delivery.PhaseVerifying {
		t.Fatalf("want Verifying once Argo CD synced B, got %s (%s)", o.Status.Phase, o.Status.Message)
	}
	e.evaluate(c2, "Compliant")
	wantOutcome(t, e.drive("p1"), delivery.Succeeded, "Verified")
}

// TestArgoCDFailures: a failed sync of the digest and a Degraded Application
// at the digest are confirmed failures; a foreign targetRevision freezes.
func TestArgoCDFailures(t *testing.T) {
	for name, app := range map[string]*unstructured.Unstructured{
		"sync failed": func() *unstructured.Unstructured {
			u := argoApp(dB, dA, "OutOfSync", "Healthy")
			_ = unstructured.SetNestedMap(u.Object, map[string]any{"phase": "Failed", "message": "one or more objects failed to apply",
				"syncResult": map[string]any{"revision": dB}}, "status", "operationState")
			return u
		}(),
		"degraded": argoApp(dB, dB, "Synced", "Degraded"),
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, argoStage(dA), argoApp(dA, dA, "Synced", "Healthy"))
			e.create(argoOp(dA, dB))
			e.drive("p1")
			e.setApp(app)
			wantOutcome(t, e.drive("p1"), delivery.Failed, "ApplyFailed")
		})
	}
	t.Run("a foreign writer freezes", func(t *testing.T) {
		e := newEnv(t, argoStage(dA), argoApp(dA, dA, "Synced", "Healthy"))
		e.create(argoOp(dA, dB))
		e.drive("p1")
		e.setApp(argoApp(dC, dC, "Synced", "Healthy"))
		o := e.drive("p1")
		wantOutcome(t, o, delivery.Errored, "ForeignWriter")
		if !strings.Contains(o.Status.Message, "spec.source.targetRevision of Application payments") {
			t.Fatalf("names the Argo CD field: %q", o.Status.Message)
		}
	})
	t.Run("a missing Application waits", func(t *testing.T) {
		e := newEnv(t, argoStage(dA))
		e.create(argoOp(dA, dB))
		if o := e.drive("p1"); o.Status.Outcome != "" {
			t.Fatalf("nothing concludes without the applier: %+v", o.Status)
		}
	})
}

// TestArgoCDWitness: an edit of targetRevision outside Pacto is witnessed.
func TestArgoCDWitness(t *testing.T) {
	e := newEnv(t, argoStage(dA), argoApp(dC, dC, "Synced", "Healthy"))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	ops := e.ops()
	if len(ops) != 1 || !ops[0].Spec.Witnessed || !strings.Contains(ops[0].Spec.Reason, "spec.source.targetRevision") {
		t.Fatalf("want one witnessed override naming the Argo CD field: %+v", ops)
	}
}
