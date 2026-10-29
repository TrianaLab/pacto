package delivery

import (
	"strings"
	"time"
)

// PlanAnswer is a plan as the CLI and the MCP tools return it: the plan and,
// in plain words, what proposing it leads to for whoever asked.
type PlanAnswer struct {
	Plan
	// Next says what happens once the operation is proposed and what to do.
	Next []string `json:"next,omitempty"`
}

// AnswerPlan is p with what it means for a caller of class on stage.
func AnswerPlan(p Plan, class ActorClass, stage StageSpec) PlanAnswer {
	return PlanAnswer{Plan: p, Next: PlanNext(p, class, stage)}
}

// PlanNext says in plain words what proposing p leads to: who must decide,
// that an agent cannot, and what happens when nobody does in time.
func PlanNext(p Plan, class ActorClass, stage StageSpec) []string {
	switch p.Predicted {
	case PredictRejected:
		return []string{"Proposing this is refused and nothing changes: do what the failing verdict or the conflict says, then plan again."}
	case PredictProceeds:
		return []string{"Nobody needs to approve it: once proposed, the promoter makes this change."}
	case PredictWaits:
		return []string{"Nobody needs to approve it, but once proposed it waits until the conflict clears."}
	}
	var next []string
	if who := whoDecides(p, stage); who != "" {
		next = append(next, who)
	}
	next = append(next, "Neither whoever proposes it nor the person it is on behalf of may decide it.")
	if class == ClassAgent {
		next = append(next, "You are an agent, so you cannot answer the question and there is no approve tool: "+
			"if you propose it, the operation is Held until a person decides.")
	} else {
		next = append(next, "If you propose it, the operation is Held until a person decides.")
	}
	t, _ := TypeByID(p.Type)
	approval, _ := t.Deadlines(stage.Deadlines)
	return append(next, "If nobody decides within "+duration(approval)+" of the proposal (the approval deadline), "+
		"the operation concludes Expired and nothing changes on the Stage. Propose again to ask again.")
}

// whoDecides names the teams whose approval counts, and says whether the
// Stage's own rule asks anyone else. "" when no team is named: the question
// then says who may answer.
func whoDecides(p Plan, stage StageSpec) string {
	if len(p.Floor.Owners) == 0 {
		return ""
	}
	verb := "approve"
	if p.Floor.Decision == AcceptUnknown {
		verb = "accept the unknown"
	}
	who := "One person in each of these teams must " + verb + ": " + strings.Join(p.Floor.Owners, ", ") +
		". Only their decisions count, and no other team needs to sign."
	if stage.Approval.Always && p.Type == TypePromote {
		who += " That also meets this Stage's rule that a person approves every promote: the team that owns " + stage.Service.Name +
			" signs only if it is listed."
	}
	return who
}

// OperationAnswer is an operation as the CLI and the MCP tools return it:
// the operation and, while it is held, what happens at its deadline.
type OperationAnswer struct {
	*OperationObject
	// Next says what happens if nobody decides by the approval deadline.
	Next string `json:"next,omitempty"`
}

// AnswerOperation is op with what happens next.
func AnswerOperation(op *OperationObject) OperationAnswer {
	return OperationAnswer{OperationObject: op, Next: DeadlineNote(op)}
}

// DeadlineNote says what happens when a held operation's approval deadline
// passes, or "" when it is not held.
func DeadlineNote(op *OperationObject) string {
	s := op.Status
	if s.Phase != PhaseHeld || s.Times.ApprovalDeadline == "" {
		return ""
	}
	if s.Applied {
		return "If nobody accepts or rejects the unknown by " + s.Times.ApprovalDeadline +
			" (the approval deadline), the operation concludes Errored and the Stage freezes on the version it runs."
	}
	return "If nobody decides by " + s.Times.ApprovalDeadline + " (the approval deadline), the operation concludes " +
		"Expired and nothing changes on the Stage. Propose again to ask again."
}

// duration writes d the way a person would: 24h, 1h30m, 15m.
func duration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// IntentDiffers names what makes spec's intent digest differ from want when
// leaving it out gives want: a propose that passes a reason or an
// on-behalf-of person the plan did not have. "" when that does not explain it.
func IntentDiffers(spec OperationSpec, stage StageSpec, want string) string {
	noReason, noBehalf, neither := spec, spec, spec
	noReason.Reason, noBehalf.OnBehalfOf = "", nil
	neither.Reason, neither.OnBehalfOf = "", nil
	switch {
	case spec.Reason != "" && IntentDigest(noReason, stage) == want:
		return "reason"
	case spec.OnBehalfOf != nil && IntentDigest(noBehalf, stage) == want:
		return "on-behalf-of person"
	case spec.Reason != "" && spec.OnBehalfOf != nil && IntentDigest(neither, stage) == want:
		return "reason and on-behalf-of person"
	}
	return ""
}
