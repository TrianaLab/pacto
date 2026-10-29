package delivery

import (
	"strings"
	"testing"
	"time"
)

func TestPlanNext(t *testing.T) {
	stage := StageSpec{Service: ServiceRef{Name: "payments"}, Approval: ApprovalPolicy{Always: true}, Deadlines: Deadlines{Approval: "90m"}}
	for p, want := range map[Prediction]string{
		PredictRejected: "Proposing this is refused and nothing changes",
		PredictProceeds: "Nobody needs to approve it",
		PredictWaits:    "it waits until the conflict clears",
	} {
		if got := PlanNext(Plan{Predicted: p}, ClassAgent, stage); len(got) != 1 || !strings.Contains(got[0], want) {
			t.Fatalf("%s: %v", p, got)
		}
	}
	held := Plan{Type: TypePromote, Predicted: PredictHeld, Floor: Floor{Human: true, Decision: Approve, Owners: []string{"orders"}}}
	got := strings.Join(PlanNext(held, ClassAgent, stage), "\n")
	for _, want := range []string{
		"One person in each of these teams must approve: orders. Only their decisions count, and no other team needs to sign.",
		"That also meets this Stage's rule that a person approves every promote: the team that owns payments signs only if it is listed.",
		"Neither whoever proposes it nor the person it is on behalf of may decide it.",
		"You are an agent, so you cannot answer the question and there is no approve tool: if you propose it, the operation is Held until a person decides.",
		"If nobody decides within 1h30m of the proposal (the approval deadline), the operation concludes Expired and nothing changes on the Stage.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("held for an agent: want %q in\n%s", want, got)
		}
	}
	held.Floor = Floor{Human: true, Decision: AcceptUnknown}
	got = strings.Join(PlanNext(held, ClassHuman, StageSpec{}), "\n")
	if strings.Contains(got, "teams must") || strings.Contains(got, "You are an agent") ||
		!strings.Contains(got, "If you propose it, the operation is Held until a person decides.") || !strings.Contains(got, "within 24h of") {
		t.Fatalf("held for a person, no team named: %s", got)
	}
	held.Floor.Owners = []string{"payments"}
	if got := whoDecides(held, StageSpec{}); !strings.HasPrefix(got, "One person in each of these teams must accept the unknown: payments.") {
		t.Fatalf("accept the unknown: %s", got)
	}
}

func TestAnswerPlan(t *testing.T) {
	a := AnswerPlan(Plan{IntentDigest: "sha256:i", Predicted: PredictProceeds}, ClassHuman, StageSpec{})
	if a.IntentDigest != "sha256:i" || len(a.Next) != 1 {
		t.Fatalf("%+v", a)
	}
}

func TestDeadlineNote(t *testing.T) {
	op := &OperationObject{}
	if AnswerOperation(op).Next != "" {
		t.Fatal("nothing waits on a deadline before a hold")
	}
	op.Status.Phase, op.Status.Times.ApprovalDeadline = PhaseHeld, "2026-10-30T09:00:00Z"
	if got := AnswerOperation(op).Next; got != "If nobody decides by 2026-10-30T09:00:00Z (the approval deadline), the operation concludes "+
		"Expired and nothing changes on the Stage. Propose again to ask again." {
		t.Fatal(got)
	}
	op.Status.Applied = true
	if got := DeadlineNote(op); !strings.Contains(got, "concludes Errored and the Stage freezes on the version it runs") {
		t.Fatal(got)
	}
	op.Status.Applied, op.Status.Question = false, "Approve?"
	if got := Summarize(op); !strings.HasSuffix(got, "waits for a person: Approve? If nobody decides by 2026-10-30T09:00:00Z (the approval deadline), "+
		"the operation concludes Expired and nothing changes on the Stage. Propose again to ask again.") {
		t.Fatal(got)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{24 * time.Hour: "24h", 90 * time.Minute: "1h30m", 15 * time.Minute: "15m", 30 * time.Second: "30s"} {
		if got := duration(d); got != want {
			t.Fatalf("%s: %s", d, got)
		}
	}
}

func TestIntentDiffers(t *testing.T) {
	stage := StageSpec{Service: ServiceRef{Name: "payments"}}
	planned := OperationSpec{Type: TypePromote, Stage: "payments", Params: Params{To: "sha256:b"}}
	want := IntentDigest(planned, stage)
	withReason, withBehalf, withBoth := planned, planned, planned
	withReason.Reason = "ship"
	withBehalf.OnBehalfOf = &Principal{User: "alice"}
	withBoth.Reason, withBoth.OnBehalfOf = "ship", &Principal{User: "alice"}
	for spec, differs := range map[*OperationSpec]string{&withReason: "reason", &withBehalf: "on-behalf-of person", &withBoth: "reason and on-behalf-of person", &planned: ""} {
		if got := IntentDiffers(*spec, stage, want); got != differs {
			t.Fatalf("%+v: %q, want %q", spec, got, differs)
		}
	}
	if got := IntentDiffers(withReason, stage, "sha256:other"); got != "" {
		t.Fatalf("a difference it cannot name: %q", got)
	}
}
