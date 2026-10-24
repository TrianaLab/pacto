package delivery

import (
	"cmp"
	"fmt"
	"strings"
)

// NowOf is the Stage as it stands, as every answer about it shows it.
func NowOf(s *StageObject) StageNow {
	st := s.Status
	return StageNow{Name: s.Metadata.Name, Pointer: st.Pointer, Frozen: st.Frozen, FrozenReason: st.FrozenReason, AutoHold: st.AutoHold,
		Verified: st.Verified, FailedDigests: st.FailedDigests, LastCheck: st.LastCheck}
}

// ExplainOperation explains op in plain terms against its Stage; a nil stage
// is one that is gone, and the operation is still explained.
func ExplainOperation(op *OperationObject, stage *StageObject) *Explanation {
	e := &Explanation{Operation: op.Metadata.Namespace + "/" + op.Metadata.Name, Spec: op.Spec}
	s := op.Status
	e.Status.Phase, e.Status.Outcome, e.Status.Verification = s.Phase, s.Outcome, s.Verification
	e.Status.Reason, e.Status.Message, e.Status.Question, e.Status.ActorClass, e.Status.Seq = s.Reason, s.Message, s.Question, s.ActorClass, s.Seq
	for _, v := range s.Verdicts {
		e.Verdicts = append(e.Verdicts, ExplainedVerdict{Verdict: v, Rerun: RerunHint(v)})
	}
	if s.Plan != nil {
		e.Order = s.Plan.Order
	}
	e.Stage = StageNow{Name: op.Spec.Stage}
	if stage != nil {
		e.Stage = NowOf(stage)
	}
	e.Summary = Summarize(op)
	return e
}

// Summarize says in one line what op asked and where it is.
func Summarize(op *OperationObject) string {
	s := op.Status
	what := fmt.Sprintf("%s on Stage %s, %s -> %s", op.Spec.Type, op.Spec.Stage, orNone(op.Spec.Params.From), orNone(op.Spec.Params.To))
	switch s.Phase {
	case PhaseConcluded:
		return fmt.Sprintf("%s concluded %s: %s: %s", what, strings.TrimSuffix(string(s.Outcome)+"/"+string(s.Verification), "/"), s.Reason, s.Message)
	case PhaseHeld:
		return what + " waits for a person: " + s.Question
	case "":
		return what + " is not admitted yet"
	}
	return fmt.Sprintf("%s is %s: %s", what, s.Phase, cmp.Or(s.Message, "in progress"))
}

func orNone(s string) string { return cmp.Or(s, "(none)") }

// RerunHint says how anyone can run a verdict's citation again.
func RerunHint(v Verdict) string {
	c := v.Citation
	switch {
	case v.Gate == GateDiff || v.Gate == GateImpact:
		return "pacto delivery plan computes it again from the same contracts and cluster"
	case c["query"] != "" && c["provider"] != "":
		return "POST query, errorQuery and latencyQuery (when present) to /api/v1/query of the " + c["provider"] +
			" provider, following next with after, and count the rows; the window bounds are absolute"
	case v.Gate == GateContract || v.Gate == GateRuntime || c["provider"] == GateContract || c["provider"] == GateRuntime:
		return "read contractStatus, findings and evaluatedRevision of the Pactos named here; the promoter re-runs it when the verdict is cited as a cause"
	case c["provider"] != "":
		return "POST {\"input\": input, \"params\": params} to the " + c["provider"] + " provider"
	}
	return "no citation to run again"
}
