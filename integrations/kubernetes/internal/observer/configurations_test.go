/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	unversioned "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

var ErrForbidden = errors.New("access forbidden")

// schemaPath is what contract.Configuration.Schema actually holds: a bundle-relative
// PATH, never inline JSON. Fixtures that declared inline JSON here hid the bug where
// the operator json.Unmarshal'd the path itself.
const schemaPath = "configuration/schema.json"

// bundleWithSchema builds a bundle FS holding schemaPath with the given schema content.
func bundleWithSchema(schema string) fstest.MapFS {
	return fstest.MapFS{schemaPath: &fstest.MapFile{Data: []byte(schema)}}
}

// TestConfigurationsDim_NoBinding tests that a required configuration with no binding -> Unsupported.
func TestConfigurationsDim_NoBinding(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = unversioned.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	obs := New(c)

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(schema),
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
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(schema),
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
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(schema),
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
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(schema),
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
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

	cfg := contract.Configuration{Name: "opt", Required: false, Schema: schemaPath}
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
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.json", Format: "json"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(schema),
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

	cfg1 := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	cfg2 := contract.Configuration{Name: "db", Required: true, Schema: schemaPath}
	binding1 := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	binding2 := unversioned.ConfigBinding{Configuration: "db", Kind: "Secret", Name: "db-secret"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg1, cfg2}},
		BundleFS:       bundleWithSchema(`{"type":"object","properties":{"port":{"type":"number"}},"required":["port"]}`),
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		ServiceName:    "test-service",
		Contract:       &contract.Contract{Service: contract.Service{Name: "test"}, Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(`{"type":"object","properties":{"port":{"type":"number"}},"required":["port"]}`),
		ConfigBindings: []unversioned.ConfigBinding{binding},
		Now:            time.Now(),
	}

	evidenceSet, _, _ := obs.Collect(ctx, input)

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
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.toml", Format: "toml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(schema),
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

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(`{"type": invalid json`),
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
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
	input := CollectInput{
		Namespace:      "default",
		Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
		BundleFS:       bundleWithSchema(`{"type": "invalid-type"}`),
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

// TestConfigurationsDim_NoBundleFS tests an inline contract (no bundle files) -> Insufficient,
// because the declared schema path cannot be read.
func TestConfigurationsDim_NoBundleFS(t *testing.T) {
	assertConfigInsufficient(t, nil)
}

// TestConfigurationsDim_SchemaNotInBundle tests a declared schema path absent from the bundle -> Insufficient.
func TestConfigurationsDim_SchemaNotInBundle(t *testing.T) {
	assertConfigInsufficient(t, fstest.MapFS{"pacto.yaml": &fstest.MapFile{Data: []byte("apiVersion: v1")}})
}

// assertConfigInsufficient runs the ConfigMap+schemaPath fixture against bundleFS and expects Insufficient.
func assertConfigInsufficient(t *testing.T, bundleFS fs.FS) {
	t.Helper()
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "app-config"},
		Data:       map[string]string{"app.yaml": "port: 8080"},
	}
	obs := New(fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build())

	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	binding := unversioned.ConfigBinding{Configuration: "app", Kind: "ConfigMap", Name: "app-config", Key: "app.yaml", Format: "yaml"}
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
	if observations[0].Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (schema unreadable), got %v", observations[0].Outcome)
	}
}

// A ConfigMap bound to a configuration that names its schema by ref is validated against the schema the
// ref resolved to; a ref that did not resolve stays Insufficient, never Conformant.
func TestConfigMapAgainstResolvedRefSchema(t *testing.T) {
	compile := func(schema string) *jsonschema.Schema {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
		if err != nil {
			t.Fatal(err)
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("mem:///ref.json", doc); err != nil {
			t.Fatal(err)
		}
		return c.MustCompile("mem:///ref.json")
	}
	resolved := validation.ConfigRefs{"db": {Name: "db", Schema: compile(`{"type":"object","required":["size"],"properties":{"size":{"enum":["small","large"]}}}`)}}
	tests := []struct {
		name        string
		content     string
		refs        validation.ConfigRefs
		wantOutcome evidence.Outcome
		wantConform bool
	}{
		{name: "conformant", content: "size: small\n", refs: resolved, wantOutcome: evidence.Observed, wantConform: true},
		{name: "mismatch", content: "size: huge\n", refs: resolved, wantOutcome: evidence.Observed},
		{name: "unresolved", content: "size: small\n", refs: validation.ConfigRefs{"db": {Name: "db"}}, wantOutcome: evidence.Insufficient},
		{name: "no resolution at all", content: "size: small\n", wantOutcome: evidence.Insufficient},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "db-config"},
				Data:       map[string]string{"db.yaml": tc.content},
			}
			obs := New(fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build())
			cfg := contract.Configuration{Name: "db", Required: true, Ref: "oci://ghcr.io/acme/pg:16.1.0"}
			input := CollectInput{
				Namespace:      "default",
				Contract:       &contract.Contract{Configurations: []contract.Configuration{cfg}},
				BundleFS:       fstest.MapFS{},
				ConfigRefs:     tc.refs,
				ConfigBindings: []unversioned.ConfigBinding{{Configuration: "db", Kind: "ConfigMap", Name: "db-config", Key: "db.yaml", Format: "yaml"}},
				Now:            time.Now(),
			}
			observations, _ := obs.observeConfigurationsDim(context.Background(), input, evidence.Provenance{Collector: "test"}, input.Now)
			if len(observations) != 1 {
				t.Fatalf("observations = %d, want 1", len(observations))
			}
			o := observations[0]
			if o.Outcome != tc.wantOutcome {
				t.Fatalf("outcome = %v, want %v", o.Outcome, tc.wantOutcome)
			}
			if o.Outcome != evidence.Observed {
				return
			}
			val, err := o.GetConfigurationObservation()
			if err != nil {
				t.Fatal(err)
			}
			if !val.Present || val.Conformant != tc.wantConform {
				t.Errorf("present=%v conformant=%v, want present and conformant=%v", val.Present, val.Conformant, tc.wantConform)
			}
		})
	}
}

// secretInput is one required configuration bound to Secret app-secret in namespace payments.
func secretInput(scoped bool, windows map[string]*metav1.Time, now time.Time) CollectInput {
	cfg := contract.Configuration{Name: "app", Required: true, Schema: schemaPath}
	return CollectInput{
		Namespace:           "payments",
		ServiceName:         "test-service",
		Contract:            &contract.Contract{Service: contract.Service{Name: "test"}, Configurations: []contract.Configuration{cfg}},
		BundleFS:            fstest.MapFS{},
		ConfigBindings:      []unversioned.ConfigBinding{{Configuration: "app", Kind: "Secret", Name: "app-secret"}},
		StabilizationWindow: 2 * time.Minute,
		ObservationWindows:  windows,
		Now:                 now,
		Scoped:              scoped,
	}
}

// secretReader is a Secret reader: Forbidden while denied, NotFound otherwise, or the Secret when present.
type secretReader struct {
	denied, present bool
	gets            int
}

func (r *secretReader) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	r.gets++
	gr := corev1.Resource("secrets")
	switch {
	case r.denied:
		return apierrors.NewForbidden(gr, key.Name, errors.New("no grant"))
	case r.present:
		obj.SetName(key.Name)
		obj.SetNamespace(key.Namespace)
		return nil
	}
	return apierrors.NewNotFound(gr, key.Name)
}

func (r *secretReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("the configuration observer never lists Secrets")
}

// noSecretClient is a cached client that fails the test on any Secret read.
func noSecretClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = unversioned.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == "Secret" {
				t.Errorf("Secret %s read through the cached client", key)
			}
			if _, ok := obj.(*corev1.Secret); ok {
				t.Errorf("typed Secret %s read through the cached client", key)
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
}

func secretObservation(t *testing.T, o *Observer, input CollectInput) (evidence.Observation, []ObservationWindowUpdate) {
	t.Helper()
	observations, updates := o.observeConfigurationsDim(context.Background(), input, evidence.Provenance{Collector: "test"}, input.Now)
	if len(observations) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(observations))
	}
	return observations[0], updates
}

func TestSecretConfigurationForbiddenIsUnsupported(t *testing.T) {
	o := New(noSecretClient(t)).WithReader(&secretReader{denied: true})
	got, _ := secretObservation(t, o, secretInput(false, nil, time.Now()))
	if got.Outcome != evidence.Unsupported {
		t.Fatalf("outcome = %v, want Unsupported (unknown, never satisfied)", got.Outcome)
	}
	if got.Detail == "" {
		t.Fatal("a Forbidden read must say which grant is missing")
	}
}

func TestGrantHintClusterWideNamesSecretGrants(t *testing.T) {
	o := New(noSecretClient(t)).WithReader(&secretReader{denied: true})
	got, _ := secretObservation(t, o, secretInput(false, nil, time.Now()))
	want := "the operator may not read Secret payments/app-secret; list payments/app-secret in controller.secretGrants"
	if got.Detail != want {
		t.Fatalf("detail = %q, want %q", got.Detail, want)
	}
	if strings.Contains(got.Detail, "watchNamespaces") {
		t.Fatal("a cluster-wide install is never told to narrow its whole fleet")
	}
}

func TestGrantHintScopedNamesWatchNamespaces(t *testing.T) {
	o := New(noSecretClient(t)).WithReader(&secretReader{denied: true})
	got, _ := secretObservation(t, o, secretInput(true, nil, time.Now()))
	want := "the operator may not read Secret payments/app-secret although it watches payments; check the chart's watch Role and RoleBinding in payments or list payments/app-secret in controller.secretGrants"
	if got.Detail != want {
		t.Fatalf("detail = %q, want %q", got.Detail, want)
	}
}

// A name too long for the bounded Detail still reads Unsupported; only the hint is dropped.
func TestGrantHintTooLongDropsDetail(t *testing.T) {
	o := New(noSecretClient(t)).WithReader(&secretReader{denied: true})
	input := secretInput(false, nil, time.Now())
	input.ConfigBindings[0].Name = strings.Repeat("s", 253)
	got, _ := secretObservation(t, o, input)
	if got.Outcome != evidence.Unsupported || got.Detail != "" {
		t.Fatalf("got outcome %v detail %q, want Unsupported without detail", got.Outcome, got.Detail)
	}
}

func TestSecretConfigurationForbiddenDoesNotFailCollection(t *testing.T) {
	o := New(noSecretClient(t)).WithReader(&secretReader{denied: true})
	set, _, err := o.Collect(context.Background(), secretInput(false, nil, time.Now()))
	if err != nil {
		t.Fatalf("a missing grant is how the operator is installed, not a failed collection: %v", err)
	}
	if len(o.run.failures) != 0 {
		t.Fatalf("failures recorded: %v", o.run.failures)
	}
	for _, obs := range set.Observations {
		if obs.Kind == evidence.ConfigurationPresent && obs.Outcome != evidence.Unsupported {
			t.Fatalf("configuration outcome = %v, want Unsupported", obs.Outcome)
		}
	}
}

func TestSecretConfigurationForbiddenResetsWindow(t *testing.T) {
	reader := &secretReader{}
	o := New(noSecretClient(t)).WithReader(reader)
	start := time.Now()
	windows := map[string]*metav1.Time{}
	apply := func(updates []ObservationWindowUpdate) {
		for _, u := range updates {
			if u.FirstObservedNegativeAt == nil {
				delete(windows, u.Kind+"/"+u.Subject)
			} else {
				windows[u.Kind+"/"+u.Subject] = u.FirstObservedNegativeAt
			}
		}
	}

	// NotFound opens a window.
	_, updates := secretObservation(t, o, secretInput(false, windows, start))
	apply(updates)
	if windows["configuration/app"] == nil {
		t.Fatal("NotFound must open a window")
	}

	// Forbidden: the period the operator could not observe never counts toward ABSENT.
	reader.denied = true
	_, updates = secretObservation(t, o, secretInput(false, windows, start.Add(time.Minute)))
	apply(updates)
	if windows["configuration/app"] != nil {
		t.Fatal("a Forbidden read must reset the window")
	}

	// Grant restored, still NotFound past the old window: a fresh window, never CONFIGURATION_ABSENT.
	reader.denied = false
	got, updates := secretObservation(t, o, secretInput(false, windows, start.Add(5*time.Minute)))
	apply(updates)
	if got.Outcome == evidence.Observed {
		t.Fatal("the window counted a period the operator could not observe")
	}
	if w := windows["configuration/app"]; w == nil || !w.Time.Equal(start.Add(5*time.Minute)) {
		t.Fatalf("expected a fresh window at the restored read, got %v", w)
	}
}

func TestSecretConfigurationReadsUncached(t *testing.T) {
	reader := &secretReader{present: true}
	o := New(noSecretClient(t)).WithReader(reader)
	got, _ := secretObservation(t, o, secretInput(false, nil, time.Now()))
	if got.Outcome != evidence.Insufficient {
		t.Fatalf("outcome = %v, want Insufficient (existence established)", got.Outcome)
	}
	if reader.gets != 1 {
		t.Fatalf("reader served %d Secret reads, want 1", reader.gets)
	}
}

func TestSecretConfigurationNilReaderUsesClient(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "payments", Name: "app-secret"}},
	).Build()
	o := &Observer{client: c, run: newCollectRun()}
	got, _ := secretObservation(t, o, secretInput(false, nil, time.Now()))
	if got.Outcome != evidence.Insufficient {
		t.Fatalf("outcome = %v, want Insufficient through the client", got.Outcome)
	}
}

func TestSecretConfigurationNotFoundStillWindowed(t *testing.T) {
	o := New(noSecretClient(t)).WithReader(&secretReader{})
	got, updates := secretObservation(t, o, secretInput(false, map[string]*metav1.Time{}, time.Now()))
	if got.Outcome != evidence.Insufficient {
		t.Fatalf("outcome = %v, want Insufficient within the window", got.Outcome)
	}
	if len(updates) != 1 || updates[0].FirstObservedNegativeAt == nil {
		t.Fatalf("NotFound must open a window, got %+v", updates)
	}
}
