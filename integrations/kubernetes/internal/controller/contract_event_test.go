/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
)

// ---------- contractStatusDegraded ----------

func TestContractStatusDegraded(t *testing.T) {
	cases := []struct {
		status string
		want   bool
	}{
		// Never reconciled: not degraded, so the first evaluation still fires an event.
		{"", false},
		{pactov1alpha1.ContractStatusCompliant, false},
		{pactov1alpha1.ContractStatusReference, false},
		{pactov1alpha1.ContractStatusWarning, true},
		{pactov1alpha1.ContractStatusNonCompliant, true},
		{pactov1alpha1.ContractStatusUnknown, true},
		{pactov1alpha1.ContractStatusInvalid, true},
		{pactov1alpha1.ContractStatusNotEvaluated, true},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			if got := contractStatusDegraded(tc.status); got != tc.want {
				t.Errorf("contractStatusDegraded(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// ---------- contractStatusMessage ----------

func TestContractStatusMessage(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{}
	pacto.Status.ContractStatus = pactov1alpha1.ContractStatusNonCompliant
	pacto.Status.Summary = &pactov1alpha1.Summary{ErrorCount: 2, WarningCount: 3}

	if got, want := contractStatusMessage(pacto), "ContractStatus: NonCompliant, 2 errors, 3 warnings"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestContractStatusMessage_NilSummary(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{}
	pacto.Status.ContractStatus = pactov1alpha1.ContractStatusUnknown

	// A missing summary must still yield a message, not a dropped event.
	if got, want := contractStatusMessage(pacto), "ContractStatus: Unknown, 0 errors, 0 warnings"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------- emitContractStatusEvent ----------

func newEventReconciler() (*PactoReconciler, *record.FakeRecorder) {
	rec := record.NewFakeRecorder(20)
	return &PactoReconciler{Recorder: rec}, rec
}

func TestEmitContractStatusEvent(t *testing.T) {
	cases := []struct {
		name       string
		prev       string
		current    string
		wantEvents int
		wantSubstr string
	}{
		{
			name: "first evaluation degraded emits warning", prev: "",
			current: pactov1alpha1.ContractStatusNonCompliant, wantEvents: 1,
			wantSubstr: "Warning " + pactov1alpha1.EventValidationFailed,
		},
		{
			name: "steady degraded is silent", prev: pactov1alpha1.ContractStatusNonCompliant,
			current: pactov1alpha1.ContractStatusNonCompliant, wantEvents: 0,
		},
		{
			name: "escalation between degraded states emits warning", prev: pactov1alpha1.ContractStatusWarning,
			current: pactov1alpha1.ContractStatusNonCompliant, wantEvents: 1,
			wantSubstr: "Warning " + pactov1alpha1.EventValidationFailed,
		},
		{
			name: "recovery to compliant emits normal", prev: pactov1alpha1.ContractStatusNonCompliant,
			current: pactov1alpha1.ContractStatusCompliant, wantEvents: 1,
			wantSubstr: "Normal " + pactov1alpha1.EventContractRecovered,
		},
		{
			name: "recovery to reference emits normal", prev: pactov1alpha1.ContractStatusInvalid,
			current: pactov1alpha1.ContractStatusReference, wantEvents: 1,
			wantSubstr: "Normal " + pactov1alpha1.EventContractRecovered,
		},
		{
			name: "steady compliant is silent", prev: pactov1alpha1.ContractStatusCompliant,
			current: pactov1alpha1.ContractStatusCompliant, wantEvents: 0,
		},
		{
			name: "first evaluation compliant is silent", prev: "",
			current: pactov1alpha1.ContractStatusCompliant, wantEvents: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, rec := newEventReconciler()
			pacto := &pactov1alpha1.Pacto{}
			pacto.Status.ContractStatus = tc.current

			r.emitContractStatusEvent(pacto, tc.prev, pactov1alpha1.EventValidationFailed, "msg")

			events := drainEvents(rec)
			if len(events) != tc.wantEvents {
				t.Fatalf("expected %d events, got %v", tc.wantEvents, events)
			}
			if tc.wantSubstr != "" && !strings.Contains(events[0], tc.wantSubstr) {
				t.Errorf("expected event containing %q, got %q", tc.wantSubstr, events[0])
			}
		})
	}
}

// ---------- terminal paths thread the previous status ----------

// A steady non-compliant reconciliation must not re-emit ValidationFailed: the
// controller watches workloads, so an unguarded event fires once per rollout step.
func TestFinishReconciliation_SteadyDegradedIsSilent(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "steady", Namespace: "default"},
	}
	r := newReconciler(pacto)
	rec := record.NewFakeRecorder(20)
	r.Recorder = rec

	pacto.Status.ContractStatus = pactov1alpha1.ContractStatusNonCompliant
	pacto.Status.Summary = &pactov1alpha1.Summary{ErrorCount: 1}

	if _, err := r.finishReconciliation(context.Background(), pacto, pactov1alpha1.ContractStatusNonCompliant); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no event for steady NonCompliant, got %v", events)
	}
}

// Coming back to Compliant from a degraded status emits the recovery event.
func TestFinishReconciliation_RecoveryEvent(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "recovered", Namespace: "default"},
	}
	r := newReconciler(pacto)
	rec := record.NewFakeRecorder(20)
	r.Recorder = rec

	pacto.Status.ContractStatus = pactov1alpha1.ContractStatusCompliant
	pacto.Status.Summary = &pactov1alpha1.Summary{}

	if _, err := r.finishReconciliation(context.Background(), pacto, pactov1alpha1.ContractStatusNonCompliant); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], pactov1alpha1.EventContractRecovered) {
		t.Errorf("expected one ContractRecovered event, got %v", events)
	}
}

// failReconciliation is guarded by the same helper: a registry that stays
// unreachable emits ContractUnavailable once, not once per requeue.
func TestFailReconciliation_SteadyUnknownIsSilent(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "steady-unknown", Namespace: "default"},
	}
	r := newReconciler(pacto)
	rec := record.NewFakeRecorder(20)
	r.Recorder = rec

	_, err := r.failReconciliation(context.Background(), pacto, "registry unreachable", nil,
		pactov1alpha1.ContractStatusUnknown, pactov1alpha1.ContractStatusUnknown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if events := drainEvents(rec); len(events) != 0 {
		t.Errorf("expected no event for steady Unknown, got %v", events)
	}
}

func TestFailReconciliation_FirstFailureEmitsWarning(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "first-fail", Namespace: "default"},
	}
	r := newReconciler(pacto)
	rec := record.NewFakeRecorder(20)
	r.Recorder = rec

	_, err := r.failReconciliation(context.Background(), pacto, "bad bundle", nil,
		pactov1alpha1.ContractStatusCompliant, pactov1alpha1.ContractStatusInvalid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	events := drainEvents(rec)
	if len(events) != 1 || !strings.Contains(events[0], pactov1alpha1.EventContractInvalid) {
		t.Errorf("expected one ContractInvalid event, got %v", events)
	}
}
