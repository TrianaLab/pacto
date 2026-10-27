package delivery

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// breakingInput is a breaking change with no consumer on a Stage that
// declares its graph complete: an autonomous Stage ships it with no person.
func breakingInput(t *testing.T, level Level, class ActorClass) PlanInput {
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
	return PlanInput{Spec: promote(dA, dB), Stage: st, Status: StageStatus{Level: level, Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		From: from, To: to, Snapshot: snap, Class: class}
}

func TestCurrentLevel(t *testing.T) {
	for _, c := range []struct {
		spec StageSpec
		st   StageStatus
		want Level
	}{
		{StageSpec{}, StageStatus{}, LevelObserve},
		{StageSpec{StartLevel: LevelAdvise}, StageStatus{}, LevelAdvise},
		{StageSpec{StartLevel: LevelAutonomous}, StageStatus{}, LevelAutonomous}, // an admin chose it
		{StageSpec{StartLevel: "yolo"}, StageStatus{}, LevelObserve},
		{StageSpec{StartLevel: LevelAdvise}, StageStatus{Level: LevelAutonomous}, LevelAutonomous},
	} {
		if got := CurrentLevel(c.spec, c.st); got != c.want {
			t.Errorf("CurrentLevel(%q, %q) = %q, want %q", c.spec.StartLevel, c.st.Level, got, c.want)
		}
	}
	if LevelAdvise.Automatic() || !LevelAutoNonBreaking.Automatic() || Level("x").Automatic() {
		t.Fatal("only auto-nonbreaking and autonomous are automatic")
	}
}

func TestLevelGatePromote(t *testing.T) {
	ctx := context.Background()
	// observe rejects every promote, whoever raises it, but leaves a rollback alone.
	for _, class := range []ActorClass{ClassHuman, ClassAgent, ClassController} {
		in := breakingInput(t, LevelObserve, class)
		if p := PlanFor(ctx, in); p.Predicted != PredictRejected || !strings.HasPrefix(p.Conflicts[0], "Observing: ") {
			t.Fatalf("%s promote on observe: %+v", class, p)
		}
	}
	rb := breakingInput(t, LevelObserve, ClassHuman)
	rb.Spec.Type, rb.Spec.Params = TypeRollback, Params{From: dA, To: dB}
	if p := PlanFor(ctx, rb); p.Predicted != PredictProceeds {
		t.Fatalf("a rollback on observe is left alone: %+v", p)
	}
	// The shadow input (level autonomous) gets the real verdicts.
	if p := PlanFor(ctx, breakingInput(t, LevelAutonomous, ClassController)); p.Predicted != PredictProceeds || p.Floor.Human {
		t.Fatalf("autonomous ships a breaking change the graph clears: %+v", p)
	}
	// advise floors promotes no person raised, not a person's.
	for _, class := range []ActorClass{ClassAgent, ClassController} {
		p := PlanFor(ctx, breakingInput(t, LevelAdvise, class))
		if p.Predicted != PredictHeld || p.Floor.Decision != Approve || !strings.Contains(p.Question, "advises") ||
			strings.Join(p.Floor.Owners, ",") != "team-payments" {
			t.Fatalf("%s promote on advise: %+v", class, p)
		}
	}
	if p := PlanFor(ctx, breakingInput(t, LevelAdvise, ClassHuman)); p.Predicted != PredictProceeds {
		t.Fatalf("a person's promote on advise: %+v", p)
	}
}

func TestLevelGatePromoteAutoNonBreaking(t *testing.T) {
	ctx := context.Background()
	// auto-nonbreaking floors a breaking-but-compatible change and an unclassified one.
	p := PlanFor(ctx, breakingInput(t, LevelAutoNonBreaking, ClassController))
	if p.Predicted != PredictHeld || !strings.Contains(p.Question, "only non-breaking") || !strings.Contains(p.Question, "BREAKING") {
		t.Fatalf("breaking on auto-nonbreaking: %+v", p)
	}
	nb := breakingInput(t, LevelAutoNonBreaking, ClassController)
	nb.To = rev(dB, c2, bundle("payments", "1.0.1"))
	nb.From = rev(dA, c1, bundle("payments", "1.0.0"))
	if p := PlanFor(ctx, nb); p.Predicted != PredictProceeds {
		t.Fatalf("non-breaking on auto-nonbreaking: %+v", p)
	}
	none := &Plan{Verdicts: []Verdict{{Gate: GateDiff, Result: Pass}}}
	levelPromote(PlanInput{Status: StageStatus{Level: LevelAutoNonBreaking}, Class: ClassAgent}, none)
	if !none.Floor.Human || !strings.Contains(none.Question, "not classified") {
		t.Fatalf("an unclassified change on auto-nonbreaking: %+v", none)
	}
	// An accept-unknown floor already set is never replaced.
	held := &Plan{Floor: Floor{Human: true, Decision: AcceptUnknown}, Question: "q"}
	levelPromote(PlanInput{Status: StageStatus{Level: LevelAdvise}, Class: ClassAgent}, held)
	if held.Floor.Decision != AcceptUnknown || held.Question != "q" {
		t.Fatalf("the floor was replaced: %+v", held)
	}
}

func TestClearedBreaking(t *testing.T) {
	p := PlanFor(context.Background(), breakingInput(t, LevelAutonomous, ClassController))
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

// autonomy plans a level change from on a Stage with st, raised by class.
func autonomy(from, to Level, st StageStatus, class ActorClass, running Revision) Plan {
	st.Level = from
	return PlanFor(context.Background(), PlanInput{
		Spec:  OperationSpec{Type: TypeAutonomy, Stage: "payments-prod", Facet: FacetLevel, Params: Params{From: string(from), To: string(to)}},
		Stage: stageSpec(), Status: st, Class: class, From: running, RequireOwnerTeam: true,
	})
}

// autonomyFixture is a Stage on advise that has earned auto-nonbreaking,
// the contract running on it and a check that a plan is refused.
func autonomyFixture(t *testing.T) (running Revision, ready StageStatus, fails func(string, Plan, string)) {
	t.Helper()
	running = rev(dA, c1, bundle("payments", "1.0.0"))
	observed := &ObservedRevision{Digest: dA, Since: "2026-10-20T10:00:00Z"}
	ready = StageStatus{Pointer: dA, Conditions: []Condition{readyGuarded(), recordStoreReady()}, ObservedRevision: observed,
		Proposal: &Proposal{Level: LevelAutoNonBreaking, Evidence: []string{"p1"}}}
	for i := range DefaultProposeAfter {
		ready.Stream.Concluded = append(ready.Stream.Concluded, promoted(fmt.Sprint("p", i), Succeeded, Verified, false))
	}
	fails = func(name string, p Plan, want string) {
		t.Helper()
		v := verdict(t, p, GateAutonomy)
		if p.Predicted != PredictRejected || v.Result != Fail || !strings.Contains(v.Message, want) {
			t.Fatalf("%s: want a refusal naming %q, got %+v", name, want, p)
		}
	}
	return running, ready, fails
}

func TestAutonomyGateLowers(t *testing.T) {
	_, _, fails := autonomyFixture(t)
	// Lowering needs no approval. The promoter only steps down one level
	// from an automatic level.
	for _, class := range []ActorClass{ClassHuman, ClassAgent} {
		if p := autonomy(LevelAutonomous, LevelAdvise, StageStatus{}, class, Revision{}); p.Predicted != PredictProceeds || p.Floor.Human {
			t.Fatalf("%s lowering: %+v", class, p)
		}
	}
	for _, from := range []Level{LevelAutonomous, LevelAutoNonBreaking} {
		to, _ := from.Below()
		if p := autonomy(from, to, StageStatus{}, ClassController, Revision{}); p.Predicted != PredictProceeds || p.Floor.Human {
			t.Fatalf("the promoter's step-down from %s: %+v", from, p)
		}
	}
	fails("the promoter lowering two levels", autonomy(LevelAutonomous, LevelAdvise, StageStatus{}, ClassController, Revision{}), "one level from an automatic level")
	fails("the promoter lowering to observe", autonomy(LevelAdvise, LevelObserve, StageStatus{}, ClassController, Revision{}), "one level from an automatic level")
	fails("an agent lowering to observe", autonomy(LevelAdvise, LevelObserve, StageStatus{}, ClassAgent, Revision{}), "may not lower a Stage to observe")
}

func TestAutonomyGateRefuses(t *testing.T) {
	running, ready, fails := autonomyFixture(t)
	fails("not a level", autonomy(LevelAdvise, "yolo", ready, ClassHuman, running), "is not a level")
	fails("no change", autonomy(LevelAdvise, LevelAdvise, ready, ClassHuman, running), "already on")
	fails("two levels", autonomy(LevelAdvise, LevelAutonomous, ready, ClassHuman, running), "one level at a time: propose auto-nonbreaking first")
	fails("the promoter raising", autonomy(LevelAdvise, LevelAutoNonBreaking, ready, ClassController, running), "never raises")
	unlabelled := ready
	unlabelled.Conditions = []Condition{{Type: "Ready", Status: "True", Reason: ReasonObserving}}
	fails("unlabelled", autonomy(LevelObserve, LevelAdvise, unlabelled, ClassHuman, running), "unlabelled-apply-ref")
	noDigest := ready
	noDigest.ObservedRevision = &ObservedRevision{Digest: "v1.2.3"}
	fails("no observed digest", autonomy(LevelObserve, LevelAdvise, noDigest, ClassHuman, running), "no-observed-digest")
	noDigest.ObservedRevision = nil
	fails("no observed revision", autonomy(LevelObserve, LevelAdvise, noDigest, ClassHuman, running), "no-observed-digest")
	noStore := ready
	noStore.Conditions = []Condition{readyGuarded(), {Type: ConditionRecordStoreReady, Status: "False"}}
	fails("no record store", autonomy(LevelAdvise, LevelAutoNonBreaking, noStore, ClassHuman, running), "no-record-store")
	if p := autonomy(LevelObserve, LevelAdvise, noStore, ClassHuman, running); p.Predicted != PredictHeld {
		t.Fatalf("advise needs no recorder: %+v", p)
	}
	// Stale From is rejected like PointerMoved.
	staleFrom := PlanFor(context.Background(), PlanInput{Spec: OperationSpec{Type: TypeAutonomy, Facet: FacetLevel, Params: Params{From: "observe", To: "advise"}},
		Status: StageStatus{Level: LevelAdvise}, Class: ClassHuman})
	if staleFrom.Predicted != PredictRejected || !strings.HasPrefix(staleFrom.Conflicts[0], "LevelMoved: ") {
		t.Fatalf("stale from: %+v", staleFrom)
	}
}

func TestAutonomyGateRaise(t *testing.T) {
	running, ready, _ := autonomyFixture(t)
	// A raise always asks a person from the running service's team.
	for _, class := range []ActorClass{ClassHuman, ClassAgent, ClassBreakGlass} {
		p := autonomy(LevelAdvise, LevelAutoNonBreaking, ready, class, running)
		if v := verdict(t, p, GateAutonomy); p.Predicted != PredictHeld || v.Result != Pass || p.Floor.Decision != Approve ||
			strings.Join(p.Floor.Owners, ",") != "team-payments" || !strings.Contains(p.Question, "Raise payments-prod from advise to auto-nonbreaking?") {
			t.Fatalf("%s raise: %+v", class, p)
		}
	}
	// A stale proposal for another level, or one the status no longer earns,
	// never passes.
	stale := ready
	stale.Proposal = &Proposal{Level: LevelAutonomous}
	if v := verdict(t, autonomy(LevelAdvise, LevelAutoNonBreaking, stale, ClassHuman, running), GateAutonomy); v.Result != Unknown {
		t.Fatalf("a proposal made for another level: %+v", v)
	}
	lapsed := ready
	lapsed.Stream.Concluded = append(slices.Clone(ready.Stream.Concluded), Concluded{UID: "rb", Type: TypeRollback, Outcome: Succeeded})
	if v := verdict(t, autonomy(LevelAdvise, LevelAutoNonBreaking, lapsed, ClassHuman, running), GateAutonomy); v.Result != Unknown {
		t.Fatalf("an incident after the proposal: %+v", v)
	}
}

func TestAutonomyGateUnknown(t *testing.T) {
	running, ready, _ := autonomyFixture(t)
	// Above the proposal is Unknown, citing the blockers.
	low := ready
	low.Proposal = &Proposal{Level: LevelAdvise, Blockers: []Blocker{{Kind: "needs-verified", Message: "3 of 10 verified"}}}
	p := autonomy(LevelAdvise, LevelAutoNonBreaking, low, ClassHuman, running)
	if v := verdict(t, p, GateAutonomy); v.Result != Unknown || p.Floor.Decision != AcceptUnknown || !strings.Contains(v.Message, "needs-verified: 3 of 10 verified") {
		t.Fatalf("a raise above the proposal: %+v", p)
	}
	low.Proposal = nil
	if v := verdict(t, autonomy(LevelAdvise, LevelAutoNonBreaking, low, ClassHuman, running), GateAutonomy); v.Result != Unknown {
		t.Fatalf("no proposal at all: %+v", v)
	}
	// An unreadable running contract is Unknown, never an open floor.
	p = autonomy(LevelAdvise, LevelAutoNonBreaking, ready, ClassHuman, Revision{Digest: dA, Err: "pull failed"})
	if v := verdict(t, p, GateAutonomy); v.Result != Unknown || !p.Floor.Human || p.Floor.Decision != AcceptUnknown ||
		!strings.Contains(v.Message, "pull failed") || !strings.Contains(p.Question, "nobody") {
		t.Fatalf("unreadable running contract: %+v", p)
	}
	if v := verdict(t, autonomy(LevelAdvise, LevelAutoNonBreaking, ready, ClassHuman, Revision{}), GateAutonomy); !strings.Contains(v.Message, "nothing is known to run") {
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
	spec := OperationSpec{Type: TypeAutonomy, Facet: FacetLevel, Params: Params{From: "observe", To: "advise"}}
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

func TestLevelRefusalAndChange(t *testing.T) {
	up := OperationSpec{Type: TypeAutonomy, Facet: FacetLevel, Params: Params{From: "observe", To: "advise"}}
	st := StageStatus{Level: LevelObserve, ObservedRevision: &ObservedRevision{Digest: dA}}
	if why := LevelRefusal(up, StageSpec{}, st); why != "" {
		t.Fatal(why)
	}
	if why := LevelRefusal(up, StageSpec{}, StageStatus{Level: LevelAdvise}); !strings.HasPrefix(why, "LevelMoved: ") {
		t.Fatal(why)
	}
	if why := LevelRefusal(up, StageSpec{}, StageStatus{}); !strings.HasPrefix(why, "NoObservedDigest: ") {
		t.Fatalf("an unseeded observe Stage: %q", why)
	}
	busy := st
	busy.Locks = map[string]string{FacetPointer: "u1"}
	if why := LevelRefusal(up, StageSpec{}, busy); !strings.HasPrefix(why, "PointerBusy: ") {
		t.Fatal(why)
	}
	ApplyEffects(&st, "raise", "u2", up, OperationStatus{}, Conclusion{Outcome: Succeeded})
	if st.Level != LevelAdvise || st.Pointer != dA {
		t.Fatalf("leaving observe adopts the pointer: %+v", st)
	}
	st.ObservedRevision.Digest = dB
	down := OperationSpec{Type: TypeAutonomy, Facet: FacetLevel, Params: Params{From: "advise", To: "observe"}}
	ApplyEffects(&st, "lower", "u3", down, OperationStatus{}, Conclusion{Outcome: Succeeded})
	if st.Level != LevelObserve || st.Pointer != "" {
		t.Fatalf("lowering to observe hands the pointer back to GitOps: %+v", st)
	}
}

// TestEarnedOnlyTheNextLevel: a stored proposal earns only the level right
// above the Stage's current one, never a jump.
func TestEarnedOnlyTheNextLevel(t *testing.T) {
	if earned(PlanInput{}, LevelAutonomous) {
		t.Fatal("a jump past the next level was earned")
	}
}

// TestSuiteChangeNeedsAPersonOnEveryLevel: a version that edits, renames or
// drops a suite it ships is held for a person on every level that promotes,
// whoever raised it, even when every consumer accepts the change.
func TestSuiteChangeNeedsAPersonOnEveryLevel(t *testing.T) {
	ctx := context.Background()
	suite := func(b *contract.Bundle, name, body string) *contract.Bundle {
		b.Contract.Checks = []contract.Check{{Name: name, File: "evals.yaml"}}
		b.FS = fstest.MapFS{"evals.yaml": {Data: []byte(body)}}
		return b
	}
	for name, to := range map[string]*contract.Bundle{
		"edited":  suite(bundle("payments", "1.0.1"), "evals", "cases: 2"),
		"renamed": suite(bundle("payments", "1.0.1"), "quality", "cases: 10"),
		"dropped": bundle("payments", "1.0.1"),
		"added": func() *contract.Bundle {
			b := suite(bundle("payments", "1.0.1"), "evals", "cases: 10")
			b.Contract.Checks = append(b.Contract.Checks, contract.Check{Name: "extra", File: "evals.yaml"})
			return b
		}(),
	} {
		for _, level := range []Level{LevelAdvise, LevelAutoNonBreaking, LevelAutonomous} {
			for _, class := range []ActorClass{ClassHuman, ClassAgent, ClassController} {
				in := breakingInput(t, level, class)
				in.From = rev(dA, c1, suite(bundle("payments", "1.0.0"), "evals", "cases: 10"))
				in.To = rev(dB, c2, to)
				p := PlanFor(ctx, in)
				v := verdict(t, p, GateSuites)
				if p.Predicted != PredictHeld || !p.Floor.Human || v.Result != Unknown ||
					!strings.Contains(v.Message, "changes the checks it is verified by") {
					t.Errorf("%s suite on %s by %s: %+v", name, level, class, p)
				}
			}
		}
	}
	// The same suite, byte for byte, holds nothing.
	in := breakingInput(t, LevelAutonomous, ClassController)
	in.From = rev(dA, c1, suite(bundle("payments", "1.0.0"), "evals", "cases: 10"))
	in.To = rev(dB, c2, suite(bundle("payments", "1.0.1"), "evals", "cases: 10"))
	if p := PlanFor(ctx, in); p.Predicted != PredictProceeds {
		t.Fatalf("an unchanged suite: %+v", p)
	}
	// A floor already set keeps its decision and leads with the suite change.
	held := &Plan{To: dB, Floor: Floor{Human: true, Decision: Approve}, Question: "Approve?",
		Verdicts: []Verdict{{Gate: GateDiff, Result: Pass, Citation: map[string]string{"checks": "checks.file modified"}}}}
	suitesGate(PlanInput{To: rev(dB, c2, bundle("payments", "1.0.1"))}, held)
	if held.Floor.Decision != Approve || !strings.HasPrefix(held.Question, short(dB)+" changes the checks") || !strings.HasSuffix(held.Question, "Approve?") {
		t.Fatalf("a floor already set: %+v", held)
	}
}
