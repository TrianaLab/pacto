/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func newReconcilerWithInterceptors(cfg Config, funcs interceptor.Funcs, objs ...client.Object) *Reconciler {
	scheme := newScheme()
	builder := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(funcs)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return &Reconciler{
		Client: builder.Build(),
		Scheme: scheme,
		Config: cfg,
	}
}

// --- ensureNamespace: non-NotFound Get error ---

func TestEnsureNamespace_GetNonNotFoundError(t *testing.T) {
	r := newReconcilerWithInterceptors(enabledCloudCfg(), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Namespace); ok {
				return fmt.Errorf("simulated namespace get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from ensureNamespace Get failure")
	}
	if got := err.Error(); got != "namespace: simulated namespace get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- apply errors: PVC / Deployment / Service ---

func TestReconcile_PVCApplyError(t *testing.T) {
	r := newReconcilerWithInterceptors(enabledFileCfg(), interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*corev1ac.PersistentVolumeClaimApplyConfiguration); ok {
				return fmt.Errorf("simulated pvc apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})
	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil || !containsString(err.Error(), "pvc: simulated pvc apply error") {
		t.Fatalf("expected wrapped pvc error, got: %v", err)
	}
}

func TestReconcile_DeploymentApplyError(t *testing.T) {
	r := newReconcilerWithInterceptors(enabledCloudCfg(), interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*appsv1ac.DeploymentApplyConfiguration); ok {
				return fmt.Errorf("simulated deploy apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})
	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil || !containsString(err.Error(), "deployment: simulated deploy apply error") {
		t.Fatalf("expected wrapped deployment error, got: %v", err)
	}
}

func TestReconcile_ServiceApplyError(t *testing.T) {
	r := newReconcilerWithInterceptors(enabledCloudCfg(), interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*corev1ac.ServiceApplyConfiguration); ok {
				return fmt.Errorf("simulated svc apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})
	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil || !containsString(err.Error(), "service: simulated svc apply error") {
		t.Fatalf("expected wrapped service error, got: %v", err)
	}
}

// --- cleanup: Get non-NotFound error ---

func TestCleanup_GetNonNotFoundError(t *testing.T) {
	r := newReconcilerWithInterceptors(Config{Enabled: false, Namespace: "test-ns"}, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return fmt.Errorf("simulated cleanup get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	result, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from cleanup Get failure")
	}
	if got := err.Error(); got != "failed to get Service: simulated cleanup get error" {
		t.Errorf("unexpected error: %s", got)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter set on cleanup failure")
	}
}

// --- cleanup: Delete error ---

func TestCleanup_DeleteError(t *testing.T) {
	r := newReconcilerWithInterceptors(Config{Enabled: false, Namespace: "test-ns"}, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("simulated delete error")
		},
	}, managedService("test-ns"))

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from cleanup Delete failure")
	}
	if got := err.Error(); got != "failed to delete Service: simulated delete error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- cleanup: uncached APIReader tolerates Forbidden ---

func TestCleanup_APIReaderForbidden_Tolerated(t *testing.T) {
	scheme := newScheme()
	apiReader := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.Deployment); ok {
					return apierrors.NewForbidden(
						schema.GroupResource{Group: "apps", Resource: "deployments"},
						key.Name, fmt.Errorf("not permitted"))
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()

	r := &Reconciler{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		APIReader: apiReader,
		Scheme:    scheme,
		Config:    Config{Enabled: false, Namespace: "test-ns"},
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err != nil {
		t.Fatalf("forbidden cleanup read should be tolerated, got: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue when disabled, got %v", result.RequeueAfter)
	}
}

// --- Reconcile disabled: cleanup error sets requeue ---

func TestReconcile_Disabled_CleanupError(t *testing.T) {
	r := newReconcilerWithInterceptors(Config{Enabled: false, Namespace: "test-ns"}, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("simulated cleanup failure")
		},
	}, managedDeployment("test-ns"))

	result, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error when cleanup fails")
	}
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter set on cleanup failure")
	}
}

// --- Start ---

func TestStart_Disabled(t *testing.T) {
	r := newReconciler(Config{Enabled: false, Namespace: "test-ns"})
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start with disabled evidence should return nil: %v", err)
	}
}

func TestStart_Enabled_ContextCancel(t *testing.T) {
	r := newReconciler(enabledCloudCfg())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // exit ticker loop on first select
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start with cancelled context should return nil: %v", err)
	}
}

func TestStart_InitialReconcileFailure(t *testing.T) {
	r := newReconcilerWithInterceptors(enabledCloudCfg(), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Namespace); ok {
				return fmt.Errorf("simulated start failure")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	err := r.Start(context.Background())
	if err == nil {
		t.Fatal("Start should return error when initial reconcile fails")
	}
	expected := "initial evidence reconciliation failed: namespace: simulated start failure"
	if got := err.Error(); got != expected {
		t.Errorf("unexpected error:\n  got:  %s\n  want: %s", got, expected)
	}
}

func TestStart_Enabled_TickerFire(t *testing.T) {
	r := newReconciler(enabledCloudCfg())
	r.tickInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Start should return nil after cancel: %v", err)
	}
}

func TestStart_Enabled_TickerReconcileError(t *testing.T) {
	errored := make(chan struct{}, 1)
	callCount := 0 // touched only from the Start goroutine's sequential loop
	r := newReconcilerWithInterceptors(enabledCloudCfg(), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Namespace); ok {
				callCount++
				if callCount > 1 {
					select {
					case errored <- struct{}{}:
					default:
					}
					return fmt.Errorf("simulated periodic failure")
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	r.tickInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Start(ctx) }()

	select {
	case <-errored:
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("ticker reconcile error branch never fired within 2s")
	}
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Start should return nil after cancel even with periodic errors: %v", err)
	}
}
