package delivery

import (
	"cmp"
	"slices"
	"strings"
)

// Progress is why a Stage can or cannot move to its newest candidate: every
// reason that holds it back, each with what clears it, and the promote
// planned for the caller and not proposed.
type Progress struct {
	State       string           `json:"state" enum:"up-to-date,can-promote,needs-person,blocked,unknown"`
	Summary     string           `json:"summary" doc:"One sentence: where the Stage stands against its newest candidate"`
	Candidate   string           `json:"candidate,omitempty"`
	Reasons     []ProgressReason `json:"reasons" doc:"Everything that holds the candidate back, in the order a person clears them"`
	AutoPromote string           `json:"autoPromote,omitempty" doc:"Why the promoter does not promote the candidate on its own; empty when it would"`
	Plan        *PlanAnswer      `json:"plan,omitempty" doc:"The promote of the candidate, planned for the caller and not proposed"`
	AsOf        string           `json:"asOf" doc:"When the Stage and its operations were read (RFC 3339)"`
}

// ProgressReason is one thing that holds a candidate back.
type ProgressReason struct {
	Code    string `json:"code" enum:"NotReady,ReadyUnknown,NoCandidate,Frozen,AutoHold,Held,InFlight,Level,OpsUnknown,PolicyUnknown,NotPermitted,RBACUnknown,PlanRejected,PlanHeld,PlanWaits,PlanUnknown"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty" doc:"What to do, often a command to copy"`
}

// ProgressInput is what ProgressOf reads beside the Stage.
type ProgressInput struct {
	Stage StageObject
	// Caller is who asks: a fix names only a command this caller may run,
	// and none while Caller.PolicyErr says the policy is unread.
	Caller Caller
	// Team is the team the running contract declares, as the unfreeze
	// offer reads it.
	Team string
	// MayCreate is the CreateOperations review for the Stage's namespace;
	// nil when it was not asked. RBACErr says the review failed.
	MayCreate *bool
	RBACErr   string
	// Ops are the Stage's operations that did not conclude Succeeded;
	// OpsErr says they could not be read.
	Ops    []OperationObject
	OpsErr string
	// Plan is the promote of the candidate (ProgressPlanRequest), nil when
	// not planned; PlanErr says the plan failed.
	Plan    *PlanAnswer
	PlanErr string
	AsOf    string
}

// progress is ProgressOf at work: the input and what it found so far.
type progress struct {
	in       ProgressInput
	ns, name string
	reasons  []ProgressReason
}

func (p *progress) add(code, message, fix string) {
	p.reasons = append(p.reasons, ProgressReason{Code: code, Message: message, Fix: fix})
}

// commands: no command while the identity policy is unread, since the
// caller's class may be wrong.
func (p *progress) commands() bool { return p.in.Caller.PolicyErr == "" }

// mayPropose: a propose command is printed only when the caller may raise
// the type and RBAC was not seen to refuse it.
func (p *progress) mayPropose(typ string) bool {
	in := p.in
	return p.commands() && MayRaise(typ, in.Caller.Class, false) && (in.MayCreate == nil || *in.MayCreate) && in.RBACErr == ""
}

func (p *progress) propose(rest string) string {
	return "pacto delivery propose -n " + p.ns + " --stage " + p.name + " " + rest
}

// ProgressOf says whether a Stage can move to its newest candidate and,
// when not, every reason with what clears it. It only describes.
func ProgressOf(in ProgressInput) Progress {
	ss := in.Stage.Status
	p := &progress{in: in, reasons: []ProgressReason{}, ns: in.Stage.Metadata.Namespace, name: in.Stage.Metadata.Name}
	ready := p.readyReasons()
	// The promoter clears the candidate once the Stage runs the newest one it
	// may take (NewestCandidate), so a running Stage offered nothing, with no
	// change open on it, is up to date.
	upToDate := ss.Pointer != "" && (ss.Candidate == "" || ss.Candidate == ss.Pointer) &&
		!slices.ContainsFunc(in.Ops, func(o OperationObject) bool { return o.Status.Phase != PhaseConcluded })
	if !upToDate {
		p.candidateReasons()
		p.freezeReasons()
		p.heldReasons()
		p.levelReasons()
		p.planReasons()
	}
	p.opsReasons()
	p.accessReasons()
	out := Progress{Candidate: ss.Candidate, Reasons: p.reasons, Plan: in.Plan, AsOf: in.AsOf}
	out.State = p.state(upToDate && ready)
	if !upToDate {
		out.AutoPromote = p.autoPromote()
	}
	label := ""
	if ss.Candidate != "" {
		label = short(ss.Candidate)
		if in.Plan != nil && in.Plan.ToVersion != "" {
			label = in.Plan.ToVersion
		}
	}
	out.Summary = progressSummary(out.State, label)
	return out
}

// readyReasons reports a Ready condition that is not True; true when it is.
func (p *progress) readyReasons() bool {
	c := readyOf(p.in.Stage.Status)
	switch {
	case c == nil:
		p.add("ReadyUnknown", "the promoter has not reported Ready for this Stage yet", "")
	case c.Status == "False":
		p.add("NotReady", strings.TrimSuffix("the Stage is not ready ("+c.Reason+"): "+c.Message, ": "), ReadyFix(c.Reason))
	case c.Status != "True":
		p.add("ReadyUnknown", strings.TrimSuffix("the promoter does not know whether the Stage is ready: "+c.Message, ": "), "")
	default:
		return true
	}
	return false
}

// candidateReasons: only a Stage that runs nothing has no candidate to wait
// for; a running one is offered nothing newer.
func (p *progress) candidateReasons() {
	if ss := p.in.Stage.Status; ss.Candidate != "" || ss.Pointer != "" {
		return
	}
	fix := ""
	if p.commands() {
		fix = "pacto delivery candidates -n " + p.ns + " --stage " + p.name
	}
	p.add("NoCandidate", "the candidate source offers nothing newer that has not failed here", fix)
}

func (p *progress) freezeReasons() {
	ss := p.in.Stage.Status
	if ss.Frozen {
		p.add("Frozen", strings.TrimSuffix("the Stage is frozen: "+ss.FrozenReason, ": "), p.unfreezeFix())
	}
	if ss.AutoHold {
		p.add("AutoHold", "the Stage is on auto-hold: a person rolled back or promoted back, so the promoter does not promote on its own "+
			"until someone unfreezes it", p.unfreezeFix())
	}
}

// unfreezeFix mirrors the promoter's unfreeze admission (unfreezeRefusal):
// the command only for a caller it would admit.
func (p *progress) unfreezeFix() string {
	in := p.in
	switch {
	case !p.commands():
		return ""
	case MayRaise(TypeUnfreeze, in.Caller.Class, false) && mayUnfreeze(in.Caller, in.Team):
		if p.mayPropose(TypeUnfreeze) {
			return p.propose(`--type unfreeze --reason "<why>"`)
		}
		// Admission would take it from this caller: only RBAC refuses, or
		// could not be asked (NotPermitted, RBACUnknown).
		return "you may unfreeze it once your Kubernetes RBAC lets you create Operations in " + p.ns
	case in.Team != "":
		return "a person of " + in.Team + " or break-glass unfreezes it"
	case in.Caller.Policy.RequireOwnerTeam:
		return "only break-glass unfreezes it: the running contract declares no team"
	}
	return "a person or break-glass unfreezes it"
}

// heldReasons names each operation waiting for a person, who may decide it
// and, for a person who may, the approve command.
func (p *progress) heldReasons() {
	for _, op := range p.in.Ops {
		switch op.Status.Phase {
		case PhaseHeld:
			p.heldReason(op)
		case PhaseConcluded:
		default:
			p.add("InFlight", op.Metadata.Name+" is "+strings.ToLower(string(cmp.Or(op.Status.Phase, PhasePending)))+
				" on the Stage: the promoter raises nothing else until it concludes", "")
		}
	}
}

func (p *progress) heldReason(op OperationObject) {
	var owners []string
	decision := Approve
	if pl := op.Status.Plan; pl != nil {
		owners, decision = pl.Floor.Owners, cmp.Or(pl.Floor.Decision, Approve)
	}
	pol, caller := p.in.Caller.Policy, p.in.Caller
	who := ""
	switch {
	case len(owners) > 0:
		who = "a person of " + strings.Join(owners, ", ")
	case !pol.RequireOwnerTeam:
		who = "any person except the one who asked or the one it is on behalf of"
	}
	const nobody = "nobody may answer it; only break-glass overrides"
	msg := op.Metadata.Name + " waits for " + string(decision)
	if who != "" {
		msg += " from " + who
	} else {
		msg += ", but " + nobody
	}
	if op.Status.Question != "" {
		msg += ": " + op.Status.Question
	}
	asker := caller.User == op.Spec.Actor.User || op.Spec.OnBehalfOf != nil && caller.User == op.Spec.OnBehalfOf.User
	fix := ""
	switch {
	case !p.commands():
	case who == "":
		fix = nobody
	case caller.Class == ClassAgent:
		fix = who + " approves it themselves; you cannot answer it"
	case caller.Class == ClassHuman && !asker && pol.InTeams(caller.Groups, owners):
		fix = "pacto delivery approve -n " + p.ns + " " + op.Metadata.Name
		if decision != Approve {
			fix += " --decision " + string(decision)
		}
	default:
		fix = who + " decides it"
	}
	p.add("Held", msg, fix)
}

// levelReasons: an observe Stage refuses every promote; on advise a person
// plans and proposes each one.
func (p *progress) levelReasons() {
	st := p.in.Stage
	if st.Status.Candidate == "" {
		return
	}
	switch level := CurrentLevel(st.Spec, st.Status); {
	case level == LevelObserve:
		fix := ""
		if p.mayPropose(TypeAutonomy) {
			fix = p.propose(`--type autonomy --to advise --reason "<why>"`)
		}
		p.add("Level", "the Stage observes: GitOps owns the applyRef, so Pacto refuses every promote and only plans what it would do", fix)
	case !level.Automatic():
		fix := ""
		if p.commands() {
			fix = "pacto delivery plan -n " + p.ns + " --stage " + p.name + " --to " + st.Status.Candidate
		}
		p.add("Level", "the Stage's level is "+string(level)+": a person proposes each promote", fix)
	}
}

func (p *progress) planReasons() {
	in := p.in
	if in.PlanErr != "" {
		p.add("PlanUnknown", "Pacto could not plan the promote of the candidate ("+in.PlanErr+")", "")
		return
	}
	if in.Plan == nil {
		if _, ok := ProgressPlanRequest(in.Stage); ok {
			p.add("PlanUnknown", "the promote of the candidate was not planned", "")
		}
		return
	}
	pl := in.Plan.Plan
	switch pl.Predicted {
	case PredictRejected:
		why := "a conflict"
		if i := slices.IndexFunc(pl.Verdicts, func(v Verdict) bool { return v.Result == Fail }); i >= 0 {
			why = cmp.Or(pl.Verdicts[i].Message, pl.Verdicts[i].Gate+" failed")
		} else if len(pl.Conflicts) > 0 {
			why = pl.Conflicts[0]
		}
		p.add("PlanRejected", "the promoter would refuse the promote: "+why, "")
	case PredictHeld:
		fix := ""
		if p.mayPropose(TypePromote) {
			fix = p.propose("--to " + pl.To)
		}
		p.add("PlanHeld", strings.TrimSuffix("a person must decide the promote: "+pl.Question, ": "), fix)
	case PredictWaits:
		// A freeze is already a Frozen reason with its fix; the plan's
		// StageFrozen conflict would say it twice.
		conflicts := slices.DeleteFunc(slices.Clone(pl.Conflicts), func(c string) bool {
			return in.Stage.Status.Frozen && strings.HasPrefix(c, "StageFrozen")
		})
		if len(conflicts) > 0 || len(pl.Conflicts) == 0 {
			p.add("PlanWaits", strings.TrimSuffix("the promote would wait: "+strings.Join(conflicts, "; "), ": "), "")
		}
	}
}

func (p *progress) opsReasons() {
	if p.in.OpsErr != "" {
		p.add("OpsUnknown", "the Stage's operations could not be read ("+p.in.OpsErr+"), so a held or in-flight change may exist", "")
	}
}

func (p *progress) accessReasons() {
	in := p.in
	if in.Caller.PolicyErr != "" {
		p.add("PolicyUnknown", "Pacto could not read the delivery identity policy ("+in.Caller.PolicyErr+"), so it cannot tell what you may ask for", "")
	}
	if in.MayCreate != nil && !*in.MayCreate {
		p.add("NotPermitted", "your Kubernetes RBAC does not let you create Operations in "+p.ns, "")
	}
	if in.RBACErr != "" {
		p.add("RBACUnknown", "Pacto could not ask the apiserver whether you may create Operations in "+p.ns+" ("+in.RBACErr+")", "")
	}
}

// state reads the reasons in a fixed order: anything that blocks, then
// anything unknown about the caller or the operations, then up to date,
// then an unknown Ready or plan, then a person, else the Stage can move.
func (p *progress) state(upToDate bool) string {
	has := func(codes ...string) bool {
		return slices.ContainsFunc(p.reasons, func(r ProgressReason) bool { return slices.Contains(codes, r.Code) })
	}
	observe := CurrentLevel(p.in.Stage.Spec, p.in.Stage.Status) == LevelObserve && has("Level")
	switch {
	case observe || has("NotReady", "NoCandidate", "Frozen", "NotPermitted", "PlanRejected", "PlanWaits"):
		return "blocked"
	case has("OpsUnknown", "PolicyUnknown", "RBACUnknown"):
		return "unknown"
	case upToDate:
		return "up-to-date"
	case has("ReadyUnknown", "PlanUnknown"):
		return "unknown"
	case has("Held", "PlanHeld"):
		return "needs-person"
	}
	return "can-promote"
}

// autoPromote is the promoter's own reason (AutoPromoteHold), then the
// in-flight check it makes against the cluster.
func (p *progress) autoPromote() string {
	if why := AutoPromoteHold(p.in.Stage.Spec, p.in.Stage.Status); why != "" {
		return why
	}
	switch {
	case p.in.OpsErr != "":
		return "the Stage's operations could not be read, so an operation may be in flight"
	case slices.ContainsFunc(p.in.Ops, func(o OperationObject) bool { return o.Status.Phase != PhaseConcluded }):
		return "an operation is already in flight on the Stage"
	}
	return ""
}

// progressSummary is one sentence for a state; candidate names the
// candidate, "" when there is none.
func progressSummary(state, candidate string) string {
	what := "its newest candidate"
	if candidate != "" {
		what = candidate
	}
	switch state {
	case "up-to-date":
		return "The Stage runs its newest candidate."
	case "can-promote":
		return "The Stage can move to " + what + "."
	case "needs-person":
		return "Moving the Stage to " + what + " waits for a person's decision."
	case "blocked":
		return "The Stage cannot move to " + what + " until the reasons below are cleared."
	}
	return "Pacto cannot tell whether the Stage can move to " + what + "."
}

// ProgressPlanRequest is the promote of the Stage's candidate, to plan for
// ProgressOf; false when there is nothing the promoter would plan: no
// candidate, the one it runs, an observe Stage or Ready not True.
func ProgressPlanRequest(st StageObject) (Request, bool) {
	ss := st.Status
	c := readyOf(ss)
	if ss.Candidate == "" || ss.Candidate == ss.Pointer || CurrentLevel(st.Spec, ss) == LevelObserve || c == nil || c.Status != "True" {
		return Request{}, false
	}
	return Request{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Type: TypePromote, To: ss.Candidate,
		Reason: "what would promoting the candidate do"}, true
}
