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

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"github.com/trianalab/pacto-operator/internal/loader"
	"github.com/trianalab/pacto/v2/pkg/contract"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Test applyConfigurationOverrides with configuration not found error
func TestApplyConfigurationOverrides_ConfigNotFound(t *testing.T) {
	c := &contract.Contract{
		Service: contract.Service{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "default", Values: map[string]any{"key": "value"}},
		},
	}

	overrides := &pactov1alpha1.ContractOverrides{
		Configurations: []pactov1alpha1.ConfigurationOverride{
			{Name: "nonexistent", Values: map[string]string{"key": "override"}},
		},
	}

	_, _, err := applyConfigurationOverrides(c, overrides)
	if err == nil {
		t.Fatal("expected error for nonexistent configuration")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Test applyConfigurationOverrides with nil values in contract (creates new map)
func TestApplyConfigurationOverrides_NilValues(t *testing.T) {
	c := &contract.Contract{
		Service: contract.Service{Name: "svc", Version: "1.0.0"},
		Configurations: []contract.Configuration{
			{Name: "default", Values: nil},
		},
	}

	overrides := &pactov1alpha1.ContractOverrides{
		Configurations: []pactov1alpha1.ConfigurationOverride{
			{Name: "default", Values: map[string]string{"new_key": "new_value"}},
		},
	}

	effective, keys, err := applyConfigurationOverrides(c, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if effective.Configurations[0].Values["new_key"] != "new_value" {
		t.Error("expected override value to be set")
	}
	if len(keys["default"]) != 1 || keys["default"][0] != "new_key" {
		t.Errorf("unexpected overridden keys: %v", keys)
	}
}

// Test ensureRevision with Get error (non-NotFound)
func TestEnsureRevision_GetError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: "uid"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.Service{Name: "svc", Version: "1.0.0"},
		},
		RawYAML:     []byte("yaml"),
		ResolvedRef: "ref",
	}

	s := newScheme()
	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
						return fmt.Errorf("get failed")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(10),
	}

	_, err := r.ensureRevision(context.Background(), pacto, lr)
	if err == nil {
		t.Fatal("expected error from Get failure")
	}
	if !strings.Contains(err.Error(), "failed to check for existing revision") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Test ensureRevision with Create error (non-AlreadyExists)
func TestEnsureRevision_CreateError(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default", UID: "uid"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.Service{Name: "svc", Version: "1.0.0"},
		},
		RawYAML:     []byte("yaml"),
		ResolvedRef: "ref",
	}

	s := newScheme()
	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).WithObjects(pacto).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*pactov1alpha1.PactoRevision); ok {
						return fmt.Errorf("create failed")
					}
					return c.Create(ctx, obj, opts...)
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(10),
	}

	_, err := r.ensureRevision(context.Background(), pacto, lr)
	if err == nil {
		t.Fatal("expected error from Create failure")
	}
	if !strings.Contains(err.Error(), "failed to create PactoRevision") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Test Reconcile with Pacto Get error (non-NotFound)
func TestReconcile_GetError(t *testing.T) {
	s := newScheme()
	r := &PactoReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*pactov1alpha1.Pacto); ok {
						return fmt.Errorf("get failed")
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(10),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "test", Namespace: "default"}})
	if err == nil {
		t.Fatal("expected error from Get failure")
	}
	if !strings.Contains(err.Error(), "get failed") {
		t.Errorf("unexpected error: %v", err)
	}
}
