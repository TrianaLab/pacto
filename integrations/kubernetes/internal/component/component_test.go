/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package component

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1: %v", err)
	}
	return s
}

func owned() map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": "pacto-operator"}
}

func managedService(ns string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: ns, Labels: owned()}}
}

func serviceResource(ns string) []Resource {
	return []Resource{{Kind: "Service", Obj: &corev1.Service{}, Key: client.ObjectKey{Namespace: ns, Name: "svc"}}}
}

// --- Run ---

// The FIRST sync is fatal: a component that cannot come up must stop the
// manager rather than sit there looking healthy.
func TestRun_InitialSyncFailureIsFatal(t *testing.T) {
	err := Run(context.Background(), "dash", true, time.Millisecond, func(context.Context) error {
		return errors.New("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "initial dash reconciliation failed: boom") {
		t.Fatalf("expected the first sync failure to surface, got %v", err)
	}
}

func TestRun_DisabledSyncsOnceThenReturns(t *testing.T) {
	calls := 0
	if err := Run(context.Background(), "dash", false, time.Millisecond, func(context.Context) error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("a disabled component has nothing to poll: want 1 sync, got %d", calls)
	}
}

// runUntil starts Run in a goroutine, waits for the sync to have been called
// wantCalls times, then cancels and returns what Run returned.
func runUntil(t *testing.T, interval time.Duration, wantCalls int32, sync func(n int32) error) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, "dash", true, interval, func(context.Context) error { return sync(calls.Add(1)) })
	}()

	deadline := time.Now().Add(10 * time.Second)
	for calls.Load() < wantCalls {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("sync ran %d times, wanted %d", calls.Load(), wantCalls)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	return <-done
}

// A tick failure is logged and retried; only the FIRST sync is fatal.
func TestRun_EnabledRetriesAfterTickFailure(t *testing.T) {
	err := runUntil(t, time.Millisecond, 3, func(n int32) error {
		if n == 1 {
			return nil
		}
		return errors.New("transient")
	})
	if err != nil {
		t.Fatalf("a failing tick must not stop the manager, got %v", err)
	}
}

// interval 0 means DefaultInterval, which is far too long to tick within a test:
// assert only that the initial sync ran and that cancelling is a clean stop.
func TestRun_ZeroIntervalUsesDefault(t *testing.T) {
	if err := runUntil(t, 0, 1, func(int32) error { return nil }); err != nil {
		t.Fatalf("cancellation is a clean stop, got %v", err)
	}
}

// --- Prune ---

func TestPrune_DeletesOwnedResource(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(managedService("ns")).Build()

	if err := Prune(context.Background(), "dash", c, c, owned(), serviceResource("ns")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "svc"}, &corev1.Service{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected the owned Service deleted, got %v", err)
	}
}

func TestPrune_SkipsMissingResource(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()

	if err := Prune(context.Background(), "dash", c, c, owned(), serviceResource("ns")); err != nil {
		t.Fatalf("nothing to delete is not an error: %v", err)
	}
}

// A component that was never enabled was never granted RBAC here, so there is
// nothing it created to delete: Forbidden must not crashloop the manager.
func TestPrune_SkipsForbidden(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "svc", errors.New("no rbac"))
		},
	}).Build()

	if err := Prune(context.Background(), "dash", c, c, owned(), serviceResource("ns")); err != nil {
		t.Fatalf("Forbidden must be skipped, got %v", err)
	}
}

func TestPrune_GetErrorFails(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errors.New("api down")
		},
	}).Build()

	err := Prune(context.Background(), "dash", c, c, owned(), serviceResource("ns"))
	if err == nil || !strings.Contains(err.Error(), "failed to get Service") {
		t.Fatalf("expected the get failure to surface, got %v", err)
	}
}

func TestPrune_SkipsUnowned(t *testing.T) {
	s := newScheme(t)
	unowned := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "svc", Namespace: "ns", Labels: map[string]string{"app.kubernetes.io/managed-by": "helm"},
	}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(unowned).Build()

	if err := Prune(context.Background(), "dash", c, c, owned(), serviceResource("ns")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "svc"}, &corev1.Service{}); err != nil {
		t.Errorf("a Service we do not own must survive: %v", err)
	}
}

func TestPrune_DeleteErrorFails(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(managedService("ns")).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return errors.New("api down")
		},
	}).Build()

	err := Prune(context.Background(), "dash", c, c, owned(), serviceResource("ns"))
	if err == nil || !strings.Contains(err.Error(), "failed to delete Service") {
		t.Fatalf("expected the delete failure to surface, got %v", err)
	}
}

// Deleted between the read and the delete: the goal was already reached.
func TestPrune_DeleteNotFoundTolerated(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(managedService("ns")).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, "svc")
		},
	}).Build()

	if err := Prune(context.Background(), "dash", c, c, owned(), serviceResource("ns")); err != nil {
		t.Fatalf("a concurrent delete is not an error: %v", err)
	}
}

// --- EnsureNamespace ---

func TestEnsureNamespace_Creates(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()

	if err := EnsureNamespace(context.Background(), c, "ns", owned()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ns := &corev1.Namespace{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "ns"}, ns); err != nil {
		t.Fatalf("expected the namespace created: %v", err)
	}
	if ns.Labels["app.kubernetes.io/managed-by"] != "pacto-operator" {
		t.Errorf("expected the caller's labels, got %v", ns.Labels)
	}
}

func TestEnsureNamespace_AlreadyExists(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns"}}).Build()

	if err := EnsureNamespace(context.Background(), c, "ns", owned()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureNamespace_GetErrorFails(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return errors.New("api down")
		},
	}).Build()

	if err := EnsureNamespace(context.Background(), c, "ns", owned()); err == nil {
		t.Fatal("expected the get failure to surface")
	}
}

func TestHasLatestTag(t *testing.T) {
	for image, want := range map[string]bool{
		"ghcr.io/trianalab/pacto/dashboard:0.24.2": false,
		"ghcr.io/trianalab/pacto/dashboard:latest": true,
		"ghcr.io/trianalab/pacto/dashboard":        true,
		"dashboard:1.0":                            false,
		"dashboard":                                true,
		"registry:5000/pacto:1.0.0":                false, // tag after the registry-port colon
		"registry:5000/pacto":                      true,  // the port colon is not a tag
		"ghcr.io/trianalab/pacto@sha256:abcd":      false, // a digest is pinned
	} {
		if got := HasLatestTag(image); got != want {
			t.Errorf("HasLatestTag(%q) = %v, want %v", image, got, want)
		}
	}
}

func TestResources(t *testing.T) {
	defaults := func() corev1.ResourceRequirements {
		return corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m")},
			Limits:   corev1.ResourceList{},
		}
	}
	if got := (Resources{}).Build(defaults()); got.Requests.Cpu().String() != "50m" || len(got.Limits) != 0 {
		t.Errorf("no overrides must keep the defaults, got %v", got)
	}
	got := Resources{CPURequest: "10m", CPULimit: "1", MemoryRequest: "32Mi", MemoryLimit: "1Gi"}.Build(defaults())
	if got.Requests.Cpu().String() != "10m" || got.Limits.Cpu().String() != "1" ||
		got.Requests.Memory().String() != "32Mi" || got.Limits.Memory().String() != "1Gi" {
		t.Errorf("overrides not applied: %v", got)
	}

	if err := (Resources{CPULimit: "1"}).Validate("node agent"); err != nil {
		t.Errorf("valid quantity rejected: %v", err)
	}
	err := Resources{MemoryLimit: "lots"}.Validate("node agent")
	if err == nil || !strings.HasPrefix(err.Error(), `invalid node agent memory-limit quantity "lots"`) {
		t.Errorf("err = %v, want the component-prefixed message", err)
	}
}
