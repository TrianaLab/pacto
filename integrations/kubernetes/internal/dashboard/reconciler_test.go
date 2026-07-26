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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = rbacv1.AddToScheme(s)
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

	// Verify no resources were created
	assertResourceNotFound(t, r.Client, ctx, &appsv1.Deployment{})
	assertResourceNotFound(t, r.Client, ctx, &corev1.Service{})
	assertResourceNotFound(t, r.Client, ctx, &corev1.ServiceAccount{})
}

func TestReconcile_Enabled_CreatesResources(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}

	// Pre-create the namespace (fake client doesn't require it, but let's be clean)
	r := newReconciler(cfg)
	ctx := context.Background()

	result, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue when enabled")
	}

	// Verify all resources exist
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, &corev1.ServiceAccount{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Name: Name}, &rbacv1.ClusterRole{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Name: Name}, &rbacv1.ClusterRoleBinding{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, &appsv1.Deployment{})
	assertResourceExists(t, r.Client, ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, &corev1.Service{})

	// Verify deployment has correct image
	deploy := &appsv1.Deployment{}
	_ = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, deploy)
	if deploy.Spec.Template.Spec.Containers[0].Image != cfg.Image {
		t.Errorf("expected image %q, got %q", cfg.Image, deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestReconcile_Enabled_UpdatesExistingResources(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.25.0",
		Namespace: "test-ns",
	}

	// Pre-create a deployment with an old image
	oldDeploy := BuildDeployment(Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.0",
		Namespace: "test-ns",
	})

	r := newReconciler(cfg,
		BuildServiceAccount(cfg),
		BuildClusterRole(),
		BuildClusterRoleBinding(cfg),
		oldDeploy,
		BuildService(cfg),
	)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify deployment was updated with new image
	deploy := &appsv1.Deployment{}
	_ = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, deploy)
	if deploy.Spec.Template.Spec.Containers[0].Image != cfg.Image {
		t.Errorf("expected updated image %q, got %q", cfg.Image, deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestReconcile_DisabledAfterEnabled_CleansUp(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}

	// Create resources as if dashboard was enabled
	r := newReconciler(cfg,
		BuildServiceAccount(cfg),
		BuildClusterRole(),
		BuildClusterRoleBinding(cfg),
		BuildDeployment(cfg),
		BuildService(cfg),
	)
	ctx := context.Background()

	// Now disable the dashboard
	r.Config.Enabled = false

	result, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue when disabled, got %v", result.RequeueAfter)
	}

	// Verify all resources were cleaned up
	assertResourceNotFound(t, r.Client, ctx, &appsv1.Deployment{})
	assertResourceNotFound(t, r.Client, ctx, &corev1.Service{})
	assertResourceNotFound(t, r.Client, ctx, &corev1.ServiceAccount{})
	assertClusterResourceNotFound(t, r.Client, ctx, &rbacv1.ClusterRole{})
	assertClusterResourceNotFound(t, r.Client, ctx, &rbacv1.ClusterRoleBinding{})
}

func TestReconcile_Cleanup_SkipsUnmanagedResources(t *testing.T) {
	cfg := Config{Enabled: false, Namespace: "test-ns"}

	// Create a service with the same name but WITHOUT our labels
	unmanagedSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      Name,
			Namespace: "test-ns",
			Labels:    map[string]string{"app": "something-else"},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80}},
		},
	}

	r := newReconciler(cfg, unmanagedSvc)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The unmanaged service should still exist
	svc := &corev1.Service{}
	err = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, svc)
	if err != nil {
		t.Errorf("unmanaged service should not have been deleted: %v", err)
	}
}

func TestReconcile_Cleanup_NoErrorWhenNoResources(t *testing.T) {
	r := newReconciler(Config{Enabled: false, Namespace: "test-ns"})
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("cleanup with no existing resources should not error: %v", err)
	}
}

func TestReconcile_PreservesExternalLabelsAndAnnotations(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.25.0",
		Namespace: "test-ns",
	}

	// Pre-create a deployment with external labels/annotations (e.g. from ArgoCD)
	deploy := BuildDeployment(cfg)
	deploy.Labels["argocd.argoproj.io/instance"] = "pacto"
	deploy.Annotations = map[string]string{
		"argocd.argoproj.io/tracking-id": "some-tracking-id",
	}

	svc := BuildService(cfg)
	svc.Annotations = map[string]string{
		"external.io/managed": "true",
	}

	r := newReconciler(cfg,
		BuildServiceAccount(cfg),
		BuildClusterRole(),
		BuildClusterRoleBinding(cfg),
		deploy,
		svc,
	)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify external labels are preserved on the deployment
	got := &appsv1.Deployment{}
	_ = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, got)
	if got.Labels["argocd.argoproj.io/instance"] != "pacto" {
		t.Error("expected external label to be preserved on deployment")
	}
	if got.Annotations["argocd.argoproj.io/tracking-id"] != "some-tracking-id" {
		t.Error("expected external annotation to be preserved on deployment")
	}
	// Operator labels should still be present
	if got.Labels[LabelManagedBy] != ManagedByValue {
		t.Error("expected operator label to still be present")
	}

	// Verify external annotations are preserved on the service
	gotSvc := &corev1.Service{}
	_ = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, gotSvc)
	if gotSvc.Annotations["external.io/managed"] != "true" {
		t.Error("expected external annotation to be preserved on service")
	}
}

func TestReconcile_Idempotent(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}

	r := newReconciler(cfg)
	ctx := context.Background()

	// First reconcile
	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	// Second reconcile (should be idempotent)
	_, err = r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Resources should still exist and be correct
	deploy := &appsv1.Deployment{}
	_ = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: Name}, deploy)
	if deploy.Spec.Template.Spec.Containers[0].Image != cfg.Image {
		t.Errorf("expected image %q after idempotent reconcile, got %q", cfg.Image, deploy.Spec.Template.Spec.Containers[0].Image)
	}
}

// --- helpers ---

func assertResourceExists(t *testing.T, c client.Client, ctx context.Context, key client.ObjectKey, obj client.Object) {
	t.Helper()
	if err := c.Get(ctx, key, obj); err != nil {
		t.Errorf("expected resource %T %v to exist: %v", obj, key, err)
	}
}

func assertResourceNotFound(t *testing.T, c client.Client, ctx context.Context, obj client.Object) {
	t.Helper()
	key := client.ObjectKey{Namespace: "test-ns", Name: Name}
	err := c.Get(ctx, key, obj)
	if err == nil {
		t.Errorf("expected resource %T %v to not exist", obj, key)
	} else if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound error for %T %v, got: %v", obj, key, err)
	}
}

func assertClusterResourceNotFound(t *testing.T, c client.Client, ctx context.Context, obj client.Object) {
	t.Helper()
	key := client.ObjectKey{Name: Name}
	err := c.Get(ctx, key, obj)
	if err == nil {
		t.Errorf("expected cluster resource %T %v to not exist", obj, key)
	} else if !apierrors.IsNotFound(err) {
		t.Errorf("expected NotFound error for %T %v, got: %v", obj, key, err)
	}
}

// ---------- OCI Credentials ----------

func TestReconcile_OCISecret_CreatesManagedSecret(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
		OCISecret: "my-creds",
	}
	srcSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-creds", Namespace: "test-ns"},
		Data:       map[string][]byte{"username": []byte("u"), "password": []byte("p"), "registry": []byte("ghcr.io")},
	}
	r := newReconciler(cfg, srcSecret)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify managed secret was created
	managed := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: ManagedSecretName}, managed)
	if err != nil {
		t.Fatalf("expected managed secret to exist: %v", err)
	}
	if managed.Type != corev1.SecretTypeDockerConfigJson {
		t.Errorf("expected dockerconfigjson type, got %s", managed.Type)
	}
	if len(managed.Data[corev1.DockerConfigJsonKey]) == 0 {
		t.Error("expected non-empty dockerconfigjson data")
	}
}

func TestReconcile_OCISecrets_CreatesManagedSecret(t *testing.T) {
	cfg := Config{
		Enabled:    true,
		Image:      "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace:  "test-ns",
		OCISecrets: []string{"creds-1", "creds-2"},
	}
	s1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds-1", Namespace: "test-ns"},
		Data:       map[string][]byte{"token": []byte("tok1"), "registry": []byte("registry.example.com")},
	}
	dockerCfg := `{"auths":{"ghcr.io":{"username":"u","password":"p"}}}`
	s2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds-2", Namespace: "test-ns"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerCfg)},
	}
	r := newReconciler(cfg, s1, s2)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	managed := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: ManagedSecretName}, managed)
	if err != nil {
		t.Fatalf("expected managed secret: %v", err)
	}
}

func TestReconcile_NoOCISecrets_NoManagedSecret(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}
	r := newReconciler(cfg)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	managed := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: ManagedSecretName}, managed)
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected managed secret to not exist, got err=%v", err)
	}
}

func TestReconcile_NoOCISecrets_CleansUpManagedSecret(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}
	// Pre-create a managed secret from a previous run
	oldManaged := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ManagedSecretName,
			Namespace: "test-ns",
			Labels:    Labels(),
		},
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("{}")},
	}
	r := newReconciler(cfg, oldManaged)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	managed := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: ManagedSecretName}, managed)
	if !apierrors.IsNotFound(err) {
		t.Error("expected managed secret to be cleaned up when no OCI secrets configured")
	}
}

func TestReconcile_OCISecret_MissingSourceSecret(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
		OCISecret: "nonexistent",
	}
	r := newReconciler(cfg)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err == nil {
		t.Fatal("expected error when source secret is missing")
	}
}

func TestReconcile_OCICredentials_CleanupGetError(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
		// No OCISecret/OCISecrets → triggers cleanup path
	}
	scheme := newScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				// Inject error only for the managed secret Get in cleanup path
				if key.Name == ManagedSecretName {
					return fmt.Errorf("injected get error")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()

	r := &Reconciler{Client: fakeClient, Scheme: scheme, Config: cfg}
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err == nil {
		t.Fatal("expected error from cleanup Get failure")
	}
	if !containsString(err.Error(), "injected get error") {
		t.Errorf("expected injected error, got: %v", err)
	}
}

func TestReconcile_OCICredentials_MergeError(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
		OCISecret: "bad-secret",
	}
	// Create an opaque secret with no valid keys → MergeToDockerConfigJSON will fail
	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-secret", Namespace: "test-ns"},
		Data:       map[string][]byte{"irrelevant": []byte("data")},
	}
	r := newReconciler(cfg, badSecret)
	ctx := context.Background()

	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err == nil {
		t.Fatal("expected error when MergeToDockerConfigJSON fails")
	}
	if !containsString(err.Error(), "merging OCI credentials") {
		t.Errorf("expected merge error, got: %v", err)
	}
}

func TestReconcile_Cleanup_IncludesManagedSecret(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "test-ns",
	}
	managedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ManagedSecretName,
			Namespace: "test-ns",
			Labels:    Labels(),
		},
	}
	r := newReconciler(cfg,
		BuildServiceAccount(cfg),
		BuildClusterRole(),
		BuildClusterRoleBinding(cfg),
		BuildDeployment(cfg),
		BuildService(cfg),
		managedSecret,
	)
	ctx := context.Background()

	// Disable and reconcile
	r.Config.Enabled = false
	_, err := r.Reconcile(ctx, ctrl.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Managed secret should be cleaned up
	s := &corev1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: ManagedSecretName}, s)
	if !apierrors.IsNotFound(err) {
		t.Error("expected managed secret to be cleaned up on disable")
	}
}
