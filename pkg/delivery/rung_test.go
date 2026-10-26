package delivery

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// breakingInput is a breaking change with no consumer on a Stage that
// declares its graph complete: an autonomous Stage ships it with no person.
func breakingInput(t *testing.T, rung Rung, class ActorClass) PlanInput {
	t.Helper()
	old, breaking := bundle("payments", "1.0.0"), bundle("payments", "2.0.0")
	old.Contract.Workload, breaking.Contract.Workload = "service", "job"
	from, to := rev(dA, c1, old), rev(dB, c2, breaking)
	snap, err := Snapshot(context.Background(), nil, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	st := stageSpec()
	st.Gates.Impact.DeclaredConsumersOnly = true
	return PlanInput{Spec: promote(dA, dB), Stage: st, Status: StageStatus{Rung: rung, Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		From: from, To: to, Snapshot: snap, Class: class}
}

func TestCurrentRung(t *testing.T) {
	for _, c := range []struct {
		spec StageSpec
		st   StageStatus
		want Rung
	}{
		{StageSpec{}, StageStatus{}, RungObserve},
		{StageSpec{StartRung: RungAdvise}, StageStatus{}, RungAdvise},
		{StageSpec{StartRung: RungAutonomous}, StageStatus{}, RungObserve}, // never seeds automatic
		{StageSpec{StartRung: RungAdvise}, StageStatus{Rung: RungAutonomous}, RungAutonomous},
	} {
		if got := CurrentRung(c.spec, c.st); got != c.want {
			t.Errorf("CurrentRung(%q, %q) = %q, want %q", c.spec.StartRung, c.st.Rung, got, c.want)
		}
	}
	if RungAdvise.Automatic() || !RungAutoNonBreaking.Automatic() || Rung("x").Automatic() {
		t.Fatal("only auto-nonbreaking and autonomous are automatic")
	}
}

func TestRungGatePromote(t *testing.T) {
	ctx := context.Background()
	// observe rejects every promote, whoever raises it, but leaves a rollback alone.
	for _, class := range []ActorClass{ClassHuman, ClassAgent, ClassController} {
		in := breakingInput(t, RungObserve, class)
		if p := PlanFor(ctx, in); p.Predicted != PredictRejected || !strings.HasPrefix(p.Conflicts[0], "Observing: ") {
			t.Fatalf("%s promote on observe: %+v", class, p)
		}
	}
	rb := breakingInput(t, RungObserve, ClassHuman)
	rb.Spec.Type, rb.Spec.Params = TypeRollback, Params{From: dA, To: dB}
	if p := PlanFor(ctx, rb); p.Predicted != PredictProceeds {
		t.Fatalf("a rollback on observe is left alone: %+v", p)
	}
	// The shadow input (rung autonomous) gets the real verdicts.
	if p := PlanFor(ctx, breakingInput(t, RungAutonomous, ClassController)); p.Predicted != PredictProceeds || p.Floor.Human {
		t.Fatalf("autonomous ships a breaking change the graph clears: %+v", p)
	}
	// advise floors promotes no person raised, not a person's.
	for _, class := range []ActorClass{ClassAgent, ClassController} {
		p := PlanFor(ctx, breakingInput(t, RungAdvise, class))
		if p.Predicted != PredictHeld || p.Floor.Decision != Approve || !strings.Contains(p.Question, "advises") ||
			strings.Join(p.Floor.Owners, ",") != "team-payments" {
			t.Fatalf("%s promote on advise: %+v", class, p)
		}
	}
	if p := PlanFor(ctx, breakingInput(t, RungAdvise, ClassHuman)); p.Predicted != PredictProceeds {
		t.Fatalf("a person's promote on advise: %+v", p)
	}
	// auto-nonbreaking floors a breaking-but-compatible change and an unclassified one.
	p := PlanFor(ctx, breakingInput(t, RungAutoNonBreaking, ClassController))
	if p.Predicted != PredictHeld || !strings.Contains(p.Question, "only non-breaking") || !strings.Contains(p.Question, "BREAKING") {
		t.Fatalf("breaking on auto-nonbreaking: %+v", p)
	}
	nb := breakingInput(t, RungAutoNonBreaking, ClassController)
	nb.To = rev(dB, c2, bundle("payments", "1.0.1"))
	nb.From = rev(dA, c1, bundle("payments", "1.0.0"))
	if p := PlanFor(ctx, nb); p.Predicted != PredictProceeds {
		t.Fatalf("non-breaking on auto-nonbreaking: %+v", p)
	}
	none := &Plan{Verdicts: []Verdict{{Gate: GateDiff, Result: Pass}}}
	rungPromote(PlanInput{Status: StageStatus{Rung: RungAutoNonBreaking}, Class: ClassAgent}, none)
	if !none.Floor.Human || !strings.Contains(none.Question, "not classified") {
		t.Fatalf("an unclassified change on auto-nonbreaking: %+v", none)
	}
	// An accept-unknown floor already set is never replaced.
	held := &Plan{Floor: Floor{Human: true, Decision: AcceptUnknown}, Question: "q"}
	rungPromote(PlanInput{Status: StageStatus{Rung: RungAdvise}, Class: ClassAgent}, held)
	if held.Floor.Decision != AcceptUnknown || held.Question != "q" {
		t.Fatalf("the floor was replaced: %+v", held)
	}
}

func TestClearedBreaking(t *testing.T) {
	p := PlanFor(context.Background(), breakingInput(t, RungAutonomous, ClassController))
	if !ClearedBreaking(&p) {
		t.Fatalf("a breaking change whose impact passed is cleared: %+v", p)
	}
	if ClearedBreaking(nil) || ClearedBreaking(&Plan{Type: TypePromote}) || ClearedBreaking(&Plan{Type: TypeRollback, Verdicts: p.Verdicts}) {
		t.Fatal("no plan, no impact verdict or no promote clears nothing")
	}
	nb := Plan{Type: TypePromote, Verdicts: []Verdict{{Gate: GateImpact, Result: Pass, Citation: map[string]string{"classification": "NON_BREAKING"}}}}
	if ClearedBreaking(&nb) {
		t.Fatal("a non-breaking change is not a cleared breaking one")
	}
}

func readyGuarded() Condition {
	return Condition{Type: "Ready", Status: "True", Reason: ReasonObservingGuarded}
}

func recordStoreReady() Condition { return Condition{Type: ConditionRecordStoreReady, Status: "True"} }

// autonomy plans a rung change from on a Stage with st, raised by class.
func autonomy(from, to Rung, st StageStatus, class ActorClass, running Revision) Plan {
	st.Rung = from
	return PlanFor(context.Background(), PlanInput{
		Spec:  OperationSpec{Type: TypeAutonomy, Stage: "payments-prod", Facet: FacetRung, Params: Params{From: string(from), To: string(to)}},
		Stage: stageSpec(), Status: st, Class: class, From: running, RequireOwnerTeam: true,
	})
}

func TestAutonomyGate(t *testing.T) {
	running := rev(dA, c1, bundle("payments", "1.0.0"))
	observed := &ObservedRevision{Digest: dA, Since: "2026-10-20T10:00:00Z"}
	ready := StageStatus{Pointer: dA, Conditions: []Condition{readyGuarded(), recordStoreReady()}, ObservedRevision: observed,
		Proposal: &Proposal{Rung: RungAutoNonBreaking, Evidence: []string{"p1"}}}
	for i := range DefaultProposeAfter {
		ready.Stream.Concluded = append(ready.Stream.Concluded, promoted(fmt.Sprint("p", i), Succeeded, Verified, false))
	}
	fails := func(name string, p Plan, want string) {
		t.Helper()
		v := verdict(t, p, GateAutonomy)
		if p.Predicted != PredictRejected || v.Result != Fail || !strings.Contains(v.Message, want) {
			t.Fatalf("%s: want a refusal naming %q, got %+v", name, want, p)
		}
	}
	// Lowering needs no approval. The promoter only steps down one rung
	// from an automatic rung.
	for _, class := range []ActorClass{ClassHuman, ClassAgent} {
		if p := autonomy(RungAutonomous, RungAdvise, StageStatus{}, class, Revision{}); p.Predicted != PredictProceeds || p.Floor.Human {
			t.Fatalf("%s lowering: %+v", class, p)
		}
	}
	for _, from := range []Rung{RungAutonomous, RungAutoNonBreaking} {
		to, _ := from.Below()
		if p := autonomy(from, to, StageStatus{}, ClassController, Revision{}); p.Predicted != PredictProceeds || p.Floor.Human {
			t.Fatalf("the promoter's step-down from %s: %+v", from, p)
		}
	}
	fails("the promoter lowering two rungs", autonomy(RungAutonomous, RungAdvise, StageStatus{}, ClassController, Revision{}), "one rung from an automatic rung")
	fails("the promoter lowering to observe", autonomy(RungAdvise, RungObserve, StageStatus{}, ClassController, Revision{}), "one rung from an automatic rung")
	fails("an agent lowering to observe", autonomy(RungAdvise, RungObserve, StageStatus{}, ClassAgent, Revision{}), "may not lower a Stage to observe")
	fails("not a rung", autonomy(RungAdvise, "yolo", ready, ClassHuman, running), "is not a rung")
	fails("no change", autonomy(RungAdvise, RungAdvise, ready, ClassHuman, running), "already on")
	fails("two rungs", autonomy(RungAdvise, RungAutonomous, ready, ClassHuman, running), "one rung at a time: propose auto-nonbreaking first")
	fails("the promoter raising", autonomy(RungAdvise, RungAutoNonBreaking, ready, ClassController, running), "never raises")
	unlabelled := ready
	unlabelled.Conditions = []Condition{{Type: "Ready", Status: "True", Reason: ReasonObserving}}
	fails("unlabelled", autonomy(RungObserve, RungAdvise, unlabelled, ClassHuman, running), "unlabelled-apply-ref")
	noDigest := ready
	noDigest.ObservedRevision = &ObservedRevision{Digest: "v1.2.3"}
	fails("no observed digest", autonomy(RungObserve, RungAdvise, noDigest, ClassHuman, running), "no-observed-digest")
	noDigest.ObservedRevision = nil
	fails("no observed revision", autonomy(RungObserve, RungAdvise, noDigest, ClassHuman, running), "no-observed-digest")
	noStore := ready
	noStore.Conditions = []Condition{readyGuarded(), {Type: ConditionRecordStoreReady, Status: "False"}}
	fails("no record store", autonomy(RungAdvise, RungAutoNonBreaking, noStore, ClassHuman, running), "no-record-store")
	if p := autonomy(RungObserve, RungAdvise, noStore, ClassHuman, running); p.Predicted != PredictHeld {
		t.Fatalf("advise needs no recorder: %+v", p)
	}
	// Stale From is rejected like PointerMoved.
	staleFrom := PlanFor(context.Background(), PlanInput{Spec: OperationSpec{Type: TypeAutonomy, Facet: FacetRung, Params: Params{From: "observe", To: "advise"}},
		Status: StageStatus{Rung: RungAdvise}, Class: ClassHuman})
	if staleFrom.Predicted != PredictRejected || !strings.HasPrefix(staleFrom.Conflicts[0], "RungMoved: ") {
		t.Fatalf("stale from: %+v", staleFrom)
	}
	// A raise always asks a person from the running service's team.
	for _, class := range []ActorClass{ClassHuman, ClassAgent, ClassBreakGlass} {
		p := autonomy(RungAdvise, RungAutoNonBreaking, ready, class, running)
		if v := verdict(t, p, GateAutonomy); p.Predicted != PredictHeld || v.Result != Pass || p.Floor.Decision != Approve ||
			strings.Join(p.Floor.Owners, ",") != "team-payments" || !strings.Contains(p.Question, "Raise payments-prod from advise to auto-nonbreaking?") {
			t.Fatalf("%s raise: %+v", class, p)
		}
	}
	// A stale proposal for another rung, or one the status no longer earns,
	// never passes.
	stale := ready
	stale.Proposal = &Proposal{Rung: RungAutonomous}
	if v := verdict(t, autonomy(RungAdvise, RungAutoNonBreaking, stale, ClassHuman, running), GateAutonomy); v.Result != Unknown {
		t.Fatalf("a proposal made for another rung: %+v", v)
	}
	lapsed := ready
	lapsed.Stream.Concluded = append(slices.Clone(ready.Stream.Concluded), Concluded{UID: "rb", Type: TypeRollback, Outcome: Succeeded})
	if v := verdict(t, autonomy(RungAdvise, RungAutoNonBreaking, lapsed, ClassHuman, running), GateAutonomy); v.Result != Unknown {
		t.Fatalf("an incident after the proposal: %+v", v)
	}
	// Above the proposal is Unknown, citing the blockers.
	low := ready
	low.Proposal = &Proposal{Rung: RungAdvise, Blockers: []Blocker{{Kind: "needs-verified", Message: "3 of 10 verified"}}}
	p := autonomy(RungAdvise, RungAutoNonBreaking, low, ClassHuman, running)
	if v := verdict(t, p, GateAutonomy); v.Result != Unknown || p.Floor.Decision != AcceptUnknown || !strings.Contains(v.Message, "needs-verified: 3 of 10 verified") {
		t.Fatalf("a raise above the proposal: %+v", p)
	}
	low.Proposal = nil
	if v := verdict(t, autonomy(RungAdvise, RungAutoNonBreaking, low, ClassHuman, running), GateAutonomy); v.Result != Unknown {
		t.Fatalf("no proposal at all: %+v", v)
	}
	// An unreadable running contract is Unknown, never an open floor.
	p = autonomy(RungAdvise, RungAutoNonBreaking, ready, ClassHuman, Revision{Digest: dA, Err: "pull failed"})
	if v := verdict(t, p, GateAutonomy); v.Result != Unknown || !p.Floor.Human || p.Floor.Decision != AcceptUnknown ||
		!strings.Contains(v.Message, "pull failed") || !strings.Contains(p.Question, "nobody") {
		t.Fatalf("unreadable running contract: %+v", p)
	}
	if v := verdict(t, autonomy(RungAdvise, RungAutoNonBreaking, ready, ClassHuman, Revision{}), GateAutonomy); !strings.Contains(v.Message, "nothing is known to run") {
		t.Fatalf("nothing running: %+v", v)
	}
}

// echoSources resolves each digest to itself.
type echoSources struct{ fakeSources }

func (echoSources) Revisions(_ context.Context, _ StageObject, from, to string) (Revision, Revision) {
	return Revision{Digest: from}, Revision{Digest: to}
}

func TestAssemblePlanInputAutonomy(t *testing.T) {
	src := &echoSources{}
	stage := StageObject{Spec: stageSpec(), Status: StageStatus{ObservedRevision: &ObservedRevision{Digest: dA}}}
	spec := OperationSpec{Type: TypeAutonomy, Facet: FacetRung, Params: Params{From: "observe", To: "advise"}}
	in, err := AssemblePlanInput(context.Background(), src, spec, stage, ClassHuman, now)
	if err != nil || in.From.Digest != dA || in.To.Digest != "" {
		t.Fatalf("observe reads what the source reports: %+v %v", in.From, err)
	}
	stage.Status.Pointer = dB
	if in, _ = AssemblePlanInput(context.Background(), src, spec, stage, ClassHuman, now); in.From.Digest != dB {
		t.Fatalf("the pointer is what runs: %+v", in.From)
	}
	spec.Params = Params{From: "advise", To: "observe"}
	if in, _ = AssemblePlanInput(context.Background(), src, spec, stage, ClassHuman, now); in.From.Digest != "" {
		t.Fatalf("lowering reads nothing: %+v", in.From)
	}
}

func TestRungRefusalAndChange(t *testing.T) {
	up := OperationSpec{Type: TypeAutonomy, Facet: FacetRung, Params: Params{From: "observe", To: "advise"}}
	st := StageStatus{Rung: RungObserve, ObservedRevision: &ObservedRevision{Digest: dA}}
	if why := RungRefusal(up, StageSpec{}, st); why != "" {
		t.Fatal(why)
	}
	if why := RungRefusal(up, StageSpec{}, StageStatus{Rung: RungAdvise}); !strings.HasPrefix(why, "RungMoved: ") {
		t.Fatal(why)
	}
	if why := RungRefusal(up, StageSpec{}, StageStatus{}); !strings.HasPrefix(why, "NoObservedDigest: ") {
		t.Fatalf("an unseeded observe Stage: %q", why)
	}
	busy := st
	busy.Locks = map[string]string{FacetPointer: "u1"}
	if why := RungRefusal(up, StageSpec{}, busy); !strings.HasPrefix(why, "PointerBusy: ") {
		t.Fatal(why)
	}
	ApplyEffects(&st, "raise", "u2", up, OperationStatus{}, Conclusion{Outcome: Succeeded})
	if st.Rung != RungAdvise || st.Pointer != dA {
		t.Fatalf("leaving observe adopts the pointer: %+v", st)
	}
	st.ObservedRevision.Digest = dB
	down := OperationSpec{Type: TypeAutonomy, Facet: FacetRung, Params: Params{From: "advise", To: "observe"}}
	ApplyEffects(&st, "lower", "u3", down, OperationStatus{}, Conclusion{Outcome: Succeeded})
	if st.Rung != RungObserve || st.Pointer != "" {
		t.Fatalf("lowering to observe hands the pointer back to GitOps: %+v", st)
	}
}
