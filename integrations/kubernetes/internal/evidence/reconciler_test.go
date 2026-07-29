/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	return s
}

func newReconciler(cfg Config, objs ...client.Object) *Reconciler {
	scheme := newScheme()
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return &Reconciler{
		Client: builder.Build(),
		Scheme: scheme,
		Config: cfg,
	}
}

func enabledFileCfg() Config {
	return Config{
		Enabled:     true,
		Image:       "ghcr.io/trianalab/pacto:0.1.0",
		Namespace:   "test-ns",
		BucketURL:   "file:///var/evidence",
		TrustSecret: "trusted-keys",
		Persistence: PersistenceConfig{Enabled: true},
	}
}

func enabledCloudCfg() Config {
	return Config{
		Enabled:     true,
		Image:       "ghcr.io/trianalab/pacto:0.1.0",
		Namespace:   "test-ns",
		BucketURL:   "s3://bucket",
		TrustSecret: "trusted-keys",
	}
}

func managedDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: Name, Namespace: ns, Labels: Labels()},
	}
}

func managedService(ns string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: Name, Namespace: ns, Labels: Labels()},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: EvidencePort}}},
	}
}

func TestReconcile_Disabled_NoResources(t *testing.T) {
	r := newReconciler(Config{Enabled: false, Namespace: "test-ns"})
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue, got %v", result.RequeueAfter)
	}
}

func TestReconcile_Enabled_FileBucket_AppliesPVCDeploymentService(t *testing.T) {
	cfg := enabledFileCfg()
	r := newReconciler(cfg)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue when enabled")
	}

	assertExists(t, r.Client, ctx, &corev1.PersistentVolumeClaim{}, PVCName)
	assertExists(t, r.Client, ctx, &appsv1.Deployment{}, Name)
	assertExists(t, r.Client, ctx, &corev1.Service{}, Name)
}

func TestReconcile_Enabled_CloudBucket_NoPVC(t *testing.T) {
	cfg := enabledCloudCfg()
	// Pre-create the namespace to exercise the ensureNamespace "exists" branch.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}}
	r := newReconciler(cfg, ns)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: PVCName}, pvc); !apierrors.IsNotFound(err) {
		t.Errorf("expected no PVC for cloud bucket, got err=%v", err)
	}
	assertExists(t, r.Client, ctx, &appsv1.Deployment{}, Name)
	assertExists(t, r.Client, ctx, &corev1.Service{}, Name)
}

func TestReconcile_DisabledAfterEnabled_CleansUpButRetainsPVC(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: PVCName, Namespace: "test-ns", Labels: Labels()},
	}
	r := newReconciler(cfg, managedDeployment("test-ns"), managedService("test-ns"), pvc)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Deployment and Service deleted.
	d := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, d); !apierrors.IsNotFound(err) {
		t.Errorf("expected deployment deleted, got err=%v", err)
	}
	s := &corev1.Service{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, s); !apierrors.IsNotFound(err) {
		t.Errorf("expected service deleted, got err=%v", err)
	}
	// PVC retained.
	assertExists(t, r.Client, ctx, &corev1.PersistentVolumeClaim{}, PVCName)
}

func TestReconcile_Cleanup_SkipsUnmanaged(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}
	unmanaged := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: Name, Namespace: "test-ns", Labels: map[string]string{"app": "other"}},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}},
	}
	r := newReconciler(cfg, unmanaged)
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unmanaged service must survive.
	assertExists(t, r.Client, ctx, &corev1.Service{}, Name)
}

func TestReconcile_Cleanup_NoResources_NotFoundSkipped(t *testing.T) {
	r := newReconciler(Config{Enabled: false, Namespace: "test-ns"})
	if _, err := r.Reconcile(context.Background(), ctrl.Request{}); err != nil {
		t.Fatalf("cleanup with no resources should not error: %v", err)
	}
}

func TestNeedsPVC(t *testing.T) {
	base := func() Config {
		return Config{BucketURL: "file:///data", Persistence: PersistenceConfig{Enabled: true}}
	}
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"file + persistence + no existing claim", base(), true},
		{"cloud bucket", func() Config { c := base(); c.BucketURL = "s3://bucket"; return c }(), false},
		{"persistence disabled", func() Config { c := base(); c.Persistence.Enabled = false; return c }(), false},
		{"existing claim set", func() Config { c := base(); c.Persistence.ExistingClaim = "x"; return c }(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Reconciler{Config: tt.cfg}
			if got := r.needsPVC(); got != tt.want {
				t.Errorf("needsPVC() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReader(t *testing.T) {
	scheme := newScheme()
	cached := fake.NewClientBuilder().WithScheme(scheme).Build()
	// Without APIReader, reader() falls back to the cached client.
	r := &Reconciler{Client: cached, Scheme: scheme}
	if r.reader() != client.Reader(cached) {
		t.Error("expected reader() to fall back to cached client")
	}
	// With APIReader set, reader() returns it.
	uncached := fake.NewClientBuilder().WithScheme(scheme).Build()
	r.APIReader = uncached
	if r.reader() != client.Reader(uncached) {
		t.Error("expected reader() to return APIReader")
	}
}

// --- helpers ---

func assertExists(t *testing.T, c client.Client, ctx context.Context, obj client.Object, name string) {
	t.Helper()
	if err := c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: name}, obj); err != nil {
		t.Errorf("expected resource %T %q to exist: %v", obj, name, err)
	}
}
