/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/loader"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const (
	providerRef    = "oci://ghcr.io/acme/pg:16.1.0"
	providerSchema = `{"type":"object","required":["size"],"properties":{"size":{"enum":["small","large"]},"version":{"type":"string"}}}`
	providerYAML   = `pactoVersion: "2.6"
service: {name: pg, version: 16.1.0}
configurations:
  - {name: db, schema: configuration/schema.json, required: true}
`
)

// refResolver answers each provider ref from a fixed bundle, or fails with err.
type refResolver struct {
	bundle *contract.Bundle
	err    error
}

func (r refResolver) ResolveBundle(context.Context, string) (*contract.Bundle, error) {
	return r.bundle, r.err
}

// refLoader is a mockLoader that also resolves configuration refs, recording what it was asked with.
type refLoader struct {
	mockLoader
	resolver    validation.BundleResolver
	contractRef string
	auth        *authn.AuthConfig
	credID      string
}

var _ ConfigRefLoader = (*refLoader)(nil)
var _ ConfigRefLoader = (*loader.Loader)(nil)

func (l *refLoader) ConfigRefResolver(contractRef string, auth *authn.AuthConfig, credID string) validation.BundleResolver {
	l.contractRef, l.auth, l.credID = contractRef, auth, credID
	return l.resolver
}

func providerBundle(t *testing.T) *contract.Bundle {
	t.Helper()
	c, err := contract.Parse(strings.NewReader(providerYAML))
	if err != nil {
		t.Fatal(err)
	}
	return &contract.Bundle{Contract: c, FS: fstest.MapFS{
		"pacto.yaml":                &fstest.MapFile{Data: []byte(providerYAML)},
		"configuration/schema.json": &fstest.MapFile{Data: []byte(providerSchema)},
	}}
}

// consumerYAML is a reference-only consumer of the provider at version, with values when given.
func consumerYAML(version, values string) string {
	s := fmt.Sprintf("pactoVersion: %q\nservice: {name: orders, version: 1.0.0}\nconfigurations:\n  - name: db\n    ref: %s\n    required: true\n", version, providerRef)
	if values != "" {
		s += "    values: " + values + "\n"
	}
	return s
}

func reconcileConsumer(t *testing.T, raw string, overrides *pactov1alpha1.ContractOverrides, resolver validation.BundleResolver) *pactov1alpha1.Pacto {
	t.Helper()
	c, err := contract.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "default", UID: "u"},
		Spec:       pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{Inline: raw}, Overrides: overrides},
	}
	r := newReconciler(pacto)
	r.Loader = &refLoader{resolver: resolver, mockLoader: mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
		return &loader.LoadResult{Contract: c, RawYAML: []byte(raw), BundleFS: fstest.MapFS{}}, nil
	}}}
	if _, err := r.Reconcile(context.Background(), reconcileReq("orders", "default")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return getPacto(t, r, "orders", "default")
}

func issueCodes(issues []pactov1alpha1.ValidationIssue) string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Code+": "+i.Message)
	}
	return strings.Join(out, "\n")
}

func TestReconcileConfigRefValues(t *testing.T) {
	resolved := refResolver{bundle: providerBundle(t)}
	tests := []struct {
		name         string
		raw          string
		overrides    *pactov1alpha1.ContractOverrides
		resolver     validation.BundleResolver
		wantStatus   string
		wantError    string
		wantWarning  string
		wantNoErrors bool
	}{
		{name: "inline values the resolved schema accepts", raw: consumerYAML("2.6", "{size: small}"), resolver: resolved,
			wantStatus: pactov1alpha1.ContractStatusReference, wantNoErrors: true},
		{name: "inline values invalid", raw: consumerYAML("2.6", "{size: huge}"), resolver: resolved,
			wantStatus: pactov1alpha1.ContractStatusInvalid, wantError: "CONFIG_VALUES_VALIDATION_FAILED"},
		{name: "override invalid", raw: consumerYAML("2.6", "{size: small}"), resolver: resolved,
			overrides:  overrideOf("db", map[string]string{"size": "huge"}),
			wantStatus: pactov1alpha1.ContractStatusInvalid, wantError: "CONFIG_VALUES_VALIDATION_FAILED"},
		{name: "unresolved ref warns and the contract stays valid", raw: consumerYAML("2.6", "{size: huge}"),
			resolver:   refResolver{err: errors.New("registry unreachable")},
			wantStatus: pactov1alpha1.ContractStatusReference, wantWarning: "CONFIG_REF_UNVERIFIED", wantNoErrors: true},
		{name: "a loader that cannot resolve leaves the ref unverified", raw: consumerYAML("2.6", "{size: huge}"),
			wantStatus: pactov1alpha1.ContractStatusReference, wantWarning: "CONFIG_REF_UNVERIFIED", wantNoErrors: true},
		{name: "2.5 override on a ref configuration", raw: consumerYAML("2.5", ""), resolver: resolved,
			overrides:  overrideOf("db", map[string]string{"size": "small"}),
			wantStatus: pactov1alpha1.ContractStatusInvalid,
			wantError:  `override for configuration "db": it takes its schema by ref; values for it need pactoVersion "2.6"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := reconcileConsumer(t, tc.raw, tc.overrides, tc.resolver)
			if got.Status.ContractStatus != tc.wantStatus {
				t.Fatalf("status = %s, want %s (validation=%+v)", got.Status.ContractStatus, tc.wantStatus, got.Status.Validation)
			}
			v := got.Status.Validation
			if v == nil {
				t.Fatal("no status.validation")
			}
			if tc.wantError != "" && !strings.Contains(issueCodes(v.Errors), tc.wantError) {
				t.Errorf("errors = %s, want %s", issueCodes(v.Errors), tc.wantError)
			}
			if tc.wantWarning != "" && !strings.Contains(issueCodes(v.Warnings), tc.wantWarning) {
				t.Errorf("warnings = %s, want %s", issueCodes(v.Warnings), tc.wantWarning)
			}
			if tc.wantNoErrors && (len(v.Errors) > 0 || !v.Valid) {
				t.Errorf("errors = %s, want none", issueCodes(v.Errors))
			}
		})
	}
}

// The resolver is asked with spec.contractRef, its pull secret's credentials and their identity,
// and a resolved ref configuration without values surfaces the provider schema's properties.
func TestReconcileConfigRef_ResolverInputsAndStatus(t *testing.T) {
	raw := consumerYAML("2.6", "")
	c, err := contract.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("t0k")},
	}
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "default", UID: "u"},
		Spec: pactov1alpha1.PactoSpec{ContractRef: pactov1alpha1.ContractRef{
			OCI: "oci://ghcr.io/acme/orders:1.0.0", PullSecretRef: "pull"}},
	}
	r := newReconciler(pacto, secret)
	ldr := &refLoader{resolver: refResolver{bundle: providerBundle(t)}, mockLoader: mockLoader{loadFn: func(context.Context, string, string) (*loader.LoadResult, error) {
		return &loader.LoadResult{Contract: c, RawYAML: []byte(raw), BundleFS: fstest.MapFS{}}, nil
	}}}
	r.Loader = ldr
	if _, err := r.Reconcile(context.Background(), reconcileReq("orders", "default")); err != nil {
		t.Fatal(err)
	}
	if ldr.contractRef != pacto.Spec.ContractRef.OCI || ldr.auth == nil || ldr.auth.RegistryToken != "t0k" ||
		!strings.HasPrefix(ldr.credID, "default/pull@") {
		t.Errorf("resolver asked with (%q, %+v, %q)", ldr.contractRef, ldr.auth, ldr.credID)
	}
	got := getPacto(t, r, "orders", "default")
	props := got.Status.Configurations[0].Properties
	if len(props) != 2 || props[0].Key != "size" && props[1].Key != "size" {
		t.Errorf("properties = %+v, want the provider schema's size and version", props)
	}
}
