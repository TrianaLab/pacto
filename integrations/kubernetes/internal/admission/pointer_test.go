/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package admission

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const fluxUser = "system:serviceaccount:flux-system:kustomize-controller"

var labelled = map[string]string{applyRef: "payments"}

func ociRepo(name string) *unstructured.Unstructured {
	return obj("source.toolkit.fluxcd.io/v1", "OCIRepository", name, labelled, map[string]any{
		"url": "oci://reg.example/apps/payments", "ref": map[string]any{"digest": digestA}})
}

func kustomization(name string) *unstructured.Unstructured {
	return obj("kustomize.toolkit.fluxcd.io/v1", "Kustomization", name, labelled, map[string]any{
		"sourceRef": map[string]any{"kind": "OCIRepository", "name": "payments"}, "path": "./",
		"serviceAccountName": "payments-deployer"})
}

func argoApp(name string) *unstructured.Unstructured {
	return obj("argoproj.io/v1alpha1", "Application", name, labelled, map[string]any{
		"project":     "payments",
		"destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": ns},
		"source":      map[string]any{"repoURL": "reg.example/apps/payments", "targetRevision": digestA}})
}

// A labelled pointer, applier or Application is created and deleted only by
// break-glass or the GitOps controller: delete and recreate is an edit too.
func TestPointerCreateAndDelete(t *testing.T) {
	ctx := context.Background()
	const msg = "only break-glass or the GitOps controller creates or deletes"
	for _, mk := range []func(string) *unstructured.Unstructured{ociRepo, kustomization, argoApp} {
		denied(t, as("alice").Create(ctx, mk(uniq("p"))), msg)
		mine := mustCreate(t, as(fluxUser), mk(uniq("p")))
		denied(t, as("alice").Delete(ctx, mine), msg)
		allowed(t, as(fluxUser).Delete(ctx, mine))
		allowed(t, as("root", breakGlass).Delete(ctx, mustCreate(t, as("root", breakGlass), mk(uniq("p")))))
	}
	// An object without the label is none of delivery's business.
	plain := ociRepo(uniq("plain"))
	plain.SetLabels(nil)
	allowed(t, as("alice").Create(ctx, plain))
	allowed(t, as("alice").Delete(ctx, plain))
}

// Every field that decides what runs is guarded on update: OCIRepository
// spec.ignore, the Kustomization's account, cluster, suspension and
// decryption, the Application's destination and project.
func TestPointerGuardsWhatRuns(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		mk   func(string) *unstructured.Unstructured
		edit func(spec map[string]any)
		msg  string
	}{
		"ignore":             {ociRepo, func(s map[string]any) { s["ignore"] = "*" }, "decide what the Pacto delivery pointer runs"},
		"serviceAccountName": {kustomization, func(s map[string]any) { delete(s, "serviceAccountName") }, "decide what the Pacto delivery pointer runs"},
		"kubeConfig":         {kustomization, func(s map[string]any) { s["kubeConfig"] = map[string]any{"secretRef": map[string]any{"name": "x"}} }, "decide what the Pacto delivery pointer runs"},
		"suspend":            {kustomization, func(s map[string]any) { s["suspend"] = true }, "decide what the Pacto delivery pointer runs"},
		"decryption":         {kustomization, func(s map[string]any) { s["decryption"] = map[string]any{"provider": "sops"} }, "decide what the Pacto delivery pointer runs"},
		"destination":        {argoApp, func(s map[string]any) { s["destination"] = map[string]any{"server": "https://elsewhere"} }, "decide what the Pacto delivery pointer runs"},
		"project":            {argoApp, func(s map[string]any) { s["project"] = "default" }, "decide what the Pacto delivery pointer runs"},
	} {
		t.Run(name, func(t *testing.T) {
			u := mustCreate(t, as(fluxUser), c.mk(uniq("g")))
			c.edit(u.Object["spec"].(map[string]any))
			denied(t, as("alice").Update(ctx, u), c.msg)
			allowed(t, as("root", breakGlass).Update(ctx, u))
		})
	}
}
