package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
		{"text", []string{"status", "--service="}, true, []string{"name the service"}},
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
			"== prod/payments-prod: ", "\n  Frozen: ", "  automatic promote: ", "\n  predicted: Held\n"}},
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
	b.Reset()
	printProgress(&b, delivery.StageRow{Namespace: "n", Stage: "s", Progress: &delivery.Progress{State: "blocked", Summary: "sum", Completeness: "partial"}})
	if !strings.HasPrefix(b.String(), "== n/s: blocked (partial: a reason may be missing)\n") {
		t.Fatalf("a partial answer says so: %q", b.String())
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

// TestDeliveryStatusApplierLine: one applier line when every Stage says the
// same, one line per Stage when they differ, a reason in words when a Stage
// does not adopt its applier, a stale warning when the promoter stopped
// checking, and the field in JSON.
func TestDeliveryStatusApplierLine(t *testing.T) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	argo := func(asOf string) *delivery.ApplierStatus {
		return &delivery.ApplierStatus{Manager: "argocd", State: delivery.ApplierAdopted, AsOf: asOf,
			Display: "Argo CD (Application payments, tracking annotation)"}
	}
	rows := []delivery.StageRow{{Namespace: "dev", Stage: "p-dev", Applier: argo("2026-11-15T09:59:30Z")},
		{Namespace: "prod", Stage: "p-prod", Applier: argo("2026-11-15T09:59:00Z")}}
	var b strings.Builder
	printApplier(&b, rows, now)
	if want := "applier: Argo CD (Application payments, tracking annotation) (as of 2026-11-15T09:59:00Z)\n"; b.String() != want {
		t.Fatalf("uniform: got %q, want %q", b.String(), want)
	}
	rows[1].Applier = &delivery.ApplierStatus{Manager: "helm", State: delivery.ApplierConflict, AsOf: "2026-11-15T09:50:00Z",
		Display: "Helm (release payments, release annotations)", Message: "Helm release payments owns Deployment payments"}
	rows = append(rows, delivery.StageRow{Namespace: "qa", Stage: "p-qa"})
	b.Reset()
	printApplier(&b, rows, now)
	if want := "applier:\n" +
		"  dev/p-dev: Argo CD (Application payments, tracking annotation) (as of 2026-11-15T09:59:30Z)\n" +
		"  prod/p-prod: Helm (release payments, release annotations), conflict: Helm release payments owns Deployment payments " +
		"(as of 2026-11-15T09:50:00Z, stale: the promoter has not checked since)\n" +
		"  qa/p-qa: not reported (the promoter predates applier detection)\n"; b.String() != want {
		t.Fatalf("per Stage: got %q, want %q", b.String(), want)
	}
	b.Reset()
	printApplier(&b, []delivery.StageRow{{Namespace: "qa", Stage: "p-qa"}}, now)
	if b.String() != "" {
		t.Fatalf("no Stage reports an applier: %q", b.String())
	}
	withApplier := statusDeps(map[string]any{"applier": map[string]any{"manager": "argocd", "state": "Adopted", "completeness": "complete",
		"display": "Argo CD (Application payments, tracking annotation)", "asOf": "2026-11-15T09:59:00Z"}})
	checkDeliveryRuns(t, withApplier, []deliveryRun{
		{"text", []string{"status", "--service", "payments"}, false, []string{"\napplier: Argo CD (Application payments, tracking annotation) (as of 2026-11-15T09:59:00Z"}},
		{"json", []string{"status", "--service", "payments"}, false, []string{`"applier": {`, `"display": "Argo CD (Application payments, tracking annotation)"`}},
	})
}
