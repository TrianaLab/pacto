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

	"net/http"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	ctrlrt "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

type failingReader struct{ client.Reader }

func (failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errTest
}

// at drives a fresh promote A->B to a phase and returns the env.
func at(t *testing.T, phase delivery.Phase) *env {
	t.Helper()
	e := baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
	switch phase {
	case delivery.PhasePending:
		e.reconcileOp("p1")
	case delivery.PhaseRunning:
		e.reconcileOp("p1")
		e.reconcileOp("p1")
	case delivery.PhaseVerifying:
		e.drive("p1")
		e.apply(dB)
		e.drive("p1")
	}
	if got := e.getOp("p1").Status.Phase; got != phase {
		t.Fatalf("setup: want %s, got %s", phase, got)
	}
	return e
}

func TestReconcileSurfacesEveryWriteFailure(t *testing.T) {
	cases := []struct {
		name  string
		phase delivery.Phase
		fail  string
		prep  func(e *env)
	}{
		{"get op", "", "get/Operation", nil},
		{"get stage", "", "get/Stage", nil},
		{"admit write", "", "status/Operation", nil},
		{"held save", delivery.PhasePending, "status/Operation", func(e *env) { delete(e.reg.bundles, contractRef(c2)) }},
		{"lock CAS read", delivery.PhasePending, "status/Stage", nil},
		{"running CAS", delivery.PhaseRunning, "status/Stage", nil},
		{"running applied write", delivery.PhaseRunning, "status/Operation", nil},
		{"kustomization list", delivery.PhaseRunning, "list/Kustomization", func(e *env) {
			e.reconcileOp("p1")
			e.reconcileOp("p1")
			e.apply(dB)
		}},
		{"verifying write", delivery.PhaseRunning, "status/Operation", func(e *env) {
			e.reconcileOp("p1")
			e.reconcileOp("p1")
			e.apply(dB)
		}},
		{"rollback create", delivery.PhaseVerifying, "create/Operation", func(e *env) { e.evaluate(c2, "NonCompliant") }},
		{"conclude CAS", delivery.PhaseVerifying, "status/Stage", func(e *env) { e.evaluate(c2, "Compliant") }},
		{"conclude write", delivery.PhaseVerifying, "status/Operation", func(e *env) { e.evaluate(c2, "Compliant") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var e *env
			if c.phase == "" {
				e = baseEnv(t, opObj("p1", delivery.TypePromote, dA, dB, bot))
			} else {
				e = at(t, c.phase)
			}
			if c.prep != nil {
				c.prep(e)
			}
			e.fails[c.fail] = true
			if err := e.reconcileOpErr("p1"); err == nil {
				t.Fatalf("%s: the failure must surface", c.fail)
			}
		})
	}
}

func TestPlanAndApprovalReadFailures(t *testing.T) {
	// A cancelled context makes the snapshot build fail: plan errors, nothing moves.
	e := at(t, delivery.PhasePending)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.op.Reconcile(ctx, reconcile.Request{NamespacedName: keyOf("p1")}); err == nil {
		t.Fatal("a failed plan must surface")
	}
	// An unreadable OCIRepository is an unreadable contract: held for a person.
	e.fails["get/OCIRepository"] = true
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld || !strings.Contains(o.Status.Question, "the apply OCIRepository") {
		t.Fatalf("held: %s %q", o.Status.Phase, o.Status.Question)
	}
	delete(e.fails, "get/OCIRepository")
	if res := e.reconcileOp("p1"); res.RequeueAfter == 0 {
		t.Fatal("held with no approval waits")
	}
	e.fails["list/Approval"] = true
	e.approve(e.getOp("p1"), "ok", "alice", delivery.AcceptUnknown)
	if o := e.drive("p1"); o.Status.Phase != delivery.PhaseHeld {
		t.Fatal("an unlistable approval is no approval")
	}
	delete(e.fails, "list/Approval")
	if _, err := e.op.Reconcile(ctx, reconcile.Request{NamespacedName: keyOf("p1")}); err == nil {
		t.Fatal("a failed re-check plan must surface")
	}
}

func TestLockDirectly(t *testing.T) {
	e := at(t, delivery.PhasePending)
	op := e.getOp("p1")
	// A holder that no longer exists leaves a free lock.
	e.setStage(func(s *delivery.StageStatus) { s.Locks = map[string]string{"pointer": "ghost"} })
	e.fails["status/Operation"] = true
	if _, err := e.op.lock(context.Background(), op, e.getStage(), false); err == nil {
		t.Fatal("a failed write after the lock must surface")
	}
	delete(e.fails, "status/Operation")
	// The pointer moved between the plan and the lock.
	e.setStage(func(s *delivery.StageStatus) { s.Locks, s.Pointer = nil, dC })
	if _, err := e.op.lock(context.Background(), e.getOp("p1"), e.getStage(), false); err != nil {
		t.Fatal(err)
	}
	wantOutcome(t, e.getOp("p1"), delivery.Rejected, "PointerMoved")
	// A list failure never reads as "no holder": the lock stays taken and the
	// reconcile retries.
	e = at(t, delivery.PhasePending)
	e.setStage(func(s *delivery.StageStatus) { s.Locks = map[string]string{"pointer": "ghost"} })
	e.fails["list/Operation"] = true
	if _, err := e.op.lock(context.Background(), e.getOp("p1"), e.getStage(), false); err == nil {
		t.Fatal("an unreadable lock holder must surface, not free the lock")
	}
	if s := e.getStage(); s.Status.Locks["pointer"] != "ghost" {
		t.Fatalf("the lock moved on a failed read: %v", s.Status.Locks)
	}
	delete(e.fails, "list/Operation")
	e.op.APIReader = failingReader{}
	if _, err := e.op.lock(context.Background(), e.getOp("p1"), e.getStage(), false); err == nil {
		t.Fatal("a failed CAS read must surface")
	}
}

func TestFirstVersionFailureFreezes(t *testing.T) {
	e := newEnv(t, stageObj(""), ociRepo("payments", "", "", false), deployment("payments", 2, 2),
		pactoObj("payments", "payments", c1, "Compliant", 2))
	e.create(opObj("p1", delivery.TypePromote, "", dB, bot))
	e.drive("p1")
	e.drive("p1") // not fetched yet
	if o := e.getOp("p1"); !strings.Contains(o.Status.Message, "has not fetched") {
		t.Fatalf("message %q", o.Status.Message)
	}
	e.apply(dB)
	e.drive("p1")
	e.evaluate(c2, "NonCompliant")
	wantOutcome(t, e.drive("p1"), delivery.Failed, "VerifyFailed")
	if s := e.getStage(); !s.Status.Frozen || !strings.Contains(s.Status.FrozenReason, "nothing to roll back") {
		t.Fatalf("first version failed: %+v", s.Status)
	}
	if e.reg.hosts != nil {
		t.Fatalf("a secure OCIRepository asks for no insecure host: %v", e.reg.hosts)
	}
}

func TestCancelWhilePending(t *testing.T) {
	e := at(t, delivery.PhasePending)
	if err := e.c.Delete(context.Background(), e.getOp("p1")); err != nil {
		t.Fatal(err)
	}
	e.reconcileOp("p1")
	if e.getOp("p1").Status.Outcome != delivery.Cancelled {
		t.Fatal("want Cancelled")
	}
}

func TestStageUpdateStatusReadFailure(t *testing.T) {
	s := stageObj(dA)
	s.Status.Stream.ID = ""
	e := newEnv(t, s)
	e.st.APIReader = failingReader{}
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed read must surface")
	}
}

func TestWorkloadLookup(t *testing.T) {
	e := newEnv(t, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "j", UID: "u", Generation: 4}})
	look := e.op.Cluster.workload(context.Background(), ns)
	if w := look("Job", "j"); w == nil || w.Generation != 4 || w.ObservedGeneration != 4 || w.UID != "u" {
		t.Fatalf("job: %+v", w)
	}
	if look("Deployment", "missing") != nil {
		t.Fatal("missing workload")
	}
}

// TestSetupWithManager: the reconcilers register, watching the Flux and Argo
// CD kinds the cluster serves and skipping the ones it does not.
func TestSetupWithManager(t *testing.T) {
	for _, flux := range []bool{false, true} {
		mapper := meta.NewDefaultRESTMapper(nil)
		mapper.Add(dv1.GroupVersion.WithKind("Approval"), meta.RESTScopeNamespace)
		mapper.Add(dv1.GroupVersion.WithKind("Stage"), meta.RESTScopeNamespace)
		if flux {
			mapper.Add(gvkOCIRepository, meta.RESTScopeNamespace)
			mapper.Add(gvkKustomization.GroupVersion().WithKind("Kustomization"), meta.RESTScopeNamespace)
			mapper.Add(gvkApplication, meta.RESTScopeNamespace)
		}
		skip := true
		mgr, err := ctrlrt.NewManager(&rest.Config{Host: "https://127.0.0.1:1"}, ctrlrt.Options{Scheme: newScheme(),
			Metrics:        metricsserver.Options{BindAddress: "0"},
			Controller:     config.Controller{SkipNameValidation: &skip},
			MapperProvider: func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return mapper, nil }})
		if err != nil {
			t.Fatal(err)
		}
		if err := (&OperationReconciler{}).SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
		if err := (&Recorder{}).SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
		if err := (&StageReconciler{Cluster: Cluster{CacheApplications: true}}).SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSetupWithManagerIndexConflict: a second Stage index of the same name is
// refused, and the reconciler does not register.
func TestSetupWithManagerIndexConflict(t *testing.T) {
	skip := true
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(dv1.GroupVersion.WithKind("Stage"), meta.RESTScopeNamespace)
	mgr, err := ctrlrt.NewManager(&rest.Config{Host: "https://127.0.0.1:1"}, ctrlrt.Options{Scheme: newScheme(),
		Metrics: metricsserver.Options{BindAddress: "0"}, Controller: config.Controller{SkipNameValidation: &skip},
		MapperProvider: func(*rest.Config, *http.Client) (meta.RESTMapper, error) { return mapper, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.GetFieldIndexer().IndexField(t.Context(), &dv1.Stage{}, stageRefs, stageRefKeys); err != nil {
		t.Fatal(err)
	}
	if err := (&StageReconciler{}).SetupWithManager(mgr); err == nil {
		t.Fatal("a conflicting index must fail the setup")
	}
}

// TestStagesReading: an event on an object wakes every Stage that reads it,
// through the index of what each Stage reads, and a failed cache read wakes
// nothing.
func TestStagesReading(t *testing.T) {
	argo := stageObj(dA)
	argo.Name, argo.Spec.ApplyRef = "argo", delivery.ApplyRef{Kind: delivery.ApplierArgoCD, Name: "app"}
	argo.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "payments-test"}}
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithIndex(&dv1.Stage{}, stageRefs, stageRefKeys).
		WithObjects(stageObj(dA), argo).Build()
	r := &StageReconciler{Client: c}
	wake := func(keys func(client.Object) []string, obj client.Object) string {
		var names []string
		for _, req := range r.stagesReading(keys)(t.Context(), obj) {
			names = append(names, req.Name)
		}
		return strings.Join(names, ",")
	}
	u := func(name string, obj map[string]any) *unstructured.Unstructured {
		o := &unstructured.Unstructured{Object: obj}
		o.SetNamespace(ns)
		o.SetName(name)
		return o
	}
	for _, tc := range []struct {
		keys func(client.Object) []string
		obj  client.Object
		want string
	}{
		{ociRepositoryKeys, u("payments", map[string]any{}), "payments-prod"},
		{ociRepositoryKeys, u("payments-candidates", map[string]any{}), "payments-prod"},
		{kustomizationKeys, u("ks", map[string]any{"spec": map[string]any{"sourceRef": map[string]any{"kind": "OCIRepository", "name": "payments"}}}), "payments-prod"},
		{kustomizationKeys, u("ks", map[string]any{"spec": map[string]any{"sourceRef": map[string]any{"kind": "OCIRepository", "name": "payments", "namespace": "other"}}}), ""},
		{kustomizationKeys, u("ks", map[string]any{"spec": map[string]any{"sourceRef": map[string]any{"kind": "GitRepository", "name": "payments"}}}), ""},
		{applicationKeys, u("app", map[string]any{}), "argo"},
		{stageKeys, &dv1.Stage{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "payments-test"}}, "argo"},
	} {
		if got := wake(tc.keys, tc.obj); got != tc.want {
			t.Errorf("%s %v woke %q, want %q", tc.obj.GetName(), tc.keys(tc.obj), got, tc.want)
		}
	}
	r.Client = fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(stageObj(dA)).Build() // no index: the list fails
	if got := wake(stageKeys, argo); got != "" {
		t.Errorf("a failed cache read woke %q", got)
	}
}

// TestWitnessLiveReadFailures: a witnessed override is raised only on the
// apiserver's word, so a failed live read of the applier or the Stage
// surfaces instead.
func TestWitnessLiveReadFailures(t *testing.T) {
	e := newEnv(t, stageObj(dA), ociRepo("payments", dA, "latest@"+dA, false))
	e.staleCache(ociRepo("payments", dC, "latest@"+dC, false))
	e.fails["get/OCIRepository"] = true
	if err := e.st.witness(t.Context(), e.getStage()); err == nil {
		t.Fatal("a failed live read of the applier must surface")
	}
	e.fails["get/OCIRepository"] = false
	stage := e.getStage()
	e.st.APIReader = failingReader{}
	if err := e.st.witness(t.Context(), stage); err == nil {
		t.Fatal("a failed live read of the Stage must surface")
	}
}

// TestOperationListFailuresRetry: a failed Operation list is never read as
// "no operations". It surfaces, so the reconcile retries, and the operation is
// judged once the list reads again: a bound rollback is not refused as
// unbound, and a promote is not let past a newer one it cannot see.
func TestOperationListFailuresRetry(t *testing.T) {
	cause := &delivery.Verdict{Gate: "health", Result: delivery.Fail}
	for _, withCause := range []bool{false, true} {
		e := healthEnv(t, healthChecks, opObj("p1", delivery.TypePromote, dA, dB, bot))
		e.shipB()
		rb := opObj("rb", delivery.TypeRollback, dB, dA, bot)
		rb.Spec.Params.Undoes = "uid-p1"
		if withCause {
			rb.Spec.Cause = &delivery.Cause{Verdict: cause}
		}
		e.create(bind(rb, e.getStage()))
		e.fails["list/Operation"] = true
		if err := e.reconcileOpErr("rb"); err == nil {
			t.Fatalf("cause %v: a failed list must surface", withCause)
		}
		if o := e.getOp("rb"); o.Status.Phase != "" {
			t.Fatalf("cause %v: judged on a failed list: %s %s", withCause, o.Status.Outcome, o.Status.Reason)
		}
		delete(e.fails, "list/Operation")
		e.health.result = delivery.Fail
		if o := e.drive("rb"); o.Status.Reason == "UnboundRollback" {
			t.Fatalf("cause %v: a bound rollback was refused as unbound", withCause)
		}
	}
	e := at(t, delivery.PhasePending)
	e.fails["list/Operation"] = true
	if err := e.reconcileOpErr("p1"); err == nil {
		t.Fatal("a failed list in the supersede check must surface")
	}
	if o := e.getOp("p1"); o.Status.Phase != delivery.PhasePending {
		t.Fatalf("moved on a failed list: %s", o.Status.Phase)
	}
}
