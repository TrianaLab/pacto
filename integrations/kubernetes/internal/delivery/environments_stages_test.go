/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// adopted sets up payments in ns, environment env's namespace: the
// namespace, a Pacto of payments there (same repository and owner as its
// bundle) targeting Deployment payments, and that Deployment carrying marks.
func (pe *placeEnv) adopted(t *testing.T, ns string, labels, annotations map[string]string) {
	t.Helper()
	pacto := ladderPacto(ns, "payments")
	_ = unstructured.SetNestedMap(pacto.Object, map[string]any{"serviceName": "payments", "workloadRef": map[string]any{"kind": "Deployment", "name": "payments"}}, "spec", "target")
	_ = unstructured.SetNestedField(pacto.Object, "oci://"+pe.root+"/payments@sha256:aa", "status", "contract", "resolvedRef")
	for _, o := range []client.Object{namespace(ns), pacto,
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "payments", Labels: labels, Annotations: annotations}}} {
		if err := pe.c.Create(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
}

// argoTracked is the tracking annotation of Application app in appNS
// applying Deployment payments in ns.
func argoTracked(appNS, app, ns string) map[string]string {
	inst := app
	if appNS != "" {
		inst = appNS + "_" + app
	}
	return map[string]string{"argocd.argoproj.io/tracking-id": inst + ":apps/Deployment:" + ns + "/payments"}
}

// application is Application name in ns, labelled labels.
func application(ns, name string, labels map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": ns, "name": name}}}
	u.SetGroupVersionKind(gvkApplication)
	u.SetLabels(labels)
	return u
}

func (pe *placeEnv) stage(t *testing.T, ns, name string) *dv1.Stage {
	t.Helper()
	s := &dv1.Stage{}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: name}, s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (pe *placeEnv) state(t *testing.T) managedState {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	var s managedState
	if err := json.Unmarshal([]byte(cm.Data["stages.json"]), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// twoRungs is payments adopted by Argo CD in payments-staging and
// payments-production, each Application beside its workload.
func twoRungs(t *testing.T, labels map[string]string) *placeEnv {
	t.Helper()
	pe := newPlaceEnv(t, interceptor.Funcs{},
		application("payments-staging", "payments", labels), application("payments-production", "payments", nil))
	pe.adopted(t, "payments-staging", nil, argoTracked("payments-staging", "payments", "payments-staging"))
	pe.adopted(t, "payments-production", nil, argoTracked("payments-production", "payments", "payments-production"))
	pe.publish(t, "payments", delivered("payments"), nil)
	return pe
}

func TestManagedStageCreatedOnAdoptedApplier(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.pass(t)
	s := pe.stage(t, "payments-staging", "payments-staging")
	svc := delivery.ServiceRef{Domain: pe.root, Name: "payments"}
	if s.Labels[delivery.ManagedLabel] != "pacto" || s.Labels["delivery.pacto.trianalab.io/environment"] != "staging" ||
		s.Annotations["delivery.pacto.trianalab.io/placement-provenance"] != delivery.PlacementObserved ||
		s.Annotations["delivery.pacto.trianalab.io/environment-source"] != delivery.DesiredFromLadder {
		t.Fatalf("meta %+v %+v", s.Labels, s.Annotations)
	}
	if s.Spec.Service != svc || s.Spec.ApplyRef != (delivery.ApplyRef{Kind: delivery.ApplierArgoCD, Name: "payments"}) ||
		s.Spec.Candidates.Registry == nil || s.Spec.StartLevel != delivery.LevelObserve || s.Spec.Approval.Always {
		t.Fatalf("staging %+v", s.Spec)
	}
	prod := pe.stage(t, "payments-production", "payments-production")
	if up := prod.Spec.Candidates.UpstreamStage; up == nil || *up != (delivery.StageRef{Name: "payments-staging", Namespace: "payments-staging"}) {
		t.Fatalf("production %+v", prod.Spec.Candidates)
	}
	if r := rowOf(t, pe.rows(t), "payments", "production"); r.Stage != "payments-production/payments-production" || r.Reason != "" {
		t.Fatalf("row %+v", r)
	}
}

func TestCreatedUIDRecordedInState(t *testing.T) {
	pe := twoRungs(t, nil)
	// The rows an older promoter wrote, with no state yet.
	if err := pe.c.Create(t.Context(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap},
		Data: map[string]string{"rows.json": "[]"}}); err != nil {
		t.Fatal(err)
	}
	pe.pass(t)
	st := pe.state(t)
	if len(st.Objects) != 2 || st.Objects[0].UID != pe.stage(t, "payments-staging", "payments-staging").UID ||
		st.Objects[0] != (managedObject{Kind: "Stage", Namespace: "payments-staging", Name: "payments-staging", UID: st.Objects[0].UID,
			Service: "payments", Domain: pe.root, Environment: "staging"}) {
		t.Fatalf("state %+v", st)
	}
	// A second pass creates nothing and changes nothing; a managed Stage
	// someone deleted leaves the state and is created again.
	pe.writes = nil
	pe.pass(t)
	if slices.ContainsFunc(pe.writes, func(w string) bool { return strings.HasPrefix(w, "create/payments-") || w == "patch/payments-staging" }) {
		t.Fatalf("writes %v", pe.writes)
	}
	if err := pe.c.Delete(t.Context(), pe.stage(t, "payments-production", "payments-production")); err != nil {
		t.Fatal(err)
	}
	pe.writes = nil
	pe.pass(t)
	if !slices.Contains(pe.writes, "create/payments-production") || len(pe.state(t).Objects) != 2 {
		t.Fatalf("writes %v state %+v", pe.writes, pe.state(t))
	}
}

func TestAdoptedGuardedStartsApprovalGatedInCluster(t *testing.T) {
	pe := twoRungs(t, map[string]string{delivery.ApplyRefLabel: "payments-staging"})
	pe.pass(t)
	if s := pe.stage(t, "payments-staging", "payments-staging"); s.Spec.StartLevel != delivery.LevelAutonomous || !s.Spec.Approval.Always {
		t.Fatalf("staging %+v", s.Spec)
	}
	if s := pe.stage(t, "payments-production", "payments-production"); s.Spec.StartLevel != delivery.LevelObserve {
		t.Fatalf("production %+v", s.Spec)
	}
}

func TestReconcilerUpdatesOnlyOwnedFields(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.pass(t)
	s := pe.stage(t, "payments-staging", "payments-staging")
	s.Spec.Approval.Always = true
	s.Spec.ApplyRef.Name = "elsewhere"
	s.Labels["team"] = "kept"
	s.Annotations = nil
	if err := pe.c.Update(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	pe.pass(t)
	s = pe.stage(t, "payments-staging", "payments-staging")
	if s.Spec.ApplyRef.Name != "payments" || !s.Spec.Approval.Always || s.Labels["team"] != "kept" ||
		s.Annotations["delivery.pacto.trianalab.io/placement-source"] != delivery.PlacementFromPacto {
		t.Fatalf("staging %+v %+v", s.Spec, s.Labels)
	}
}

func TestHandWrittenStageNeverTouched(t *testing.T) {
	// A hand-written Stage of payments in staging: Pacto creates nothing for
	// staging and chains production to it.
	pe := twoRungs(t, nil)
	hand := &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "pay-stg"},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Domain: pe.root, Name: "payments"}, Environment: "staging"}}
	// A hand-written Stage of another service that holds the name Pacto
	// would give production.
	squat := &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-production", Name: "payments-production"},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: "orders"}, Environment: "qa"}}
	for _, o := range []client.Object{hand, squat} {
		if err := pe.c.Create(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
	pe.writes = nil
	pe.pass(t)
	for _, w := range pe.writes {
		if strings.HasPrefix(w, "create/payments-") || w == "patch/pay-stg" || w == "patch/payments-production" {
			t.Fatalf("writes %v", pe.writes)
		}
	}
	rows := pe.rows(t)
	if p := rowOf(t, rows, "payments", "production").Placement; p.Placed() || p.Refused != "a Stage payments-production not created by Pacto exists in payments-production" {
		t.Fatalf("production %+v", p)
	}
	if got := pe.stage(t, "payments-production", "payments-production"); got.Spec.Environment != "qa" || len(pe.state(t).Objects) != 0 {
		t.Fatalf("squat %+v", got.Spec)
	}
	// With the squat gone production is created, fed from the hand-written
	// staging Stage.
	if err := pe.c.Delete(t.Context(), squat); err != nil {
		t.Fatal(err)
	}
	pe.pass(t)
	if up := pe.stage(t, "payments-production", "payments-production").Spec.Candidates.UpstreamStage; up == nil || *up != (delivery.StageRef{Name: "pay-stg", Namespace: "team-a"}) {
		t.Fatalf("upstream %+v", up)
	}
}

func TestLabelledHandWrittenObjectNeverRetired(t *testing.T) {
	pe := twoRungs(t, nil)
	forged := &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: "payments-staging", Name: "payments-staging",
		Labels: delivery.ManagedLabels(delivery.ServiceRef{Name: "payments"}, "qa")},
		Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: "orders"}, Environment: "qa"}}
	if err := pe.c.Create(t.Context(), forged); err != nil {
		t.Fatal(err)
	}
	pe.writes = nil
	pe.pass(t)
	if slices.Contains(pe.writes, "patch/payments-staging") {
		t.Fatalf("writes %v", pe.writes)
	}
	if got := pe.stage(t, "payments-staging", "payments-staging"); got.Spec.Environment != "qa" {
		t.Fatalf("forged %+v", got.Spec)
	}
	// production has no Stage upstream of it in this cluster.
	if r := rowOf(t, pe.rows(t), "payments", "production"); r.Stage != "" || r.Reason != "upstream staging has no Stage of payments in this cluster yet" {
		t.Fatalf("production %+v", r)
	}
}

func TestUpstreamNotHostedGetsNoStage(t *testing.T) {
	pe := twoRungs(t, nil)
	pe.r.ClusterName = "eu-prod"
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "production"); r.Stage != "" || !strings.HasPrefix(r.Reason, "upstream staging is not in this cluster") {
		t.Fatalf("production %+v", r)
	}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: "payments-production", Name: "payments-production"}, &dv1.Stage{}); err == nil {
		t.Fatal("a Stage was created")
	}
}

func TestStagesOnlyBesideAnAdoptedApplier(t *testing.T) {
	ociSource := func(name, srcKind, srcNS string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": "flux-system", "name": name},
			"spec": map[string]any{"sourceRef": map[string]any{"kind": srcKind, "name": "payments", "namespace": srcNS}}}}
		u.SetGroupVersionKind(gvkKs)
		return u
	}
	flux := func(k string) map[string]string {
		return map[string]string{"kustomize.toolkit.fluxcd.io/name": k, "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}
	}
	pe := newPlaceEnv(t, interceptor.Funcs{}, ociSource("ok", "OCIRepository", "payments-a"),
		ociSource("git", "GitRepository", ""), ociSource("far", "OCIRepository", "elsewhere"))
	pe.r.Ladder = []string{"a", "f", "b", "c", "d", "e", "g"}
	pe.adopted(t, "payments-a", flux("ok"), nil)
	pe.adopted(t, "payments-b", flux("git"), nil)
	pe.adopted(t, "payments-c", flux("far"), nil)
	pe.adopted(t, "payments-d", flux("gone"), nil)
	pe.adopted(t, "payments-e", nil, argoTracked("", "payments", "payments-e")) // in the Argo CD namespace
	pe.adopted(t, "payments-f", map[string]string{"helm.toolkit.fluxcd.io/name": "payments", "helm.toolkit.fluxcd.io/namespace": "payments-f"}, nil)
	pe.adopted(t, "payments-g", nil, nil) // nothing applies it
	pe.publish(t, "payments", delivered("payments"), nil)
	pe.pass(t)
	rows := pe.rows(t)
	if s := pe.stage(t, "payments-a", "payments-a"); s.Spec.ApplyRef != (delivery.ApplyRef{Kind: delivery.ApplierFlux, Name: "payments"}) {
		t.Fatalf("a %+v", s.Spec.ApplyRef)
	}
	for env, want := range map[string]string{
		"b": "Kustomization flux-system/git applies from a GitRepository",
		"c": "the OCIRepository of Kustomization flux-system/far applies payments from outside payments-c",
		"d": "Kustomization flux-system/gone, which applies the workload, could not be read",
		"e": "Argo CD (Application payments, tracking annotation) applies payments from outside payments-e",
		"g": "nothing applies payments in payments-g yet; " + delivery.NoApplierMessage,
	} {
		if r := rowOf(t, rows, "payments", env); r.Stage != "" || !strings.HasPrefix(r.Reason, want) {
			t.Errorf("%s: %+v", env, r)
		}
	}
	if s := pe.stage(t, "payments-f", "payments-f"); s.Spec.ApplyRef != (delivery.ApplyRef{Kind: delivery.ApplierHelmRelease, Name: "payments"}) ||
		*s.Spec.Candidates.UpstreamStage != (delivery.StageRef{Name: "payments-a", Namespace: "payments-a"}) {
		t.Fatalf("f %+v", s.Spec)
	}
}

func TestManagedStageWriteFailures(t *testing.T) {
	boom := errors.New("boom")
	pe := newPlaceEnv(t, interceptor.Funcs{}, application("payments-staging", "payments", nil), application("payments-production", "payments", nil))
	pe.adopted(t, "payments-staging", nil, argoTracked("payments-staging", "payments", "payments-staging"))
	pe.adopted(t, "payments-production", nil, argoTracked("payments-production", "payments", "payments-production"))
	pe.publish(t, "payments", delivered("payments"), nil)

	// A full state creates no more.
	saved := maxManagedObjects
	t.Cleanup(func() { maxManagedObjects = saved })
	maxManagedObjects = 1
	v := pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "production"); !strings.HasPrefix(r.Reason, "Pacto keeps at most 1 objects") ||
		!strings.Contains(v.CompletenessReason, "the private state is full") {
		t.Fatalf("production %+v view %+v", r, v)
	}
	maxManagedObjects = saved

	// A create the apiserver refuses says why.
	pe.r.Client = interceptor.NewClient(pe.c, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			if _, ok := o.(*dv1.Stage); ok {
				return boom
			}
			return c.Create(ctx, o, opts...)
		},
		Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
			if _, ok := o.(*dv1.Stage); ok {
				return boom
			}
			return c.Patch(ctx, o, p, opts...)
		}})
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "production"); r.Reason != "the Stage payments-production/payments-production could not be created: boom" {
		t.Fatalf("production %+v", r)
	}
	// A drift patch that fails is partial.
	s := pe.stage(t, "payments-staging", "payments-staging")
	s.Spec.ApplyRef.Name = "drift"
	if err := pe.c.Update(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if v := pe.pass(t); !strings.Contains(v.CompletenessReason, "the Stage payments-staging/payments-staging could not be updated: boom") {
		t.Fatalf("view %+v", v)
	}
}

func TestUnreadableKustomizationIsPartial(t *testing.T) {
	boom := errors.New("boom")
	pe := newPlaceEnv(t, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, o client.Object, opts ...client.GetOption) error {
		if k.Namespace == "flux-system" {
			return boom
		}
		return c.Get(ctx, k, o, opts...)
	}})
	pe.r.Ladder = []string{"staging"}
	pe.adopted(t, "payments-staging", map[string]string{"kustomize.toolkit.fluxcd.io/name": "p", "kustomize.toolkit.fluxcd.io/namespace": "flux-system"}, nil)
	pe.publish(t, "payments", delivered("payments"), nil)
	if v := pe.pass(t); !strings.Contains(v.CompletenessReason, "Kustomization flux-system/p: boom") {
		t.Fatalf("view %+v", v)
	}
}
