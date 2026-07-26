/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pactoapi "github.com/trianalab/pacto/integrations/kubernetes/api/v1alpha1"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

func TestObserveDependenciesDim(t *testing.T) {
	now := time.Now()
	prov := evidence.Provenance{Collector: "k8s-observer/sibling-resolve", DetectedAt: now}
	testNS := "test-ns"
	contractSvc := "my-service"
	window := 2 * time.Minute

	// Helper to create a Pacto CR with resolved contract status
	makePacto := func(name, ns, svcName, version, ref string) *pactoapi.Pacto {
		return &pactoapi.Pacto{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: pactoapi.PactoSpec{
				Target: pactoapi.TargetRef{ServiceName: svcName},
			},
			Status: pactoapi.PactoStatus{
				Contract: &pactoapi.ContractInfo{
					ServiceName: svcName,
					Version:     version,
					ResolvedRef: ref,
				},
			},
		}
	}

	// Helper to create a Service with ready endpoints
	makeSvc := func(name, ns string, hasEndpoints bool) (*corev1.Service, *discoveryv1.EndpointSlice) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": name},
				Ports:    []corev1.ServicePort{{Port: 80}},
			},
		}
		var slice *discoveryv1.EndpointSlice
		if hasEndpoints {
			ready := true
			port := int32(80)
			slice = &discoveryv1.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-slice",
					Namespace: ns,
					Labels:    map[string]string{discoveryv1.LabelServiceName: name},
				},
				Endpoints: []discoveryv1.Endpoint{
					{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
				},
				Ports: []discoveryv1.EndpointPort{{Port: &port}},
			}
		}
		return svc, slice
	}

	tests := []struct {
		name             string
		deps             []contract.Dependency
		objects          []runtime.Object
		existingWindows  map[string]*metav1.Time
		wantObservations []struct {
			depName string
			outcome evidence.Outcome
			value   *evidence.DependencyObservation
		}
		wantWindowUpdates []struct {
			depName      string
			windowActive bool
		}
	}{
		{
			name: "required dependency resolved to sibling with ready endpoints",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true, Compatibility: "1.x"},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.2.3", "oci://registry/payments-pacto:1.0.0"),
				func() *corev1.Service { svc, _ := makeSvc("payment-service", testNS, true); return svc }(),
				func() *discoveryv1.EndpointSlice { _, slice := makeSvc("payment-service", testNS, true); return slice }(),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Observed, value: &evidence.DependencyObservation{Reachable: true}},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "payments", windowActive: false},
			},
		},
		{
			name: "required dependency with zero ready endpoints first observation",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
				func() *corev1.Service { svc, _ := makeSvc("payment-service", testNS, false); return svc }(),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Insufficient, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "payments", windowActive: true},
			},
		},
		{
			name: "required dependency zero ready within window",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
				func() *corev1.Service { svc, _ := makeSvc("payment-service", testNS, false); return svc }(),
			},
			existingWindows: map[string]*metav1.Time{
				"dependency/payments": func() *metav1.Time { t := metav1.NewTime(now.Add(-1 * time.Minute)); return &t }(),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Insufficient, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "payments", windowActive: true},
			},
		},
		{
			name: "required dependency zero ready beyond window",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
				func() *corev1.Service { svc, _ := makeSvc("payment-service", testNS, false); return svc }(),
			},
			existingWindows: map[string]*metav1.Time{
				"dependency/payments": func() *metav1.Time { t := metav1.NewTime(now.Add(-3 * time.Minute)); return &t }(),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Observed, value: &evidence.DependencyObservation{Reachable: false}},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "payments", windowActive: true},
			},
		},
		{
			name: "required dependency Service NotFound beyond window",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
				// Service does not exist
			},
			existingWindows: map[string]*metav1.Time{
				"dependency/payments": func() *metav1.Time { t := metav1.NewTime(now.Add(-3 * time.Minute)); return &t }(),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Observed, value: &evidence.DependencyObservation{Reachable: false}},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "payments", windowActive: true},
			},
		},
		{
			name: "no matching sibling CR (external dependency)",
			deps: []contract.Dependency{
				{Name: "external-api", Ref: "oci://registry/external-api:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				// No Pacto CR for external-api
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "external-api", outcome: evidence.Unsupported, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{},
		},
		{
			name: "sibling CR with ExternalName Service",
			deps: []contract.Dependency{
				{Name: "external-svc", Ref: "oci://registry/external-svc:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("external-svc-pacto", testNS, "external-service", "1.0.0", "oci://registry/external-svc:1.0.0"),
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "external-service", Namespace: testNS},
					Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName},
				},
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "external-svc", outcome: evidence.Unsupported, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{},
		},
		{
			name: "sibling CR with reference-only contract (empty serviceName)",
			deps: []contract.Dependency{
				{Name: "ref-only", Ref: "oci://registry/ref-only:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				&pactoapi.Pacto{
					ObjectMeta: metav1.ObjectMeta{Name: "ref-only-pacto", Namespace: testNS},
					Spec:       pactoapi.PactoSpec{},
					Status: pactoapi.PactoStatus{
						Contract: &pactoapi.ContractInfo{
							ServiceName: "ref-service",
							Version:     "1.0.0",
							ResolvedRef: "oci://registry/ref-only:1.0.0",
						},
					},
				},
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "ref-only", outcome: evidence.Insufficient, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{},
		},
		{
			name: "optional dependency unreachable emits Observed",
			deps: []contract.Dependency{
				{Name: "cache", Ref: "oci://registry/cache:1.0.0", Required: false},
			},
			objects: []runtime.Object{
				makePacto("cache-pacto", testNS, "cache-service", "1.0.0", "oci://registry/cache:1.0.0"),
				func() *corev1.Service { svc, _ := makeSvc("cache-service", testNS, false); return svc }(),
			},
			existingWindows: map[string]*metav1.Time{
				"dependency/cache": func() *metav1.Time { t := metav1.NewTime(now.Add(-3 * time.Minute)); return &t }(),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "cache", outcome: evidence.Observed, value: &evidence.DependencyObservation{Reachable: false}},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "cache", windowActive: true},
			},
		},
		{
			name: "compatibility semver match",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto", Required: true, Compatibility: "1.x"},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.5.0", "oci://registry/payments-pacto:1.5.0"),
				func() *corev1.Service { svc, _ := makeSvc("payment-service", testNS, true); return svc }(),
				func() *discoveryv1.EndpointSlice { _, slice := makeSvc("payment-service", testNS, true); return slice }(),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Observed, value: &evidence.DependencyObservation{Reachable: true}},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "payments", windowActive: false},
			},
		},
		{
			name: "sibling CR without status.contract",
			deps: []contract.Dependency{
				{Name: "no-status", Ref: "oci://registry/no-status:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				&pactoapi.Pacto{
					ObjectMeta: metav1.ObjectMeta{Name: "no-status-pacto", Namespace: testNS},
					Spec:       pactoapi.PactoSpec{Target: pactoapi.TargetRef{ServiceName: "no-status-service"}},
					Status:     pactoapi.PactoStatus{}, // No Contract field
				},
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "no-status", outcome: evidence.Unsupported, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{},
		},
		{
			name: "compatibility constraint does not match",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto", Required: true, Compatibility: "2.x"},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Unsupported, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{},
		},
		{
			name: "sibling CR with selector-less Service (first negative)",
			deps: []contract.Dependency{
				{Name: "headless", Ref: "oci://registry/headless:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("headless-pacto", testNS, "headless-service", "1.0.0", "oci://registry/headless:1.0.0"),
				&corev1.Service{
					ObjectMeta: metav1.ObjectMeta{Name: "headless-service", Namespace: testNS},
					Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}}, // No selector, so no EndpointSlices
				},
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "headless", outcome: evidence.Insufficient, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "headless", windowActive: true},
			},
		},
		{
			name: "sibling CR exists but ref does not match",
			deps: []contract.Dependency{
				{Name: "different", Ref: "oci://registry/different-service:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("other-pacto", testNS, "other-service", "1.0.0", "oci://registry/other-service:1.0.0"),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "different", outcome: evidence.Unsupported, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{},
		},
		{
			name: "ambiguous multi-match (blue-green, equal resolvedRef) -> Insufficient",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				// Two siblings share the resolvedRef but expose distinct target services -> an arbitrary
				// first-match pick would risk a wrong verdict, so the resolution must be Insufficient.
				makePacto("payments-blue", testNS, "payment-blue", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
				makePacto("payments-green", testNS, "payment-green", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Insufficient, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{},
		},
		{
			name: "dependency with all not-ready endpoints (not just missing slices)",
			deps: []contract.Dependency{
				{Name: "payments", Ref: "oci://registry/payments-pacto:1.0.0", Required: true},
			},
			objects: []runtime.Object{
				makePacto("payments-pacto", testNS, "payment-service", "1.0.0", "oci://registry/payments-pacto:1.0.0"),
				func() *corev1.Service { svc, _ := makeSvc("payment-service", testNS, false); return svc }(),
				&discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "payment-service-slice",
						Namespace: testNS,
						Labels:    map[string]string{discoveryv1.LabelServiceName: "payment-service"},
					},
					Endpoints: []discoveryv1.Endpoint{
						{Conditions: discoveryv1.EndpointConditions{Ready: func() *bool { r := false; return &r }()}},
					},
					Ports: []discoveryv1.EndpointPort{{Port: func() *int32 { p := int32(80); return &p }()}},
				},
			},
			wantObservations: []struct {
				depName string
				outcome evidence.Outcome
				value   *evidence.DependencyObservation
			}{
				{depName: "payments", outcome: evidence.Insufficient, value: nil},
			},
			wantWindowUpdates: []struct {
				depName      string
				windowActive bool
			}{
				{depName: "payments", windowActive: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = pactoapi.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)
			_ = discoveryv1.AddToScheme(scheme)

			client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.objects...).Build()
			obs := New(client)

			input := CollectInput{
				Namespace:           testNS,
				ServiceName:         "test-svc",
				Contract:            &contract.Contract{Service: contract.Service{Name: contractSvc}, Dependencies: tt.deps},
				StabilizationWindow: window,
				ObservationWindows:  tt.existingWindows,
				Now:                 now,
			}

			gotObs, gotUpdates := obs.observeDependenciesDim(context.Background(), input, prov, now)

			if len(gotObs) != len(tt.wantObservations) {
				t.Fatalf("got %d observations, want %d", len(gotObs), len(tt.wantObservations))
			}

			for i, want := range tt.wantObservations {
				got := gotObs[i]
				if got.Subject.Kind != "dependency" {
					t.Errorf("observation[%d] Subject.Kind = %q, want %q", i, got.Subject.Kind, "dependency")
				}
				if got.Subject.Name != want.depName {
					t.Errorf("observation[%d] Subject.Name = %q, want %q", i, got.Subject.Name, want.depName)
				}
				if got.Outcome != want.outcome {
					t.Errorf("observation[%d] Outcome = %q, want %q", i, got.Outcome, want.outcome)
				}
				if want.value != nil {
					val, err := got.GetDependencyObservation()
					if err != nil {
						t.Errorf("observation[%d] GetDependencyObservation() error = %v", i, err)
					} else if val.Reachable != want.value.Reachable {
						t.Errorf("observation[%d] Reachable = %v, want %v", i, val.Reachable, want.value.Reachable)
					}
				} else if got.Outcome == evidence.Observed {
					t.Errorf("observation[%d] has Outcome=Observed but want.value is nil", i)
				}
			}

			// Verify window updates
			if len(gotUpdates) != len(tt.wantWindowUpdates) {
				t.Fatalf("got %d window updates, want %d", len(gotUpdates), len(tt.wantWindowUpdates))
			}
			for i, want := range tt.wantWindowUpdates {
				got := gotUpdates[i]
				if got.Kind != "dependency" {
					t.Errorf("windowUpdate[%d] Kind = %q, want %q", i, got.Kind, "dependency")
				}
				if got.Subject != want.depName {
					t.Errorf("windowUpdate[%d] Subject = %q, want %q", i, got.Subject, want.depName)
				}
				if want.windowActive && got.FirstObservedNegativeAt == nil {
					t.Errorf("windowUpdate[%d] window should be active but FirstObservedNegativeAt is nil", i)
				}
				if !want.windowActive && got.FirstObservedNegativeAt != nil {
					t.Errorf("windowUpdate[%d] window should be cleared but FirstObservedNegativeAt = %v", i, got.FirstObservedNegativeAt)
				}
			}
		})
	}
}

func TestObserveDependenciesDim_SubjectIdentity(t *testing.T) {
	// INV-1b: Subject.Name must be the CONTRACT dependency name, not the k8s namespace/service target.
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

	client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pacto, svc, slice).Build()
	obs := New(client)

	input := CollectInput{
		Namespace:   testNS,
		ServiceName: "my-service",
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

	got := gotObs[0]
	if got.Subject.Kind != "dependency" {
		t.Errorf("Subject.Kind = %q, want %q", got.Subject.Kind, "dependency")
	}
	if got.Subject.Name != "payments" {
		t.Errorf("Subject.Name = %q, want %q (the CONTRACT dependency name, not %q)", got.Subject.Name, "payments", testNS+"/payment-service")
	}
}

func TestStripRefSuffix(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "unversioned", ref: "oci://registry/service", want: "oci://registry/service"},
		{name: "tagged", ref: "oci://registry/service:1.0.0", want: "oci://registry/service"},
		{name: "digest", ref: "oci://registry/service@sha256:abcd", want: "oci://registry/service"},
		{name: "no scheme", ref: "registry/service:1.0.0", want: "registry/service"},
		{name: "empty", ref: "", want: ""},
		{name: "short oci", ref: "oci://", want: "oci://"},
		// host:port authority: the registry port colon must NOT be mistaken for a tag separator.
		{name: "ported registry untagged", ref: "oci://registry:5000/payments", want: "oci://registry:5000/payments"},
		{name: "ported registry tagged", ref: "oci://registry:5000/payments:1.2.3", want: "oci://registry:5000/payments"},
		{name: "ported registry digest", ref: "oci://registry:5000/payments@sha256:abcd", want: "oci://registry:5000/payments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripRefSuffix(tt.ref)
			if got != tt.want {
				t.Errorf("stripRefSuffix(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestMatchesCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		version    string
		want       bool
	}{
		{name: "1.x matches 1.0.0", constraint: "1.x", version: "1.0.0", want: true},
		{name: "1.x matches 1.5.3", constraint: "1.x", version: "1.5.3", want: true},
		{name: "1.x does not match 2.0.0", constraint: "1.x", version: "2.0.0", want: false},
		{name: "2.x matches 2.1.0", constraint: "2.x", version: "2.1.0", want: true},
		// Real npm-style ranges (the canonical/validated form) must resolve, not fall through to equality.
		{name: "caret matches within major", constraint: "^1.0.0", version: "1.4.2", want: true},
		{name: "caret does not cross major", constraint: "^1.0.0", version: "2.0.0", want: false},
		{name: "tilde matches within minor", constraint: "~1.2.0", version: "1.2.9", want: true},
		{name: "tilde does not cross minor", constraint: "~1.2.0", version: "1.3.0", want: false},
		{name: "exact match", constraint: "1.2.3", version: "1.2.3", want: true},
		{name: "exact no match", constraint: "1.2.3", version: "1.2.4", want: false},
		{name: "invalid constraint never matches", constraint: "", version: "1.0.0", want: false},
		{name: "unparseable version never matches", constraint: "^1.0.0", version: "not-semver", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesCompatibility(tt.constraint, tt.version)
			if got != tt.want {
				t.Errorf("matchesCompatibility(%q, %q) = %v, want %v", tt.constraint, tt.version, got, tt.want)
			}
		})
	}
}

func TestCountReadyEndpointsForService(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = discoveryv1.AddToScheme(scheme)

	ready := true
	notReady := false
	port := int32(80)

	tests := []struct {
		name    string
		objects []runtime.Object
		want    int
	}{
		{
			name:    "empty slices list",
			objects: []runtime.Object{},
			want:    0,
		},
		{
			name: "all ready endpoints",
			objects: []runtime.Object{
				&discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-slice", Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: "test-svc"}},
					Endpoints:  []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}}, {Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
					Ports:      []discoveryv1.EndpointPort{{Port: &port}},
				},
			},
			want: 2,
		},
		{
			name: "mixed ready and not ready",
			objects: []runtime.Object{
				&discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-slice", Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: "test-svc"}},
					Endpoints:  []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}}, {Conditions: discoveryv1.EndpointConditions{Ready: &notReady}}},
					Ports:      []discoveryv1.EndpointPort{{Port: &port}},
				},
			},
			want: 1,
		},
		{
			name: "multiple slices",
			objects: []runtime.Object{
				&discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-slice-1", Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: "test-svc"}},
					Endpoints:  []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
					Ports:      []discoveryv1.EndpointPort{{Port: &port}},
				},
				&discoveryv1.EndpointSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "svc-slice-2", Namespace: "test", Labels: map[string]string{discoveryv1.LabelServiceName: "test-svc"}},
					Endpoints:  []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
					Ports:      []discoveryv1.EndpointPort{{Port: &port}},
				},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tt.objects...).Build()
			obs := New(client)
			got := obs.countReadyEndpointsForService(context.Background(), "test", "test-svc")
			if got != tt.want {
				t.Errorf("countReadyEndpointsForService() = %d, want %d", got, tt.want)
			}
		})
	}
}
