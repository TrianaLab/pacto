/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestReconcile_ObservedCallersCoverage writes the caller coverage with the
// refresh interval a reader judges its age by, and never carries a complete
// coverage from an older evaluation over: here the agent left no data, so the
// earlier complete coverage turns unavailable.
func TestReconcile_ObservedCallersCoverage(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef:          pactov1alpha1.ContractRef{OCI: "oci://r/pactos/payments@" + evaluatedDigest},
			Target:               pactov1alpha1.TargetRef{ServiceName: "pay"},
			CheckIntervalSeconds: 60,
		},
		Status: pactov1alpha1.PactoStatus{ObservedCallersCoverage: &pactov1alpha1.CallerCoverage{State: pactov1alpha1.CallerCoverageComplete}},
	}
	cl := fake.NewClientBuilder().WithScheme(newSchemeWithApps()).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).WithObjects(pacto).Build()
	r := &PactoReconciler{Client: cl, Scheme: cl.Scheme(), Recorder: record.NewFakeRecorder(20),
		KernelEvidence: true, KernelEvidenceNamespace: "pacto-system",
		Loader: &mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract:       &contract.Contract{Service: contract.Service{Name: "payments", Version: "1.0.0"}, Workload: "service"},
				RawYAML:        []byte(validContract),
				ResolvedRef:    "r/pactos/payments@" + evaluatedDigest,
				ResolvedDigest: evaluatedDigest,
			}, nil
		}}}
	if _, err := r.Reconcile(context.Background(), reconcileReq("pay", "default")); err != nil {
		t.Fatal(err)
	}
	got := getPacto(t, r, "pay", "default").Status
	if c := got.ObservedCallersCoverage; c == nil || c.State != pactov1alpha1.CallerCoverageUnavailable || c.RefreshSeconds != 60 {
		t.Fatalf("coverage = %+v, want unavailable with a 60s refresh", c)
	}
	if got.ObservedCallers != nil {
		t.Fatalf("callers = %+v", got.ObservedCallers)
	}
}
