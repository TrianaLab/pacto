/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
)

// An inline contract that does not declare its name never reaches the observers:
// the loader refuses it and ContractValid is False with reason ContractUndeclared.
func TestReconcile_UndeclaredInlineContract(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "nameless", Namespace: "default"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{Inline: "pactoVersion: \"2.5\"\nservice:\n  version: 1.0.0\n"},
		},
	}
	r := newReconciler(pacto)
	r.Loader = loader.New()
	if _, err := r.Reconcile(context.Background(), reconcileReq("nameless", "default")); err != nil {
		t.Fatal(err)
	}
	got := getPacto(t, r, "nameless", "default")
	cond := findCondition(got.Status.Conditions, pactov1alpha1.ConditionContractValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != pactov1alpha1.ReasonContractUndeclared {
		t.Fatalf("ContractValid = %+v, want False/ContractUndeclared", cond)
	}
	if got.Status.ContractStatus != pactov1alpha1.ContractStatusInvalid {
		t.Fatalf("contract status %s", got.Status.ContractStatus)
	}
}
