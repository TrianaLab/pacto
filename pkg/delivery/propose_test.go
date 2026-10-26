package delivery

import (
	"slices"
	"strings"
	"testing"
)

func kinds(p *Proposal) []string {
	var out []string
	for _, b := range p.Blockers {
		out = append(out, b.Kind)
	}
	return out
}

func shadowSeen(d string, v Verification) Shadow {
	return Shadow{Candidate: d, ObservedAt: "2026-10-20T09:00:00Z", Verification: v}
}

func promoted(uid string, o Outcome, v Verification, cleared bool) Concluded {
	return Concluded{UID: uid, Type: TypePromote, Outcome: o, Verification: v, ClearedBreaking: cleared}
}

func impactPlan(completeness string, r Result) *Plan {
	return &Plan{Verdicts: []Verdict{{Gate: GateImpact, Result: r, Message: "m", Citation: map[string]string{"completeness": completeness}}}}
}

func TestBelow(t *testing.T) {
	if r, ok := RungAutonomous.Below(); !ok || r != RungAutoNonBreaking {
		t.Fatal(r)
	}
	if r, ok := RungAdvise.Below(); !ok || r != RungObserve {
		t.Fatal(r)
	}
	if _, ok := RungObserve.Below(); ok {
		t.Fatal("never below observe")
	}
	if _, ok := Rung("x").Below(); ok {
		t.Fatal("not a rung")
	}
}

func TestProposeRungObserve(t *testing.T) {
	spec := StageSpec{Autonomy: AutonomyPolicy{ProposeAfter: 2}}
	st := StageStatus{Rung: RungObserve, ObservedRevision: &ObservedRevision{Digest: dB}, Conditions: []Condition{readyGuarded()},
		Shadow: []Shadow{shadowSeen(dB, Verified), {Candidate: dC}, shadowSeen(dA, Verified), shadowSeen(c1, "")}}
	if p := ProposeRung(spec, st, nil); p.Rung != RungAdvise || !slices.Equal(p.Evidence, []string{dB, dA}) || len(p.Blockers) != 0 {
		t.Fatalf("two verified observed candidates earn advise; an unobserved one is skipped: %+v", p)
	}
	unverified := st
	unverified.Shadow = []Shadow{shadowSeen(dB, Verified), shadowSeen(dA, "")}
	if p := ProposeRung(spec, unverified, nil); p.Rung != "" || !slices.Equal(kinds(p), []string{BlockerNeedsVerified}) {
		t.Fatalf("an unverified entry blocks: %+v", p)
	}
	failed := st
	failed.FailedDigests = []string{dA}
	if p := ProposeRung(spec, failed, nil); p.Rung != "" || !slices.Equal(kinds(p), []string{BlockerIncident}) {
		t.Fatalf("a failed entry is an incident: %+v", p)
	}
	bare := StageStatus{Rung: RungObserve}
	if p := ProposeRung(StageSpec{}, bare, nil); !slices.Equal(kinds(p), []string{BlockerNeedsVerified, BlockerUnlabelledApplyRef, BlockerNoObservedDigest}) ||
		!strings.Contains(p.Blockers[0].Message, "0 of 10") {
		t.Fatalf("defaults to 10, and names every blocker: %+v", p)
	}
	if p := ProposeRung(StageSpec{}, StageStatus{Rung: RungAutonomous}, nil); p.Rung != "" || len(p.Blockers) != 0 {
		t.Fatalf("nothing above autonomous: %+v", p)
	}
}

func TestProposeRungPromotes(t *testing.T) {
	spec := StageSpec{Autonomy: AutonomyPolicy{ProposeAfter: 2}}
	ok := StageStatus{Rung: RungAdvise, Conditions: []Condition{recordStoreReady()}, Stream: Stream{Concluded: []Concluded{
		promoted("old", Failed, "", false), // before the rung change: forgotten
		{UID: "rung", Type: TypeAutonomy, Outcome: Succeeded},
		promoted("p1", Succeeded, Verified, false),
		{UID: "f", Type: TypeFreeze, Outcome: Succeeded, Person: true}, // a person's freeze is no incident
		{UID: "rj", Type: TypeAutonomy, Outcome: Rejected},              // a refused rung change restarts nothing
		promoted("p2", Succeeded, Verified, true),
		promoted("p3", Succeeded, Verified, false),
	}}}
	if p := ProposeRung(spec, ok, nil); p.Rung != RungAutoNonBreaking || !slices.Equal(p.Evidence, []string{"p3", "p2"}) {
		t.Fatalf("advise earns auto-nonbreaking from the newest verified promotes: %+v", p)
	}
	auto := ok
	auto.Rung = RungAutoNonBreaking
	if p := ProposeRung(spec, auto, impactPlan("complete", Pass)); p.Rung != RungAutonomous {
		t.Fatalf("a cleared breaking change on a complete graph earns autonomous: %+v", p)
	}
	auto.Stream.Concluded[5].ClearedBreaking = false
	for _, c := range []struct {
		plan *Plan
		want []string
	}{
		{nil, []string{BlockerNeedsClearedBreaking, BlockerPartialGraph}},
		{&Plan{}, []string{BlockerNeedsClearedBreaking, BlockerPartialGraph}},
		{impactPlan("partial", Unknown), []string{BlockerNeedsClearedBreaking, BlockerPartialGraph}},
		{impactPlan("complete", Unknown), []string{BlockerNeedsClearedBreaking, BlockerUndeclaredRange}},
	} {
		if p := ProposeRung(spec, auto, c.plan); p.Rung != "" || !slices.Equal(kinds(p), c.want) {
			t.Fatalf("%+v: %+v", c.plan, p)
		}
	}
	bad := ok
	bad.Frozen, bad.FrozenReason, bad.Conditions = true, "x", nil
	bad.Stream.Concluded = append(slices.Clone(ok.Stream.Concluded),
		Concluded{UID: "rb", Type: TypeRollback, Outcome: Succeeded}, promoted("p4", Succeeded, Unverified, false))
	if p := ProposeRung(spec, bad, nil); p.Rung != "" ||
		!slices.Equal(kinds(p), []string{BlockerNeedsVerified, BlockerIncident, BlockerIncident, BlockerNoRecordStore}) {
		t.Fatalf("an automatic rollback, an unverified promote, a freeze and no recorder: %+v", p)
	}
	few := StageStatus{Rung: RungAdvise, Conditions: []Condition{recordStoreReady()}, Stream: Stream{Concluded: []Concluded{promoted("p1", Succeeded, Verified, false)}}}
	if p := ProposeRung(spec, few, nil); p.Rung != "" || !strings.Contains(p.Blockers[0].Message, "1 of 2") {
		t.Fatalf("too few promotes: %+v", p)
	}
	many := few
	for range MaxBlockers + 3 {
		many.Stream.Concluded = append(many.Stream.Concluded, promoted("x", Failed, "", false))
	}
	if p := ProposeRung(spec, many, nil); len(p.Blockers) != MaxBlockers {
		t.Fatalf("blockers are capped: %d", len(p.Blockers))
	}
}

func TestRungChangeResetsTheWindows(t *testing.T) {
	s := StageStatus{Rung: RungAdvise, Shadow: []Shadow{{Candidate: dA}}, Proposal: &Proposal{Rung: RungAutoNonBreaking}}
	changeRung(&s, OperationSpec{Params: Params{From: string(RungAdvise), To: string(RungAutoNonBreaking)}})
	if s.Proposal != nil || len(s.Shadow) != 1 {
		t.Fatalf("a raise drops the proposal and keeps the shadow: %+v", s)
	}
	changeRung(&s, OperationSpec{Params: Params{From: string(RungAdvise), To: string(RungObserve)}})
	if s.Shadow != nil || s.Rung != RungObserve {
		t.Fatalf("observing again restarts the shadow window: %+v", s)
	}
}

func TestRecordOutcomeRung(t *testing.T) {
	in := RecordInput{Operation: OperationObject{Spec: OperationSpec{Type: TypeAutonomy, Params: Params{From: "advise", To: "observe"}},
		Status: OperationStatus{Outcome: Succeeded}}}
	if got := NewOperationRecord(in).Predicate.Outcome.Rung; got != (RungChange{From: RungAdvise, To: RungObserve}) {
		t.Fatalf("rung change: %+v", got)
	}
	in.Operation.Status.Outcome = Rejected
	if got := NewOperationRecord(in).Predicate.Outcome.Rung; got != (RungChange{}) {
		t.Fatalf("a refused change moved nothing: %+v", got)
	}
}
