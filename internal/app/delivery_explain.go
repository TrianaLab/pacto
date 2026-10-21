package app

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func stageNow(s *delivery.StageObject) delivery.StageNow {
	st := s.Status
	return delivery.StageNow{Name: s.Metadata.Name, Pointer: st.Pointer, Frozen: st.Frozen, FrozenReason: st.FrozenReason, AutoHold: st.AutoHold,
		Verified: st.Verified, FailedDigests: st.FailedDigests, LastCheck: st.LastCheck}
}

// Explain reads one operation and its Stage and explains them. The Stage may
// be gone; the operation is still explained.
func (d *Delivery) Explain(ctx context.Context, ns, name string) (*delivery.Explanation, error) {
	op, err := d.Get(ctx, ns, name)
	if err != nil {
		return nil, err
	}
	e := &delivery.Explanation{Operation: ns + "/" + name, Spec: op.Spec}
	s := op.Status
	e.Status.Phase, e.Status.Outcome, e.Status.Verification = s.Phase, s.Outcome, s.Verification
	e.Status.Reason, e.Status.Message, e.Status.Question, e.Status.ActorClass, e.Status.Seq = s.Reason, s.Message, s.Question, s.ActorClass, s.Seq
	for _, v := range s.Verdicts {
		e.Verdicts = append(e.Verdicts, delivery.ExplainedVerdict{Verdict: v, Rerun: rerunHint(v)})
	}
	if s.Plan != nil {
		e.Order = s.Plan.Order
	}
	if stage, err := d.Stage(ctx, ns, op.Spec.Stage); err == nil {
		e.Stage = stageNow(stage)
	} else {
		e.Stage = delivery.StageNow{Name: op.Spec.Stage}
	}
	e.Summary = summarize(op)
	return e, nil
}

func summarize(op *delivery.OperationObject) string {
	s := op.Status
	what := fmt.Sprintf("%s on Stage %s, %s -> %s", op.Spec.Type, op.Spec.Stage, orDash(op.Spec.Params.From), orDash(op.Spec.Params.To))
	switch s.Phase {
	case delivery.PhaseConcluded:
		return fmt.Sprintf("%s concluded %s: %s: %s", what, strings.TrimSuffix(string(s.Outcome)+"/"+string(s.Verification), "/"), s.Reason, s.Message)
	case delivery.PhaseHeld:
		return what + " waits for a person: " + s.Question
	case "":
		return what + " is not admitted yet"
	}
	return fmt.Sprintf("%s is %s: %s", what, s.Phase, cmp.Or(s.Message, "in progress"))
}

func orDash(s string) string { return cmp.Or(s, "(none)") }

// rerunHint says how anyone can run a verdict's citation again.
func rerunHint(v delivery.Verdict) string {
	c := v.Citation
	switch {
	case v.Gate == delivery.GateDiff || v.Gate == delivery.GateImpact:
		return "pacto delivery plan computes it again from the same contracts and cluster"
	case c["query"] != "" && c["provider"] != "":
		return "POST query, errorQuery and latencyQuery (when present) to /api/v1/query of the " + c["provider"] +
			" provider, following next with after, and count the rows; the window bounds are absolute"
	case v.Gate == delivery.GateContract || v.Gate == delivery.GateRuntime || c["provider"] == delivery.GateContract || c["provider"] == delivery.GateRuntime:
		return "read contractStatus, findings and evaluatedRevision of the Pactos named here; the promoter re-runs it when the verdict is cited as a cause"
	case c["provider"] != "":
		return "POST {\"input\": input, \"params\": params} to the " + c["provider"] + " provider"
	}
	return "no citation to run again"
}

// inFlight sorts operations without a stream seq (not concluded) last.
func inFlight(o delivery.OperationObject) int {
	if o.Status.Seq == 0 {
		return 1
	}
	return 0
}

// historyNote is what every history answer says about where it came from.
const historyNote = "read from the Operation objects that still exist; an operation deleted from the cluster is missing " +
	"until signed records arrive"

// History lists the operations on a Stage: concluded ones in stream order,
// then the ones in flight, oldest first.
func (d *Delivery) History(ctx context.Context, ns, stage string) (*delivery.History, error) {
	st, err := d.Stage(ctx, ns, stage)
	if err != nil {
		return nil, err
	}
	list, err := d.Dynamic.Resource(gvrOperations).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing Operations in %s: %w", ns, err)
	}
	var ops struct {
		Items []delivery.OperationObject `json:"items"`
	}
	b, _ := list.MarshalJSON()
	_ = json.Unmarshal(b, &ops)
	ops.Items = slices.DeleteFunc(ops.Items, func(o delivery.OperationObject) bool { return o.Spec.Stage != stage })
	slices.SortStableFunc(ops.Items, func(a, b delivery.OperationObject) int {
		return cmp.Or(cmp.Compare(inFlight(a), inFlight(b)), cmp.Compare(a.Status.Seq, b.Status.Seq),
			cmp.Compare(a.Metadata.CreationTimestamp, b.Metadata.CreationTimestamp))
	})
	h := &delivery.History{Stage: stageNow(st), AsOf: d.Now().UTC().Format(time.RFC3339), Completeness: "partial", Note: historyNote,
		Entries: []delivery.HistoryEntry{}}
	for _, o := range ops.Items {
		s := o.Status
		h.Entries = append(h.Entries, delivery.HistoryEntry{Seq: s.Seq, Operation: o.Metadata.Name, Type: o.Spec.Type, From: o.Spec.Params.From,
			To: o.Spec.Params.To, Actor: o.Spec.Actor.User, ActorClass: s.ActorClass, Phase: s.Phase, Outcome: s.Outcome,
			Verification: s.Verification, Reason: s.Reason, Finished: s.Times.Finished})
	}
	return h, nil
}
