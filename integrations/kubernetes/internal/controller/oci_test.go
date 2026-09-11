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
	"time"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
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
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0"},
		},
	}

	r := newReconciler(pacto, existingRev)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
	}

	// No stored digest: the drift check is skipped, so the loader is never called
	// (mockLoader.Load returns "not implemented" and would surface as a create attempt).
	err := r.syncAllRevisions(context.Background(), pacto, "oci://ghcr.io/org/svc", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSyncAllRevisions_ForcePushedTwice_ComparesNewestRevision covers the second half of
// finding 46's fix: a force-push leaves two revisions on the SAME ref, and the drift check
// must compare against the newest, or TagOverwritten re-fires on every reconcile forever.
func TestSyncAllRevisions_ForcePushedTwice_ComparesNewestRevision(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pacto", Namespace: "default", UID: "test-uid"},
	}

	rev := func(name, digest string, ageMinutes int) *pactov1alpha1.PactoRevision {
		return &pactov1alpha1.PactoRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Duration(ageMinutes) * time.Minute)),
				Labels:            map[string]string{pactov1alpha1.LabelPactoName: "my-pacto"},
			},
			Spec: pactov1alpha1.PactoRevisionSpec{
				Version:  "1.0.0",
				PactoRef: "my-pacto",
				Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:1.0.0", Digest: digest},
			},
		}
	}

	// "zz-newer" sorts last so List order alone cannot pick the right one either way;
	// only the creationTimestamp comparison can.
	for _, objs := range [][]client.Object{
		{pacto, rev("aa-newer", "sha256:currentdig", 1), rev("zz-older", "sha256:supersede1", 9)},
		{pacto, rev("aa-older", "sha256:supersede1", 9), rev("zz-newer", "sha256:currentdig", 1)},
	} {
		recorder := record.NewFakeRecorder(20)
		r := newReconciler(objs...)
		r.Recorder = recorder
		r.Loader = &mockLoader{
			listTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.0.0"}, nil },
			loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
				return &loader.LoadResult{
					Contract:       &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
					RawYAML:        []byte("yaml"),
					ResolvedRef:    "ghcr.io/org/svc:1.0.0",
					ResolvedDigest: "sha256:currentdig",
				}, nil
			},
		}

		if err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		select {
		case event := <-recorder.Events:
			t.Errorf("expected no event (registry matches the newest revision), got %s", event)
		default:
		}
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
	if err == nil || !strings.Contains(err.Error(), "digest check for tag 1.0.0") {
		t.Fatalf("expected the per-tag failure to be reported, got %v", err)
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
	if err == nil || !strings.Contains(err.Error(), "tag 2.0.0") {
		t.Fatalf("expected the per-tag failure to be reported, got %v", err)
	}
}

// TestSyncAllRevisions_OneBadTagDoesNotStopTheRest pins the shape of the reporting
// change: a failing tag is surfaced, but the pass still mirrors every other tag.
func TestSyncAllRevisions_OneBadTagDoesNotStopTheRest(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pacto", Namespace: "default", UID: "test-uid"},
	}

	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			return []string{"1.0.0", "2.0.0"}, nil
		},
		loadFn: func(_ context.Context, ref string, _ string) (*loader.LoadResult, error) {
			if strings.HasSuffix(ref, ":1.0.0") {
				return nil, fmt.Errorf("load failed")
			}
			return &loader.LoadResult{
				Contract:    &contract.Contract{Service: contract.Service{Name: "svc", Version: "2.0.0"}},
				RawYAML:     []byte("v2-yaml"),
				ResolvedRef: ref,
			}, nil
		},
	}

	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil)
	if err == nil || !strings.Contains(err.Error(), "tag 1.0.0") {
		t.Fatalf("expected the bad tag to be reported, got %v", err)
	}
	if strings.Contains(err.Error(), "tag 2.0.0") {
		t.Fatalf("the good tag must not be reported as a failure: %v", err)
	}

	revList := &pactov1alpha1.PactoRevisionList{}
	if err := r.List(context.Background(), revList, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list revisions: %v", err)
	}
	if len(revList.Items) != 1 {
		t.Fatalf("expected the good tag to still be mirrored, got %d revisions", len(revList.Items))
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

	// The revision index is a precondition for the whole sync, not a per-tag detail:
	// silently continuing would recreate every revision as if none existed.
	err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil)
	if err == nil || !strings.Contains(err.Error(), "failed to list revisions") {
		t.Fatalf("expected a list error, got %v", err)
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
	if err == nil || !strings.Contains(err.Error(), "revision for tag 6.0.0") {
		t.Fatalf("expected the per-tag failure to be reported, got %v", err)
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
	r := &PactoReconciler{Client: c, APIReader: c, Scheme: s}

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
	r := &PactoReconciler{Client: c, APIReader: c, Scheme: s}

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
	r := &PactoReconciler{Client: c, APIReader: c, Scheme: s}

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
	r := &PactoReconciler{Client: c, APIReader: c, Scheme: s}

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
	r := &PactoReconciler{Client: c, APIReader: c, Scheme: s}

	_, err := r.resolveOCIAuth(context.Background(), "default", "bad-secret", "ghcr.io/org/repo")
	if err == nil {
		t.Fatal("expected error for invalid keys")
	}
	if !strings.Contains(err.Error(), "must contain") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// TestSyncAllRevisions_ForcePushDetected_VPrefixedTag pins finding 46: the registry tag
// ("v1.2.0") and the contract's service.version ("1.2.0") are different strings, so a
// lookup keyed on the version label misses and the drift check never runs.
func TestSyncAllRevisions_ForcePushDetected_VPrefixedTag(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pacto", Namespace: "default", UID: "test-uid"},
	}

	existingRev := &pactov1alpha1.PactoRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-pacto-1-2-0-abc",
			Namespace: "default",
			Labels: map[string]string{
				pactov1alpha1.LabelPactoName:       "my-pacto",
				pactov1alpha1.LabelRevisionVersion: "1.2.0",
			},
		},
		Spec: pactov1alpha1.PactoRevisionSpec{
			Version:  "1.2.0",
			PactoRef: "my-pacto",
			Source:   pactov1alpha1.RevisionSource{OCI: "ghcr.io/org/svc:v1.2.0", Digest: "sha256:olddigest000"},
		},
	}

	recorder := record.NewFakeRecorder(20)
	r := newReconciler(pacto, existingRev)
	r.Recorder = recorder
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"v1.2.0"}, nil },
		loadFn: func(_ context.Context, _ string, _ string) (*loader.LoadResult, error) {
			return &loader.LoadResult{
				Contract:       &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.2.0"}},
				RawYAML:        []byte("new-yaml"),
				ResolvedRef:    "ghcr.io/org/svc:v1.2.0",
				ResolvedDigest: "sha256:newdigest111",
			}, nil
		},
	}

	if err := r.syncAllRevisions(context.Background(), pacto, "ghcr.io/org/svc", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, "TagOverwritten") {
			t.Errorf("expected TagOverwritten event, got %s", event)
		}
	default:
		t.Fatal("expected TagOverwritten event for force-pushed v-prefixed tag")
	}
}
