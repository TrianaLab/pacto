package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func opFixture(name string, spec, status map[string]any) runtime.Object {
	return obj("delivery.pacto.trianalab.io/v1alpha1", "Operation", "prod", name, map[string]any{"spec": spec, "status": status})
}

func TestDeliveryExplain(t *testing.T) {
	failed := opFixture("p1", map[string]any{"type": delivery.TypePromote, "stage": "payments-prod",
		"params": map[string]any{"from": dlA, "to": dlB}, "actor": map[string]any{"user": "system:serviceaccount:pacto-delivery:promoter"}},
		map[string]any{"phase": "Concluded", "outcome": "Failed", "reason": "VerifyFailed", "message": "health: 10 of 100 spans failed", "seq": int64(3),
			"verdicts": []any{
				map[string]any{"gate": "pacto.diff", "phase": "pre", "result": "Pass"},
				map[string]any{"gate": "health", "phase": "verify", "result": "Fail", "citation": map[string]any{"provider": "mira", "query": "{}"}},
				map[string]any{"gate": "claims", "phase": "verify", "result": "Pass", "citation": map[string]any{"provider": "pacto.runtime"}},
				map[string]any{"gate": "p99", "phase": "verify", "result": "Pass", "citation": map[string]any{"provider": "dd"}},
				map[string]any{"gate": "x", "phase": "verify", "result": "Unknown"},
			}})
	held := opFixture("h1", map[string]any{"type": delivery.TypePromote, "stage": "gone"},
		map[string]any{"phase": "Held", "question": "Accept the unknown?"})
	d, _, _ := deliveryFixture(t, "alice", nil, nil, failed, held)
	e, err := d.Explain(context.Background(), "prod", "p1")
	if err != nil {
		t.Fatal(err)
	}
	if e.Operation != "prod/p1" || e.Status.Outcome != delivery.Failed || e.Stage.Pointer != dlA || len(e.Verdicts) != 5 ||
		!strings.Contains(e.Summary, "concluded Failed: VerifyFailed: health: 10 of 100") {
		t.Fatalf("explain: %+v", e)
	}
	for i, want := range []string{"pacto delivery plan", "/api/v1/query of the mira provider", "read contractStatus", "to the dd provider", "no citation"} {
		if !strings.Contains(e.Verdicts[i].Rerun, want) {
			t.Errorf("verdict %d rerun %q, want %q", i, e.Verdicts[i].Rerun, want)
		}
	}
	e, err = d.Explain(context.Background(), "prod", "h1")
	if err != nil || e.Stage.Name != "gone" || e.Stage.Pointer != "" || !strings.Contains(e.Summary, "waits for a person: Accept the unknown?") {
		t.Fatalf("held, Stage gone: %+v %v", e, err)
	}
	if _, err := d.Explain(context.Background(), "prod", "missing"); err == nil {
		t.Fatal("missing operation")
	}
	for _, c := range []struct {
		status map[string]any
		want   string
	}{{map[string]any{}, "is not admitted yet"}, {map[string]any{"phase": "Verifying", "message": "soaking"}, "is Verifying: soaking"},
		{map[string]any{"phase": "Running"}, "is Running: in progress"}} {
		if got := summarize(&delivery.OperationObject{Status: decodeStatus(t, c.status)}); !strings.Contains(got, c.want) {
			t.Errorf("summary %q, want %q", got, c.want)
		}
	}
}

func decodeStatus(t *testing.T, m map[string]any) delivery.OperationStatus {
	t.Helper()
	s := delivery.OperationStatus{}
	if p, ok := m["phase"].(string); ok {
		s.Phase = delivery.Phase(p)
	}
	if msg, ok := m["message"].(string); ok {
		s.Message = msg
	}
	return s
}

func TestDeliveryHistory(t *testing.T) {
	spec := func(stage, to string) map[string]any {
		return map[string]any{"type": delivery.TypePromote, "stage": stage, "params": map[string]any{"from": dlA, "to": to},
			"actor": map[string]any{"user": "bot"}}
	}
	d, c, _ := deliveryFixture(t, "alice", nil, nil,
		opFixture("second", spec("payments-prod", dlB), map[string]any{"phase": "Concluded", "outcome": "Succeeded", "seq": int64(2)}),
		opFixture("live", spec("payments-prod", dlB), map[string]any{"phase": "Verifying"}),
		opFixture("first", spec("payments-prod", dlA), map[string]any{"phase": "Concluded", "outcome": "Rejected", "seq": int64(1)}),
		opFixture("elsewhere", spec("payments-test", dlB), map[string]any{"phase": "Concluded", "seq": int64(9)}))
	h, err := d.History(context.Background(), "prod", "payments-prod")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range h.Entries {
		names = append(names, e.Operation)
	}
	if strings.Join(names, ",") != "first,second,live" || h.Completeness != "partial" || h.AsOf == "" || h.Stage.Pointer != dlA ||
		!strings.Contains(h.Note, "deleted") || h.Entries[1].To != dlB || h.Entries[1].Actor != "bot" {
		t.Fatalf("history: %+v", h)
	}
	if _, err := d.History(context.Background(), "prod", "missing"); err == nil {
		t.Fatal("missing Stage")
	}
	c.PrependReactor("list", "operations", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	_, err = d.History(context.Background(), "prod", "payments-prod")
	wantErrContaining(t, err, "listing Operations in prod: forbidden")
}

func TestDeliveryProposeCarriesTheCause(t *testing.T) {
	d, _, _ := deliveryFixture(t, "system:serviceaccount:agents:bot", nil, nil)
	cause := &delivery.Cause{Verdict: &delivery.Verdict{Gate: "health", Phase: "verify", Result: delivery.Fail, Citation: map[string]string{"input": "{}"}}}
	op, _, err := d.Propose(context.Background(), delivery.Request{Namespace: "prod", Stage: "payments-prod", To: dlB, Name: "p", Cause: cause})
	if err != nil || op.Spec.Cause == nil || op.Spec.Cause.Verdict.Gate != "health" || op.Spec.Cause.Verdict.Citation["input"] != "{}" {
		t.Fatalf("cause: %+v %v", op, err)
	}
}
