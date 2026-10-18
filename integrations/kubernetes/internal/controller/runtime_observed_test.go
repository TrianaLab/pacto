/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/observer"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestReconcile_RuntimeObservedReflectsCollectionFailures proves RuntimeObserved is True only when every
// runtime read succeeded: a failed workload read (which Collect reports, not the back-compat Service read)
// turns it False with the failure in the message.
func TestReconcile_RuntimeObservedReflectsCollectionFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failGet    bool
		wantStatus metav1.ConditionStatus
	}{
		{"every read succeeds", false, metav1.ConditionTrue},
		{"the workload read fails", true, metav1.ConditionFalse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pacto := &pactov1alpha1.Pacto{
				ObjectMeta: metav1.ObjectMeta{Name: "collect", Namespace: "default", UID: "u"},
				Spec: pactov1alpha1.PactoSpec{
					ContractRef: pactov1alpha1.ContractRef{Inline: validContract},
					Target: pactov1alpha1.TargetRef{
						ServiceName: "svc",
						WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "app", Kind: "Deployment"},
					},
				},
			}
			s := newSchemeWithApps()
			cl := fake.NewClientBuilder().WithScheme(s).
				WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
				WithObjects(pacto, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "default"}}).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*appsv1.Deployment); ok && tc.failGet {
							return apierrors.NewInternalError(fmt.Errorf("simulated API error"))
						}
						return c.Get(ctx, key, obj, opts...)
					},
				}).Build()
			r := &PactoReconciler{
				Client:   cl,
				Scheme:   s,
				Recorder: record.NewFakeRecorder(20),
				Loader: &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
					return &loader.LoadResult{
						Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}, Workload: "service"},
						RawYAML:  []byte(validContract),
					}, nil
				}},
			}
			if _, err := r.Reconcile(context.Background(), reconcileReq("collect", "default")); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := getPacto(t, r, "collect", "default")
			cond := meta.FindStatusCondition(got.Status.Conditions, pactov1alpha1.ConditionRuntimeObserved)
			if cond == nil || cond.Status != tc.wantStatus {
				t.Fatalf("RuntimeObserved = %+v, want %s", cond, tc.wantStatus)
			}
			if tc.failGet {
				if cond.Reason != pactov1alpha1.ReasonObservationFailed || !strings.Contains(cond.Message, "workload: failed to get Deployment app") {
					t.Errorf("condition = %s %q, want the workload failure", cond.Reason, cond.Message)
				}
				if got.Status.Resources == nil || got.Status.Resources.Workload == nil || got.Status.Resources.Workload.Exists {
					t.Errorf("resources = %+v, want the workload reported as not observed", got.Status.Resources)
				}
			}
		})
	}
}

// TestObserveBackCompat_NoTargetLeavesResourcesUnset covers a target that names neither a Service nor a
// workload: there is nothing to report, so status.resources stays nil.
func TestObserveBackCompat_NoTargetLeavesResourcesUnset(t *testing.T) {
	r := &PactoReconciler{}
	cl := fake.NewClientBuilder().WithScheme(newSchemeWithApps()).Build()
	pacto := &pactov1alpha1.Pacto{Spec: pactov1alpha1.PactoSpec{Target: pactov1alpha1.TargetRef{
		WorkloadRef: &pactov1alpha1.WorkloadRef{Kind: "Deployment"},
	}}}
	if err := r.observeBackCompat(context.Background(), observer.New(cl), pacto); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pacto.Status.Resources != nil || pacto.Status.ObservedRuntime != nil {
		t.Fatalf("status = %+v, want nothing reported", pacto.Status)
	}
}
