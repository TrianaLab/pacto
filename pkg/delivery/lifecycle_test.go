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
