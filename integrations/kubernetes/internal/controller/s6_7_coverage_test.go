/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/observer"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/oci"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------- classifyLoadError ----------

func TestClassifyLoadError_RegistryUnreachable(t *testing.T) {
	err := &oci.RegistryUnreachableError{Ref: "test", Err: errors.New("network timeout")}
	if got := classifyLoadError(err); got != pactov1alpha1.ContractStatusUnknown {
		t.Fatalf("expected Unknown for RegistryUnreachableError, got %q", got)
	}
}

func TestClassifyLoadError_Authentication(t *testing.T) {
	err := &oci.AuthenticationError{Ref: "test", Err: errors.New("401 unauthorized")}
	if got := classifyLoadError(err); got != pactov1alpha1.ContractStatusUnknown {
		t.Fatalf("expected Unknown for AuthenticationError, got %q", got)
	}
}

func TestClassifyLoadError_ArtifactNotFound(t *testing.T) {
	err := &oci.ArtifactNotFoundError{Ref: "test", Err: errors.New("404 not found")}
	if got := classifyLoadError(err); got != pactov1alpha1.ContractStatusUnknown {
		t.Fatalf("expected Unknown for ArtifactNotFoundError, got %q", got)
	}
}

func TestClassifyLoadError_InvalidRef(t *testing.T) {
	err := &oci.InvalidRefError{Ref: "test", Err: errors.New("malformed ref")}
	if got := classifyLoadError(err); got != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("expected Invalid for InvalidRefError, got %q", got)
	}
}

func TestClassifyLoadError_InvalidBundle(t *testing.T) {
	err := &oci.InvalidBundleError{Ref: "test", Err: errors.New("corrupt tar")}
	if got := classifyLoadError(err); got != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("expected Invalid for InvalidBundleError, got %q", got)
	}
}

func TestClassifyLoadError_NoMatchingVersion(t *testing.T) {
	err := &oci.NoMatchingVersionError{Ref: "test", Constraint: "^1.0.0", Err: errors.New("no semver tags")}
	if got := classifyLoadError(err); got != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("expected Invalid for NoMatchingVersionError, got %q", got)
	}
}

func TestClassifyLoadError_WrappedRegistryUnreachable(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", &oci.RegistryUnreachableError{Ref: "test", Err: errors.New("inner")})
	if got := classifyLoadError(wrapped); got != pactov1alpha1.ContractStatusUnknown {
		t.Fatalf("expected Unknown for wrapped RegistryUnreachableError, got %q", got)
	}
}

func TestClassifyLoadError_WrappedInvalidRef(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", &oci.InvalidRefError{Ref: "test", Err: errors.New("bad")})
	if got := classifyLoadError(wrapped); got != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("expected Invalid for wrapped InvalidRefError, got %q", got)
	}
}

func TestClassifyLoadError_GenericError(t *testing.T) {
	err := errors.New("some other error")
	if got := classifyLoadError(err); got != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("expected Invalid (fail-closed) for generic error, got %q", got)
	}
}

// ---------- errorsAsAny ----------

func TestErrorsAsAny_RegistryUnreachableMatch(t *testing.T) {
	err := &oci.RegistryUnreachableError{Ref: "test", Err: errors.New("test")}
	if !errorsAsAny(err, &oci.RegistryUnreachableError{}) {
		t.Fatal("expected match for RegistryUnreachableError")
	}
}

func TestErrorsAsAny_AuthenticationMatch(t *testing.T) {
	err := &oci.AuthenticationError{Ref: "test", Err: errors.New("test")}
	if !errorsAsAny(err, &oci.AuthenticationError{}) {
		t.Fatal("expected match for AuthenticationError")
	}
}

func TestErrorsAsAny_ArtifactNotFoundMatch(t *testing.T) {
	err := &oci.ArtifactNotFoundError{Ref: "test", Err: errors.New("test")}
	if !errorsAsAny(err, &oci.ArtifactNotFoundError{}) {
		t.Fatal("expected match for ArtifactNotFoundError")
	}
}

func TestErrorsAsAny_InvalidRefMatch(t *testing.T) {
	err := &oci.InvalidRefError{Ref: "test", Err: errors.New("test")}
	if !errorsAsAny(err, &oci.InvalidRefError{}) {
		t.Fatal("expected match for InvalidRefError")
	}
}

func TestErrorsAsAny_InvalidBundleMatch(t *testing.T) {
	err := &oci.InvalidBundleError{Ref: "test", Err: errors.New("test")}
	if !errorsAsAny(err, &oci.InvalidBundleError{}) {
		t.Fatal("expected match for InvalidBundleError")
	}
}

func TestErrorsAsAny_NoMatchingVersionMatch(t *testing.T) {
	err := &oci.NoMatchingVersionError{Ref: "test", Constraint: "^1.0.0", Err: errors.New("test")}
	if !errorsAsAny(err, &oci.NoMatchingVersionError{}) {
		t.Fatal("expected match for NoMatchingVersionError")
	}
}

func TestErrorsAsAny_NoMatch(t *testing.T) {
	err := errors.New("generic")
	if errorsAsAny(err, &oci.RegistryUnreachableError{}) {
		t.Fatal("expected no match for generic error")
	}
}

func TestErrorsAsAny_MultipleTargets(t *testing.T) {
	err := &oci.InvalidRefError{Ref: "test", Err: errors.New("test")}
	if !errorsAsAny(err, &oci.RegistryUnreachableError{}, &oci.InvalidRefError{}) {
		t.Fatal("expected match in multi-target check")
	}
}

func TestErrorsAsAny_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", &oci.AuthenticationError{Ref: "test", Err: errors.New("inner")})
	if !errorsAsAny(wrapped, &oci.AuthenticationError{}) {
		t.Fatal("expected match for wrapped error")
	}
}

// ---------- applyObservationWindowUpdates ----------

func TestApplyObservationWindowUpdates_InsertNew(t *testing.T) {
	r := newReconciler()
	pacto := &pactov1alpha1.Pacto{}
	now := metav1.Now()
	updates := []observer.ObservationWindowUpdate{
		{Kind: "check", Subject: "foo", FirstObservedNegativeAt: &now},
	}

	r.applyObservationWindowUpdates(pacto, updates, map[string]bool{"check/foo": true})

	if len(pacto.Status.ObservationWindows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(pacto.Status.ObservationWindows))
	}
	w := pacto.Status.ObservationWindows[0]
	if w.Kind != "check" || w.Subject != "foo" {
		t.Fatalf("unexpected window: %+v", w)
	}
	if w.FirstObservedNegativeAt != now {
		t.Fatalf("expected timestamp %v, got %v", now, w.FirstObservedNegativeAt)
	}
}

func TestApplyObservationWindowUpdates_UpdateExisting(t *testing.T) {
	r := newReconciler()
	old := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	pacto := &pactov1alpha1.Pacto{
		Status: pactov1alpha1.PactoStatus{
			ObservationWindows: []pactov1alpha1.ObservationWindow{
				{Kind: "check", Subject: "foo", FirstObservedNegativeAt: old},
			},
		},
	}
	now := metav1.Now()
	updates := []observer.ObservationWindowUpdate{
		{Kind: "check", Subject: "foo", FirstObservedNegativeAt: &now},
	}

	r.applyObservationWindowUpdates(pacto, updates, map[string]bool{"check/foo": true})

	if len(pacto.Status.ObservationWindows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(pacto.Status.ObservationWindows))
	}
	w := pacto.Status.ObservationWindows[0]
	if w.FirstObservedNegativeAt != now {
		t.Fatalf("expected updated timestamp %v, got %v", now, w.FirstObservedNegativeAt)
	}
}

func TestApplyObservationWindowUpdates_RemoveEntry(t *testing.T) {
	r := newReconciler()
	old := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	pacto := &pactov1alpha1.Pacto{
		Status: pactov1alpha1.PactoStatus{
			ObservationWindows: []pactov1alpha1.ObservationWindow{
				{Kind: "check", Subject: "foo", FirstObservedNegativeAt: old},
			},
		},
	}
	updates := []observer.ObservationWindowUpdate{
		{Kind: "check", Subject: "foo", FirstObservedNegativeAt: nil}, // reset
	}

	r.applyObservationWindowUpdates(pacto, updates, map[string]bool{"check/foo": true})

	if len(pacto.Status.ObservationWindows) != 0 {
		t.Fatalf("expected 0 windows after reset, got %d", len(pacto.Status.ObservationWindows))
	}
}

func TestApplyObservationWindowUpdates_MultipleUpdates(t *testing.T) {
	r := newReconciler()
	old := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	pacto := &pactov1alpha1.Pacto{
		Status: pactov1alpha1.PactoStatus{
			ObservationWindows: []pactov1alpha1.ObservationWindow{
				{Kind: "check", Subject: "existing", FirstObservedNegativeAt: old},
			},
		},
	}
	now := metav1.Now()
	updates := []observer.ObservationWindowUpdate{
		{Kind: "check", Subject: "new", FirstObservedNegativeAt: &now},
		{Kind: "check", Subject: "existing", FirstObservedNegativeAt: &now},
	}

	r.applyObservationWindowUpdates(pacto, updates, map[string]bool{"check/existing": true, "check/new": true})

	if len(pacto.Status.ObservationWindows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(pacto.Status.ObservationWindows))
	}

	found := make(map[string]metav1.Time)
	for _, w := range pacto.Status.ObservationWindows {
		found[w.Subject] = w.FirstObservedNegativeAt
	}
	if _, ok := found["existing"]; !ok {
		t.Fatal("expected 'existing' window")
	}
	if _, ok := found["new"]; !ok {
		t.Fatal("expected 'new' window")
	}
	if found["existing"] != now {
		t.Fatal("expected 'existing' to be updated")
	}
	if found["new"] != now {
		t.Fatal("expected 'new' to have correct timestamp")
	}
}

// TestApplyObservationWindowUpdates_EmptyInput proves a still-declared window with no update this cycle is
// preserved (e.g. a COLLECTION_FAILED cycle emits no update, so the stabilization clock must not be lost).
func TestApplyObservationWindowUpdates_EmptyInput(t *testing.T) {
	r := newReconciler()
	old := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	pacto := &pactov1alpha1.Pacto{
		Status: pactov1alpha1.PactoStatus{
			ObservationWindows: []pactov1alpha1.ObservationWindow{
				{Kind: "check", Subject: "existing", FirstObservedNegativeAt: old},
			},
		},
	}

	r.applyObservationWindowUpdates(pacto, nil, map[string]bool{"check/existing": true})

	if len(pacto.Status.ObservationWindows) != 1 {
		t.Fatalf("expected 1 window to remain, got %d", len(pacto.Status.ObservationWindows))
	}
}

// TestApplyObservationWindowUpdates_PrunesUndeclared proves that when an assertion is removed from the
// contract (its key is absent from declaredKeys), its stale window is pruned even with no update this
// cycle (spec section 9.5) — otherwise a re-added still-negative assertion would fire a premature
// confirmed-negative and status would grow unbounded under churn.
func TestApplyObservationWindowUpdates_PrunesUndeclared(t *testing.T) {
	r := newReconciler()
	old := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	pacto := &pactov1alpha1.Pacto{
		Status: pactov1alpha1.PactoStatus{
			ObservationWindows: []pactov1alpha1.ObservationWindow{
				{Kind: "dependency", Subject: "db", FirstObservedNegativeAt: old},
				{Kind: "dependency", Subject: "cache", FirstObservedNegativeAt: old},
			},
		},
	}

	// "db" was removed from the contract; only "cache" is still declared.
	r.applyObservationWindowUpdates(pacto, nil, map[string]bool{"dependency/cache": true})

	if len(pacto.Status.ObservationWindows) != 1 {
		t.Fatalf("expected 1 window after pruning removed assertion, got %d", len(pacto.Status.ObservationWindows))
	}
	if pacto.Status.ObservationWindows[0].Subject != "cache" {
		t.Fatalf("expected the still-declared 'cache' window to survive, got %q", pacto.Status.ObservationWindows[0].Subject)
	}
}

// TestDeclaredWindowKeys proves the key set covers every windowing dimension of the effective contract.
func TestDeclaredWindowKeys(t *testing.T) {
	c := &contract.Contract{
		Interfaces:     []contract.Interface{{Name: "api"}},
		Dependencies:   []contract.Dependency{{Name: "payments"}},
		Capabilities:   []contract.Capability{{Type: contract.CapabilityHealth}},
		Configurations: []contract.Configuration{{Name: "appcfg"}},
	}
	keys := declaredWindowKeys(c)
	for _, want := range []string{"interface/api", "dependency/payments", "capability/health", "configuration/appcfg"} {
		if !keys[want] {
			t.Errorf("declaredWindowKeys missing %q; got %v", want, keys)
		}
	}
}

// ---------- failReconciliation status branches ----------

func TestFailReconciliation_UnknownStatus(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	r := newReconciler(pacto)

	_, err := r.failReconciliation(context.Background(), pacto, "contract unavailable", nil, nil, pactov1alpha1.ContractStatusUnknown)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cond := findCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
	if cond == nil || cond.Status != metav1.ConditionUnknown {
		t.Fatal("expected ContractValid=Unknown condition")
	}
	if cond.Reason != pactov1alpha1.ReasonContractUnavailable {
		t.Fatalf("expected ReasonContractUnavailable, got %s", cond.Reason)
	}
	if pacto.Status.Summary == nil || pacto.Status.Summary.UnknownCount != 1 {
		t.Fatal("expected Summary.UnknownCount=1")
	}
	if pacto.Status.ContractStatus != pactov1alpha1.ContractStatusUnknown {
		t.Fatalf("expected ContractStatus=Unknown, got %s", pacto.Status.ContractStatus)
	}
}

func TestFailReconciliation_InvalidStatus(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	r := newReconciler(pacto)

	_, err := r.failReconciliation(context.Background(), pacto, "contract invalid", nil, nil, pactov1alpha1.ContractStatusInvalid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cond := findCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatal("expected ContractValid=False condition")
	}
	if cond.Reason != pactov1alpha1.ReasonContractInvalid {
		t.Fatalf("expected ReasonContractInvalid, got %s", cond.Reason)
	}
	if pacto.Status.Summary == nil || pacto.Status.Summary.ErrorCount != 1 {
		t.Fatal("expected Summary.ErrorCount=1")
	}
	if pacto.Status.ContractStatus != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("expected ContractStatus=Invalid, got %s", pacto.Status.ContractStatus)
	}
}

func TestFailReconciliation_UnexpectedStatusFallback(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
	}
	r := newReconciler(pacto)

	_, err := r.failReconciliation(context.Background(), pacto, "unexpected", nil, nil, "unexpected-status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should fall back to Invalid
	if pacto.Status.ContractStatus != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("expected fallback to Invalid, got %s", pacto.Status.ContractStatus)
	}
	cond := findCondition(pacto.Status.Conditions, pactov1alpha1.ConditionContractValid)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatal("expected ContractValid=False condition")
	}
	if pacto.Status.Summary == nil || pacto.Status.Summary.ErrorCount != 1 {
		t.Fatal("expected Summary.ErrorCount=1 in fallback")
	}
}

// findCondition is a test helper to locate a condition by type.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
