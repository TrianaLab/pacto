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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto-operator/internal/prober"
	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
)

func TestObserveHealthDim_NoBinding(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{Type: contract.CapabilityHealth}
	input := CollectInput{
		Contract:            &contract.Contract{Service: contract.Service{Name: "test"}},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported, got %s", obs.Outcome)
	}
	if obs.Subject.Kind != "capability" || obs.Subject.Name != "health" {
		t.Errorf("expected Subject{capability,health}, got %+v", obs.Subject)
	}
	if len(updates) != 0 {
		t.Errorf("expected no window updates, got %d", len(updates))
	}
}

func TestObserveHealthDim_NonHTTPBinding(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      "grpc",
			Interface: "api",
		},
	}
	input := CollectInput{
		Contract:            &contract.Contract{Service: contract.Service{Name: "test"}},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported for non-http binding, got %s", obs.Outcome)
	}
}

func TestObserveHealthDim_NoInterfaceBinding(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Contract:            &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings:   []InterfaceBinding{},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when owning interface has no binding, got %s", obs.Outcome)
	}
}

func TestObserveHealthDim_ServiceNotFound(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract:    &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when Service not found, got %s", obs.Outcome)
	}
}

func TestObserveHealthDim_ServicePortNotFound(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 9090},
			},
		},
	}
	o := &Observer{client: fake.NewClientBuilder().WithObjects(svc).Build()}
	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract:    &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when bound port not in Service, got %s", obs.Outcome)
	}
}

func TestObserveHealthDim_TierA_404_Windowed(t *testing.T) {
	// Test the stabilize function directly (the core windowing logic).
	now := time.Now()
	stabilizationWindow := 2 * time.Minute

	isNegative := true
	outcome, updatedWindow := stabilize(nil, isNegative, now, stabilizationWindow)
	if outcome != evidence.Insufficient {
		t.Errorf("first negative should be Insufficient, got %s", outcome)
	}
	if updatedWindow == nil {
		t.Fatal("expected window to be set")
	}

	// Second observation (within window) -> still Insufficient.
	now2 := now.Add(1 * time.Minute)
	outcome2, updatedWindow2 := stabilize(updatedWindow, isNegative, now2, stabilizationWindow)
	if outcome2 != evidence.Insufficient {
		t.Errorf("within window should be Insufficient, got %s", outcome2)
	}
	if updatedWindow2 != updatedWindow {
		t.Error("window should not change within window")
	}

	// Third observation (beyond window) -> Observed.
	now3 := now.Add(3 * time.Minute)
	outcome3, updatedWindow3 := stabilize(updatedWindow, isNegative, now3, stabilizationWindow)
	if outcome3 != evidence.Observed {
		t.Errorf("beyond window should be Observed, got %s", outcome3)
	}
	if updatedWindow3 != updatedWindow {
		t.Error("window should remain unchanged when beyond window")
	}
}

func TestObserveHealthDim_TierB_ReadinessProbe_Satisfied(t *testing.T) {
	// Create a Service.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	// Create a Deployment with an httpGet readiness probe.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "test:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(8080),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create EndpointSlice with Ready endpoint.
	ready := true
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep, slice).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// The direct probe will fail (cannot reach in-cluster DNS), so tier B should kick in.
	// Tier B: httpGet readiness probe + ready endpoint -> satisfied.
	if obs.Outcome != evidence.Observed {
		t.Errorf("expected Observed for tier B satisfied, got %s", obs.Outcome)
	}

	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if !payload.Present {
		t.Error("expected Present=true for tier B satisfied")
	}

	if len(updates) != 0 {
		t.Errorf("expected no window updates for satisfied, got %d", len(updates))
	}
}

func TestObserveHealthDim_TierB_LivenessOnly_Insufficient(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	// Deployment with only liveness probe (no readiness).
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "test:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(8080),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	ready := true
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep, slice).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// No active probe, tier B has liveness-only -> no usable evidence -> EVIDENCE_INSUFFICIENT (spec section 7.4).
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for liveness-only, got %s", obs.Outcome)
	}
}

func TestObserveHealthDim_TierB_NotReady_Failed(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "test:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(8080),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// EndpointSlice with not-ready endpoint.
	ready := false
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep, slice).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Direct probe fails, tier B has readiness probe but not ready -> Failed.
	if obs.Outcome != evidence.Failed {
		t.Errorf("expected Failed for not-ready, got %s", obs.Outcome)
	}
}

func TestObserveHealthDim_SubjectIdentity(t *testing.T) {
	// Verify that Subject.Name is the capability AssertionKey ("health"), NOT the k8s target.
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{Type: contract.CapabilityHealth}
	input := CollectInput{
		Namespace:           "ns",
		ServiceName:         "svc",
		Contract:            &contract.Contract{Service: contract.Service{Name: "my-service"}},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	if obs.Subject.Kind != "capability" {
		t.Errorf("expected Subject.Kind=capability, got %s", obs.Subject.Kind)
	}
	if obs.Subject.Name != "health" {
		t.Errorf("expected Subject.Name=health (AssertionKey), got %s", obs.Subject.Name)
	}
	if obs.Subject.Name == "ns/svc" {
		t.Error("Subject.Name must NOT be the k8s target")
	}
}

func TestObserveHealthDim_Sidecar_CorrectContainer(t *testing.T) {
	// Verify that the readiness probe check finds the correct container (not just Containers[0]).
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(9090)},
			},
		},
	}

	// Deployment with sidecar (first container) and app container (second) with readiness probe.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "sidecar",
							Image: "sidecar:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 7070},
							},
							// No readiness probe.
						},
						{
							Name:  "app",
							Image: "app:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 9090},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(9090),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	ready := true
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep, slice).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Tier B should find the app container's readiness probe (not the sidecar).
	if obs.Outcome != evidence.Observed {
		t.Errorf("expected Observed (tier B found app container), got %s", obs.Outcome)
	}

	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if !payload.Present {
		t.Error("expected Present=true for sidecar-safe readiness check")
	}
}

func TestObserveHealthDim_NamedTargetPort(t *testing.T) {
	// Verify that named target ports are resolved correctly.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromString("http")},
			},
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "app:latest",
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: 9090},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromString("http"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	ready := true
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep, slice).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Tier B should resolve the named target port.
	if obs.Outcome != evidence.Observed {
		t.Errorf("expected Observed (named target port resolved), got %s", obs.Outcome)
	}

	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if !payload.Present {
		t.Error("expected Present=true for named target port")
	}
}

// TestObserveHealthDim_TierB_UsesOwningBindingNotFirst proves the Tier-B readiness fallback checks the
// health capability's OWNING binding port, not an arbitrary InterfaceBindings[0]. Bindings are ordered
// [{metrics,9090},{api,8080}] with health owning "api". The metrics container (9090) is Ready with a
// readiness probe, so the old [0]-based resolution would over-claim SATISFIED; the api container (8080)
// has NO readiness probe, so checking the owning binding must yield Failed.
func TestObserveHealthDim_TierB_UsesOwningBindingNotFirst(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 9090, TargetPort: intstr.FromInt32(9090)},
				{Name: "api", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "metrics",
							Image: "metrics:latest",
							Ports: []corev1.ContainerPort{{ContainerPort: 9090}},
							ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{Path: "/metrics", Port: intstr.FromInt32(9090)},
							}},
						},
						{
							Name:  "api",
							Image: "api:latest",
							Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
							// No readiness probe on the health-owning container.
						},
					},
				},
			},
		},
	}
	// Ready endpoints on the METRICS port, so the buggy [0]-based path would resolve to SATISFIED.
	ready := true
	metricsPort := int32(9090)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
		},
		Ports:     []discoveryv1.EndpointPort{{Port: &metricsPort}},
		Endpoints: []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: &ready}}},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep, slice).Build(),
		prober: prober.New(5 * time.Second),
	}
	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api", // owning interface is "api" (8080), NOT InterfaceBindings[0] ("metrics", 9090)
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "metrics", ServicePort: intstr.FromInt32(9090)},
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Tier-B checks the api container (no readiness probe) -> no usable evidence -> Insufficient.
	// A regression to InterfaceBindings[0] (metrics, ready) would wrongly report Observed.
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("Outcome = %s, want Insufficient (Tier-B must check the owning 'api' binding, not metrics[0])", obs.Outcome)
	}
}

func TestObserveHealthDim_WindowUpdates(t *testing.T) {
	// Verify that window updates are correctly emitted and reset.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}

	now := time.Now()
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 now,
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	// The probe will fail (cannot reach in-cluster DNS), so this will be a Failed outcome.
	_, updates := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Failed should NOT emit window updates (only 404 does).
	if len(updates) != 0 {
		t.Errorf("expected no window updates for Failed, got %d", len(updates))
	}
}

func TestObserveHealthDim_Job_CronJob(t *testing.T) {
	// Test Job and CronJob workload kinds.
	kinds := []struct {
		name string
		obj  client.Object
	}{
		{
			name: "Job",
			obj: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "test-job", Namespace: "default"},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "app:latest",
									Ports: []corev1.ContainerPort{
										{ContainerPort: 8080},
									},
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											HTTPGet: &corev1.HTTPGetAction{
												Path: "/health",
												Port: intstr.FromInt32(8080),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "CronJob",
			obj: &batchv1.CronJob{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cron", Namespace: "default"},
				Spec: batchv1.CronJobSpec{
					JobTemplate: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{
										{
											Name:  "app",
											Image: "app:latest",
											Ports: []corev1.ContainerPort{
												{ContainerPort: 8080},
											},
											ReadinessProbe: &corev1.Probe{
												ProbeHandler: corev1.ProbeHandler{
													HTTPGet: &corev1.HTTPGetAction{
														Path: "/health",
														Port: intstr.FromInt32(8080),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	ready := true
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			o := &Observer{
				client: fake.NewClientBuilder().WithObjects(svc, tc.obj, slice).Build(),
				prober: prober.New(5 * time.Second),
			}

			cap := contract.Capability{
				Type: contract.CapabilityHealth,
				Binding: &contract.CapabilityBinding{
					Type:      contract.CapabilityBindingHTTP,
					Interface: "api",
					Path:      "/health",
				},
			}

			workloadName := tc.obj.GetName()
			input := CollectInput{
				Namespace:    "default",
				ServiceName:  "test-svc",
				WorkloadName: workloadName,
				WorkloadKind: tc.name,
				Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
				InterfaceBindings: []InterfaceBinding{
					{Interface: "api", ServicePort: intstr.FromInt32(8080)},
				},
				StabilizationWindow: 2 * time.Minute,
				ObservationWindows:  make(map[string]*metav1.Time),
				Now:                 time.Now(),
			}
			prov := evidence.Provenance{Collector: "k8s-observer"}

			obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

			if obs.Outcome != evidence.Observed {
				t.Errorf("%s: expected Observed, got %s", tc.name, obs.Outcome)
			}
		})
	}
}

func TestObserveHealthDim_EmptyInterfaceBindings_NoPort(t *testing.T) {
	// Test case where InterfaceBindings is empty, so we can't resolve the port.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:           "default",
		ServiceName:         "test-svc",
		Contract:            &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings:   []InterfaceBinding{}, // empty
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// No interface binding -> Unsupported.
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported for empty bindings, got %s", obs.Outcome)
	}
}

func TestObserveHealthDim_NoReadinessProbe_Insufficient(t *testing.T) {
	// Test case where there's no readiness probe at all.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "app:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080},
							},
							// No probe at all.
						},
					},
				},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// No readiness probe -> no usable Tier-B evidence -> EVIDENCE_INSUFFICIENT (spec section 7.4).
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for no readiness probe, got %s", obs.Outcome)
	}
}

func TestHandleHealthProbeResult_2xx_Satisfied(t *testing.T) {
	result := prober.Result{
		Reachable:  true,
		StatusCode: 200,
	}
	subj := evidence.SubjectRef{Kind: "capability", Name: "health"}
	input := CollectInput{
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := handleHealthProbeResult(result, subj, "health", input, prov, input.Now, nil, context.Background(), 8080)

	if obs.Outcome != evidence.Observed {
		t.Errorf("expected Observed for 2xx, got %s", obs.Outcome)
	}

	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if !payload.Present {
		t.Error("expected Present=true for 2xx")
	}

	if len(updates) != 0 {
		t.Errorf("expected no window updates for 2xx, got %d", len(updates))
	}
}

func TestHandleHealthProbeResult_3xx_Satisfied(t *testing.T) {
	result := prober.Result{
		Reachable:  true,
		StatusCode: 301,
	}
	subj := evidence.SubjectRef{Kind: "capability", Name: "health"}
	input := CollectInput{
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := handleHealthProbeResult(result, subj, "health", input, prov, input.Now, nil, context.Background(), 8080)

	if obs.Outcome != evidence.Observed {
		t.Errorf("expected Observed for 3xx, got %s", obs.Outcome)
	}

	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if !payload.Present {
		t.Error("expected Present=true for 3xx")
	}
}

func TestHandleHealthProbeResult_404_WithinWindow(t *testing.T) {
	result := prober.Result{
		Reachable:  true,
		StatusCode: 404,
	}
	subj := evidence.SubjectRef{Kind: "capability", Name: "health"}
	now := time.Now()
	input := CollectInput{
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 now,
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := handleHealthProbeResult(result, subj, "health", input, prov, now, nil, context.Background(), 8080)

	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for first 404, got %s", obs.Outcome)
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 window update, got %d", len(updates))
	}

	if updates[0].FirstObservedNegativeAt == nil {
		t.Error("expected window to be set")
	}
}

func TestHandleHealthProbeResult_404_BeyondWindow(t *testing.T) {
	result := prober.Result{
		Reachable:  true,
		StatusCode: 404,
	}
	subj := evidence.SubjectRef{Kind: "capability", Name: "health"}
	now := time.Now()
	firstObserved := metav1.NewTime(now.Add(-3 * time.Minute))

	input := CollectInput{
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows: map[string]*metav1.Time{
			"capability/health": &firstObserved,
		},
		Now: now,
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := handleHealthProbeResult(result, subj, "health", input, prov, now, nil, context.Background(), 8080)

	if obs.Outcome != evidence.Observed {
		t.Errorf("expected Observed for 404 beyond window, got %s", obs.Outcome)
	}

	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if payload.Present {
		t.Error("expected Present=false for 404 beyond window")
	}

	if len(updates) != 1 {
		t.Fatalf("expected 1 window update, got %d", len(updates))
	}
}

func TestHandleHealthProbeResult_5xx_Insufficient(t *testing.T) {
	result := prober.Result{
		Reachable:  true,
		StatusCode: 500,
	}
	subj := evidence.SubjectRef{Kind: "capability", Name: "health"}
	input := CollectInput{
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := handleHealthProbeResult(result, subj, "health", input, prov, input.Now, nil, context.Background(), 8080)

	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for 5xx, got %s", obs.Outcome)
	}

	if len(updates) != 0 {
		t.Errorf("expected no window updates for 5xx, got %d", len(updates))
	}
}

func TestHandleHealthProbeResult_501_Insufficient(t *testing.T) {
	result := prober.Result{
		Reachable:  true,
		StatusCode: 501,
	}
	subj := evidence.SubjectRef{Kind: "capability", Name: "health"}
	input := CollectInput{
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := handleHealthProbeResult(result, subj, "health", input, prov, input.Now, nil, context.Background(), 8080)

	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for 501, got %s", obs.Outcome)
	}
}

func TestHandleHealthProbeResult_Unreachable_TierB(t *testing.T) {
	// When probe is unreachable, tier B fallback is tried.
	result := prober.Result{
		Reachable: false,
		Error:     "connection refused",
	}
	subj := evidence.SubjectRef{Kind: "capability", Name: "health"}

	// Create a test setup with readiness probe.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "app:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(8080),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	ready := true
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	obs := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep, slice).Build(),
		prober: prober.New(5 * time.Second),
	}

	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	observation, _ := handleHealthProbeResult(result, subj, "health", input, prov, input.Now, obs, context.Background(), 8080)

	// Tier B should succeed.
	if observation.Outcome != evidence.Observed {
		t.Errorf("expected Observed for tier B success, got %s", observation.Outcome)
	}

	payload, err := observation.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if !payload.Present {
		t.Error("expected Present=true for tier B success")
	}
}

func TestObserveHealthDim_ContainerNotExposingPort_Insufficient(t *testing.T) {
	// Test case where the container doesn't expose the target port.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(9999)},
			},
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "app:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: 8080},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromInt32(8080),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep).Build(),
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
		Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Container not exposing the target port -> unresolvable Tier-B target -> EVIDENCE_INSUFFICIENT.
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for container not exposing port, got %s", obs.Outcome)
	}
}

// Full-coverage enforcement: cover all branches in observeHealthDim and checkReadinessProbeFallback.
func TestObserveHealthDim_Coverage_AllWorkloadKinds(t *testing.T) {
	// Test all workload kinds (StatefulSet, ReplicaSet, Job, CronJob) for tier B.
	kinds := []struct {
		name string
		obj  client.Object
	}{
		{
			name: "StatefulSet",
			obj: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "test-sts", Namespace: "default"},
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "app:latest",
									Ports: []corev1.ContainerPort{
										{ContainerPort: 8080},
									},
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											HTTPGet: &corev1.HTTPGetAction{
												Path: "/health",
												Port: intstr.FromInt32(8080),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "ReplicaSet",
			obj: &appsv1.ReplicaSet{
				ObjectMeta: metav1.ObjectMeta{Name: "test-rs", Namespace: "default"},
				Spec: appsv1.ReplicaSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "app:latest",
									Ports: []corev1.ContainerPort{
										{ContainerPort: 8080},
									},
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											HTTPGet: &corev1.HTTPGetAction{
												Path: "/health",
												Port: intstr.FromInt32(8080),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	ready := true
	port := int32(8080)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc-abc",
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: "test-svc",
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{Port: &port},
		},
		Endpoints: []discoveryv1.Endpoint{
			{
				Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			},
		},
	}

	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			o := &Observer{
				client: fake.NewClientBuilder().WithObjects(svc, tc.obj, slice).Build(),
				prober: prober.New(5 * time.Second),
			}

			cap := contract.Capability{
				Type: contract.CapabilityHealth,
				Binding: &contract.CapabilityBinding{
					Type:      contract.CapabilityBindingHTTP,
					Interface: "api",
					Path:      "/health",
				},
			}

			workloadName := tc.obj.GetName()
			input := CollectInput{
				Namespace:    "default",
				ServiceName:  "test-svc",
				WorkloadName: workloadName,
				WorkloadKind: tc.name,
				Contract:     &contract.Contract{Service: contract.Service{Name: "test"}},
				InterfaceBindings: []InterfaceBinding{
					{Interface: "api", ServicePort: intstr.FromInt32(8080)},
				},
				StabilizationWindow: 2 * time.Minute,
				ObservationWindows:  make(map[string]*metav1.Time),
				Now:                 time.Now(),
			}
			prov := evidence.Provenance{Collector: "k8s-observer"}

			obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

			if obs.Outcome != evidence.Observed {
				t.Errorf("%s: expected Observed, got %s", tc.name, obs.Outcome)
			}
		})
	}
}

func TestObserveHealthDim_ServiceGetError_Failed(t *testing.T) {
	// Test non-NotFound Service GET error -> COLLECTION_FAILED.
	clientWithError := fake.NewClientBuilder().
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Service); ok {
					return errors.NewInternalError(fmt.Errorf("forced Service GET error"))
				}
				return client.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	o := &Observer{
		client: clientWithError,
		prober: prober.New(5 * time.Second),
	}

	cap := contract.Capability{
		Type: contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/health",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract:    &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	if obs.Outcome != evidence.Failed {
		t.Errorf("expected Failed for Service GET error, got %s", obs.Outcome)
	}
}

func TestCheckReadinessProbeFallbackFromInput_ServiceGetError(t *testing.T) {
	// Test Service GET error in checkReadinessProbeFallbackFromInput.
	clientWithError := fake.NewClientBuilder().
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Service); ok {
					return errors.NewInternalError(fmt.Errorf("forced Service GET error"))
				}
				return client.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	o := &Observer{client: clientWithError}

	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
	}

	hasProbe, podReady := o.checkReadinessProbeFallbackFromInput(context.Background(), input, 8080)

	if hasProbe || podReady {
		t.Errorf("expected (false, false) for Service GET error, got (%v, %v)", hasProbe, podReady)
	}
}

func TestCheckReadinessProbeFallback_NoPodSpec(t *testing.T) {
	// Test podSpec == nil (workload exists but second Get fails).
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
			},
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "app:latest"},
					},
				},
			},
		},
	}

	// Use an interceptor that succeeds on the first Get but fails on the second.
	getCount := 0
	clientWithError := fake.NewClientBuilder().
		WithObjects(svc, dep).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.Deployment); ok {
					getCount++
					if getCount > 1 {
						// Second Get fails.
						return errors.NewInternalError(fmt.Errorf("forced second Get error"))
					}
				}
				return client.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	o := &Observer{client: clientWithError}

	input := CollectInput{
		Namespace:    "default",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
	}

	hasProbe, podReady := o.checkReadinessProbeFallback(context.Background(), input, svc, 8080)

	if hasProbe || podReady {
		t.Errorf("expected (false, false) for nil podSpec, got (%v, %v)", hasProbe, podReady)
	}
}

func TestCheckReadinessProbeFallback_NoTargetPortMatch(t *testing.T) {
	// Test !foundTargetPort (Service target port maps to no container port).
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				// Target port is a named port that doesn't exist in any container.
				{Name: "http", Port: 8080, TargetPort: intstr.FromString("nonexistent")},
			},
		},
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "app:latest",
							Ports: []corev1.ContainerPort{
								{Name: "http", ContainerPort: 9090},
							},
						},
					},
				},
			},
		},
	}

	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep).Build(),
	}

	input := CollectInput{
		Namespace:    "default",
		ServiceName:  "test-svc",
		WorkloadName: "test-deploy",
		WorkloadKind: "Deployment",
	}

	hasProbe, podReady := o.checkReadinessProbeFallback(context.Background(), input, svc, 8080)

	if hasProbe || podReady {
		t.Errorf("expected (false, false) for no target port match, got (%v, %v)", hasProbe, podReady)
	}
}

// M4a: --enable-probing gates the active Tier-A HTTP probe (SSRF surface; opt-in).

func TestObserveHealthDim_ProbingEnabled_TierAActive(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc).Build(),
		prober: &fakeMetricsProber{result: prober.Result{Reachable: true, StatusCode: 200}},
	}
	cap := contract.Capability{
		Type:    contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{Type: contract.CapabilityBindingHTTP, Interface: "api", Path: "/health"},
	}
	input := CollectInput{
		Namespace:           "default",
		ServiceName:         "test-svc",
		WorkloadName:        "test-deploy",
		WorkloadKind:        "Deployment",
		Contract:            &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
		EnableProbing:       true,
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Active Tier-A probe ran and returned 2xx -> satisfied.
	if obs.Outcome != evidence.Observed {
		t.Fatalf("expected Observed from active Tier-A probe, got %s", obs.Outcome)
	}
	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("expected payload, got error: %v", err)
	}
	if !payload.Present {
		t.Error("expected Present=true for Tier-A 2xx")
	}
}

func TestObserveHealthDim_ProbingDisabled_SkipsActiveProbe(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)}},
		},
	}
	// Deployment with no readiness probe -> Tier-B cannot establish availability.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "test-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "app:latest", Ports: []corev1.ContainerPort{{ContainerPort: 8080}}},
					},
				},
			},
		},
	}
	o := &Observer{
		client: fake.NewClientBuilder().WithObjects(svc, dep).Build(),
		// A 200-returning prober: if the gate were bypassed this would report Observed.
		prober: &fakeMetricsProber{result: prober.Result{Reachable: true, StatusCode: 200}},
	}
	cap := contract.Capability{
		Type:    contract.CapabilityHealth,
		Binding: &contract.CapabilityBinding{Type: contract.CapabilityBindingHTTP, Interface: "api", Path: "/health"},
	}
	input := CollectInput{
		Namespace:           "default",
		ServiceName:         "test-svc",
		WorkloadName:        "test-deploy",
		WorkloadKind:        "Deployment",
		Contract:            &contract.Contract{Service: contract.Service{Name: "test"}},
		InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time),
		Now:                 time.Now(),
		EnableProbing:       false,
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeHealthDim(context.Background(), input, cap, prov, input.Now)

	// Active probe skipped; Tier-B has no readiness probe -> honest Unknown (EVIDENCE_INSUFFICIENT),
	// NOT the fake prober's 200. Observed here would mean the gate leaked the active probe.
	if obs.Outcome != evidence.Insufficient {
		t.Fatalf("expected Insufficient (active probe skipped, Tier-B absent), got %s", obs.Outcome)
	}
}
