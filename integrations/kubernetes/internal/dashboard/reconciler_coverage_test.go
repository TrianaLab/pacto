/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// newReconcilerWithInterceptors creates a Reconciler with a fake client that uses interceptor functions.
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
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
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

// --- reconcileServiceAccount error ---

func TestReconcile_ServiceAccountError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*corev1ac.ServiceAccountApplyConfiguration); ok {
				return fmt.Errorf("simulated sa apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileServiceAccount")
	}
	if !containsString(err.Error(), "service account") {
		t.Errorf("unexpected error: %s", err)
	}
}

// --- reconcileClusterRole error ---

func TestReconcile_ClusterRoleError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*rbacv1ac.ClusterRoleApplyConfiguration); ok {
				return fmt.Errorf("simulated cr apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileClusterRole")
	}
	if !containsString(err.Error(), "cluster role") {
		t.Errorf("unexpected error: %s", err)
	}
}

// --- reconcileClusterRoleBinding error ---

func TestReconcile_ClusterRoleBindingError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*rbacv1ac.ClusterRoleBindingApplyConfiguration); ok {
				return fmt.Errorf("simulated crb apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileClusterRoleBinding")
	}
	if !containsString(err.Error(), "cluster role binding") {
		t.Errorf("unexpected error: %s", err)
	}
}

// --- reconcileDeployment error ---

func TestReconcile_DeploymentError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*appsv1ac.DeploymentApplyConfiguration); ok {
				return fmt.Errorf("simulated deploy apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileDeployment")
	}
	if !containsString(err.Error(), "deployment") {
		t.Errorf("unexpected error: %s", err)
	}
}

// --- reconcileService error ---

func TestReconcile_ServiceError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, opts ...client.ApplyOption) error {
			if _, ok := obj.(*corev1ac.ServiceApplyConfiguration); ok {
				return fmt.Errorf("simulated svc apply error")
			}
			return c.Apply(ctx, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from reconcileService")
	}
	if !containsString(err.Error(), "service") {
		t.Errorf("unexpected error: %s", err)
	}
}

// --- cleanup: Get non-NotFound error ---

func TestCleanup_GetNonNotFoundError(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	// Inject a Get error for Service (first resource in cleanup order)
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return fmt.Errorf("simulated cleanup get error")
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from cleanup Get failure")
	}
	if got := err.Error(); got != "failed to get Service: simulated cleanup get error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- cleanup: Delete error ---

func TestCleanup_DeleteError(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	// Pre-create a managed service so cleanup will try to delete it
	svc := BuildService(Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"})

	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("simulated delete error")
		},
	}, svc)

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from cleanup Delete failure")
	}
	if got := err.Error(); got != "failed to delete Service: simulated delete error" {
		t.Errorf("unexpected error: %s", got)
	}
}

// --- Start: disabled (no ticker, returns nil) ---

func TestStart_Disabled(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	r := newReconciler(cfg)

	err := r.Start(context.Background())
	if err != nil {
		t.Fatalf("Start with disabled dashboard should return nil: %v", err)
	}
}

// --- Start: enabled with context cancel ---

func TestStart_Enabled_ContextCancel(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconciler(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the ticker loop exits on first select
	cancel()

	err := r.Start(ctx)
	if err != nil {
		t.Fatalf("Start with cancelled context should return nil: %v", err)
	}
}

// --- Start: initial reconcile failure ---

func TestStart_InitialReconcileFailure(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	// Use a scheme missing Namespace type to cause ensureNamespace to fail
	brokenScheme := runtime.NewScheme()
	// Register enough types for the client to work but NOT Namespace
	_ = appsv1.AddToScheme(brokenScheme)
	_ = rbacv1.AddToScheme(brokenScheme)
	// Don't register corev1, so Namespace Get will fail

	// Instead, use interceptors to force an error on the initial reconcile
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
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
	expected := "initial dashboard reconciliation failed: namespace: simulated start failure"
	if got := err.Error(); got != expected {
		t.Errorf("unexpected error:\n  got:  %s\n  want: %s", got, expected)
	}
}

// --- Start: periodic reconcile with ticker fire ---

func TestStart_Enabled_TickerFire(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	r := newReconciler(cfg)
	r.tickInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- r.Start(ctx)
	}()

	// Let at least one tick fire
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("Start should return nil after cancel: %v", err)
	}
}

func TestStart_Enabled_TickerReconcileError(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}

	callCount := 0
	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			// Let the initial reconcile succeed, then fail on ticker reconcile
			if _, ok := obj.(*corev1.Namespace); ok {
				callCount++
				if callCount > 1 {
					return fmt.Errorf("simulated periodic failure")
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	r.tickInterval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- r.Start(ctx)
	}()

	// Wait for a few ticks to fire (some will error, which is logged but not returned)
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("Start should return nil after cancel even with periodic errors: %v", err)
	}
}

// --- Reconcile: cleanup error when disabled ---

func TestReconcile_Disabled_CleanupError(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	// Create a managed deployment so cleanup will attempt to get/delete it
	deploy := BuildDeployment(Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"})

	r := newReconcilerWithInterceptors(cfg, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			return fmt.Errorf("simulated cleanup failure")
		},
	}, deploy)

	result, err := r.Reconcile(context.Background(), ctrl.Request{})
	if err == nil {
		t.Fatal("expected error when cleanup fails")
	}
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter to be set on cleanup failure")
	}
}
