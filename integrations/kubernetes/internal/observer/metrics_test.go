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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/trianalab/pacto/integrations/kubernetes/internal/prober"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

func TestObserveMetricsDim_NoCapability(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	input := CollectInput{
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs != nil {
		t.Errorf("expected nil observation when no metrics capability, got %+v", obs)
	}
	if len(updates) != 0 {
		t.Errorf("expected no window updates, got %d", len(updates))
	}
}

func TestObserveMetricsDim_Disabled(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: false, // DISABLED
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when disabled, got %s", obs.Outcome)
	}
	if len(updates) != 0 {
		t.Errorf("expected no window updates, got %d", len(updates))
	}
}

func TestObserveMetricsDim_NoBinding(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{Type: contract.CapabilityMetrics}
	input := CollectInput{
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported, got %s", obs.Outcome)
	}
	if obs.Subject.Kind != "capability" || obs.Subject.Name != "metrics" {
		t.Errorf("expected Subject{capability,metrics}, got %+v", obs.Subject)
	}
	if len(updates) != 0 {
		t.Errorf("expected no window updates, got %d", len(updates))
	}
}

func TestObserveMetricsDim_NonHTTPBinding(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      "grpc",
			Interface: "api",
		},
	}
	input := CollectInput{
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported for non-http binding, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_NoInterfaceBinding(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings:        []InterfaceBinding{},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when owning interface has no binding, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_ServiceNotFound(t *testing.T) {
	o := &Observer{client: fake.NewClientBuilder().Build()}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when Service not found, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_ServicePortNotFound(t *testing.T) {
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
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported when bound port not in Service, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_ServiceGetError(t *testing.T) {
	errClient := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			return fmt.Errorf("api error")
		},
	}).Build()

	o := &Observer{client: errClient}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Failed {
		t.Errorf("expected Failed for Service GET error, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_ProbeReachable200Parsed(t *testing.T) {
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
		prober: &fakeMetricsProber{
			result: prober.Result{
				Reachable:        true,
				StatusCode:       200,
				PrometheusParsed: true,
			},
		},
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, updates := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Observed {
		t.Errorf("expected Observed, got %s", obs.Outcome)
	}
	payload, err := obs.GetCapabilityObservation()
	if err != nil {
		t.Fatalf("failed to get payload: %v", err)
	}
	if !payload.Present {
		t.Errorf("expected Present=true, got false")
	}
	if len(updates) != 0 {
		t.Errorf("expected no window updates for satisfied, got %d", len(updates))
	}
}

func TestObserveMetricsDim_Probe200NotParsed(t *testing.T) {
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
		prober: &fakeMetricsProber{
			result: prober.Result{
				Reachable:        true,
				StatusCode:       200,
				PrometheusParsed: false,
			},
		},
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for non-parseable body, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_Probe404(t *testing.T) {
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
		prober: &fakeMetricsProber{
			result: prober.Result{
				Reachable:  true,
				StatusCode: 404,
			},
		},
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for 404 (no reliable negative), got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_Probe410(t *testing.T) {
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
		prober: &fakeMetricsProber{
			result: prober.Result{
				Reachable:  true,
				StatusCode: 410,
			},
		},
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for 410, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_Probe5xx(t *testing.T) {
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
		prober: &fakeMetricsProber{
			result: prober.Result{
				Reachable:  true,
				StatusCode: 500,
			},
		},
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for 5xx, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_Probe401(t *testing.T) {
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
		prober: &fakeMetricsProber{
			result: prober.Result{
				Reachable:  true,
				StatusCode: 401,
			},
		},
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient for 401, got %s", obs.Outcome)
	}
}

func TestObserveMetricsDim_ProbeTransportError(t *testing.T) {
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
		prober: &fakeMetricsProber{
			result: prober.Result{
				Reachable: false,
				Error:     "connection refused",
			},
		},
	}
	cap := contract.Capability{
		Type: contract.CapabilityMetrics,
		Binding: &contract.CapabilityBinding{
			Type:      contract.CapabilityBindingHTTP,
			Interface: "api",
			Path:      "/metrics",
		},
	}
	input := CollectInput{
		Namespace:   "default",
		ServiceName: "test-svc",
		Contract: &contract.Contract{
			Service:      contract.Service{Name: "test"},
			Capabilities: []contract.Capability{cap},
		},
		InterfaceBindings: []InterfaceBinding{
			{Interface: "api", ServicePort: intstr.FromInt32(8080)},
		},
		StabilizationWindow:      2 * time.Minute,
		EnableMetricsObservation: true,
		ObservationWindows:       make(map[string]*metav1.Time),
		Now:                      time.Now(),
	}
	prov := evidence.Provenance{Collector: "k8s-observer"}

	obs, _ := o.observeMetricsDim(context.Background(), input, prov, input.Now)

	if obs == nil {
		t.Fatal("expected observation, got nil")
	}
	if obs.Outcome != evidence.Failed {
		t.Errorf("expected Failed for transport error, got %s", obs.Outcome)
	}
}
