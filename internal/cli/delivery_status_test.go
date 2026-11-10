package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// statusDeps is deliveryDeps with the Stage's status fields set.
func statusDeps(fields map[string]any) deps {
	d := deliveryDeps("alice", nil)
	plain := d.newDynamic
	d.newDynamic = func() (dynamic.Interface, error) {
		c, err := plain()
		stages := c.Resource(schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}).Namespace("prod")
		s, _ := stages.Get(context.Background(), "payments-prod", metav1.GetOptions{})
		for k, v := range fields {
			_ = unstructured.SetNestedField(s.Object, v, "status", k)
		}
		_, _ = stages.Update(context.Background(), s, metav1.UpdateOptions{})
		return c, err
	}
	return d
}

func TestDeliveryStatusText(t *testing.T) {
	notReady := statusDeps(map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False", "reason": "CandidateUnverified", "message": "no signature"}}})
	checkDeliveryRuns(t, notReady, []deliveryRun{
		{"text", []string{"status", "--service", "payments"}, false, []string{
			"payments: 1 Stage you can see, as of ", ", complete\n",
			"STAGE               ENVIRONMENT  UPSTREAM                        RUNS   STATE           CANDIDATE  LAST CHECK                          LEVEL       YOU MAY\n",
			"prod/payments-prod               artifact payments (unverified)  1.0.0  frozen, 1 held  -          Unknown 2026-10-20T10:00:00Z stale  autonomous  -\n",
			"not offered:\n  prod/payments-prod promote: Pacto could not read the delivery identity policy (",
			"  prod/payments-prod undo: no change Pacto could read moved the Stage to ",
			"not ready:\n  prod/payments-prod CandidateUnverified: no signature\n    fix: ",
			"the Stages of payments you can see, in promotion order, from each Stage's spec.candidates\n"}},
		{"text", []string{"status"}, true, []string{`required flag(s) "service" not set`}},
		{"text", []string{"status", "--service="}, true, []string{"name the service: pacto delivery status --service <name>"}},
	})
}

func TestDeliveryStatusJSON(t *testing.T) {
	checkDeliveryRuns(t, deliveryDeps("alice", nil), []deliveryRun{
		{"json", []string{"status", "--service", "payments"}, false, []string{`"service": "payments"`, `"state": "frozen"`, `"upstream": {`, `"kind": "artifact"`, `"completeness": "complete"`}},
	})
}

func TestDeliveryStatusWhy(t *testing.T) {
	ready := statusDeps(map[string]any{"candidate": cliB, "conditions": []any{map[string]any{"type": "Ready", "status": "True", "reason": "Ready"}}})
	checkDeliveryRuns(t, ready, []deliveryRun{
		{"text", []string{"status", "--service", "payments", "--why"}, false, []string{
			"== prod/payments-prod: ", "\n  Frozen: ", "  automatic promote: ", "\n  predicted: Waits\n"}},
	})
}

func TestDeliveryStatusNoCluster(t *testing.T) {
	checkDeliveryRuns(t, deliveryDeps("alice", errors.New("no kubeconfig")), []deliveryRun{
		{"text", []string{"status", "--service", "payments"}, true, []string{"no kubeconfig"}},
	})
}

// TestPrintProgressFix: a reason's fix is printed under it.
func TestPrintProgressFix(t *testing.T) {
	var b strings.Builder
	printProgress(&b, delivery.StageRow{Namespace: "n", Stage: "s", Progress: &delivery.Progress{State: "blocked", Summary: "sum",
		Reasons: []delivery.ProgressReason{{Code: "Frozen", Message: "frozen", Fix: "pacto delivery propose --type unfreeze"}}, AutoPromote: "frozen"}})
	if want := "== n/s: blocked\n  sum\n  Frozen: frozen\n    fix: pacto delivery propose --type unfreeze\n  automatic promote: frozen\n"; b.String() != want {
		t.Fatalf("got %q, want %q", b.String(), want)
	}
}

func TestDeliveryStatusNamespaceFlag(t *testing.T) {
	checkDeliveryRuns(t, deliveryDeps("alice", nil), []deliveryRun{
		{"text", []string{"status", "--service", "payments", "-n", "prod"}, false, []string{"payments: 1 Stage you can see"}},
		{"text", []string{"status", "--service", "payments", "-n", "elsewhere"}, false, []string{"payments: 0 Stages you can see"}},
	})
}

// TestDeliveryStatusCells: each cell formats what the row carries.
func TestDeliveryStatusCells(t *testing.T) {
	cases := map[string]string{
		upstreamCell(nil): "-",
		upstreamCell(&delivery.Upstream{Kind: "stage", Namespace: "s", Stage: "p", Problem: "missing"}): "stage s/p (missing)",
		upstreamCell(&delivery.Upstream{Kind: "records", Stream: "c/n/s", Trust: "trusted"}):            "records c/n/s (trusted)",
		upstreamCell(&delivery.Upstream{Kind: "none"}):                                                  "none",
		checkCell(nil): "-",
		checkCell(&delivery.CheckSummary{Result: delivery.Pass, At: "t", Current: true,
			Verdicts: []delivery.Verdict{{Gate: delivery.GateDiff, Result: delivery.Pass}}}): "Checked t",
		checkCell(&delivery.CheckSummary{Result: delivery.Fail, At: "t"}): "Fail t stale",
		stateCell(delivery.StageRow{State: "unknown", HeldUnknown: true}): "not known: its operations could not be read, ? held",
		stateCell(delivery.StageRow{State: "waiting", Held: 2}):           "waiting for a person, 2 held",
		versionCell("", ""): "-",
		runsCell(delivery.StageRow{Version: "1.0.0", SourceRevision: "0123456789abcdef"}): "1.0.0 (0123456)",
		runsCell(delivery.StageRow{Pointer: cliB}):                                        cliB[:19],
		versionCell("", cliB): cliB[:19],
		offersCell([]delivery.Offer{{Action: "promote", Available: true}, {Action: "freeze", Available: true}}): "promote, freeze",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if !strings.HasPrefix(cliB, "sha256:") {
		t.Fatal(cliB)
	}
}
