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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// TestCandidateShownOnAManualStage: the stage reconciler writes the newest
// eligible candidate to status.candidate on a Stage without auto, which is
// what the CLI and the dashboard offer to promote, and promotes nothing.
func TestCandidateShownOnAManualStage(t *testing.T) {
	up := stageObj(dB)
	up.Name = "up"
	up.Status.Verified, up.Status.Offered = []string{dB}, []string{dB}
	s := stageObj(dA)
	s.Spec.Candidates = delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "up"}}
	e := newEnv(t, s, up)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if got := e.getStage().Status.Candidate; got != dB || len(e.ops()) != 0 {
		t.Fatalf("want candidate B and no operation, got %q %d", got, len(e.ops()))
	}
	e.fails["get/Stage/up"] = true
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed upstream read must surface")
	}
	e.fails = fails{}
	u := &dv1.Stage{}
	if err := e.c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "up"}, u); err != nil {
		t.Fatal(err)
	}
	u.Status.Verified, u.Status.Offered = []string{dB, dC}, []string{dB, dC}
	if err := e.c.Status().Update(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	e.fails = fails{"status/Stage": true}
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed candidate write must surface")
	}
}

// TestAutoPromoteAgainAfterAConclusion: the same move is proposed again once
// an earlier automatic promote of it concluded without shipping (here it
// expired), because the stream seq the name carries moved on.
func TestAutoPromoteAgainAfterAConclusion(t *testing.T) {
	s := stageObj(dA)
	s.Spec.Auto = true
	s.Spec.Approval.Always = true
	e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false), verifiedRepo("payments-candidates", "latest@"+dB),
		kustomization("payments", "payments", "True", "ReconciliationSucceeded", "latest@"+dA, "latest@"+dA),
		deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2))
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 1 {
		t.Fatalf("want one automatic promote: %v %d", err, len(e.ops()))
	}
	first := e.ops()[0].Name
	e.drive(first)
	e.now = e.now.Add(25 * time.Hour)
	wantOutcome(t, e.drive(first), delivery.Expired, "Expired")
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	var open []string
	for _, o := range e.ops() {
		if o.Status.Phase != delivery.PhaseConcluded {
			open = append(open, o.Name)
		}
	}
	if len(open) != 1 || open[0] == first {
		t.Fatalf("B is still eligible, so it is proposed again: open=%v", open)
	}
}

// TestRaisedNameSquatted: an operation someone else created under the name
// the promoter would raise its automatic promote or witnessed override with
// freezes the Stage instead of silently skipping the decision.
func TestRaisedNameSquatted(t *testing.T) {
	s := stageObj(dA)
	s.Spec.Auto = true
	s.Spec.Candidates.OCIRepositoryRef = &delivery.LocalRef{Name: "payments-candidates"}
	e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false), verifiedRepo("payments-candidates", "latest@"+dB))
	e.squat(raisedName(s, "auto", delivery.Params{From: dA, To: dB}))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if st := e.getStage(); !st.Status.Frozen || !strings.Contains(st.Status.FrozenReason, "is taken by a "+delivery.TypePromote+" "+bot) {
		t.Fatalf("want frozen: %+v", st.Status)
	}
}

// TestAutoPromoteFromUpstreamAndHolds: the newest verified upstream digest is the
// candidate; frozen, auto-held or non-auto Stages raise nothing.
func TestAutoPromoteFromUpstreamAndHolds(t *testing.T) {
	up := stageObj(dB)
	up.Name = "payments-test"
	up.Status.Verified, up.Status.Offered = []string{dA, dB, dC}, []string{dA, dB}
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
	up.Status.Verified, up.Status.Offered = []string{dB}, []string{dB}
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
	// spec.verify with no key and no OIDC identity accepts anyone's keyless
	// signature, so it proves nothing about the publisher.
	anyone := verifiedRepo("payments-candidates", "latest@"+dB)
	unstructured.RemoveNestedField(anyone.Object, "spec", "verify", "secretRef")
	for name, repo := range map[string]*unstructured.Unstructured{"unsigned": unsigned, "no spec.verify": noVerify, "no identity": anyone} {
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
	keyless := map[string]any{"spec": map[string]any{"verify": map[string]any{"provider": "cosign",
		"matchOIDCIdentity": []any{map[string]any{"issuer": "^https://token.actions.githubusercontent.com$", "subject": "^https://github.com/acme/"}}}}}
	if !pinsIdentity(keyless) {
		t.Fatal("a keyless spec.verify with matchOIDCIdentity names the publisher")
	}
	// A failed read or write while remembering a published digest surfaces.
	s := stageObj(dA)
	s.Status.Published = nil
	s.Status.Conditions = []delivery.Condition{{Type: "Ready", Status: "True", Reason: "Ready", Message: "the Stage can promote published versions"}}
	s.Status.AuthFreshness = "15m0s"
	e := newEnv(t, s, ociRepo("payments", dA, "latest@"+dA, false), verifiedRepo("payments-candidates", "latest@"+dB))
	a, _ := e.st.Cluster.applier(context.Background(), ns, e.getStage().Spec.ApplyRef)
	med, _ := e.st.Cluster.mediated(context.Background(), ns, a)
	e.setStage(func(s *delivery.StageStatus) { s.Conditions = append(s.Conditions, med) })
	e.fails["status/Stage"] = true
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed write of the published memory must surface")
	}
}

// TestStageReady: a Stage that can never promote is not Ready, and Ready names
// what is missing (design M2). The condition is written only when it changes.
func TestStageReady(t *testing.T) {
	noVerify := ociRepo("payments-candidates", "", "latest@"+dB, false)
	cases := []struct {
		name   string
		cands  delivery.Candidates
		objs   []client.Object
		status string
		reason string
	}{
		{"verified", delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "payments-candidates"}}, []client.Object{verifiedRepo("payments-candidates", "latest@"+dB)}, "True", "Ready"},
		{"no spec.verify", delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "payments-candidates"}}, []client.Object{noVerify}, "False", "CandidateUnverified"},
		{"missing", delivery.Candidates{OCIRepositoryRef: &delivery.LocalRef{Name: "payments-candidates"}}, nil, "False", "CandidateNotFound"},
		{"upstream", delivery.Candidates{UpstreamStage: &delivery.StageRef{Name: "payments-test"}}, nil, "True", "Ready"},
		{"none", delivery.Candidates{}, nil, "False", "NoCandidateSource"},
	}
	for _, c := range cases {
		s := stageObj(dA)
		s.Spec.Candidates = c.cands
		e := newEnv(t, append(c.objs, s)...)
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		conds := e.getStage().Status.Conditions
		if len(conds) != 1 || conds[0].Type != "Ready" || conds[0].Status != c.status || conds[0].Reason != c.reason || conds[0].Message == "" {
			t.Fatalf("%s: conditions %+v", c.name, conds)
		}
		// Unchanged, it is not written again.
		e.fails["status/Stage"] = true
		if err := e.reconcileStage("payments-prod"); err != nil {
			t.Fatalf("%s: an unchanged Ready was written again: %v", c.name, err)
		}
	}
	// A failed read of the candidate OCIRepository or a failed write surfaces.
	e := newEnv(t, stageObj(dA))
	e.fails["status/Stage"] = true
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed write of Ready must surface")
	}
	e.fails = fails{"get/OCIRepository": true}
	if _, err := e.st.ready(context.Background(), e.getStage()); err == nil {
		t.Fatal("a failed read of the candidate OCIRepository must surface")
	}
	// Ready follows the OCIRepository: dropping spec.verify flips it to False.
	repo := verifiedRepo("payments-candidates", "latest@"+dB)
	e = newEnv(t, stageObj(dA), repo)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if err := e.c.Get(context.Background(), client.ObjectKeyFromObject(repo), repo); err != nil {
		t.Fatal(err)
	}
	unstructured.RemoveNestedField(repo.Object, "spec", "verify")
	if err := e.c.Update(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if c := e.getStage().Status.Conditions; len(c) != 1 || c[0].Reason != "CandidateUnverified" {
		t.Fatalf("Ready did not follow the OCIRepository: %+v", c)
	}
}

// TestCreateRaised: raising the promoter's own operation again is a no-op,
// and a read that fails after the name was found taken is an error, retried.
func TestCreateRaised(t *testing.T) {
	e := newEnv(t, stageObj(dA))
	op := opObj("raised", delivery.TypePromote, dA, dB, promoterSA)
	for range 2 {
		if squat, err := createRaised(context.Background(), e.c, op.DeepCopy()); err != nil || squat != "" {
			t.Fatalf("raising twice: %q %v", squat, err)
		}
	}
	e.fails["get/Operation/raised"] = true
	if _, err := createRaised(context.Background(), e.c, op.DeepCopy()); err == nil {
		t.Fatal("a failed read of the taken name must be an error")
	}
}

// TestApplyRefUnlabelled: a Stage whose apply object lacks the apply-ref
// label is not Ready, naming the label, since admission neither guards its
// pointer nor lets the promoter write it.
func TestApplyRefUnlabelled(t *testing.T) {
	apply := ociRepo("payments", "", "", false)
	apply.SetLabels(nil)
	e := newEnv(t, stageObj(""), apply, verifiedRepo("payments-candidates", "latest@"+dB))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	c := conditionOf(e.getStage().Status.Conditions, "Ready")
	if c.Status != statusFalse || c.Reason != "ApplyRefUnlabelled" || !strings.Contains(c.Message, ApplyRefLabel) {
		t.Fatalf("want Ready=False ApplyRefUnlabelled, got %+v", c)
	}
	e.fails["get/OCIRepository/payments"] = true
	if e.reconcileStage("payments-prod") == nil {
		t.Fatal("a failed apply object read must surface")
	}
}

// TestReadyObservesTheGeneration: Ready and the Stage status name the
// generation they were judged from, and the transition time moves only when
// Ready's status does, so kstatus and Argo CD can tell a stale status.
func TestReadyObservesTheGeneration(t *testing.T) {
	st := stageObj("")
	st.Generation = 3
	e := newEnv(t, st, ociRepo("payments", "", "", false), verifiedRepo("payments-candidates", "latest@"+dB))
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	s := e.getStage()
	c := *conditionOf(s.Status.Conditions, "Ready")
	if s.Generation != 3 || s.Status.ObservedGeneration != 3 || c.ObservedGeneration != 3 || c.LastTransitionTime != ts(e.now) || c.Status != "True" ||
		s.Status.AuthFreshness != "15m0s" {
		t.Fatalf("status %d, condition %+v, generation %d", s.Status.ObservedGeneration, c, s.Generation)
	}
	// A spec change: the apiserver bumps the generation (the fake client
	// keeps what it is given). Ready is judged again; its status did not
	// change, so neither does its transition time.
	s.Generation = 4
	if err := e.c.Update(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	created := c.LastTransitionTime
	e.now = e.now.Add(time.Minute)
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	s = e.getStage()
	c = *conditionOf(s.Status.Conditions, "Ready")
	if s.Status.ObservedGeneration != 4 || c.ObservedGeneration != 4 || c.LastTransitionTime != created {
		t.Fatalf("after the spec change: status %d, condition %+v", s.Status.ObservedGeneration, c)
	}
	// The promoter's own login freshness is what the Stage publishes.
	e.st.Freshness = 6 * time.Minute
	if err := e.reconcileStage("payments-prod"); err != nil || e.getStage().Status.AuthFreshness != "6m0s" {
		t.Fatalf("published freshness: %v %q", err, e.getStage().Status.AuthFreshness)
	}
	// Ready turning False is a transition.
	if err := e.c.Delete(context.Background(), verifiedRepo("payments-candidates", "")); err != nil {
		t.Fatal(err)
	}
	if err := e.reconcileStage("payments-prod"); err != nil {
		t.Fatal(err)
	}
	if c = *conditionOf(e.getStage().Status.Conditions, "Ready"); c.Status != statusFalse || c.LastTransitionTime != ts(e.now) {
		t.Fatalf("a transition: %+v", c)
	}
}
