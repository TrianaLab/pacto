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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

func TestRevisionMirror_IsRunnable(t *testing.T) {
	var m any = &RevisionMirror{}
	if _, ok := m.(manager.Runnable); !ok {
		t.Error("must implement manager.Runnable: cmd/main.go registers it with mgr.Add")
	}
}

func mirrorPacto(name, ociRef, pullSecret string) *pactov1alpha1.Pacto {
	return &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("u-" + name)},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: ociRef, PullSecretRef: pullSecret},
		},
	}
}

func loadOK(_ context.Context, ref, _ string) (*loader.LoadResult, error) {
	return &loader.LoadResult{
		Contract:    &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
		RawYAML:     []byte("yaml"),
		ResolvedRef: ref,
	}, nil
}

// TestRevisionMirror_Sync_MirrorsOnlyTagShapedRefs covers the whole walk: a
// tag-shaped ref is mirrored, and the refs with no tag set to enumerate (inline,
// digest-pinned) are skipped before any registry call.
func TestRevisionMirror_Sync_MirrorsOnlyTagShapedRefs(t *testing.T) {
	tagged := mirrorPacto("tagged", "ghcr.io/org/svc:1.0.0", "")
	inline := mirrorPacto("inline", "", "")
	pinned := mirrorPacto("pinned", "ghcr.io/org/svc@sha256:abc", "")

	var listed []string
	r := newReconciler(tagged, inline, pinned)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, ref string) ([]string, error) {
			listed = append(listed, ref)
			return []string{"1.0.0"}, nil
		},
		loadFn: loadOK,
	}

	m := &RevisionMirror{Reconciler: r}
	if err := m.Sync(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(listed) != 1 || listed[0] != "ghcr.io/org/svc:1.0.0" {
		t.Fatalf("expected only the tag-shaped ref to reach the registry, got %v", listed)
	}

	revList := &pactov1alpha1.PactoRevisionList{}
	if err := r.List(context.Background(), revList, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list revisions: %v", err)
	}
	if len(revList.Items) != 1 {
		t.Fatalf("expected 1 mirrored revision, got %d", len(revList.Items))
	}
}

func TestRevisionMirror_Sync_ListError(t *testing.T) {
	s := newScheme()
	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
					return fmt.Errorf("simulated list error")
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader:   &mockLoader{},
	}

	err := (&RevisionMirror{Reconciler: r}).Sync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to list pactos") {
		t.Fatalf("expected a list error, got %v", err)
	}
}

// TestRevisionMirror_FailuresBecomeEvents proves the mirror speaks the main load
// path's vocabulary instead of the V(1) lines that used to hide every failure:
// classifyLoadError picks ContractUnavailable for a transient obtain-failure and
// ContractInvalid for anything else.
func TestRevisionMirror_FailuresBecomeEvents(t *testing.T) {
	for _, tc := range []struct {
		name       string
		listErr    error
		wantReason string
	}{
		{
			name:       "transient obtain failure",
			listErr:    &oci.RegistryUnreachableError{Ref: "ghcr.io/org/svc:1.0.0", Err: fmt.Errorf("dial tcp: no route to host")},
			wantReason: pactov1alpha1.EventContractUnavailable,
		},
		{
			name:       "anything else",
			listErr:    fmt.Errorf("malformed reference"),
			wantReason: pactov1alpha1.EventContractInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pacto := mirrorPacto("broken", "ghcr.io/org/svc:1.0.0", "")
			recorder := record.NewFakeRecorder(20)
			r := newReconciler(pacto)
			r.Recorder = recorder
			r.Loader = &mockLoader{
				listTagsFn: func(_ context.Context, _ string) ([]string, error) { return nil, tc.listErr },
			}

			if err := (&RevisionMirror{Reconciler: r}).Sync(context.Background()); err != nil {
				t.Fatalf("a per-Pacto failure must not fail the pass: %v", err)
			}
			select {
			case event := <-recorder.Events:
				if !strings.Contains(event, tc.wantReason) {
					t.Errorf("expected %s, got %s", tc.wantReason, event)
				}
			default:
				t.Fatal("expected an event for the mirror failure")
			}
		})
	}
}

func TestRevisionMirror_PullSecretFailureBecomesAnEvent(t *testing.T) {
	pacto := mirrorPacto("needs-auth", "ghcr.io/org/svc:1.0.0", "missing-secret")
	recorder := record.NewFakeRecorder(20)
	r := newReconciler(pacto)
	r.Recorder = recorder
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			t.Error("the registry must not be touched without credentials")
			return nil, nil
		},
	}

	if err := (&RevisionMirror{Reconciler: r}).Sync(context.Background()); err != nil {
		t.Fatalf("a per-Pacto failure must not fail the pass: %v", err)
	}
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, `pull secret "missing-secret"`) {
			t.Errorf("expected the pull secret to be named, got %s", event)
		}
	default:
		t.Fatal("expected an event for the pull secret failure")
	}
}

func TestRevisionMirror_UsesThePullSecret(t *testing.T) {
	pacto := mirrorPacto("authed", "ghcr.io/org/svc:1.0.0", "creds")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("ghp_tok")},
	}

	r := newReconciler(pacto, secret)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) { return []string{"1.0.0"}, nil },
		loadFn:     loadOK,
	}

	if err := (&RevisionMirror{Reconciler: r}).Sync(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	revList := &pactov1alpha1.PactoRevisionList{}
	if err := r.List(context.Background(), revList, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list revisions: %v", err)
	}
	if len(revList.Items) != 1 {
		t.Fatalf("expected the authenticated mirror to create a revision, got %d", len(revList.Items))
	}
}

// TestRevisionMirror_Start_TicksUntilCancelled exercises the manager.Runnable body:
// component.Run syncs once up front and then on the ticker until the context ends.
func TestRevisionMirror_Start_TicksUntilCancelled(t *testing.T) {
	pacto := mirrorPacto("ticker", "ghcr.io/org/svc:1.0.0", "")
	syncs := make(chan struct{}, 8)
	r := newReconciler(pacto)
	r.Loader = &mockLoader{
		listTagsFn: func(_ context.Context, _ string) ([]string, error) {
			select {
			case syncs <- struct{}{}:
			default:
			}
			return nil, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- (&RevisionMirror{Reconciler: r, Interval: time.Millisecond}).Start(ctx) }()

	for range 2 {
		select {
		case <-syncs:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for a mirror pass")
		}
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the context was cancelled")
	}
}
