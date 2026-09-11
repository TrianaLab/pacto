/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// INV-5 (see PactoReconciler) says a Secret's values never enter an informer
// store. The dashboard is enabled by default, and its embedded client.Client is
// the manager's CACHED delegating client, so a single typed Secret Get on it is
// enough to lazily start a corev1.Secret informer -- cluster-wide, because the
// chart grants secrets list/watch cluster-scoped. Every in-scope Secret's .Data
// then sits in the operator's heap, which is exactly what the invariant denies.
//
// The controller package proves the same invariant for the pull-secret WATCH
// (secret_watch_test.go), and that probe cannot be widened to cover this leak:
// it reads through a manager built with ReaderFailOnMissingInformer, which
// suppresses the lazy informer creation the leak depends on, so a cached Get
// there fails instead of leaking and the probe stays green either way.
//
// This is the honest shape instead: the cached client refuses every Secret read
// the way a ReaderFailOnMissingInformer cache would, so the sync completes only
// if the read went straight to the API server through APIReader.
func TestOCICredentials_SecretsAreNeverReadThroughTheCache(t *testing.T) {
	const ns = "test-ns"
	source := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "registry-creds", Namespace: ns},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"ghcr.io":{"username":"u","password":"p"}}}`),
		},
	}
	managed := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: ManagedSecretName, Namespace: ns},
		Type:       corev1.SecretTypeDockerConfigJson,
	}

	for _, tc := range []struct {
		name        string
		cfg         Config
		objects     []client.Object
		wantManaged bool
	}{
		{
			// The configured-secrets path: read each source Secret, merge, apply.
			name:        "reading the configured OCI secrets",
			cfg:         Config{Enabled: true, Image: "img", Namespace: ns, OCISecrets: []string{source.Name}},
			objects:     []client.Object{source},
			wantManaged: true,
		},
		{
			// The no-secrets-configured path: look the managed Secret up, delete it.
			name:    "cleaning up the managed OCI secret",
			cfg:     Config{Enabled: true, Image: "img", Namespace: ns},
			objects: []client.Object{managed},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := newScheme()
			apiReader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.objects...).Build()
			cached := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.objects...).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*corev1.Secret); ok {
							return &cache.ErrResourceNotCached{GVK: corev1.SchemeGroupVersion.WithKind("Secret")}
						}
						return c.Get(ctx, key, obj, opts...)
					},
				}).Build()

			r := &Reconciler{Client: cached, APIReader: apiReader, Scheme: scheme, Config: tc.cfg}
			if err := r.reconcileOCICredentials(ctx); err != nil {
				t.Fatalf("a Secret was read through the cached client (INV-5): %v", err)
			}

			// Listed, not Get, because the interceptor above blocks every Secret Get.
			list := &corev1.SecretList{}
			if err := cached.List(ctx, list, client.InNamespace(ns)); err != nil {
				t.Fatalf("listing secrets: %v", err)
			}
			found := false
			for _, s := range list.Items {
				found = found || s.Name == ManagedSecretName
			}
			if found != tc.wantManaged {
				t.Fatalf("managed secret present = %v, want %v", found, tc.wantManaged)
			}
		})
	}
}
