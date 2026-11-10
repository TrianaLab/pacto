package delivery

import (
	"slices"
	"strings"
	"testing"
)

var readyTrue = []Condition{{Type: ConditionReady, Status: "True", Reason: "Guarded"}}

// progStage is the payments Stage running rowRun with rowNext as candidate,
// Ready True, at level.
func progStage(level Level, edit func(*StageStatus)) StageObject {
	st := StageStatus{Pointer: rowRun, Candidate: rowNext, Conditions: readyTrue}
	if edit != nil {
		edit(&st)
	}
	return rowStage(level, st)
}

func progPlan(p Prediction, edit func(*Plan)) *PlanAnswer {
	pl := &PlanAnswer{Plan: Plan{Type: TypePromote, From: rowRun, To: rowNext, ToVersion: "1.1.0", Predicted: p}}
	if edit != nil {
		edit(&pl.Plan)
	}
	return pl
}

func heldOp(name string, owners ...string) OperationObject {
	return OperationObject{Metadata: ObjectMeta{Namespace: "prod", Name: name},
		Spec: OperationSpec{Type: TypePromote, Stage: "payments", Actor: Actor{User: "bob"}, OnBehalfOf: &Principal{User: "dana"}},
		Status: OperationStatus{Phase: PhaseHeld, Question: "Approve the break?",
			Plan: &Plan{Floor: Floor{Human: true, Decision: Approve, Owners: owners}}}}
}

func codes(p Progress) []string {
	var out []string
	for _, r := range p.Reasons {
		out = append(out, r.Code)
	}
	return out
}

func reasonOf(p Progress, code string) *ProgressReason {
	if i := slices.IndexFunc(p.Reasons, func(r ProgressReason) bool { return r.Code == code }); i >= 0 {
		return &p.Reasons[i]
	}
	return nil
}

// anyCommand reports whether a fix in p holds a pacto command.
func anyCommand(p Progress, verb string) bool {
	return slices.ContainsFunc(p.Reasons, func(r ProgressReason) bool { return strings.Contains(r.Fix, "pacto delivery "+verb) })
}

type progressCase struct {
	name      string
	in        ProgressInput
	state     string
	codes     []string
	auto      string // a substring AutoPromote holds; "-" for empty
	fix       map[string]string
	noPlanned bool
}

func checkProgress(t *testing.T, c progressCase) {
	t.Helper()
	c.in.AsOf = "2027-01-01T00:00:00Z"
	p := ProgressOf(c.in)
	if p.State != c.state || !slices.Equal(codes(p), c.codes) || p.AsOf != c.in.AsOf || p.Reasons == nil || p.Summary == "" {
		t.Fatalf("state %s codes %v, want %s %v: %+v", p.State, codes(p), c.state, c.codes, p)
	}
	switch {
	case c.auto == "-" && p.AutoPromote != "":
		t.Errorf("autoPromote %q, want empty", p.AutoPromote)
	case c.auto != "-" && !strings.Contains(p.AutoPromote, c.auto):
		t.Errorf("autoPromote %q, want %q", p.AutoPromote, c.auto)
	}
	checkFixes(t, p, c.fix)
	if _, ok := ProgressPlanRequest(c.in.Stage); c.noPlanned && ok {
		t.Errorf("planned a promote the Stage refuses")
	}
}

// checkFixes: each reason's fix holds want; "" wants no fix.
func checkFixes(t *testing.T, p Progress, fixes map[string]string) {
	t.Helper()
	for code, want := range fixes {
		if r := reasonOf(p, code); want == "" && r.Fix != "" || !strings.Contains(r.Fix, want) {
			t.Errorf("%s fix %q, want %q", code, r.Fix, want)
		}
	}
}

func TestProgressOf(t *testing.T) {
	person := rowCaller(ClassHuman, "acme:team-payments")
	cases := []progressCase{
		{name: "ready false unverified", state: "blocked", codes: []string{"NotReady", "NoCandidate"},
			in: ProgressInput{Stage: progStage(LevelAdvise, func(s *StageStatus) {
				s.Candidate = ""
				s.Conditions = []Condition{{Type: ConditionReady, Status: "False", Reason: ReadyReasonCandidateUnverified, Message: "no verify"}}
			})},
			fix: map[string]string{"NotReady": "spec.verify"}},
		{name: "ready missing", state: "unknown", codes: []string{"ReadyUnknown"},
			in: ProgressInput{Stage: progStage(LevelAutonomous, func(s *StageStatus) { s.Conditions = nil })}},
		{name: "ready unknown", state: "unknown", codes: []string{"ReadyUnknown"},
			in: ProgressInput{Stage: progStage(LevelAutonomous, func(s *StageStatus) {
				s.Conditions = []Condition{{Type: ConditionReady, Status: "Unknown", Message: "reading"}}
			})}},
		{name: "no candidate", state: "blocked", codes: []string{"NoCandidate"},
			in:  ProgressInput{Stage: progStage(LevelAdvise, func(s *StageStatus) { s.Candidate = "" })},
			fix: map[string]string{"NoCandidate": "pacto delivery candidates -n prod --stage payments"}},
		{name: "up to date", state: "up-to-date", codes: nil, auto: "-",
			in: ProgressInput{Stage: progStage(LevelAutonomous, func(s *StageStatus) { s.Candidate = rowRun })}},
		{name: "frozen with a held promote", state: "blocked", codes: []string{"Frozen", "Held", "PlanHeld"}, auto: "frozen",
			in: ProgressInput{Stage: progStage(LevelAutonomous, func(s *StageStatus) { s.Frozen, s.FrozenReason = true, "Errored" }),
				Caller: person, Team: "team-payments", Ops: []OperationObject{heldOp("op-1", "team-checkout")},
				Plan: progPlan(PredictHeld, func(p *Plan) { p.Question = "Approve?"; p.Conflicts = []string{"StageFrozen: Errored"} })},
			fix: map[string]string{"Frozen": "--type unfreeze"}},
		{name: "auto-hold on advise", state: "can-promote", codes: []string{"AutoHold", "Level"}, auto: "advise",
			in: ProgressInput{Stage: progStage(LevelAdvise, func(s *StageStatus) { s.AutoHold = true }), Caller: person, Team: "team-payments",
				Plan: progPlan(PredictProceeds, nil)},
			fix: map[string]string{"Level": "pacto delivery plan -n prod --stage payments --to " + rowNext}},
		{name: "auto-hold on autonomous", state: "can-promote", codes: []string{"AutoHold"}, auto: "auto-hold",
			in: ProgressInput{Stage: progStage(LevelAutonomous, func(s *StageStatus) { s.AutoHold = true }), Plan: progPlan(PredictProceeds, nil)}},
		{name: "autonomous proceeds", state: "can-promote", codes: nil, auto: "-",
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: progPlan(PredictProceeds, nil)}},
		{name: "plan rejected", state: "blocked", codes: []string{"PlanRejected"},
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: progPlan(PredictRejected, func(p *Plan) {
				p.Verdicts = []Verdict{{Gate: GateDiff, Result: Pass}, {Gate: GateImpact, Result: Fail, Message: "checkout breaks"}}
			})}},
		{name: "plan rejected by a conflict", state: "blocked", codes: []string{"PlanRejected"},
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: progPlan(PredictRejected, func(p *Plan) { p.Conflicts = []string{"PointerMoved"} })}},
		{name: "plan waits", state: "blocked", codes: []string{"PlanWaits"},
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: progPlan(PredictWaits, func(p *Plan) { p.Conflicts = []string{"Busy"} })}},
		{name: "plan error", state: "unknown", codes: []string{"PlanUnknown"},
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil), PlanErr: "registry down"}},
		{name: "not planned", state: "unknown", codes: []string{"PlanUnknown"},
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil)}},
		{name: "in flight", state: "can-promote", codes: []string{"InFlight"}, auto: "in flight",
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: progPlan(PredictProceeds, nil),
				Ops: []OperationObject{{Metadata: ObjectMeta{Name: "op-2"}, Status: OperationStatus{Phase: PhaseRunning}},
					{Metadata: ObjectMeta{Name: "old"}, Status: OperationStatus{Phase: PhaseConcluded, Outcome: Errored}}}}},
		{name: "held plan", state: "needs-person", codes: []string{"Level", "PlanHeld"},
			in:  ProgressInput{Stage: progStage(LevelAdvise, nil), Caller: person, Plan: progPlan(PredictHeld, func(p *Plan) { p.Question = "Approve?" })},
			fix: map[string]string{"PlanHeld": "pacto delivery propose -n prod --stage payments --to " + rowNext}},
		{name: "observe with a candidate", state: "blocked", codes: []string{"Level"}, auto: "observes", noPlanned: true,
			in:  ProgressInput{Stage: progStage(LevelObserve, nil), Caller: person},
			fix: map[string]string{"Level": "--type autonomy --to advise"}},
		{name: "observe, a class that may not raise autonomy", state: "blocked", codes: []string{"Level"}, noPlanned: true,
			in:  ProgressInput{Stage: progStage(LevelObserve, nil), Caller: Caller{Class: "visitor"}},
			fix: map[string]string{"Level": ""}},
		{name: "up to date wins over a missing plan", state: "up-to-date", codes: nil,
			in: ProgressInput{Stage: progStage(LevelAdvise, func(s *StageStatus) { s.Candidate = rowRun })}},
		{name: "ops unreadable", state: "unknown", codes: []string{"OpsUnknown"}, auto: "could not be read",
			in: ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: progPlan(PredictProceeds, nil), OpsErr: "forbidden"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkProgress(t, c) })
	}
}

func TestProgressOfMessages(t *testing.T) {
	st := progStage(LevelAdvise, func(s *StageStatus) { s.Frozen, s.FrozenReason = true, "Errored" })
	p := ProgressOf(ProgressInput{Stage: st, Plan: progPlan(PredictRejected, func(p *Plan) {
		p.Verdicts = []Verdict{{Gate: GateImpact, Result: Fail, Message: "checkout breaks"}}
	})})
	if r := reasonOf(p, "PlanRejected"); !strings.Contains(r.Message, "checkout breaks") {
		t.Errorf("rejected names the failing verdict: %q", r.Message)
	}
	if r := reasonOf(p, "Frozen"); !strings.Contains(r.Message, "Errored") {
		t.Errorf("frozen names its reason: %q", r.Message)
	}
	if !strings.Contains(p.Summary, "1.1.0") {
		t.Errorf("summary names the candidate version: %q", p.Summary)
	}
	for _, s := range []string{"up-to-date", "can-promote", "needs-person", "blocked", "unknown"} {
		if progressSummary(s, "") == "" || progressSummary(s, "1.1.0") == progressSummary(s, "") && s != "up-to-date" {
			t.Errorf("summary for %s", s)
		}
	}
	wait := ProgressOf(ProgressInput{Stage: progStage(LevelAutonomous, nil), Plan: progPlan(PredictWaits, func(p *Plan) { p.Conflicts = []string{"Busy", "Other"} })})
	if r := reasonOf(wait, "PlanWaits"); !strings.Contains(r.Message, "Busy; Other") {
		t.Errorf("waits lists its conflicts: %q", r.Message)
	}
	accept := heldOp("op-3", "team-payments")
	accept.Status.Plan.Floor.Decision = AcceptUnknown
	held := ProgressOf(ProgressInput{Stage: progStage(LevelAdvise, nil), Caller: rowCaller(ClassHuman, "acme:team-payments"),
		Ops: []OperationObject{accept}, Plan: progPlan(PredictHeld, nil)})
	if r := reasonOf(held, "Held"); !strings.Contains(r.Message, "op-3 waits for accept-unknown from a person of team-payments: Approve the break?") ||
		r.Fix != "pacto delivery approve -n prod op-3 --decision accept-unknown" {
		t.Errorf("accept-unknown: %+v", r)
	}
	noPlan := heldOp("op-4")
	noPlan.Status.Plan, noPlan.Status.Question = nil, ""
	held = ProgressOf(ProgressInput{Stage: progStage(LevelAdvise, nil), Caller: rowCaller(ClassHuman), Ops: []OperationObject{noPlan},
		Plan: progPlan(PredictHeld, nil)})
	if r := reasonOf(held, "Held"); r.Message != "op-4 waits for approve from any person except the one who asked or the one it is on behalf of" {
		t.Errorf("held with no plan: %+v", r)
	}
}

// TestProgressOfAgentGetsNoApproveCommand: an agent never gets an approve or
// unfreeze command, however the Stage stands.
func TestProgressOfAgentGetsNoApproveCommand(t *testing.T) {
	agent := rowCaller(ClassAgent, "acme:team-payments", "pacto:agents")
	p := ProgressOf(ProgressInput{Stage: progStage(LevelAdvise, func(s *StageStatus) { s.Frozen = true }), Caller: agent, Team: "team-payments",
		Ops: []OperationObject{heldOp("op-1", "team-payments")}, Plan: progPlan(PredictHeld, nil)})
	if anyCommand(p, "approve") || anyCommand(p, "propose -n prod --stage payments --type unfreeze") {
		t.Fatalf("an agent got a command it cannot run: %+v", p.Reasons)
	}
	if r := reasonOf(p, "Held"); r.Fix != "a person of team-payments approves it themselves; you cannot answer it" {
		t.Errorf("agent held fix %q", r.Fix)
	}
}

func TestProgressOfRequireOwnerTeam(t *testing.T) {
	c := rowCaller(ClassHuman)
	c.Policy.RequireOwnerTeam = true
	for _, class := range []ActorClass{ClassHuman, ClassAgent} {
		c.Class = class
		p := ProgressOf(ProgressInput{Stage: progStage(LevelAdvise, func(s *StageStatus) { s.Frozen = true }), Caller: c,
			Ops: []OperationObject{heldOp("op-1")}, Plan: progPlan(PredictHeld, nil)})
		r := reasonOf(p, "Held")
		if !strings.Contains(r.Message, "nobody may answer it; only break-glass overrides") || strings.Contains(r.Message+r.Fix, "anyone") || anyCommand(p, "approve") {
			t.Errorf("%s: %+v", class, r)
		}
		if f := reasonOf(p, "Frozen").Fix; class == ClassHuman && f != "only break-glass unfreezes it: the running contract declares no team" {
			t.Errorf("unfreeze with no team: %q", f)
		}
	}
	c.Class, c.Policy.RequireOwnerTeam = ClassAgent, false
	p := ProgressOf(ProgressInput{Stage: progStage(LevelAdvise, func(s *StageStatus) { s.Frozen = true }), Caller: c, Plan: progPlan(PredictHeld, nil)})
	if f := reasonOf(p, "Frozen").Fix; f != "a person or break-glass unfreezes it" {
		t.Errorf("an agent on a Stage with no team: %q", f)
	}
}

func TestProgressOfOpsUnreadableIsUnknown(t *testing.T) {
	for _, st := range []StageObject{progStage(LevelAutonomous, nil), progStage(LevelAutonomous, func(s *StageStatus) { s.Candidate = rowRun })} {
		p := ProgressOf(ProgressInput{Stage: st, Plan: progPlan(PredictProceeds, nil), OpsErr: "forbidden"})
		if p.State != "unknown" || !strings.Contains(reasonOf(p, "OpsUnknown").Message, "forbidden") {
			t.Fatalf("ops unreadable: %+v", p)
		}
	}
}

func TestProgressOfUnfreezeFixNeedsTeam(t *testing.T) {
	frozen := progStage(LevelAdvise, func(s *StageStatus) { s.Frozen = true })
	out := ProgressOf(ProgressInput{Stage: frozen, Caller: rowCaller(ClassHuman, "acme:team-ledger"), Team: "team-payments", Plan: progPlan(PredictHeld, nil)})
	if r := reasonOf(out, "Frozen"); anyCommand(out, "propose -n prod --stage payments --type unfreeze") || r.Fix != "a person of team-payments or break-glass unfreezes it" {
		t.Errorf("outside the team: %+v", r)
	}
	in := ProgressOf(ProgressInput{Stage: frozen, Caller: rowCaller(ClassHuman, "acme:team-payments"), Team: "team-payments", Plan: progPlan(PredictHeld, nil)})
	if r := reasonOf(in, "Frozen"); r.Fix != `pacto delivery propose -n prod --stage payments --type unfreeze --reason "<why>"` {
		t.Errorf("in the team: %+v", r)
	}
}

func TestProgressOfApproveOnlyForOwner(t *testing.T) {
	op := heldOp("op-1", "team-payments")
	ask := func(user string, groups ...string) Progress {
		c := rowCaller(ClassHuman, groups...)
		c.User = user
		return ProgressOf(ProgressInput{Stage: progStage(LevelAdvise, nil), Caller: c, Ops: []OperationObject{op}, Plan: progPlan(PredictHeld, nil)})
	}
	for _, p := range []Progress{ask("alice", "acme:team-ledger"), ask("bob", "acme:team-payments"), ask("dana", "acme:team-payments")} {
		if anyCommand(p, "approve") || reasonOf(p, "Held").Fix != "a person of team-payments decides it" {
			t.Errorf("not an owner, the asker or the person it is on behalf of: %+v", reasonOf(p, "Held"))
		}
	}
	if p := ask("alice", "acme:team-payments"); reasonOf(p, "Held").Fix != "pacto delivery approve -n prod op-1" {
		t.Errorf("an owner: %+v", reasonOf(p, "Held"))
	}
}

func TestProgressOfPolicyUnreadableNoCommands(t *testing.T) {
	c := rowCaller(ClassHuman, "acme:team-payments")
	c.PolicyErr = "configmaps forbidden"
	for _, st := range []StageObject{
		progStage(LevelAdvise, func(s *StageStatus) { s.AutoHold = true }),
		progStage(LevelObserve, nil),
		progStage(LevelAdvise, func(s *StageStatus) { s.Candidate = "" }),
	} {
		p := ProgressOf(ProgressInput{Stage: st, Caller: c, Team: "team-payments", Ops: []OperationObject{heldOp("op-1", "team-payments")},
			Plan: progPlan(PredictHeld, nil)})
		if anyCommand(p, "") || reasonOf(p, "PolicyUnknown") == nil || p.State == "can-promote" || p.State == "needs-person" {
			t.Errorf("policy unread: %s %+v", p.State, p.Reasons)
		}
	}
	p := ProgressOf(ProgressInput{Stage: progStage(LevelAdvise, nil), Caller: c, Plan: progPlan(PredictHeld, nil)})
	if p.State != "unknown" || !strings.Contains(reasonOf(p, "PolicyUnknown").Message, "configmaps forbidden") {
		t.Errorf("policy unread on a Stage that could move: %+v", p)
	}
}

func TestProgressOfRBACDenied(t *testing.T) {
	no, yes := false, true
	person := rowCaller(ClassHuman, "acme:team-payments")
	st := progStage(LevelAdvise, func(s *StageStatus) { s.AutoHold = true })
	denied := ProgressOf(ProgressInput{Stage: st, Caller: person, Team: "team-payments", MayCreate: &no, Plan: progPlan(PredictProceeds, nil)})
	if denied.State != "blocked" || !strings.Contains(reasonOf(denied, "NotPermitted").Message, "create Operations in prod") || anyCommand(denied, "propose") {
		t.Errorf("denied: %+v", denied)
	}
	failed := ProgressOf(ProgressInput{Stage: st, Caller: person, Team: "team-payments", RBACErr: "timeout", Plan: progPlan(PredictProceeds, nil)})
	if failed.State != "unknown" || !strings.Contains(reasonOf(failed, "RBACUnknown").Message, "timeout") || anyCommand(failed, "propose") {
		t.Errorf("review failed: %+v", failed)
	}
	allowed := ProgressOf(ProgressInput{Stage: st, Caller: person, Team: "team-payments", MayCreate: &yes, Plan: progPlan(PredictProceeds, nil)})
	if allowed.State != "can-promote" || !anyCommand(allowed, "propose") {
		t.Errorf("allowed: %+v", allowed)
	}
}

func TestProgressPlanRequest(t *testing.T) {
	req, ok := ProgressPlanRequest(progStage(LevelAdvise, nil))
	if !ok || req != (Request{Namespace: "prod", Stage: "payments", Type: TypePromote, To: rowNext, Reason: "what would promoting the candidate do"}) {
		t.Fatalf("request %+v %v", req, ok)
	}
	for name, st := range map[string]StageObject{
		"observe":      progStage(LevelObserve, nil),
		"no candidate": progStage(LevelAdvise, func(s *StageStatus) { s.Candidate = "" }),
		"up to date":   progStage(LevelAdvise, func(s *StageStatus) { s.Candidate = rowRun }),
		"not ready":    progStage(LevelAdvise, func(s *StageStatus) { s.Conditions = []Condition{{Type: ConditionReady, Status: "False"}} }),
		"no ready":     progStage(LevelAdvise, func(s *StageStatus) { s.Conditions = nil }),
	} {
		if _, ok := ProgressPlanRequest(st); ok {
			t.Errorf("%s: planned", name)
		}
	}
}
