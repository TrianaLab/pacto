package cli

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

func TestLifecycleText(t *testing.T) {
	l := &delivery.Lifecycle{Listing: delivery.Listing{AsOf: "2026-11-08T10:00:00Z", Completeness: "partial", Unreadable: []string{"staging/x: forbidden"}},
		Service: "payments", Version: "3.7.4", Digest: "sha256:ab12", Contract: "oci://ghcr.io/acme/payments@sha256:cc",
		SourceRevision: "4f2c1e0aa", Signature: delivery.SignatureSigned, SignedOn: []string{"test/payments"}, Note: "n",
		Stages: []delivery.LifecycleStage{
			{Namespace: "test", Stage: "payments", Environment: "test", Running: true, Verified: "yes", Failed: "no", Source: delivery.SourceRecords,
				RecordsVerified: true, Completeness: "complete", LastCheck: &delivery.Recheck{At: "2026-11-08T09:59:02Z",
					Verdicts: []delivery.Verdict{{Gate: "health", Result: delivery.Pass}}},
				Events: []delivery.LifecycleEvent{{Role: "arrived", HistoryEntry: delivery.HistoryEntry{Applied: "2026-11-07T14:02:11Z",
					Type: delivery.TypePromote, Operation: "payments-promote-x7", Outcome: delivery.Succeeded, Verification: delivery.Verified,
					Actor: "agent-a", ActorClass: delivery.ClassAgent, Record: "sha256:9f1c"}}}},
			{Namespace: "prod", Stage: "payments", Environment: "prod", Verified: "no", Failed: "no", Source: delivery.SourceRecords, Completeness: "complete"},
			{Namespace: "staging", Stage: "payments", Environment: "staging", Verified: "unknown", Failed: "unknown", Source: delivery.SourceStatus, Completeness: "partial"},
			{Namespace: "qa", Stage: "payments", Environment: "qa", Verified: "unknown", Failed: "unknown", Source: delivery.SourceStatus, Completeness: "partial",
				Events: []delivery.LifecycleEvent{{Role: "attempted", HistoryEntry: delivery.HistoryEntry{Since: "2026-11-08T08:00:00Z",
					Type: delivery.TypePromote, Operation: "held", Phase: delivery.PhaseHeld, Actor: "bob"}}}},
		}}
	var w strings.Builder
	printLifecycle(&w, l)
	want := "payments 3.7.4 sha256:ab12, contract oci://ghcr.io/acme/payments@sha256:cc, source revision 4f2c1e0 (an annotation, not signed)\n" +
		"signature: signed (published on test/payments)\n" +
		"test/payments (test): running, verified, records verified\n" +
		"  2026-11-07T14:02:11Z  arrived  promote  payments-promote-x7  Succeeded/Verified  agent-a (agent)  sha256:9f1c\n" +
		"  re-check 2026-11-08T09:59:02Z: Pass\n" +
		"prod/payments (prod): never reached\n" +
		"staging/payments (staging): not seen in the history read (partial)\n" +
		"qa/payments (qa): verified: unknown, failed: unknown, from the Operations' status, history partial\n" +
		"  2026-11-08T08:00:00Z  attempted  promote  held  Held  bob  -\n" +
		"lines with record - are read from the Operation's status: their cause, correlation and applied time are not signed\n"
	if !strings.HasPrefix(w.String(), want) || !strings.Contains(w.String(), "staging/x: forbidden") ||
		!strings.HasSuffix(w.String(), "as of 2026-11-08T10:00:00Z, partial: n\n") {
		t.Fatalf("lifecycle text:\n%s\nwant prefix\n%s", w.String(), want)
	}
	for sig, want := range map[string]string{delivery.SignatureUnsigned: "signature: unsigned (an entry Stage holds it unpublished: test/payments)\n",
		delivery.SignatureUnknown: "signature: unknown\n"} {
		w.Reset()
		printLifecycle(&w, &delivery.Lifecycle{Digest: "sha256:ab12", Signature: sig, UnsignedOn: []string{"test/payments"},
			Stages: []delivery.LifecycleStage{{Namespace: "n", Stage: "s", Verified: "no", Failed: "yes", Offered: true, Source: delivery.SourceRecords, Completeness: "complete"}}})
		if !strings.Contains(w.String(), want) || !strings.Contains(w.String(), "n/s (): not verified, failed, offered downstream, records not verified\n") {
			t.Errorf("%s:\n%s", sig, w.String())
		}
	}
}

func TestDeliveryLifecycleCommand(t *testing.T) {
	checkDeliveryRuns(t, deliveryDeps("alice", nil), []deliveryRun{
		{"text", []string{"lifecycle", "--service", "payments", "--digest", cliA}, false, []string{"payments 1.0.0 " + cliA, "prod/payments-prod (): running, verified, failed: unknown"}},
		{"json", []string{"lifecycle", "--service", "payments", "--digest", cliA, "-n", "prod"}, false, []string{`"digest": "` + cliA + `"`, `"stages": [`}},
		{"text", []string{"lifecycle", "--digest", cliA}, true, []string{`required flag(s) "service" not set`}},
		{"text", []string{"lifecycle", "--service", "payments"}, true, []string{"pass exactly one of digest, version or contract"}},
	})
}
