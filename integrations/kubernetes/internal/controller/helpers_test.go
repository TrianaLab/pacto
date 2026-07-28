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
	"testing/fstest"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/schemax"
	"github.com/trianalab/pacto/v3/pkg/validation"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// mockLoader implements ContractLoader for testing.
type mockLoader struct {
	loadFn     func(ctx context.Context, ociRef, inline string) (*loader.LoadResult, error)
	listTagsFn func(ctx context.Context, ociRef string) ([]string, error)
}

var _ ContractLoader = (*mockLoader)(nil)

func (m *mockLoader) Load(ctx context.Context, ociRef, inline string, _ *authn.AuthConfig) (*loader.LoadResult, error) {
	if m.loadFn != nil {
		return m.loadFn(ctx, ociRef, inline)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLoader) ListTags(ctx context.Context, ociRef string, _ *authn.AuthConfig) ([]string, error) {
	if m.listTagsFn != nil {
		return m.listTagsFn(ctx, ociRef)
	}
	return nil, fmt.Errorf("not implemented")
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = pactov1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newReconciler(objs ...client.Object) *PactoReconciler {
	s := newScheme()
	cb := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&pactov1alpha1.Pacto{}, &pactov1alpha1.PactoRevision{})
	if len(objs) > 0 {
		cb = cb.WithObjects(objs...)
	}
	return &PactoReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(20),
		Loader:   &mockLoader{},
	}
}

// ---------- formatValidationErrors ----------

func TestFormatValidationErrors_WithPath(t *testing.T) {
	errs := []contract.ValidationError{
		{Code: "E001", Path: "service.name", Message: "name is required"},
	}
	got := formatValidationErrors(errs)
	if !strings.Contains(got, "service.name: name is required") {
		t.Fatalf("expected path:message format, got %q", got)
	}
	if !strings.HasPrefix(got, "Contract validation failed:") {
		t.Fatalf("expected prefix, got %q", got)
	}
}

func TestFormatValidationErrors_WithoutPath(t *testing.T) {
	errs := []contract.ValidationError{
		{Code: "E002", Message: "something wrong"},
	}
	got := formatValidationErrors(errs)
	if !strings.Contains(got, "something wrong") {
		t.Fatalf("expected message, got %q", got)
	}
	expected := "Contract validation failed: something wrong"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatValidationErrors_Multiple(t *testing.T) {
	errs := []contract.ValidationError{
		{Path: "a", Message: "err1"},
		{Message: "err2"},
	}
	got := formatValidationErrors(errs)
	if !strings.Contains(got, "a: err1") {
		t.Fatalf("expected first error, got %q", got)
	}
	if !strings.Contains(got, "err2") {
		t.Fatalf("expected second error, got %q", got)
	}
	if !strings.Contains(got, "; ") {
		t.Fatalf("expected semicolon separator, got %q", got)
	}
}

// ---------- mapValidationResult ----------

func TestMapValidationResult_ErrorsOnly(t *testing.T) {
	vr := validation.ValidationResult{
		Errors: []contract.ValidationError{
			{Code: "E1", Path: "p", Message: "m"},
		},
	}
	got := mapValidationResult(vr)
	if got.Valid {
		t.Fatal("expected Valid=false when errors present")
	}
	if len(got.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(got.Errors))
	}
	if got.Errors[0].Code != "E1" || got.Errors[0].Path != "p" || got.Errors[0].Message != "m" {
		t.Fatalf("error fields mismatch: %+v", got.Errors[0])
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d", len(got.Warnings))
	}
}

func TestMapValidationResult_WarningsOnly(t *testing.T) {
	vr := validation.ValidationResult{
		Warnings: []contract.ValidationWarning{
			{Code: "W1", Path: "wp", Message: "wm"},
		},
	}
	got := mapValidationResult(vr)
	if !got.Valid {
		t.Fatal("expected Valid=true when no errors")
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(got.Warnings))
	}
	if got.Warnings[0].Code != "W1" || got.Warnings[0].Path != "wp" || got.Warnings[0].Message != "wm" {
		t.Fatalf("warning fields mismatch: %+v", got.Warnings[0])
	}
}

func TestMapValidationResult_ErrorsAndWarnings(t *testing.T) {
	vr := validation.ValidationResult{
		Errors:   []contract.ValidationError{{Code: "E1", Message: "e"}},
		Warnings: []contract.ValidationWarning{{Code: "W1", Message: "w"}},
	}
	got := mapValidationResult(vr)
	if got.Valid {
		t.Fatal("expected Valid=false")
	}
	if len(got.Errors) != 1 || len(got.Warnings) != 1 {
		t.Fatalf("expected 1 error and 1 warning, got %d/%d", len(got.Errors), len(got.Warnings))
	}
}

func TestMapValidationResult_Empty(t *testing.T) {
	vr := validation.ValidationResult{}
	got := mapValidationResult(vr)
	if !got.Valid {
		t.Fatal("expected Valid=true when no errors")
	}
}

// ---------- resolutionPolicy ----------

func TestResolutionPolicy_Unversioned(t *testing.T) {
	got := resolutionPolicy("ghcr.io/org/service")
	if got != pactov1alpha1.ResolutionPolicyLatest {
		t.Errorf("expected Latest, got %s", got)
	}
}

func TestResolutionPolicy_Tag(t *testing.T) {
	got := resolutionPolicy("ghcr.io/org/service:1.0.0")
	if got != pactov1alpha1.ResolutionPolicyPinnedTag {
		t.Errorf("expected PinnedTag, got %s", got)
	}
}

func TestResolutionPolicy_Digest(t *testing.T) {
	got := resolutionPolicy("ghcr.io/org/service@sha256:abc123")
	if got != pactov1alpha1.ResolutionPolicyPinnedDigest {
		t.Errorf("expected PinnedDigest, got %s", got)
	}
}

func TestResolutionPolicy_Empty(t *testing.T) {
	got := resolutionPolicy("")
	if got != "" {
		t.Errorf("expected empty string for empty ref, got %s", got)
	}
}

// ---------- applyConfigurationOverrides ----------

func TestApplyConfigurationOverrides_SingleScope(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{
			{
				Name: "default",
				Values: map[string]any{
					"db_host": "localhost",
					"db_port": "5432",
				},
			},
		},
	}
	overrides := &pactov1alpha1.ContractOverrides{
		Configurations: []pactov1alpha1.ConfigurationOverride{
			{
				Name:   "default",
				Values: map[string]string{"db_host": "prod-db.example.com"},
			},
		},
	}
	merged, overriddenKeys, err := applyConfigurationOverrides(c, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Configurations[0].Values["db_host"] != "prod-db.example.com" {
		t.Errorf("expected overridden db_host, got %v", merged.Configurations[0].Values["db_host"])
	}
	if merged.Configurations[0].Values["db_port"] != "5432" {
		t.Errorf("expected original db_port, got %v", merged.Configurations[0].Values["db_port"])
	}
	if len(overriddenKeys["default"]) != 1 || overriddenKeys["default"][0] != "db_host" {
		t.Errorf("expected overriddenKeys[default]=[db_host], got %v", overriddenKeys["default"])
	}
}

func TestApplyConfigurationOverrides_NoMatch(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{{Name: "default"}},
	}
	overrides := &pactov1alpha1.ContractOverrides{
		Configurations: []pactov1alpha1.ConfigurationOverride{
			{Name: "nonexistent", Values: map[string]string{"key": "value"}},
		},
	}
	_, _, err := applyConfigurationOverrides(c, overrides)
	if err == nil {
		t.Fatal("expected error for non-matching configuration scope")
	}
	if !strings.Contains(err.Error(), "configuration \"nonexistent\" not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestApplyConfigurationOverrides_MultipleScopes(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{
			{Name: "default", Values: map[string]any{"a": "1"}},
			{Name: "prod", Values: map[string]any{"b": "2"}},
		},
	}
	overrides := &pactov1alpha1.ContractOverrides{
		Configurations: []pactov1alpha1.ConfigurationOverride{
			{Name: "default", Values: map[string]string{"a": "100"}},
			{Name: "prod", Values: map[string]string{"b": "200"}},
		},
	}
	merged, overriddenKeys, err := applyConfigurationOverrides(c, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.Configurations[0].Values["a"] != "100" {
		t.Errorf("expected overridden a, got %v", merged.Configurations[0].Values["a"])
	}
	if merged.Configurations[1].Values["b"] != "200" {
		t.Errorf("expected overridden b, got %v", merged.Configurations[1].Values["b"])
	}
	if len(overriddenKeys) != 2 {
		t.Errorf("expected 2 scopes in overriddenKeys, got %d", len(overriddenKeys))
	}
}

// ---------- mapOwnerToInfo ----------

func TestMapOwnerToInfo_TeamOnly(t *testing.T) {
	owner := contract.Owner{Team: "platform"}
	info := mapOwnerToInfo(owner)
	if info == nil || info.Team != "platform" {
		t.Errorf("expected team=platform, got %+v", info)
	}
}

func TestMapOwnerToInfo_DRIOnly(t *testing.T) {
	owner := contract.Owner{DRI: "alice@example.com"}
	info := mapOwnerToInfo(owner)
	if info == nil || info.DRI != "alice@example.com" {
		t.Errorf("expected DRI=alice@example.com, got %+v", info)
	}
}

func TestMapOwnerToInfo_Contacts(t *testing.T) {
	owner := contract.Owner{
		Contacts: []contract.OwnerContact{
			{Type: "email", Value: "team@example.com", Purpose: "support"},
		},
	}
	info := mapOwnerToInfo(owner)
	if info == nil || len(info.Contacts) != 1 {
		t.Fatalf("expected 1 contact, got %+v", info)
	}
	if info.Contacts[0].Type != "email" || info.Contacts[0].Value != "team@example.com" {
		t.Errorf("unexpected contact: %+v", info.Contacts[0])
	}
}

func TestMapOwnerToInfo_Empty(t *testing.T) {
	owner := contract.Owner{}
	info := mapOwnerToInfo(owner)
	if info != nil {
		t.Errorf("expected nil for empty owner, got %+v", info)
	}
}

// ---------- requeueInterval ----------

func TestRequeueInterval_Default(t *testing.T) {
	r := &PactoReconciler{}
	p := &pactov1alpha1.Pacto{}
	got := r.requeueInterval(p)
	if got != 5*time.Minute {
		t.Fatalf("expected 5m, got %v", got)
	}
}

func TestRequeueInterval_Custom(t *testing.T) {
	r := &PactoReconciler{}
	p := &pactov1alpha1.Pacto{
		Spec: pactov1alpha1.PactoSpec{
			CheckIntervalSeconds: 60,
		},
	}
	got := r.requeueInterval(p)
	if got != 60*time.Second {
		t.Fatalf("expected 60s, got %v", got)
	}
}

// ---------- schema helpers ----------

func TestSchemaPropsFromBundle_NilFS(t *testing.T) {
	got := schemaPropsFromBundle(nil, "config/schema.json")
	if got != nil {
		t.Errorf("expected nil for nil FS, got %v", got)
	}
}

func TestSchemaPropsFromBundle_EmptyPath(t *testing.T) {
	fsys := fstest.MapFS{"config/schema.json": &fstest.MapFile{Data: []byte("{}")}}
	got := schemaPropsFromBundle(fsys, "")
	if got != nil {
		t.Errorf("expected nil for empty path, got %v", got)
	}
}

func TestSchemaPropsFromBundle_FileNotFound(t *testing.T) {
	fsys := fstest.MapFS{}
	got := schemaPropsFromBundle(fsys, "missing.json")
	if got != nil {
		t.Errorf("expected nil for missing file, got %v", got)
	}
}

func TestSchemaPropsFromBundle_Valid(t *testing.T) {
	schema := `{"properties":{"key":{"type":"string","default":"value"}}}`
	fsys := fstest.MapFS{"config/schema.json": &fstest.MapFile{Data: []byte(schema)}}
	got := schemaPropsFromBundle(fsys, "config/schema.json")
	if len(got) == 0 {
		t.Fatalf("expected properties, got none")
	}
	if got[0].Key != "key" || got[0].Type != "string" {
		t.Errorf("unexpected property: %+v", got[0])
	}
}

func TestSchemaMetaFromBundle_NilFS(t *testing.T) {
	title, desc := schemaMetaFromBundle(nil, "schema.json")
	if title != "" || desc != "" {
		t.Errorf("expected empty strings for nil FS, got %q/%q", title, desc)
	}
}

func TestSchemaMetaFromBundle_EmptyPath(t *testing.T) {
	fsys := fstest.MapFS{"schema.json": &fstest.MapFile{Data: []byte("{}")}}
	title, desc := schemaMetaFromBundle(fsys, "")
	if title != "" || desc != "" {
		t.Errorf("expected empty strings for empty path, got %q/%q", title, desc)
	}
}

func TestSchemaMetaFromBundle_FileNotFound(t *testing.T) {
	fsys := fstest.MapFS{}
	title, desc := schemaMetaFromBundle(fsys, "missing.json")
	if title != "" || desc != "" {
		t.Errorf("expected empty strings for missing file, got %q/%q", title, desc)
	}
}

func TestSchemaMetaFromBundle_Valid(t *testing.T) {
	schema := `{"title":"Config","description":"Service config"}`
	fsys := fstest.MapFS{"schema.json": &fstest.MapFile{Data: []byte(schema)}}
	title, desc := schemaMetaFromBundle(fsys, "schema.json")
	if title != "Config" {
		t.Errorf("expected title=Config, got %q", title)
	}
	if desc != "Service config" {
		t.Errorf("expected desc=Service config, got %q", desc)
	}
}

func TestToSchemaProps_Empty(t *testing.T) {
	got := toSchemaProps(nil)
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestToSchemaProps_Valid(t *testing.T) {
	input := []schemax.Property{
		{Key: "k1", Value: "v1", Type: "string"},
		{Key: "k2", Value: "42", Type: "integer"},
	}
	got := toSchemaProps(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 props, got %d", len(got))
	}
	if got[0].Key != "k1" || got[0].Type != "string" {
		t.Errorf("unexpected first prop: %+v", got[0])
	}
	if got[1].Key != "k2" || got[1].Type != "integer" {
		t.Errorf("unexpected second prop: %+v", got[1])
	}
}

func TestShortDigest(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"sha256:abcdef0123456789", "sha256:abcde"}, // long -> first 12 chars
		{"", ""},                         // empty -> no panic
		{"short", "short"},               // shorter than 12 -> whole string
		{"exactly12chr", "exactly12chr"}, // exactly 12 -> whole string
	}
	for _, tc := range cases {
		if got := shortDigest(tc.in); got != tc.want {
			t.Errorf("shortDigest(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
