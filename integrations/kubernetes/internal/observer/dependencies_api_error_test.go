/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/api/v1alpha1"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

// TestObserveDependenciesDim_APIErrors tests various API error scenarios.
func TestObserveDependenciesDim_APIErrors(t *testing.T) {
	now := time.Now()
	prov := evidence.Provenance{Collector: "k8s-observer/sibling-resolve", DetectedAt: now}
	testNS := "test-ns"

	scheme := runtime.NewScheme()
	_ = pactoapi.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = discoveryv1.AddToScheme(scheme)

	pacto := &pactoapi.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-pacto", Namespace: testNS},
		Spec: pactoapi.PactoSpec{
			Target: pactoapi.TargetRef{ServiceName: "payment-service"},
		},
		Status: pactoapi.PactoStatus{
			Contract: &pactoapi.ContractInfo{
				ServiceName: "payment-service",
				Version:     "1.0.0",
				ResolvedRef: "oci://registry/payments-pacto:1.0.0",
			},
		},
	}

	t.Run("Service GET non-NotFound error", func(t *testing.T) {
		// Use an interceptor to force a GET error on Service objects.
		clientWithError := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(pacto).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*corev1.Service); ok {
						return errors.NewInternalError(fmt.Errorf("forced Service GET error"))
					}
					return client.Get(ctx, key, obj, opts...)
				},
			}).
			Build()

		obs := New(clientWithError)
		input := CollectInput{
			Namespace:   testNS,
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Service:      contract.Service{Name: "my-service"},
				Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true}},
			},
			StabilizationWindow: 2 * time.Minute,
			Now:                 now,
		}

		gotObs, _ := obs.observeDependenciesDim(context.Background(), input, prov, now)

		if len(gotObs) != 1 {
			t.Fatalf("got %d observations, want 1", len(gotObs))
		}

		if gotObs[0].Outcome != evidence.Failed {
			t.Errorf("Outcome = %q, want %q", gotObs[0].Outcome, evidence.Failed)
		}
	})

	t.Run("EndpointSlice LIST error", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "payment-service", Namespace: testNS},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "payment"},
				Ports:    []corev1.ServicePort{{Port: 80}},
			},
		}

		// Use an interceptor to force a LIST error on EndpointSlice objects.
		clientWithError := fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(pacto, svc).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*discoveryv1.EndpointSliceList); ok {
						return errors.NewInternalError(fmt.Errorf("forced EndpointSlice LIST error"))
					}
					return client.List(ctx, list, opts...)
				},
			}).
			Build()

		obs := New(clientWithError)
		input := CollectInput{
			Namespace:   testNS,
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Service:      contract.Service{Name: "my-service"},
				Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true}},
			},
			StabilizationWindow: 2 * time.Minute,
			Now:                 now,
		}

		gotObs, _ := obs.observeDependenciesDim(context.Background(), input, prov, now)

		if len(gotObs) != 1 {
			t.Fatalf("got %d observations, want 1", len(gotObs))
		}

		if gotObs[0].Outcome != evidence.Failed {
			t.Errorf("Outcome = %q, want %q", gotObs[0].Outcome, evidence.Failed)
		}
	})

	t.Run("Pacto CR LIST error", func(t *testing.T) {
		// Use an interceptor to force a LIST error on Pacto CR LIST.
		clientWithError := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*pactoapi.PactoList); ok {
						return errors.NewInternalError(fmt.Errorf("forced Pacto LIST error"))
					}
					return client.List(ctx, list, opts...)
				},
			}).
			Build()

		obs := New(clientWithError)
		input := CollectInput{
			Namespace:   testNS,
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Service:      contract.Service{Name: "my-service"},
				Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true}},
			},
			StabilizationWindow: 2 * time.Minute,
			Now:                 now,
		}

		gotObs, _ := obs.observeDependenciesDim(context.Background(), input, prov, now)

		if len(gotObs) != 1 {
			t.Fatalf("got %d observations, want 1", len(gotObs))
		}

		if gotObs[0].Outcome != evidence.Failed {
			t.Errorf("Outcome = %q, want %q (COLLECTION_FAILED from Pacto LIST error)", gotObs[0].Outcome, evidence.Failed)
		}
	})
}

// TestCollect_Dependencies tests that the dependencies producer is called when deps are declared.
func TestCollect_Dependencies(t *testing.T) {
	now := time.Now()
	testNS := "test-ns"

	scheme := runtime.NewScheme()
	_ = pactoapi.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = discoveryv1.AddToScheme(scheme)

	pacto := &pactoapi.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-pacto", Namespace: testNS},
		Spec: pactoapi.PactoSpec{
			Target: pactoapi.TargetRef{ServiceName: "payment-service"},
		},
		Status: pactoapi.PactoStatus{
			Contract: &pactoapi.ContractInfo{
				ServiceName: "payment-service",
				Version:     "1.0.0",
				ResolvedRef: "oci://registry/payments-pacto:1.0.0",
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "payment-service", Namespace: testNS},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "payment"},
			Ports:    []corev1.ServicePort{{Port: 80}},
		},
	}
	ready := true
	port := int32(80)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-service-slice",
			Namespace: testNS,
			Labels:    map[string]string{discoveryv1.LabelServiceName: "payment-service"},
		},
		Endpoints: []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
		Ports:     []discoveryv1.EndpointPort{{Port: &port}},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pacto, svc, slice).Build()
	obs := New(fakeClient)

	input := CollectInput{
		Namespace:    testNS,
		ServiceName:  "my-service",
		WorkloadName: "my-deployment",
		WorkloadKind: "Deployment",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "my-service"},
			Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true}},
		},
		StabilizationWindow: 2 * time.Minute,
		Now:                 now,
	}

	es, windowUpdates := obs.Collect(context.Background(), input)

	// Check that we have at least one dependency observation.
	foundDep := false
	for _, o := range es.Observations {
		if o.Kind == evidence.DependencyReachable {
			foundDep = true
			if o.Subject.Name != "payments" {
				t.Errorf("dependency observation Subject.Name = %q, want %q", o.Subject.Name, "payments")
			}
		}
	}

	if !foundDep {
		t.Errorf("Collect() did not emit a dependency observation")
	}

	// Check that we have a window update for the dependency.
	foundUpdate := false
	for _, u := range windowUpdates {
		if u.Kind == "dependency" && u.Subject == "payments" {
			foundUpdate = true
		}
	}

	if !foundUpdate {
		t.Errorf("Collect() did not emit a window update for the dependency")
	}
}

// TestCollect_NoDependencies tests that no dependency observations are emitted when no deps are declared.
func TestCollect_NoDependencies(t *testing.T) {
	now := time.Now()
	testNS := "test-ns"

	scheme := runtime.NewScheme()
	_ = pactoapi.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	obs := New(fakeClient)

	input := CollectInput{
		Namespace:    testNS,
		ServiceName:  "my-service",
		WorkloadName: "my-deployment",
		WorkloadKind: "Deployment",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "my-service"},
			Dependencies: nil,
		},
		StabilizationWindow: 2 * time.Minute,
		Now:                 now,
	}

	es, windowUpdates := obs.Collect(context.Background(), input)

	// Check that no dependency observations are emitted.
	for _, o := range es.Observations {
		if o.Kind == evidence.DependencyReachable {
			t.Errorf("Collect() emitted unexpected dependency observation when no deps declared")
		}
	}

	// Check that no dependency window updates are emitted.
	for _, u := range windowUpdates {
		if u.Kind == "dependency" {
			t.Errorf("Collect() emitted unexpected dependency window update when no deps declared")
		}
	}
}
