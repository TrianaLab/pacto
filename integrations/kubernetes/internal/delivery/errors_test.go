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

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrlrt "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
		{"held save", delivery.PhasePending, "status/Operation", func(e *env) { delete(e.reg.contracts, dB) }},
		{"lock CAS read", delivery.PhasePending, "status/Stage", nil},
		{"running CAS", delivery.PhaseRunning, "status/Stage", nil},
		{"running applied write", delivery.PhaseRunning, "status/Operation", nil},
		{"digest patch", delivery.PhaseRunning, "patch/OCIRepository", nil},
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
	// A list failure reads as "no holder".
	e = at(t, delivery.PhasePending)
	e.setStage(func(s *delivery.StageStatus) { s.Locks = map[string]string{"pointer": "ghost"} })
	e.fails["list/Operation"] = true
	if _, err := e.op.lock(context.Background(), e.getOp("p1"), e.getStage(), false); err != nil {
		t.Fatal(err)
	}
	e.op.APIReader = failingReader{}
	if _, err := e.op.lock(context.Background(), e.getOp("p1"), e.getStage(), false); err == nil {
		t.Fatal("a failed CAS read must surface")
	}
}

func TestStillPasses(t *testing.T) {
	pass := delivery.Verdict{Gate: "g", Result: delivery.Pass}
	unknown := delivery.Verdict{Gate: "u", Result: delivery.Unknown}
	prev := delivery.Plan{Verdicts: []delivery.Verdict{pass, unknown}, Floor: delivery.Floor{Human: true, Decision: delivery.AcceptUnknown}}
	if !stillPasses(prev, delivery.Plan{Verdicts: []delivery.Verdict{pass}}) {
		t.Fatal("same passes, no new floor")
	}
	if stillPasses(prev, delivery.Plan{Verdicts: []delivery.Verdict{{Gate: "g", Result: delivery.Unknown}}}) {
		t.Fatal("a pass that came back Unknown")
	}
	if stillPasses(prev, delivery.Plan{Verdicts: []delivery.Verdict{pass}, Floor: delivery.Floor{Human: true, Decision: delivery.Approve}}) {
		t.Fatal("an approval-needing floor accept-unknown never answered")
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

func TestSetupWithManager(t *testing.T) {
	mgr, err := ctrlrt.NewManager(&rest.Config{Host: "https://127.0.0.1:1"}, ctrlrt.Options{Scheme: newScheme(),
		Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&OperationReconciler{}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	if err := (&StageReconciler{}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
}
