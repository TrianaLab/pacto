package delivery

import (
	"strings"
	"testing"
	"time"
)

// TestApprovalEligible: every reason the promoter passes an Approval over,
// each named, and the one it counts.
func TestApprovalEligible(t *testing.T) {
	at := time.Unix(1_000_000, 0)
	policy := ClassPolicy{ControllerUser: "system:serviceaccount:d:promoter", AgentGroups: []string{"pacto:agents"}}
	spec := OperationSpec{IntentDigest: "sha256:i", Actor: Actor{User: "bot"}, OnBehalfOf: &Principal{User: "carol"}}
	held := OperationStatus{Phase: PhaseHeld, Times: Times{Held: at.Add(-time.Minute).UTC().Format(time.RFC3339)},
		Plan: &Plan{Floor: Floor{Human: true, Decision: Approve, Owners: []string{"team-checkout"}}}}
	good := ApprovalSpec{OperationUID: "u1", IntentDigest: "sha256:i", Decision: Approve, By: "alice",
		Groups: []string{"team-checkout"}, AuthTime: "999990"}
	shipped := held
	shipped.Applied = true
	unknownFloor := held
	unknownFloor.Plan = &Plan{Floor: Floor{Human: true, Decision: AcceptUnknown, Owners: []string{"team-checkout"}}}
	running := held
	running.Phase = PhaseRunning
	with := func(f func(*ApprovalSpec)) ApprovalSpec { a := good; f(&a); return a }
	for _, c := range []struct {
		name    string
		st      OperationStatus
		a       ApprovalSpec
		created time.Time
		why     string
	}{
		{"counts", held, good, at, ""},
		{"accept on an unknown floor", unknownFloor, with(func(a *ApprovalSpec) { a.Decision = AcceptUnknown }), at, ""},
		{"accept after it shipped", shipped, with(func(a *ApprovalSpec) { a.Decision = AcceptUnknown }), at, ""},
		{"not held", running, good, at, "not waiting"},
		{"another operation", held, with(func(a *ApprovalSpec) { a.OperationUID = "u2" }), at, "another operation"},
		{"an older intent", held, with(func(a *ApprovalSpec) { a.IntentDigest = "sha256:old" }), at, "older intent"},
		{"no decision", held, with(func(a *ApprovalSpec) { a.Decision = "maybe" }), at, `"maybe" is not a decision`},
		{"approve after it shipped", shipped, good, at, "already shipped"},
		{"accept where an approval is needed", held, with(func(a *ApprovalSpec) { a.Decision = AcceptUnknown }), at, "needs an approval"},
		{"the actor", held, with(func(a *ApprovalSpec) { a.By = "bot" }), at, "bot asked for this change"},
		{"whoever the actor acts for", held, with(func(a *ApprovalSpec) { a.By = "carol" }), at, "carol asked for this change"},
		{"an agent", held, with(func(a *ApprovalSpec) { a.Groups = append(a.Groups, "pacto:agents") }), at, "not a person"},
		{"another team", held, with(func(a *ApprovalSpec) { a.Groups = []string{"team-ledger"} }), at, "not in a team this question is asked of (team-checkout)"},
		{"before the hold", held, good, at.Add(-2 * time.Minute), "before the question was last asked"},
		{"a stale login", held, with(func(a *ApprovalSpec) { a.AuthTime = "990000" }), at, "login was not within 15m0s"},
		{"no login time", held, with(func(a *ApprovalSpec) { a.AuthTime = "nope" }), at, "sign in again"},
		{"a login from the future", held, with(func(a *ApprovalSpec) { a.AuthTime = at.Add(time.Hour).Format(time.RFC3339) }), at, "sign in again"},
	} {
		ok, why := ApprovalEligible(spec, c.st, "u1", c.a, c.created, policy, AuthFreshness)
		if ok != (c.why == "") || !strings.Contains(why, c.why) || ok && why != "" {
			t.Errorf("%s: got %v %q, want %q", c.name, ok, why, c.why)
		}
	}
	if ok, _ := ApprovalEligible(spec, OperationStatus{Phase: PhaseHeld}, "u1", good, at, policy, AuthFreshness); !ok {
		t.Error("a held operation with no plan asks no team in particular")
	}
	if code, _ := ApprovalRefusal(spec, held, "u1", with(func(a *ApprovalSpec) { a.Groups = []string{"team-ledger"} }), at, policy, AuthFreshness); code != "NotYourTeam" {
		t.Errorf("a refusal carries its code: %q", code)
	}
	policy.RequireOwnerTeam = true
	if ok, why := ApprovalEligible(spec, OperationStatus{Phase: PhaseHeld}, "u1", good, at, policy, AuthFreshness); ok || !strings.Contains(why, "only break-glass can override") {
		t.Errorf("with an owner team required, an ownerless question has nobody to answer it: %v %q", ok, why)
	}
}

func TestSetCondition(t *testing.T) {
	ready := Condition{Type: "Ready", Status: "True", Reason: "Ready", ObservedGeneration: 1}
	other := Condition{Type: "Other", Status: "True"}
	conds, changed := SetCondition([]Condition{other}, ready, t0)
	if !changed || len(conds) != 2 || conds[1].LastTransitionTime != "2026-10-20T10:00:00Z" {
		t.Fatalf("added: %v %+v", changed, conds)
	}
	if again, changed := SetCondition(conds, ready, t1); changed || again[1].LastTransitionTime != "2026-10-20T10:00:00Z" {
		t.Fatalf("the same condition is no change: %v %+v", changed, again)
	}
	moved := ready
	moved.Message, moved.ObservedGeneration = "now with a message", 2
	same, changed := SetCondition(conds, moved, t1)
	if !changed || same[1].LastTransitionTime != "2026-10-20T10:00:00Z" || same[1].ObservedGeneration != 2 {
		t.Fatalf("a new message keeps the transition time: %v %+v", changed, same)
	}
	flipped, changed := SetCondition(conds, Condition{Type: "Ready", Status: "False", Reason: "X"}, t1)
	if !changed || flipped[1].LastTransitionTime != "2026-10-20T10:10:00Z" || flipped[0] != other {
		t.Fatalf("a new status is a transition: %v %+v", changed, flipped)
	}
	if conds[1].Status != "True" {
		t.Fatal("SetCondition edited its input")
	}
}

func TestDecisionFreshnessFor(t *testing.T) {
	for in, want := range map[string]time.Duration{"": DecisionFreshness, "15m0s": DecisionFreshness, "6m": 4 * time.Minute, "nonsense": DecisionFreshness, "-1m": DecisionFreshness} {
		if got := DecisionFreshnessFor(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
	if PublishedFreshness("6m") != 6*time.Minute || PublishedFreshness("") != AuthFreshness {
		t.Error("PublishedFreshness")
	}
}

// TestApplyEffectsOffersOnlyPublishedPromotes: a promote that passed the
// published gate here and verified is offered to a downstream Stage. One a
// person accepted on an unknown published verdict is not, and nothing but a
// promote is.
func TestApplyEffectsOffersOnlyPublishedPromotes(t *testing.T) {
	published := func(typ, to string, r Result) (OperationSpec, OperationStatus) {
		return OperationSpec{Type: typ, Params: Params{From: dA, To: to}},
			OperationStatus{Verdicts: []Verdict{{Gate: GatePublished, Phase: "pre", Result: r}, {Gate: GateDiff, Phase: "pre", Result: Pass}}}
	}
	s := StageStatus{}
	verified := Conclusion{Outcome: Succeeded, Verification: Verified}
	for _, c := range []struct {
		typ, to string
		r       Result
	}{{TypePromote, dB, Pass}, {TypePromote, dC, Unknown}, {TypeOverride, dC, Pass}} {
		spec, st := published(c.typ, c.to, c.r)
		ApplyEffects(&s, "op", "uid", spec, st, verified)
	}
	if len(s.Offered) != 1 || s.Offered[0] != dB || len(s.Verified) != 2 {
		t.Fatalf("offered %v verified %v", s.Offered, s.Verified)
	}
}

// TestApplyEffects: what each conclusion leaves on the Stage.
func TestApplyEffects(t *testing.T) {
	type in struct {
		typ     string
		class   ActorClass
		applied bool
		before  string
		c       Conclusion
		s       StageStatus
	}
	for name, c := range map[string]struct {
		in   in
		want func(StageStatus) bool
	}{
		"a person's rollback holds auto": {in{typ: TypeRollback, class: ClassHuman, applied: true, c: Conclusion{Outcome: Succeeded}},
			func(s StageStatus) bool { return s.AutoHold }},
		"a person's promote releases it": {in{typ: TypePromote, class: ClassHuman, applied: true, c: Conclusion{Outcome: Succeeded}, s: StageStatus{AutoHold: true}},
			func(s StageStatus) bool { return !s.AutoHold }},
		"unfreeze": {in{typ: TypeUnfreeze, c: Conclusion{Outcome: Succeeded}, s: StageStatus{Frozen: true, FrozenReason: "x", AutoHold: true}},
			func(s StageStatus) bool { return !s.Frozen && s.FrozenReason == "" && !s.AutoHold }},
		"acknowledge": {in{typ: TypeAcknowledge, c: Conclusion{Outcome: Succeeded}, s: StageStatus{UpstreamAcknowledged: []string{"sha256:old"}}},
			func(s StageStatus) bool {
				return strings.Join(s.UpstreamAcknowledged, ",") == "sha256:old,sha256:rec" && !s.Frozen
			}},
		"freeze": {in{typ: TypeFreeze, c: Conclusion{Outcome: Succeeded}},
			func(s StageStatus) bool { return s.Frozen && s.FrozenReason == "op: frozen: because" }},
		"freeze keeps an earlier reason": {in{typ: TypeFreeze, c: Conclusion{Outcome: Succeeded}, s: StageStatus{Frozen: true, FrozenReason: "re-check"}},
			func(s StageStatus) bool { return s.FrozenReason == "re-check" }},
		"a refused automatic rollback freezes": {in{typ: TypeRollback, class: ClassController, c: Conclusion{Outcome: Rejected, Reason: "PointerMoved: x"}},
			func(s StageStatus) bool {
				return s.Frozen && s.FrozenReason == "op: the automatic rollback was refused: PointerMoved: x"
			}},
		"a failed rollback freezes": {in{typ: TypeRollback, class: ClassHuman, c: Conclusion{Outcome: Failed, Reason: "VerifyFailed: x"}, s: StageStatus{Verified: []string{dB}}},
			func(s StageStatus) bool {
				return s.Frozen && strings.Contains(s.FrozenReason, "failed: VerifyFailed") && len(s.Verified) == 0 && s.FailedDigests[0] == dB
			}},
		"a failed first version freezes": {in{typ: TypePromote, c: Conclusion{Outcome: Failed}},
			func(s StageStatus) bool {
				return s.Frozen && strings.Contains(s.FrozenReason, "nothing to roll back to")
			}},
		"a failed promote over a failed version freezes": {in{typ: TypePromote, before: dA, c: Conclusion{Outcome: Failed}, s: StageStatus{FailedDigests: []string{dA}}},
			func(s StageStatus) bool { return s.Frozen && strings.Contains(s.FrozenReason, "nothing safe") }},
		"a failed promote is rolled back once": {in{typ: TypePromote, before: dA, c: Conclusion{Outcome: Failed}},
			func(s StageStatus) bool { return !s.Frozen && len(s.RolledBack) == 1 && s.RolledBack[0] == "uid" }},
		"a conclusion that freezes": {in{typ: TypePromote, c: Conclusion{Outcome: Errored, Reason: "ExecutionDeadline: x", Freeze: true}},
			func(s StageStatus) bool { return s.FrozenReason == "op: ExecutionDeadline: x" }},
	} {
		s := c.in.s
		spec := OperationSpec{Type: c.in.typ, Reason: "because", Params: Params{From: c.in.before, To: dB, Record: "sha256:rec"}}
		st := OperationStatus{ActorClass: c.in.class, Applied: c.in.applied, Pointer: PointerChange{Before: c.in.before, After: dB}}
		ApplyEffects(&s, "op", "uid", spec, st, c.in.c)
		if !c.want(s) {
			t.Errorf("%s: %+v", name, s)
		}
	}
}

func TestTeamsWaiting(t *testing.T) {
	p := ClassPolicy{TeamGroupPrefix: "acme:"}
	owners := []string{"checkout", "orders"}
	got := AnsweredTeams(p, owners, []string{"acme:checkout", "checkout"}, nil)
	if strings.Join(got, ",") != "checkout" || strings.Join(TeamsWaiting(owners, got), ",") != "orders" {
		t.Fatalf("answered %v, waiting %v", got, TeamsWaiting(owners, got))
	}
	if got = AnsweredTeams(p, owners, []string{"acme:checkout", "acme:orders"}, got); len(TeamsWaiting(owners, got)) != 0 {
		t.Fatalf("every team answered: %v", got)
	}
}
