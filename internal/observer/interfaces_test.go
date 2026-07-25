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
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/evidence"
)

// Test the stabilize helper with all branches.
func TestStabilize(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	window := 2 * time.Minute

	t.Run("non-negative resets window", func(t *testing.T) {
		existing := metav1.NewTime(now.Add(-3 * time.Minute))
		outcome, updated := stabilize(&existing, false, now, window)
		if outcome != evidence.Observed {
			t.Errorf("outcome = %v, want Observed", outcome)
		}
		if updated != nil {
			t.Errorf("updated = %v, want nil (reset)", updated)
		}
	})

	t.Run("first negative starts window", func(t *testing.T) {
		outcome, updated := stabilize(nil, true, now, window)
		if outcome != evidence.Insufficient {
			t.Errorf("outcome = %v, want Insufficient", outcome)
		}
		if updated == nil {
			t.Fatal("updated = nil, want a timestamp")
		}
		if !updated.Time.Equal(now) {
			t.Errorf("updated.Time = %v, want %v", updated.Time, now)
		}
	})

	t.Run("negative within window stays Insufficient", func(t *testing.T) {
		existing := metav1.NewTime(now.Add(-1 * time.Minute))
		outcome, updated := stabilize(&existing, true, now, window)
		if outcome != evidence.Insufficient {
			t.Errorf("outcome = %v, want Insufficient", outcome)
		}
		if updated == nil || !updated.Time.Equal(existing.Time) {
			t.Errorf("updated = %v, want %v (unchanged)", updated, existing.Time)
		}
	})

	t.Run("negative beyond window emits Observed", func(t *testing.T) {
		existing := metav1.NewTime(now.Add(-3 * time.Minute))
		outcome, updated := stabilize(&existing, true, now, window)
		if outcome != evidence.Observed {
			t.Errorf("outcome = %v, want Observed (beyond window)", outcome)
		}
		if updated == nil || !updated.Time.Equal(existing.Time) {
			t.Errorf("updated = %v, want %v (preserved)", updated, existing.Time)
		}
	})

	t.Run("negative exactly at window boundary is beyond", func(t *testing.T) {
		existing := metav1.NewTime(now.Add(-window))
		outcome, _ := stabilize(&existing, true, now, window)
		if outcome != evidence.Observed {
			t.Errorf("outcome = %v, want Observed (at boundary)", outcome)
		}
	})
}

// Test interfaces producer with various states.
func TestObserveInterfacesDim(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	window := 2 * time.Minute

	setup := func(objects ...client.Object) *Observer {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = discoveryv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
		return New(fakeClient)
	}

	t.Run("no binding - Unsupported", func(t *testing.T) {
		obs := setup()
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if len(observations) != 1 {
			t.Fatalf("len(observations) = %d, want 1", len(observations))
		}
		if observations[0].Outcome != evidence.Unsupported {
			t.Errorf("Outcome = %v, want Unsupported", observations[0].Outcome)
		}
		if observations[0].Subject.Name != "api" {
			t.Errorf("Subject.Name = %v, want api", observations[0].Subject.Name)
		}
		if len(windowUpdates) != 0 {
			t.Errorf("len(windowUpdates) = %d, want 0 (no binding)", len(windowUpdates))
		}
	})

	t.Run("Service NotFound - Unsupported", func(t *testing.T) {
		obs := setup()
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if len(observations) != 1 {
			t.Fatalf("len(observations) = %d, want 1", len(observations))
		}
		if observations[0].Outcome != evidence.Unsupported {
			t.Errorf("Outcome = %v, want Unsupported (Service NotFound)", observations[0].Outcome)
		}
		if len(windowUpdates) != 0 {
			t.Errorf("len(windowUpdates) = %d, want 0 (unmappable)", len(windowUpdates))
		}
	})

	t.Run("Service ExternalName - Unsupported", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Type:         corev1.ServiceTypeExternalName,
				ExternalName: "external.example.com",
			},
		}
		obs := setup(svc)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, _ := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if len(observations) != 1 {
			t.Fatalf("len(observations) = %d, want 1", len(observations))
		}
		if observations[0].Outcome != evidence.Unsupported {
			t.Errorf("Outcome = %v, want Unsupported (ExternalName)", observations[0].Outcome)
		}
	})

	t.Run("port not found - Unsupported", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		obs := setup(svc)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(9090)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, _ := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if observations[0].Outcome != evidence.Unsupported {
			t.Errorf("Outcome = %v, want Unsupported (port not found)", observations[0].Outcome)
		}
	})

	t.Run("ready endpoints - Observed Present=true", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		ready := true
		port := int32(8080)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		obs := setup(svc, slice)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromString("http")}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if len(observations) != 1 {
			t.Fatalf("len(observations) = %d, want 1", len(observations))
		}
		if observations[0].Outcome != evidence.Observed {
			t.Errorf("Outcome = %v, want Observed", observations[0].Outcome)
		}
		iObs, err := observations[0].GetInterfaceObservation()
		if err != nil {
			t.Fatalf("GetInterfaceObservation() error = %v", err)
		}
		if !iObs.Present {
			t.Errorf("Present = false, want true")
		}
		if iObs.Type != "openapi" {
			t.Errorf("Type = %v, want openapi", iObs.Type)
		}
		if len(windowUpdates) != 1 {
			t.Fatalf("len(windowUpdates) = %d, want 1", len(windowUpdates))
		}
		if windowUpdates[0].FirstObservedNegativeAt != nil {
			t.Errorf("FirstObservedNegativeAt = %v, want nil (reset)", windowUpdates[0].FirstObservedNegativeAt)
		}
	})

	t.Run("zero ready first time - Insufficient", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		ready := false
		port := int32(8080)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		obs := setup(svc, slice)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if observations[0].Outcome != evidence.Insufficient {
			t.Errorf("Outcome = %v, want Insufficient (first negative)", observations[0].Outcome)
		}
		if len(windowUpdates) != 1 {
			t.Fatalf("len(windowUpdates) = %d, want 1", len(windowUpdates))
		}
		if windowUpdates[0].FirstObservedNegativeAt == nil {
			t.Fatal("FirstObservedNegativeAt = nil, want timestamp")
		}
		if !windowUpdates[0].FirstObservedNegativeAt.Time.Equal(now) {
			t.Errorf("FirstObservedNegativeAt = %v, want %v", windowUpdates[0].FirstObservedNegativeAt.Time, now)
		}
	})

	t.Run("zero ready within window - Insufficient", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		ready := false
		port := int32(8080)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		obs := setup(svc, slice)
		firstNegative := metav1.NewTime(now.Add(-1 * time.Minute))
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{"interface/api": &firstNegative},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if observations[0].Outcome != evidence.Insufficient {
			t.Errorf("Outcome = %v, want Insufficient (within window)", observations[0].Outcome)
		}
		if windowUpdates[0].FirstObservedNegativeAt == nil || !windowUpdates[0].FirstObservedNegativeAt.Time.Equal(firstNegative.Time) {
			t.Errorf("FirstObservedNegativeAt = %v, want %v (preserved)", windowUpdates[0].FirstObservedNegativeAt, firstNegative.Time)
		}
	})

	t.Run("zero ready beyond window - Observed Present=false", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		ready := false
		port := int32(8080)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		obs := setup(svc, slice)
		firstNegative := metav1.NewTime(now.Add(-3 * time.Minute))
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{"interface/api": &firstNegative},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if observations[0].Outcome != evidence.Observed {
			t.Errorf("Outcome = %v, want Observed (beyond window)", observations[0].Outcome)
		}
		iObs, err := observations[0].GetInterfaceObservation()
		if err != nil {
			t.Fatalf("GetInterfaceObservation() error = %v", err)
		}
		if iObs.Present {
			t.Errorf("Present = true, want false (INTERFACE_ABSENT beyond window)")
		}
		if windowUpdates[0].FirstObservedNegativeAt == nil || !windowUpdates[0].FirstObservedNegativeAt.Time.Equal(firstNegative.Time) {
			t.Errorf("FirstObservedNegativeAt = %v, want %v (preserved)", windowUpdates[0].FirstObservedNegativeAt, firstNegative.Time)
		}
	})

	t.Run("multiple interfaces", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports: []corev1.ServicePort{
					{Name: "http", Port: 8080},
					{Name: "grpc", Port: 9090},
				},
			},
		}
		ready := true
		port1 := int32(8080)
		port2 := int32(9090)
		slice1 := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port1}, {Port: &port2}},
		}
		obs := setup(svc, slice1)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{
					{Name: "api", Type: "openapi"},
					{Name: "rpc", Type: "grpc"},
				},
			},
			InterfaceBindings: []InterfaceBinding{
				{Interface: "api", ServicePort: intstr.FromString("http")},
				{Interface: "rpc", ServicePort: intstr.FromString("grpc")},
			},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if len(observations) != 2 {
			t.Fatalf("len(observations) = %d, want 2", len(observations))
		}
		for i, obs := range observations {
			if obs.Outcome != evidence.Observed {
				t.Errorf("observations[%d].Outcome = %v, want Observed", i, obs.Outcome)
			}
			iObs, err := obs.GetInterfaceObservation()
			if err != nil {
				t.Fatalf("observations[%d].GetInterfaceObservation() error = %v", i, err)
			}
			if !iObs.Present {
				t.Errorf("observations[%d].Present = false, want true", i)
			}
		}
		if len(windowUpdates) != 2 {
			t.Fatalf("len(windowUpdates) = %d, want 2", len(windowUpdates))
		}
	})

	t.Run("empty EndpointSlice list - zero ready", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		obs := setup(svc)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if observations[0].Outcome != evidence.Insufficient {
			t.Errorf("Outcome = %v, want Insufficient (empty slice list)", observations[0].Outcome)
		}
		if windowUpdates[0].FirstObservedNegativeAt == nil {
			t.Error("FirstObservedNegativeAt = nil, want timestamp (first negative)")
		}
	})

	t.Run("EndpointSlice list error - Failed", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		// Do NOT add discoveryv1 to scheme -> List will fail.
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
		obs := New(fakeClient)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: window,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 now,
		}
		prov := evidence.Provenance{Collector: "k8s-observer", DetectedAt: now}

		observations, windowUpdates := obs.observeInterfacesDim(context.Background(), input, prov, now)

		if observations[0].Outcome != evidence.Failed {
			t.Errorf("Outcome = %v, want Failed (API error)", observations[0].Outcome)
		}
		if len(windowUpdates) != 0 {
			t.Errorf("len(windowUpdates) = %d, want 0 (API error)", len(windowUpdates))
		}
	})
}

func TestCollect_Now(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = discoveryv1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	obs := New(fakeClient)

	t.Run("uses Now from input", func(t *testing.T) {
		fixedNow := time.Date(2026, 7, 24, 15, 30, 0, 0, time.UTC)
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract:    &contract.Contract{},
			Now:         fixedNow,
		}
		es, _, err := obs.Collect(context.Background(), input)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		if !es.ObservedAt.Equal(fixedNow) {
			t.Errorf("ObservedAt = %v, want %v", es.ObservedAt, fixedNow)
		}
	})

	t.Run("defaults to time.Now when zero", func(t *testing.T) {
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract:    &contract.Contract{},
			Now:         time.Time{},
		}
		before := time.Now()
		es, _, err := obs.Collect(context.Background(), input)
		after := time.Now()
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		if es.ObservedAt.Before(before) || es.ObservedAt.After(after) {
			t.Errorf("ObservedAt = %v, want between %v and %v", es.ObservedAt, before, after)
		}
	})

	t.Run("subject uses workload name when service name is empty", func(t *testing.T) {
		input := CollectInput{
			Namespace:    "test-ns",
			ServiceName:  "",
			WorkloadName: "test-workload",
			Contract:     &contract.Contract{},
			Now:          time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		}
		es, _, err := obs.Collect(context.Background(), input)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		want := "test-ns/test-workload"
		if es.Subject.Name != want {
			t.Errorf("Subject.Name = %q, want %q", es.Subject.Name, want)
		}
	})

	t.Run("contract with no interfaces emits no interface observations", func(t *testing.T) {
		input := CollectInput{
			Namespace:   "test-ns",
			ServiceName: "test-svc",
			Contract: &contract.Contract{
				Interfaces: []contract.Interface{},
			},
			Now: time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		}
		es, windowUpdates, err := obs.Collect(context.Background(), input)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		if len(es.Observations) != 0 {
			t.Errorf("len(Observations) = %d, want 0 (no dimensions)", len(es.Observations))
		}
		if len(windowUpdates) != 0 {
			t.Errorf("len(windowUpdates) = %d, want 0 (no interfaces)", len(windowUpdates))
		}
	})

	t.Run("collect with interfaces and workload", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = appsv1.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)
		_ = discoveryv1.AddToScheme(scheme)
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-workload", Namespace: "test-ns"},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "main", Image: "test:latest"}},
					},
				},
			},
		}
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		ready := true
		port := int32(8080)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep, svc, slice).Build()
		obs := New(fakeClient)

		input := CollectInput{
			Namespace:        "test-ns",
			ServiceName:      "test-svc",
			WorkloadName:     "test-workload",
			WorkloadKind:     "Deployment",
			WorkloadExplicit: true,
			Contract: &contract.Contract{
				Service:    contract.Service{Name: "test-service"},
				Workload:   "service",
				Interfaces: []contract.Interface{{Name: "api", Type: "openapi"}},
			},
			InterfaceBindings:   []InterfaceBinding{{Interface: "api", ServicePort: intstr.FromInt32(8080)}},
			StabilizationWindow: 2 * time.Minute,
			ObservationWindows:  map[string]*metav1.Time{},
			Now:                 time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		}
		es, _, err := obs.Collect(context.Background(), input)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		// Should have both interface and workload observations.
		if len(es.Observations) != 2 {
			t.Errorf("len(Observations) = %d, want 2 (interface + workload)", len(es.Observations))
		}
	})

	t.Run("collect with persistence", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = appsv1.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "test-workload", Namespace: "test-ns"},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "main", Image: "test:latest"}},
						Volumes: []corev1.Volume{{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"},
							},
						}},
					},
				},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep).Build()
		obs := New(fakeClient)

		input := CollectInput{
			Namespace:    "test-ns",
			WorkloadName: "test-workload",
			WorkloadKind: "Deployment",
			Contract: &contract.Contract{
				Service: contract.Service{Name: "test-service"},
				State:   &contract.State{Persistence: contract.Persistence{Durability: "persistent"}},
			},
			Now: time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		}
		es, _, err := obs.Collect(context.Background(), input)
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		// Should have persistence observation.
		if len(es.Observations) != 1 {
			t.Errorf("len(Observations) = %d, want 1 (persistence)", len(es.Observations))
		}
		if es.Observations[0].Kind != evidence.PersistenceObserved {
			t.Errorf("Observation kind = %v, want PersistenceObserved", es.Observations[0].Kind)
		}
	})
}

func TestCountReadyEndpoints(t *testing.T) {
	setup := func(objects ...client.Object) *Observer {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = discoveryv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
		return New(fakeClient)
	}

	t.Run("Service NotFound", func(t *testing.T) {
		obs := setup()
		count, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromInt32(8080))
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		if count != -1 {
			t.Errorf("count = %d, want -1 (NotFound)", count)
		}
	})

	t.Run("Service without selector", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}},
		}
		obs := setup(svc)
		count, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromInt32(8080))
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		if count != -1 {
			t.Errorf("count = %d, want -1 (no selector)", count)
		}
	})

	t.Run("port match by name", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
			},
		}
		ready := true
		port := int32(8080)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		obs := setup(svc, slice)
		count, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromString("http"))
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		if count != 1 {
			t.Errorf("count = %d, want 1", count)
		}
	})

	t.Run("multiple ready endpoints", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Port: 8080}},
			},
		}
		ready := true
		notReady := false
		port := int32(8080)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
				{Conditions: discoveryv1.EndpointConditions{Ready: &notReady}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		obs := setup(svc, slice)
		count, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromInt32(8080))
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		if count != 2 {
			t.Errorf("count = %d, want 2 (two ready)", count)
		}
	})

	t.Run("multiple slices", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Port: 8080}},
			},
		}
		ready := true
		port := int32(8080)
		slice1 := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		slice2 := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-def",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &port}},
		}
		obs := setup(svc, slice1, slice2)
		count, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromInt32(8080))
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		if count != 3 {
			t.Errorf("count = %d, want 3 (across two slices)", count)
		}
	})

	t.Run("slice without matching port", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Port: 8080}},
			},
		}
		ready := true
		wrongPort := int32(9090)
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-svc-abc",
				Namespace: "test-ns",
				Labels:    map[string]string{discoveryv1.LabelServiceName: "test-svc"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Conditions: discoveryv1.EndpointConditions{Ready: &ready}},
			},
			Ports: []discoveryv1.EndpointPort{{Port: &wrongPort}},
		}
		obs := setup(svc, slice)
		count, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromInt32(8080))
		if err != nil {
			t.Errorf("error = %v, want nil", err)
		}
		if count != 0 {
			t.Errorf("count = %d, want 0 (no matching port)", count)
		}
	})

	t.Run("EndpointSlice list error", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		// Do NOT add discoveryv1 to scheme -> List will fail.
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "test-ns"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "test"},
				Ports:    []corev1.ServicePort{{Port: 8080}},
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svc).Build()
		obs := New(fakeClient)
		// Scheme doesn't include discoveryv1, so List will fail.
		_, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromInt32(8080))
		if err == nil {
			t.Error("error = nil, want error (list failed)")
		}
	})

	t.Run("Service GET error", func(t *testing.T) {
		scheme := runtime.NewScheme()
		// Do NOT add corev1 to scheme -> Get will fail.
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		obs := New(fakeClient)
		_, err := obs.countReadyEndpoints(context.Background(), "test-ns", "test-svc", intstr.FromInt32(8080))
		if err == nil {
			t.Error("error = nil, want error (Service Get failed)")
		}
	})
}

func TestMatchesServicePort(t *testing.T) {
	t.Run("match by port number", func(t *testing.T) {
		p := corev1.ServicePort{Name: "http", Port: 8080}
		if !matchesServicePort(p, intstr.FromInt32(8080)) {
			t.Error("matchesServicePort = false, want true (int match)")
		}
	})

	t.Run("match by port name", func(t *testing.T) {
		p := corev1.ServicePort{Name: "http", Port: 8080}
		if !matchesServicePort(p, intstr.FromString("http")) {
			t.Error("matchesServicePort = false, want true (string match)")
		}
	})

	t.Run("no match by number", func(t *testing.T) {
		p := corev1.ServicePort{Name: "http", Port: 8080}
		if matchesServicePort(p, intstr.FromInt32(9090)) {
			t.Error("matchesServicePort = true, want false (int mismatch)")
		}
	})

	t.Run("no match by name", func(t *testing.T) {
		p := corev1.ServicePort{Name: "http", Port: 8080}
		if matchesServicePort(p, intstr.FromString("grpc")) {
			t.Error("matchesServicePort = true, want false (string mismatch)")
		}
	})

	t.Run("invalid type returns false", func(t *testing.T) {
		p := corev1.ServicePort{Name: "http", Port: 8080}
		// Create an IntOrString with an invalid type (defensive coverage).
		var invalid intstr.IntOrString
		invalid.Type = 99 // Invalid type.
		if matchesServicePort(p, invalid) {
			t.Error("matchesServicePort = true, want false (invalid type)")
		}
	})
}
