/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
)

var ErrForbidden = errors.New("access forbidden")

// TestConfigurationsDim_NoBinding tests that a required configuration with no binding -> Unsupported.
func TestConfigurationsDim_NoBinding(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = unversioned.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: nil, // NO binding
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Subject.Kind != "configuration" || o.Subject.Name != "app" {
		t.Errorf("unexpected Subject: %+v", o.Subject)
	}
	if o.Outcome != evidence.Unsupported {
		t.Errorf("expected Unsupported, got %v", o.Outcome)
	}
}

// TestConfigurationsDim_SecretPresent tests Secret existence-only (metadata GET) -> Insufficient.
func TestConfigurationsDim_SecretPresent(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-secret"}},
	).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "Secret", Name: "app-secret"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (existence-only), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_SecretNotFound_WithinWindow tests Secret NotFound within stabilization window -> Insufficient.
func TestConfigurationsDim_SecretNotFound_WithinWindow(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build() // No Secret
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "Secret", Name: "app-secret"}
	input := CollectInput{
		Namespace:           "default",
		Contract:            &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:            fstest.MapFS{},
		ConfigBindings:      []unversioned.ConfigBinding{binding},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  make(map[string]*metav1.Time), // no existing window
		Now:                 time.Now(),
	}

	observations, updates := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (within window), got %v", o.Outcome)
	}
	if len(updates) != 1 || updates[0].FirstObservedNegativeAt == nil {
		t.Errorf("expected window start to be recorded")
	}
}

// TestConfigurationsDim_SecretNotFound_BeyondWindow tests Secret NotFound beyond window -> Observed{Present:false}.
func TestConfigurationsDim_SecretNotFound_BeyondWindow(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build() // No Secret
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "Secret", Name: "app-secret"}
	now := time.Now()
	windowStart := metav1.NewTime(now.Add(-5 * time.Minute))
	input := CollectInput{
		Namespace:           "default",
		Contract:            &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:            fstest.MapFS{},
		ConfigBindings:      []unversioned.ConfigBinding{binding},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  map[string]*metav1.Time{"configuration/app": &windowStart},
		Now:                 now,
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Observed {
		t.Errorf("expected Observed (beyond window), got %v", o.Outcome)
	}
	val, err := o.GetConfigurationObservation()
	if err != nil {
		t.Fatalf("GetConfigurationObservation error: %v", err)
	}
	if val.Present {
		t.Errorf("expected Present=false (confirmed absent), got %v", val.Present)
	}
}

// TestConfigurationsDim_ConfigMapNoKeyFormat tests ConfigMap without key+format -> Insufficient.
func TestConfigurationsDim_ConfigMapNoKeyFormat(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"}},
	).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config"} // no Key/Format
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (no key+format), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_ConfigMapConforms tests ConfigMap key+format conform to local schema -> Observed{Present:true}.
func TestConfigurationsDim_ConfigMapConforms(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080\nhost: localhost"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	schema := `{"type":"object","properties":{"port":{"type":"number"},"host":{"type":"string"}},"required":["port"]}`
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schema}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	bundleFS := fstest.MapFS{}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleFS,
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Observed {
		t.Errorf("expected Observed (conforms), got %v", o.Outcome)
	}
	val, err := o.GetConfigurationObservation()
	if err != nil {
		t.Fatalf("GetConfigurationObservation error: %v", err)
	}
	if !val.Present {
		t.Errorf("expected Present=true (conforms), got %v", val.Present)
	}
}

// TestConfigurationsDim_ConfigMapNotConform tests ConfigMap key+format provably non-conform -> Observed{Present:false}.
func TestConfigurationsDim_ConfigMapNotConform(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "host: localhost"}, // missing required "port"
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	schema := `{"type":"object","properties":{"port":{"type":"number"},"host":{"type":"string"}},"required":["port"]}`
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schema}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Observed {
		t.Errorf("expected Observed (non-conform), got %v", o.Outcome)
	}
	val, err := o.GetConfigurationObservation()
	if err != nil {
		t.Fatalf("GetConfigurationObservation error: %v", err)
	}
	if !val.Present {
		t.Errorf("expected Present=true (ConfigMap exists), got %v", val.Present)
	}
	if val.Conformant {
		t.Errorf("expected Conformant=false (schema validation failed), got %v", val.Conformant)
	}
}

// TestConfigurationsDim_ConfigMapKeyMissing tests ConfigMap with key missing -> Insufficient.
func TestConfigurationsDim_ConfigMapKeyMissing(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{}, // no keys
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	schema := `{"type":"object","properties":{"port":{"type":"number"}},"required":["port"]}`
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schema}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (key missing), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_ConfigMapParseFail tests ConfigMap parse failure -> Insufficient.
func TestConfigurationsDim_ConfigMapParseFail(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "invalid: yaml: content:"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	schema := `{"type":"object"}`
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schema}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (parse failure), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_ConfigMapNotFound_WithinWindow tests ConfigMap NotFound within window -> Insufficient.
func TestConfigurationsDim_ConfigMapNotFound_WithinWindow(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build() // No ConfigMap
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	now := time.Now()
	windowStart := metav1.NewTime(now.Add(-30 * time.Second)) // Within 2-minute window
	input := CollectInput{
		Namespace:           "default",
		Contract:            &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:            fstest.MapFS{},
		ConfigBindings:      []unversioned.ConfigBinding{binding},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  map[string]*metav1.Time{"configuration/app": &windowStart},
		Now:                 now,
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (within window), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_ConfigMapNotFound_BeyondWindow tests ConfigMap NotFound beyond window -> CONFIGURATION_ABSENT.
func TestConfigurationsDim_ConfigMapNotFound_BeyondWindow(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build() // No ConfigMap
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	now := time.Now()
	windowStart := metav1.NewTime(now.Add(-5 * time.Minute))
	input := CollectInput{
		Namespace:           "default",
		Contract:            &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:            fstest.MapFS{},
		ConfigBindings:      []unversioned.ConfigBinding{binding},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  map[string]*metav1.Time{"configuration/app": &windowStart},
		Now:                 now,
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Observed {
		t.Errorf("expected Observed (beyond window), got %v", o.Outcome)
	}
	val, err := o.GetConfigurationObservation()
	if err != nil {
		t.Fatalf("GetConfigurationObservation error: %v", err)
	}
	if val.Present {
		t.Errorf("expected Present=false (NotFound beyond window), got %v", val.Present)
	}
}

// TestConfigurationsDim_ConfigMapAPIError tests ConfigMap API error -> Failed.
func TestConfigurationsDim_ConfigMapAPIError(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return ErrForbidden
			},
		}).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Failed {
		t.Errorf("expected Failed (API error), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_SecretAPIError tests Secret API error (non-NotFound) -> Failed.
func TestConfigurationsDim_SecretAPIError(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return ErrForbidden
			},
		}).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "Secret", Name: "app-secret"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Failed {
		t.Errorf("expected Failed (Secret API error), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_SecretNotFound_SustainedWithinWindow tests Secret NotFound sustained within window.
func TestConfigurationsDim_SecretNotFound_SustainedWithinWindow(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build() // No Secret
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: "{}"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "Secret", Name: "app-secret"}
	now := time.Now()
	windowStart := metav1.NewTime(now.Add(-30 * time.Second)) // Within 2-minute window
	input := CollectInput{
		Namespace:           "default",
		Contract:            &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:            fstest.MapFS{},
		ConfigBindings:      []unversioned.ConfigBinding{binding},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  map[string]*metav1.Time{"configuration/app": &windowStart},
		Now:                 now,
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (sustained within window), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_OptionalConfigNotBound tests optional config (Required=false) with no binding -> no observation.
func TestConfigurationsDim_OptionalConfigNotBound(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "opt", Required: false, Schema: "{}"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: nil,
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	// Optional + no binding -> no observation emitted per spec (only required dimensions emit Unsupported).
	if len(observations) != 0 {
		t.Errorf("expected 0 observations for optional+no-binding, got %d", len(observations))
	}
}

// TestConfigurationsDim_JSONFormat tests JSON format parsing.
func TestConfigurationsDim_JSONFormat(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.json": `{"port":8080,"host":"localhost"}`},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	schema := `{"type":"object","properties":{"port":{"type":"number"},"host":{"type":"string"}},"required":["port"]}`
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schema}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.json", Format: "json"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Observed {
		t.Errorf("expected Observed (JSON conforms), got %v", o.Outcome)
	}
	val, err := o.GetConfigurationObservation()
	if err != nil {
		t.Fatalf("GetConfigurationObservation error: %v", err)
	}
	if !val.Present {
		t.Errorf("expected Present=true (JSON conforms), got %v", val.Present)
	}
}

// TestConfigurationsDim_RemoteRefSchema tests remote ref schema (not resolvable locally) -> Insufficient.
func TestConfigurationsDim_RemoteRefSchema(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	// Remote ref schema -> collector cannot resolve -> Insufficient.
	cfg := contract.Configuration{Name: "app", Required: true, Ref: "oci://registry/schema-pacto"}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{}, // No schema in bundle
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (remote ref schema), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_MultipleConfigurations tests multiple configurations produce multiple observations.
func TestConfigurationsDim_MultipleConfigurations(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080"},
	}
	sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "db-secret"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm1, sec).Build()
	obs := New(c)

	cfg1 := contract.Configuration{Name: "app", Required: true, Schema: `{"type":"object","properties":{"port":{"type":"number"}},"required":["port"]}`}
	cfg2 := contract.Configuration{Name: "db", Required: true, Schema: "{}"}
	binding1 := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	binding2 := unversioned.ConfigBinding{Configuration: "db", Kind: "Secret", Name: "db-secret"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg1, cfg2}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding1, binding2},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(observations))
	}
	// app: ConfigMap conforms -> Observed{Present:true}
	// db: Secret present -> Insufficient
	foundApp := false
	foundDB := false
	for _, o := range observations {
		if o.Subject.Name == "app" {
			foundApp = true
			if o.Outcome != evidence.Observed {
				t.Errorf("app: expected Observed, got %v", o.Outcome)
			}
		}
		if o.Subject.Name == "db" {
			foundDB = true
			if o.Outcome != evidence.Insufficient {
				t.Errorf("db: expected Insufficient (Secret existence-only), got %v", o.Outcome)
			}
		}
	}
	if !foundApp || !foundDB {
		t.Errorf("did not find both observations: app=%v db=%v", foundApp, foundDB)
	}
}

// TestCollect_WithConfiguration tests Collect integration with configurations.
func TestCollect_WithConfiguration(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = unversioned.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: `{"type":"object","properties":{"port":{"type":"number"}},"required":["port"]}`}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		ServiceName:    "test-service",
		Contract:       &contract.Contract{Service: contract.Service{Name: "test"}, Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	evidenceSet, _ := obs.Collect(ctx, input)

	// Find the configuration observation.
	var configObs *evidence.Observation
	for i := range evidenceSet.Observations {
		if evidenceSet.Observations[i].Kind == evidence.ConfigurationPresent {
			configObs = &evidenceSet.Observations[i]
			break
		}
	}
	if configObs == nil {
		t.Fatalf("no configuration observation found")
	}
	if configObs.Outcome != evidence.Observed {
		t.Errorf("expected Observed, got %v", configObs.Outcome)
	}
}

// TestConfigurationsDim_UnsupportedFormat tests unsupported format -> Insufficient.
func TestConfigurationsDim_UnsupportedFormat(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.toml": "port = 8080"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	schema := `{"type":"object"}`
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schema}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.toml", Format: "toml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (unsupported format), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_EmptySchema tests empty schema -> Insufficient.
func TestConfigurationsDim_EmptySchema(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: ""} // Empty schema
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (empty schema), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_MalformedSchemaJSON tests malformed schema JSON -> Insufficient.
func TestConfigurationsDim_MalformedSchemaJSON(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: `{"type": invalid json`}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (malformed schema), got %v", o.Outcome)
	}
}

// TestConfigurationsDim_InvalidSchemaCompilation tests schema that fails to compile -> Insufficient.
func TestConfigurationsDim_InvalidSchemaCompilation(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
	obs := New(c)

	// Schema that parses as JSON but fails jsonschema compilation.
	cfg := contract.Configuration{Name: "app", Required: true, Schema: `{"type": "invalid-type"}`}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       fstest.MapFS{},
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	observations, _ := obs.observeConfigurationsDim(ctx, input, evidence.Provenance{Collector: "test"}, input.Now)

	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	o := observations[0]
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (schema compilation failure), got %v", o.Outcome)
	}
}
