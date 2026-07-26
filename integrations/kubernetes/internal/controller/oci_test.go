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

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/internal/loader"
	"github.com/trianalab/pacto/v3/pkg/contract"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// ---------- syncAllRevisions ----------

func TestSyncAllRevisions_ListTagsError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return nil, fmt.Errorf("registry unreachable")
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to list tags") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncAllRevisions_TagAlreadyHasRevision_NoDigest(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto-1-0-0-abc",
			Namespace: "default",
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       "my-pacto",
				pactov1alpha1.LabelRevisionVersion: "1.0.0",
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "my-pacto",
		},
	}

	r := newReconciler(pacto, existingRev)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSyncAllRevisions_TagAlreadyHasRevision_DigestMatches(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto-1-0-0-abc",
			Namespace: "default",
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       "my-pacto",
				pactov1alpha1.LabelRevisionVersion: "1.0.0",
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "my-pacto",
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0", Digest: "sha256:matchingdigest"},
		},
	}

	loadCalled := false
	r := newReconciler(pacto, existingRev)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			loadCalled = true
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.Service{Name: "svc", Version: "1.0.0"},
				},
				RawYAML:        []byte("yaml"),
				ResolvedDigest: "sha256:matchingdigest",
			}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !loadCalled {
		t.Fatal("expected load to be called for digest check")
	}
}

func TestSyncAllRevisions_ForcePushDetected(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto-1-0-0-abc",
			Namespace: "default",
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       "my-pacto",
				pactov1alpha1.LabelRevisionVersion: "1.0.0",
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "my-pacto",
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0", Digest: "sha256:olddigest000"},
		},
	}

	recorder := record.NewFakeRecorder(20)
	r := newReconciler(pacto, existingRev)
	r.Recorder = recorder
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.Service{Name: "svc", Version: "1.0.0"},
				},
				RawYAML:        []byte("new-yaml"),
				ResolvedDigest: "sha256:newdigest111",
			}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "TagOverwritten") {
			t.Errorf("expected TagOverwritten event, got %s", event)
		}
	default:
		t.Fatal("expected event for force-push detection")
	}
}

func TestSyncAllRevisions_ForcePush_LoadError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto-1-0-0-abc",
			Namespace: "default",
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       "my-pacto",
				pactov1alpha1.LabelRevisionVersion: "1.0.0",
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.0.0",
			PactoRef: "my-pacto",
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0", Digest: "sha256:olddigest000"},
		},
	}

	r := newReconciler(pacto, existingRev)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return nil, fmt.Errorf("load failed")
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error (should continue on load error): %v", err)
	}
}

func TestSyncAllRevisions_LoadError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"2.0.0"}, nil
		},
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return nil, fmt.Errorf("load failed")
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error (should continue on load error): %v", err)
	}
}

func TestSyncAllRevisions_Success(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"2.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.Service{Name: "svc", Version: "2.0.0"},
				},
				RawYAML:     []byte("v2-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	revList := &pactov1alpha1.PactoRevisionList{}
	if err := r.List(context.Background(), revList, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list revisions: %v", err)
	}
	if len(revList.Items) == 0 {
		t.Fatal("expected at least one revision to be created")
	}
}

func TestSyncAllRevisions_TagWithColon(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	var capturedRef string
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"3.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			capturedRef = ref
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.Service{Name: "svc", Version: "3.0.0"},
				},
				RawYAML:     []byte("v3-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedRef, ":3.0.0") {
		t.Errorf("expected ref with tag :3.0.0, got %s", capturedRef)
	}
}

func TestSyncAllRevisions_BaseRefWithExistingTag(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	var capturedRef string
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"4.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			capturedRef = ref
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.Service{Name: "svc", Version: "4.0.0"},
				},
				RawYAML:     []byte("v4-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc:latest", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(capturedRef, ":latest") {
		t.Errorf("expected tag to be stripped and replaced, got %s", capturedRef)
	}
	if !strings.Contains(capturedRef, ":4.0.0") {
		t.Errorf("expected ref with tag :4.0.0, got %s", capturedRef)
	}
}

func TestSyncAllRevisions_RevisionListError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	s := newScheme()
	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*pactov1alpha1.PactoRevisionList); ok {
						return fmt.Errorf("simulated list error")
					}
					return c.List(ctx, list, opts...)
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader: &mockLoader{
			listTagsFn: func(_ context.Context, _ string) ([]string, error) {
				return []string{"5.0.0"}, nil
			},
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error (should continue on list error): %v", err)
	}
}

func TestSyncAllRevisions_EnsureRevisionError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"6.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract: &contract.Contract{
					Service: contract.Service{Name: "svc", Version: "6.0.0"},
				},
				RawYAML:     []byte("v6-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	s := newScheme()
	r.Client = fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
					return fmt.Errorf("simulated create error")
				}
				return c.Create(ctx, obj, opts...)
			},
		}).Build()

	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error (should continue on ensureRevision error): %v", err)
	}
}

// ---------- resolveOCIAuth ----------

func TestResolveOCIAuth_Token(t *testing.T) {
	s := newScheme()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("ghp_mytoken123")},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	r := &PactoReconciler{Client: c, Scheme: s}

	auth, err := r.resolveOCIAuth(context.Background(), "default", "my-secret", "ghcr.io/org/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.RegistryToken != "ghp_mytoken123" {
		t.Fatalf("expected token ghp_mytoken123, got %s", auth.RegistryToken)
	}
}

func TestResolveOCIAuth_UsernamePassword(t *testing.T) {
	s := newScheme()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "default"},
		Data:       map[string][]byte{"username": []byte("user"), "password": []byte("pass")},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	r := &PactoReconciler{Client: c, Scheme: s}

	auth, err := r.resolveOCIAuth(context.Background(), "default", "my-secret", "ghcr.io/org/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Username != "user" || auth.Password != "pass" {
		t.Fatalf("expected user/pass, got %s/%s", auth.Username, auth.Password)
	}
}

func TestResolveOCIAuth_DockerConfigJSON(t *testing.T) {
	s := newScheme()
	dockerCfg := `{"auths":{"ghcr.io":{"username":"docker-user","password":"docker-pass"}}}`
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "docker-secret", Namespace: "default"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(dockerCfg)},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	r := &PactoReconciler{Client: c, Scheme: s}

	auth, err := r.resolveOCIAuth(context.Background(), "default", "docker-secret", "ghcr.io/org/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Username != "docker-user" || auth.Password != "docker-pass" {
		t.Fatalf("expected docker-user/docker-pass, got %s/%s", auth.Username, auth.Password)
	}
}

func TestResolveOCIAuth_MissingSecret(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &PactoReconciler{Client: c, Scheme: s}

	_, err := r.resolveOCIAuth(context.Background(), "default", "nonexistent", "ghcr.io/org/repo")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestResolveOCIAuth_InvalidKeys(t *testing.T) {
	s := newScheme()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-secret", Namespace: "default"},
		Data:       map[string][]byte{"something": []byte("else")},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	r := &PactoReconciler{Client: c, Scheme: s}

	_, err := r.resolveOCIAuth(context.Background(), "default", "bad-secret", "ghcr.io/org/repo")
	if err == nil {
		t.Fatal("expected error for invalid keys")
	}
	if !strings.Contains(err.Error(), "must contain") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
