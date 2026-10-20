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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func (e *env) reconcileStage(name string) error {
	_, err := e.st.Reconcile(context.Background(), reconcile.Request{NamespacedName: keyOf(name)})
	return err
}

func (e *env) ops() []dv1.Operation {
	e.t.Helper()
	list := &dv1.OperationList{}
	if err := e.c.List(context.Background(), list); err != nil {
		e.t.Fatal(err)
	}
	return list.Items
}

// TestAutoPromoteFromOCIRepository: the newest candidate is promoted by the
// controller, once, under a deterministic name.
func TestAutoPromoteFromOCIRepository(t *testing.T) {
	s := stageObj(dA)
	s.Status.Stream.ID = ""
	s.Spec.Auto = true
	s.Spec.Candidates.OCIRepositoryRef = &delivery.LocalRef{Name: "payments-candidates"}
	s.Status.Published = nil
	e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false), verifiedRepo("payments-candidates", "latest@"+dB))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if got := e.getStage().Status.Stream.ID; got != "cid/prod/payments-prod" {
		t.Fatalf("stream id %q", got)
	}
	for range 2 {
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatal(err)
		}
	}
	ops := e.ops()
	if len(ops) != 1 || ops[0].Spec.Type != delivery.TypePromote || ops[0].Spec.Params.From != dA || ops[0].Spec.Params.To != dB ||
		ops[0].Spec.Actor.User != promoterSA || !strings.HasPrefix(ops[0].Name, "payments-prod-auto-") ||
		ops[0].Spec.IntentDigest != delivery.IntentDigest(ops[0].Spec, s.Spec) || ops[0].Labels[StageLabel] != "payments-prod" {
		t.Fatalf("want one controller promote A->B, got %+v", ops)
	}
	if p := e.getStage().Status.Published; len(p) != 1 || p[0] != dB {
		t.Fatalf("B is remembered as published: %v", p)
	}
	// An operation in flight blocks the next automatic one.
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 1 {
		t.Fatalf("in flight: %v %d", err, len(e.ops()))
	}
}

// TestAutoPromoteFromUpstreamAndHolds: the newest verified upstream digest is the
// candidate; frozen, auto-held or non-auto Stages raise nothing.
func TestAutoPromoteFromUpstreamAndHolds(t *testing.T) {
	up := stageObj(dB)
	up.Name = "payments-test"
	up.Status.Verified = []string{dA, dB}
	s := stageObj(dA)
	s.Spec.Auto = true
	s.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "payments-test", Namespace: ns}}
	e := newEnv(t, up, s, ociRepo("payments", dA, "latest@"+dA, false))
	for _, mut := range []func(*delivery.StageStatus){
		func(st *delivery.StageStatus) { st.Frozen = true },
		func(st *delivery.StageStatus) { st.Frozen, st.AutoHold = false, true },
	} {
		e.setStage(mut)
		if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 0 {
			t.Fatalf("a frozen or held Stage raises nothing: %v %d", err, len(e.ops()))
		}
	}
	e.setStage(func(st *delivery.StageStatus) { st.AutoHold = false })
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 1 || e.ops()[0].Spec.Params.To != dB {
		t.Fatalf("upstream candidate: %v %+v", err, e.ops())
	}
}

// TestAutoPromoteSourcesMissing: a missing candidate source or upstream Stage is
// not an error, just nothing to promote.
func TestAutoPromoteSourcesMissing(t *testing.T) {
	for _, c := range []delivery.Candidates{
		{OCIRepositoryRef: &delivery.LocalRef{Name: "gone"}},
		{UpstreamStage: &delivery.StageRef{Name: "gone"}},
		{},
	} {
		s := stageObj(dA)
		s.Spec.Auto, s.Spec.Candidates = true, c
		e := newEnv(t, s, ociRepo("payments", dA, "", false))
		if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 0 {
			t.Fatalf("%+v: %v %d", c, err, len(e.ops()))
		}
	}
}

// TestWitnessedOverride: an edit of spec.ref.digest the promoter did not make
// is recorded as a witnessed override, which adopts the edit and freezes.
func TestWitnessedOverride(t *testing.T) {
	e := newEnv(t, stageObj(dA), ociRepo("payments", dC, "latest@"+dC, false),
		kustomization("payments", "payments", "True", "", "latest@"+dC, "latest@"+dC))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	ops := e.ops()
	if len(ops) != 1 || ops[0].Spec.Type != delivery.TypeOverride || !ops[0].Spec.Witnessed || ops[0].Spec.Params.To != dC {
		t.Fatalf("want one witnessed override to C: %+v", ops)
	}
	o := e.drive(ops[0].Name)
	wantOutcome(t, o, delivery.Witnessed, "Overridden")
	if s := e.getStage(); s.Status.Pointer != dC || !s.Status.Frozen {
		t.Fatalf("witnessed override adopts and freezes: %+v", s.Status)
	}
	// No drift once adopted; nothing is witnessed while a lock is held, for an
	// empty Stage, for a non-digest value or for a missing OCIRepository.
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 1 {
		t.Fatalf("adopted: %v %d", err, len(e.ops()))
	}
	for _, st := range []*dv1.Stage{
		func() *dv1.Stage { s := stageObj(dA); s.Status.Locks = map[string]string{"pointer": "x"}; return s }(),
		stageObj(""),
	} {
		e := newEnv(t, st, ociRepo("payments", dC, "", false))
		if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 0 {
			t.Fatalf("no witness: %v %d", err, len(e.ops()))
		}
	}
	e = newEnv(t, stageObj(dA), ociRepo("payments", "", "", false))
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 0 {
		t.Fatal("an unpinned OCIRepository is not drift")
	}
	e = newEnv(t, stageObj(dA))
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 0 {
		t.Fatal("a missing OCIRepository is not drift")
	}
}

func TestStageReconcileErrors(t *testing.T) {
	e := newEnv(t)
	if err := e.reconcileStage("missing"); err != nil {
		t.Fatal("a missing Stage is not an error")
	}
	s := stageObj(dA)
	s.Status.Stream.ID = ""
	e = newEnv(t, s, ociRepo("payments", dC, "", false))
	e.fails["get/Stage"] = true
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed Stage read must surface")
	}
	e.fails = fails{"status/Stage": true}
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed stream id write must surface")
	}
	e.fails = fails{"create/Operation": true}
	if e.reconcileStage("payments-prod") == nil {
		e.fails = fails{}
		_ = e.reconcileStage("payments-prod")
		e.fails = fails{"create/Operation": true}
		if e.reconcileStage("payments-prod") == nil {
			t.Fatal("a failed witness create must surface")
		}
	}
	e.fails = fails{"get/OCIRepository": true}
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed OCIRepository read must surface")
	}
	up := stageObj(dB)
	up.Name = "up"
	up.Status.Verified = []string{dB}
	s = stageObj(dA)
	s.Spec.Auto, s.Spec.Candidates = true, delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "up"}}
	e = newEnv(t, s, up)
	e.fails["create/Operation"] = true
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed promote create must surface")
	}
	e.fails = fails{"list/Operation": true}
	err := e.reconcileStage("payments-prod")
	e.fails = fails{}
	if err == nil || len(e.ops()) != 0 {
		t.Fatalf("a failed list is an error and promotes nothing: %v", err)
	}
}

func TestSetupHelpers(t *testing.T) {
	if got := approvalToOperation(context.Background(), &dv1.Approval{ObjectMeta: metav1.ObjectMeta{Namespace: ns}, Spec: delivery.ApprovalSpec{Operation: "o"}}); got[0].Name != "o" {
		t.Fatal(got)
	}
	if got := operationToStage(context.Background(), &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: ns}, Spec: delivery.OperationSpec{Stage: "s"}}); got[0].Name != "s" {
		t.Fatal(got)
	}
	if ServiceAccountUser("a", "b") != "system:serviceaccount:a:b" || strings.Join(SplitList(" a, ,b "), "|") != "a|b" {
		t.Fatal("helpers")
	}
}

// TestNeverACandidateUnlessPublished: a candidate OCIRepository without
// spec.verify, or whose newest revision Flux could not verify (an artifact
// pushed by something other than pacto publish), offers nothing.
func TestNeverACandidateUnlessPublished(t *testing.T) {
	unsigned := verifiedRepo("payments-candidates", "latest@"+dB)
	_ = unstructured.SetNestedSlice(unsigned.Object, []any{map[string]any{"type": "SourceVerified", "status": "False",
		"message": "failed to verify the signature using provider 'cosign': no signatures found"}}, "status", "conditions")
	noVerify := ociRepo("payments-candidates", "", "latest@"+dB, false)
	_ = unstructured.SetNestedSlice(noVerify.Object, []any{map[string]any{"type": "SourceVerified", "status": "True",
		"message": "verified signature of revision latest@" + dB}}, "status", "conditions")
	for name, repo := range map[string]*unstructured.Unstructured{"unsigned": unsigned, "no spec.verify": noVerify} {
		s := stageObj(dA)
		s.Spec.Auto, s.Status.Published = true, nil
		e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false), repo)
		for range 3 {
			if err := e.reconcileStage("payments-prod"); err != nil {
				t.Fatal(err)
			}
		}
		if len(e.ops()) != 0 || len(e.getStage().Status.Published) != 0 {
			t.Fatalf("%s: offered %+v, published %v", name, e.ops(), e.getStage().Status.Published)
		}
	}
	// A failed read or write while remembering a published digest surfaces.
	s := stageObj(dA)
	s.Status.Published = nil
	e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false), verifiedRepo("payments-candidates", "latest@"+dB))
	e.fails["status/Stage"] = true
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed write of the published memory must surface")
	}
	e.fails = fails{"get/OCIRepository": true}
	if e.st.recordPublished(context.Background(), e.getStage()) == nil {
		t.Fatal("a failed read of the candidate OCIRepository must surface")
	}
}
