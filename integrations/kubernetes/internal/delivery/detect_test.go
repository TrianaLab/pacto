/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrlrt "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// detect runs detectApplier for the Stage s over the env's cluster.
func (e *env) detect(s *dv1.Stage) delivery.ApplierStatus {
	e.t.Helper()
	return e.st.Cluster.detectApplier(context.Background(), s, e.now)
}

// relabel edits the payments Deployment in the apiserver.
func (e *env) relabel(edit func(d *appsv1.Deployment)) {
	e.t.Helper()
	d := &appsv1.Deployment{}
	must(e.t, e.c.Get(context.Background(), keyOf("payments"), d))
	edit(d)
	must(e.t, e.c.Update(context.Background(), d))
}

func wantApplier(t *testing.T, st delivery.ApplierStatus, state, manager, completeness, msg string) {
	t.Helper()
	if st.State != state || st.Manager != manager || st.Completeness != completeness || !strings.Contains(st.Message, msg) {
		t.Fatalf("want %s %s %s %q, got %+v", state, manager, completeness, msg, st)
	}
}

// TestDetectApplierArgo: a workload carrying the tracking id of the Stage's
// own Application, in the Stage namespace, reads Adopted.
func TestDetectApplierArgo(t *testing.T) {
	e := newEnv(t, argoDeployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	st := e.detect(argoStage(dA))
	wantApplier(t, st, delivery.ApplierAdopted, "argocd", "complete", "applies Deployment payments")
	if st.Display != "Argo CD (Application prod/payments, tracking annotation)" || st.AsOf != "2026-10-20T10:00:00Z" || st.Since != st.AsOf {
		t.Fatalf("display and times: %+v", st)
	}
	// The same workload under a Flux Stage is another manager's.
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierConflict, "argocd", "complete", "Pacto never writes a pointer")
}

// TestDetectApplierFluxKustomization: a workload the Kustomization that
// applies the Stage's OCIRepository labelled reads Adopted; Flux turned off
// or an unlistable Kustomization reads Unknown, partial.
func TestDetectApplierFluxKustomization(t *testing.T) {
	e := newEnv(t, deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2),
		kustomization("payments", "payments", "True", "", "", ""))
	st := e.detect(stageObj(dA))
	wantApplier(t, st, delivery.ApplierAdopted, "flux", "complete", "applies Deployment payments")
	if st.Owner != "Kustomization prod/payments" {
		t.Fatalf("owner: %+v", st)
	}
	e.st.Cluster.NoFlux = true
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnknown, "flux", "partial", "Kustomization prod/payments")
	e.st.Cluster.NoFlux = false
	e.fails["list/Kustomization"] = true
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnknown, "flux", "partial", "Kustomization prod/payments")
}

// TestDetectApplierNoPactoYet: without a Pacto, with a Pacto that evaluated
// no workload or with a workload that is gone, there is nothing to check.
func TestDetectApplierNoPactoYet(t *testing.T) {
	e := newEnv(t)
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnmanaged, "none", "complete", "no workload to check yet")
	unevaluated := pactoObj("payments", "payments", c1, "Compliant", 2)
	delete(unevaluated.Object["status"].(map[string]any), "evaluatedRevision")
	other := pactoObj("checkout", "checkout", c1, "Compliant", 2)
	e.create(unevaluated, other)
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnmanaged, "none", "complete", "no workload to check yet")
	e = newEnv(t, pactoObj("payments", "payments", c1, "Compliant", 2))
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnmanaged, "none", "complete", "no workload to check yet")
}

// TestDetectApplierUnlabelledWorkloadIsUnknown: a workload without its
// applier's marks under an OCIRepository applyRef reads Unknown, never
// Adopted; the absence of other managers is no evidence.
func TestDetectApplierUnlabelledWorkloadIsUnknown(t *testing.T) {
	d := deployment("payments", 2, 2)
	d.Labels = nil
	e := newEnv(t, d, pactoObj("payments", "payments", c1, "Compliant", 2), kustomization("payments", "payments", "True", "", "", ""))
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnknown, "none", "complete", "leaves no mark on Deployment payments")
	// Only a managedFields entry naming the manager: Pacto cannot tell which
	// Kustomization applies it.
	// The fake apiserver keeps no managedFields, so the read returns them.
	e.st.Cluster.Client = interceptor.NewClient(e.c, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		err := c.Get(ctx, key, obj, opts...)
		obj.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "kube-controller-manager", Subresource: "status"}, {Manager: "kustomize-controller"}})
		return err
	}})
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnknown, "flux", "complete", "field manager kustomize-controller")
}

// TestDetectApplierControllerOwnerIsUnknown: a Job an operator owns (a
// controller ownerReference) reads Unknown: Pacto adopts only Argo CD and
// Flux.
func TestDetectApplierControllerOwnerIsUnknown(t *testing.T) {
	yes := true
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "payments", OwnerReferences: []metav1.OwnerReference{
		{APIVersion: "v1", Kind: "ConfigMap", Name: "not-a-controller", UID: "u1"},
		{APIVersion: "example.com/v1", Kind: "PaymentsJob", Name: "nightly", UID: "u2", Controller: &yes},
	}}}
	p := pactoObj("payments", "payments", c1, "Compliant", 2)
	p.Object["status"].(map[string]any)["evaluatedRevision"].(map[string]any)["workload"].(map[string]any)["kind"] = "Job"
	e := newEnv(t, job, p)
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnknown, "unsupported", "complete", "PaymentsJob nightly")
}

// TestDetectApplierForbiddenWorkloadIsUnknown: a workload or a Pacto list
// the promoter may not read reads Unknown, partial, naming what it could not
// read.
func TestDetectApplierForbiddenWorkloadIsUnknown(t *testing.T) {
	e := newEnv(t, deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	e.st.Cluster.Client = interceptor.NewClient(e.c, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if kindOf(obj) == "Deployment" {
			return apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, key.Name, errors.New("no RBAC"))
		}
		return c.Get(ctx, key, obj, opts...)
	}})
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnknown, "", "partial", "Deployment payments")
	e = newEnv(t)
	e.fails["list/Pacto"] = true
	wantApplier(t, e.detect(stageObj(dA)), delivery.ApplierUnknown, "", "partial", "the Pactos in prod")
}

// TestDetectApplierNeverWrites: detection reads and never writes, whatever
// it finds.
func TestDetectApplierNeverWrites(t *testing.T) {
	bare := deployment("payments", 2, 2)
	bare.Labels = nil
	for name, objs := range map[string][]client.Object{
		"adopted":   {deployment("payments", 2, 2), kustomization("payments", "payments", "True", "", "", "")},
		"conflict":  {argoDeployment("payments", 2, 2)},
		"unknown":   {bare},
		"unmanaged": nil,
	} {
		e := newEnv(t, append(objs, pactoObj("payments", "payments", c1, "Compliant", 2))...)
		e.st.Cluster.Client = interceptor.NewClient(e.c, interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				t.Fatalf("%s: detection created an object", name)
				return nil
			},
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				t.Fatalf("%s: detection updated an object", name)
				return nil
			},
			Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
				t.Fatalf("%s: detection patched an object", name)
				return nil
			},
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				t.Fatalf("%s: detection deleted an object", name)
				return nil
			},
			SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
				t.Fatalf("%s: detection wrote a status", name)
				return nil
			},
			SubResourcePatch: func(context.Context, client.Client, string, client.Object, client.Patch, ...client.SubResourcePatchOption) error {
				t.Fatalf("%s: detection patched a status", name)
				return nil
			},
		})
		if st := e.detect(stageObj(dA)); st.State == "" {
			t.Fatalf("%s: no state", name)
		}
	}
}

// TestPlanOnDetectsFresh: the operation reconciler plans a promote on a
// fresh detection, never on the Stage's stored one: a workload another
// manager took since the Stage last reconciled rejects the promote.
func TestPlanOnDetectsFresh(t *testing.T) {
	e := baseEnv(t, nil)
	e.setStage(func(st *delivery.StageStatus) {
		st.Applier = &delivery.ApplierStatus{State: delivery.ApplierAdopted, Manager: "flux", Completeness: "complete"}
	})
	e.relabel(func(d *appsv1.Deployment) {
		d.Labels, d.Annotations = nil, map[string]string{"argocd.argoproj.io/tracking-id": "prod_payments:apps/Deployment:prod/payments"}
	})
	op := opObj("p1", delivery.TypePromote, dA, dB, bot)
	op.Status.ActorClass = delivery.ClassAgent
	p, err := e.op.plan(context.Background(), op, e.getStage())
	if err != nil || p.Predicted != delivery.PredictRejected || !strings.HasPrefix(planRefusal(p), "ApplierConflict: Argo CD Application prod/payments owns Deployment payments") {
		t.Fatalf("want Rejected on the fresh detection: %v %q %+v", err, planRefusal(p), p)
	}
}

// TestPromoteRejectedOnApplierConflict: a promote whose workloads another
// manager owns concludes Rejected and never writes the pointer.
func TestPromoteRejectedOnApplierConflict(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.relabel(func(d *appsv1.Deployment) {
		d.Labels, d.Annotations = nil, map[string]string{"meta.helm.sh/release-name": "payments", "meta.helm.sh/release-namespace": ns}
	})
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Rejected, "ApplierConflict")
	if !strings.Contains(o.Status.Message, "Helm release payments owns Deployment payments") || e.ociDigest("payments") != dA {
		t.Fatalf("message %q, pointer %s", o.Status.Message, e.ociDigest("payments"))
	}
}

// TestPromoteRejectedOnApplierUnknown: a promote whose workload carries no
// mark of the Stage's applier concludes Rejected, naming ApplierUnknown,
// without a panic on the plan's conflicts.
func TestPromoteRejectedOnApplierUnknown(t *testing.T) {
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	e.relabel(func(d *appsv1.Deployment) { d.Labels = nil })
	o := e.drive("p1")
	wantOutcome(t, o, delivery.Rejected, "ApplierUnknown")
	if !strings.Contains(o.Status.Message, "leaves no mark on Deployment payments") || e.ociDigest("payments") != dA {
		t.Fatalf("message %q, pointer %s", o.Status.Message, e.ociDigest("payments"))
	}
}

// TestShadowPlanCarriesApplierUnknown: what GitOps applied on an observing
// Stage whose workload carries no applier mark is a promote Pacto would have
// refused, and the shadow entry says why.
func TestShadowPlanCarriesApplierUnknown(t *testing.T) {
	e := observeEnv(t)
	e.relabel(func(d *appsv1.Deployment) { d.Labels = nil })
	must(t, e.reconcileStage("payments-prod"))
	e.apply(dC)
	must(t, e.reconcileStage("payments-prod"))
	b := shadowOf(e.getStage().Status, dC)
	if b == nil || b.Predicted != delivery.PredictRejected || !strings.HasPrefix(b.Question, "ApplierUnknown: ") {
		t.Fatalf("want the shadow plan to name ApplierUnknown: %+v", b)
	}
}

// askedMapper records the kinds the manager asks it to map.
type askedMapper struct {
	meta.RESTMapper
	asked map[string]bool
}

func (m askedMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	m.asked[gk.Kind] = true
	return m.RESTMapper.RESTMapping(gk, versions...)
}

// TestNoFluxWatchesWithoutFlux: NoFlux watches no OCIRepository or
// Kustomization; the zero value keeps both.
func TestNoFluxWatchesWithoutFlux(t *testing.T) {
	for _, noFlux := range []bool{false, true} {
		mapper := askedMapper{meta.NewDefaultRESTMapper(nil), map[string]bool{}}
		mapper.RESTMapper.(*meta.DefaultRESTMapper).Add(dv1.GroupVersion.WithKind("Stage"), meta.RESTScopeNamespace)
		mapper.RESTMapper.(*meta.DefaultRESTMapper).Add(gvkOCIRepository, meta.RESTScopeNamespace)
		mapper.RESTMapper.(*meta.DefaultRESTMapper).Add(gvkKustomization.GroupVersion().WithKind("Kustomization"), meta.RESTScopeNamespace)
		skip := true
		mgr, err := ctrlrt.NewManager(&rest.Config{Host: "https://127.0.0.1:1"}, ctrlrt.Options{Scheme: newScheme(),
			Metrics: metricsserver.Options{BindAddress: "0"}, Controller: config.Controller{SkipNameValidation: &skip},
			MapperProvider: func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return mapper, nil }})
		if err != nil {
			t.Fatal(err)
		}
		if err := (&StageReconciler{Cluster: Cluster{NoFlux: noFlux}}).SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
		if got := mapper.asked["OCIRepository"] || mapper.asked["Kustomization"]; got == noFlux {
			t.Fatalf("NoFlux=%v: watched the Flux kinds: %v", noFlux, mapper.asked)
		}
	}
}
