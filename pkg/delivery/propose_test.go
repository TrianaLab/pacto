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

// uncontractedPlan is a complete, passing impact verdict on a change that
// names runtime callers with no contract under key.
func uncontractedPlan(key string) *Plan {
	p := impactPlan("complete", Pass)
	p.Verdicts[0].Citation[key] = "1"
	return p
}

func TestBelow(t *testing.T) {
	if r, ok := LevelAutonomous.Below(); !ok || r != LevelAutoNonBreaking {
		t.Fatal(r)
	}
	if r, ok := LevelAdvise.Below(); !ok || r != LevelObserve {
		t.Fatal(r)
	}
	if _, ok := LevelObserve.Below(); ok {
		t.Fatal("never below observe")
	}
	if _, ok := Level("x").Below(); ok {
		t.Fatal("not a level")
	}
}

func TestProposeLevelObserve(t *testing.T) {
	spec := StageSpec{Autonomy: AutonomyPolicy{ProposeAfter: 2}}
	st := StageStatus{Level: LevelObserve, ObservedRevision: &ObservedRevision{Digest: dB}, Conditions: []Condition{readyGuarded()},
		Shadow: []Shadow{shadowSeen(dB, Verified), {Candidate: dC}, shadowSeen(dA, Verified), shadowSeen(c1, "")}}
	if p := ProposeLevel(spec, st, nil); p.Level != LevelAdvise || !slices.Equal(p.Evidence, []string{dB, dA}) || len(p.Blockers) != 0 {
		t.Fatalf("two verified observed candidates earn advise; an unobserved one is skipped: %+v", p)
	}
	unverified := st
	unverified.Shadow = []Shadow{shadowSeen(dB, Verified), shadowSeen(dA, "")}
	if p := ProposeLevel(spec, unverified, nil); p.Level != "" || !slices.Equal(kinds(p), []string{BlockerNeedsVerified}) {
		t.Fatalf("an unverified entry blocks: %+v", p)
	}
	failed := st
	failed.FailedDigests = []string{dA}
	if p := ProposeLevel(spec, failed, nil); p.Level != "" || !slices.Equal(kinds(p), []string{BlockerIncident}) {
		t.Fatalf("a failed entry is an incident: %+v", p)
	}
	bare := StageStatus{Level: LevelObserve}
	if p := ProposeLevel(StageSpec{}, bare, nil); !slices.Equal(kinds(p), []string{BlockerNeedsVerified, BlockerUnlabelledApplyRef, BlockerNoObservedDigest}) ||
		!strings.Contains(p.Blockers[0].Message, "0 of 10") {
		t.Fatalf("defaults to 10, and names every blocker: %+v", p)
	}
	if p := ProposeLevel(StageSpec{}, StageStatus{Level: LevelAutonomous}, nil); p.Level != "" || len(p.Blockers) != 0 {
		t.Fatalf("nothing above autonomous: %+v", p)
	}
}

func TestProposeLevelPromotes(t *testing.T) {
	spec := StageSpec{Autonomy: AutonomyPolicy{ProposeAfter: 2}}
	ok := StageStatus{Level: LevelAdvise, Conditions: []Condition{recordStoreReady()}, Stream: Stream{Concluded: []Concluded{
		promoted("old", Failed, "", false), // before the level change: forgotten
		{UID: "level", Type: TypeAutonomy, Outcome: Succeeded},
		promoted("p1", Succeeded, Verified, false),
		{UID: "f", Type: TypeFreeze, Outcome: Succeeded, Person: true}, // a person's freeze is no incident
		{UID: "rj", Type: TypeAutonomy, Outcome: Rejected},             // a refused level change restarts nothing
		promoted("p2", Succeeded, Verified, true),
		promoted("p3", Succeeded, Verified, false),
	}}}
	if p := ProposeLevel(spec, ok, nil); p.Level != LevelAutoNonBreaking || !slices.Equal(p.Evidence, []string{"p3", "p2"}) {
		t.Fatalf("advise earns auto-nonbreaking from the newest verified promotes: %+v", p)
	}
	auto := ok
	auto.Level = LevelAutoNonBreaking
	if p := ProposeLevel(spec, auto, impactPlan("complete", Pass)); p.Level != LevelAutonomous {
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
		{uncontractedPlan("uncontracted"), []string{BlockerNeedsClearedBreaking, BlockerMissingContract}},
		{uncontractedPlan("callersUnseen"), []string{BlockerNeedsClearedBreaking, BlockerMissingContract}},
	} {
		if p := ProposeLevel(spec, auto, c.plan); p.Level != "" || !slices.Equal(kinds(p), c.want) {
			t.Fatalf("%+v: %+v", c.plan, p)
		}
	}
	bad := ok
	bad.Frozen, bad.FrozenReason, bad.Conditions = true, "x", nil
	bad.Stream.Concluded = append(slices.Clone(ok.Stream.Concluded),
		Concluded{UID: "rb", Type: TypeRollback, Outcome: Succeeded}, promoted("p4", Succeeded, Unverified, false))
	if p := ProposeLevel(spec, bad, nil); p.Level != "" ||
		!slices.Equal(kinds(p), []string{BlockerNeedsVerified, BlockerIncident, BlockerIncident, BlockerNoRecordStore}) {
		t.Fatalf("an automatic rollback, an unverified promote, a freeze and no recorder: %+v", p)
	}
	few := StageStatus{Level: LevelAdvise, Conditions: []Condition{recordStoreReady()}, Stream: Stream{Concluded: []Concluded{promoted("p1", Succeeded, Verified, false)}}}
	if p := ProposeLevel(spec, few, nil); p.Level != "" || !strings.Contains(p.Blockers[0].Message, "1 of 2") {
		t.Fatalf("too few promotes: %+v", p)
	}
	many := few
	for range MaxBlockers + 3 {
		many.Stream.Concluded = append(many.Stream.Concluded, promoted("x", Failed, "", false))
	}
	if p := ProposeLevel(spec, many, nil); len(p.Blockers) != MaxBlockers {
		t.Fatalf("blockers are capped: %d", len(p.Blockers))
	}
}

// TestStepDownRestartsAtZero: the rollback and failed promote of the failure
// a step-down cites conclude after it and are no incident; the count restarts.
func TestStepDownRestartsAtZero(t *testing.T) {
	spec := StageSpec{Autonomy: AutonomyPolicy{ProposeAfter: 1}}
	st := StageStatus{Level: LevelAdvise, Conditions: []Condition{recordStoreReady()}, Stream: Stream{Concluded: []Concluded{
		{UID: "down", Type: TypeAutonomy, Outcome: Succeeded, Cause: "p1"},
		promoted("p1", Failed, "", false),
		{UID: "rb", Type: TypeRollback, Outcome: Succeeded, Verification: Verified, Cause: "p1"},
		{UID: "fz", Type: TypeFreeze, Outcome: Succeeded, Cause: "p1"},
	}}}
	if p := ProposeLevel(spec, st, nil); !slices.Equal(kinds(p), []string{BlockerNeedsVerified}) || !strings.Contains(p.Blockers[0].Message, "0 of 1") {
		t.Fatalf("the step-down's own failure is no incident; the count restarts at zero: %+v", p)
	}
	st.Stream.Concluded = append(st.Stream.Concluded, promoted("p2", Succeeded, Verified, false))
	if p := ProposeLevel(spec, st, nil); p.Level != LevelAutoNonBreaking {
		t.Fatalf("one verified promote re-earns the level: %+v", p)
	}
	for _, rb := range []Concluded{
		{UID: "rb", Type: TypeRollback, Outcome: Failed, Cause: "p1"},
		{UID: "rb", Type: TypeRollback, Outcome: Errored, Cause: "p1"},
		{UID: "rb", Type: TypeRollback, Outcome: Rejected, Cause: "p1"},
		{UID: "rb", Type: TypeRollback, Outcome: Succeeded, Verification: Unverified, Cause: "p1"},
		{UID: "rb", Type: TypeRollback, Outcome: Succeeded, Verification: AcceptedUnknown, Cause: "p1"},
		{UID: "rb", Type: TypeFreeze, Outcome: Cancelled, Cause: "p1"},
	} {
		st.Stream.Concluded[2] = rb
		if p := ProposeLevel(spec, st, nil); !slices.Contains(kinds(p), BlockerIncident) {
			t.Fatalf("a remediation that did not succeed and verify is still an incident: %+v -> %+v", rb, p)
		}
	}
	st.Stream.Concluded[2] = Concluded{UID: "rb2", Type: TypeRollback, Outcome: Succeeded, Cause: "other"}
	if p := ProposeLevel(spec, st, nil); !slices.Contains(kinds(p), BlockerIncident) {
		t.Fatalf("a rollback of another failure is an incident: %+v", p)
	}
}

func TestLevelChangeResetsTheWindows(t *testing.T) {
	s := StageStatus{Level: LevelAdvise, Shadow: []Shadow{{Candidate: dA}}, Proposal: &Proposal{Level: LevelAutoNonBreaking}}
	changeLevel(&s, OperationSpec{Params: Params{From: string(LevelAdvise), To: string(LevelAutoNonBreaking)}})
	if s.Proposal != nil || len(s.Shadow) != 1 {
		t.Fatalf("a raise drops the proposal and keeps the shadow: %+v", s)
	}
	changeLevel(&s, OperationSpec{Params: Params{From: string(LevelAdvise), To: string(LevelObserve)}})
	if s.Shadow != nil || s.Level != LevelObserve {
		t.Fatalf("observing again restarts the shadow window: %+v", s)
	}
}

func TestRecordOutcomeLevel(t *testing.T) {
	in := RecordInput{Operation: OperationObject{Spec: OperationSpec{Type: TypeAutonomy, Params: Params{From: "advise", To: "observe"}},
		Status: OperationStatus{Outcome: Succeeded}}}
	if got := NewOperationRecord(in).Predicate.Outcome.Level; got != (LevelChange{From: LevelAdvise, To: LevelObserve}) {
		t.Fatalf("level change: %+v", got)
	}
	in.Operation.Status.Outcome = Rejected
	if got := NewOperationRecord(in).Predicate.Outcome.Level; got != (LevelChange{}) {
		t.Fatalf("a refused change moved nothing: %+v", got)
	}
}
