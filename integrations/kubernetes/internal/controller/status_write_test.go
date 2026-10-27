/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"testing"
	"time"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// A reconcile that finds nothing new writes no status until the heartbeat is due,
// so a watch event or the operator's own write never turns into a status write.
func TestReconcile_WritesStatusOnlyOnChangeOrHeartbeat(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "steady", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef:          pactov1alpha1.ContractRef{Inline: driftRawYAML},
			Target:               pactov1alpha1.TargetRef{WorkloadRef: &pactov1alpha1.WorkloadRef{Name: "steady-job", Kind: "Job"}},
			CheckIntervalSeconds: 120,
		},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "steady-job", Namespace: "default"},
		Spec:       batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "img:v1"}}}}},
	}
	writes := 0
	s := newSchemeWithApps()
	cl := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{}).
		WithObjects(pacto, job).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if _, ok := obj.(*pactov1alpha1.Pacto); ok {
					writes++
				}
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
		}).Build()
	r := &PactoReconciler{
		Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(50),
		Loader: &mockLoader{loadFn: func(_ context.Context, _, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service:  contract.Service{Name: "drift-svc", Version: "1.0.0"},
					Workload: "service",
					State:    &contract.State{Type: contract.StateStateful, Persistence: contract.Persistence{Durability: contract.DurabilityPersistent}},
				},
				RawYAML: []byte(driftRawYAML),
			}, nil
		}},
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		res, err := r.Reconcile(ctx, reconcileReq("steady", "default"))
		if err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		if res.RequeueAfter != 120*time.Second {
			t.Fatalf("reconcile %d requeues after %s, want the 120s check interval", i, res.RequeueAfter)
		}
	}
	if writes != 1 {
		t.Fatalf("three reconciles of an unchanged target wrote status %d times, want 1", writes)
	}

	// Once half the interval has passed the heartbeat refreshes lastReconciledAt.
	p := getPacto(t, r, "steady", "default")
	old := metav1.NewTime(time.Now().Add(-61 * time.Second))
	p.Status.LastReconciledAt = &old
	if err := cl.Status().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	writes = 0
	if _, err := r.Reconcile(ctx, reconcileReq("steady", "default")); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("a due heartbeat wrote status %d times, want 1", writes)
	}
	if got := getPacto(t, r, "steady", "default").Status.LastReconciledAt; got == nil || time.Since(got.Time) > 5*time.Second {
		t.Fatalf("heartbeat left lastReconciledAt at %v", got)
	}
}
