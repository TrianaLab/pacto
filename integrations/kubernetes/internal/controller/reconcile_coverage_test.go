/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"testing"
	"testing/fstest"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
	"github.com/trianalab/pacto/v3/pkg/contract"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These tests target specific uncovered branches in populateContractStatus
// and other reconcile helper functions to drive coverage to 100%.

func TestPopulateContractStatus_FullContract(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "full", Namespace: "default"},
	}

	// Contract with all optional fields populated
	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.Service{
				Name:    "my-service",
				Version: "1.0.0",
				Owner: contract.Owner{
					Team: "platform",
					DRI:  "alice@example.com",
					Contacts: []contract.OwnerContact{
						{Type: "email", Value: "team@example.com"},
					},
				},
			},
			Interfaces: []contract.Interface{
				{Name: "http", Type: "openapi", Ref: "openapi.yaml", Visibility: "public"},
			},
			Capabilities: []contract.Capability{
				{Type: "persistence", Ref: "db-claim"},
			},
			Configurations: []contract.Configuration{
				{
					Name:   "default",
					Schema: "config/schema.json",
					Values: map[string]any{
						"db_host":     "localhost",
						"secret_key":  "secret://vault/key",
						"port":        "5432",
						"other_value": 123,
					},
				},
				{
					Name:   "prod",
					Schema: "",
					Values: map[string]any{},
				},
			},
			Dependencies: []contract.Dependency{
				{Name: "database", Ref: "ghcr.io/org/db-pacto:1.0.0", Required: true, Compatibility: "^1.0.0"},
			},
			Policies: []contract.Policy{
				{Name: "security", Schema: "policy/security.json"},
				{Name: "performance", Ref: "ghcr.io/org/perf-policy:1.0.0"},
			},
			Metadata: map[string]any{
				"team":        "platform",
				"cost_center": "cc-123",
				"priority":    1,
			},
		},
		ResolvedRef: "ghcr.io/org/my-service-pacto:1.0.0",
		BundleFS: fstest.MapFS{
			"config/schema.json": &fstest.MapFile{
				Data: []byte(`{"title":"Config","description":"Service config","properties":{"db_host":{"type":"string","default":"localhost"},"port":{"type":"string","default":"5432"}}}`),
			},
			"policy/security.json": &fstest.MapFile{
				Data: []byte(`{"title":"Security Policy","description":"Security requirements","properties":{"tls_version":{"type":"string","default":"1.3"}}}`),
			},
		},
	}

	r := newReconciler()
	r.populateContractStatus(pacto, lr)

	// Verify contract info
	if pacto.Status.Contract.ServiceName != "my-service" {
		t.Errorf("expected serviceName=my-service, got %s", pacto.Status.Contract.ServiceName)
	}
	if pacto.Status.Contract.Version != "1.0.0" {
		t.Errorf("expected version=1.0.0, got %s", pacto.Status.Contract.Version)
	}
	if pacto.Status.Contract.ResolvedRef != "ghcr.io/org/my-service-pacto:1.0.0" {
		t.Errorf("unexpected resolvedRef: %s", pacto.Status.Contract.ResolvedRef)
	}
	if pacto.Status.Contract.Owner == nil || pacto.Status.Contract.Owner.Team != "platform" {
		t.Errorf("expected owner.team=platform, got %+v", pacto.Status.Contract.Owner)
	}
	if pacto.Status.Contract.OwnerDisplay == "" {
		t.Error("expected ownerDisplay to be set")
	}

	// Verify interfaces
	if len(pacto.Status.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(pacto.Status.Interfaces))
	}
	if pacto.Status.Interfaces[0].Name != "http" || pacto.Status.Interfaces[0].Type != "openapi" {
		t.Errorf("unexpected interface: %+v", pacto.Status.Interfaces[0])
	}

	// Verify capabilities
	if len(pacto.Status.Capabilities) != 1 {
		t.Fatalf("expected 1 capability, got %d", len(pacto.Status.Capabilities))
	}
	if pacto.Status.Capabilities[0].Type != "persistence" {
		t.Errorf("unexpected capability type: %s", pacto.Status.Capabilities[0].Type)
	}

	// Verify configurations
	if len(pacto.Status.Configurations) != 2 {
		t.Fatalf("expected 2 configurations, got %d", len(pacto.Status.Configurations))
	}
	defaultCfg := pacto.Status.Configurations[0]
	if defaultCfg.Name != "default" || !defaultCfg.HasSchema {
		t.Errorf("unexpected default config: %+v", defaultCfg)
	}
	if len(defaultCfg.ValueKeys) != 4 {
		t.Errorf("expected 4 value keys, got %d: %v", len(defaultCfg.ValueKeys), defaultCfg.ValueKeys)
	}
	if len(defaultCfg.SecretKeys) != 1 || defaultCfg.SecretKeys[0] != "secret_key" {
		t.Errorf("expected secretKeys=[secret_key], got %v", defaultCfg.SecretKeys)
	}
	if len(defaultCfg.Properties) == 0 {
		t.Error("expected properties to be populated from values")
	}

	prodCfg := pacto.Status.Configurations[1]
	if prodCfg.Name != "prod" || prodCfg.HasSchema {
		t.Errorf("unexpected prod config: %+v", prodCfg)
	}

	// Verify dependencies
	if len(pacto.Status.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(pacto.Status.Dependencies))
	}
	if pacto.Status.Dependencies[0].Name != "database" || !pacto.Status.Dependencies[0].Required {
		t.Errorf("unexpected dependency: %+v", pacto.Status.Dependencies[0])
	}

	// Verify policies
	if len(pacto.Status.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(pacto.Status.Policies))
	}
	if pacto.Status.Policies[0].Name != "security" || !pacto.Status.Policies[0].HasSchema {
		t.Errorf("unexpected security policy: %+v", pacto.Status.Policies[0])
	}
	if pacto.Status.Policies[0].Title != "Security Policy" {
		t.Errorf("expected policy title to be populated, got %s", pacto.Status.Policies[0].Title)
	}
	if len(pacto.Status.Policies[0].Properties) == 0 {
		t.Error("expected policy properties to be populated from schema")
	}

	// Verify metadata
	if len(pacto.Status.Metadata) != 3 {
		t.Fatalf("expected 3 metadata entries, got %d", len(pacto.Status.Metadata))
	}
	if pacto.Status.Metadata["team"] != "platform" {
		t.Errorf("expected metadata[team]=platform, got %s", pacto.Status.Metadata["team"])
	}
	if pacto.Status.Metadata["priority"] != "1" {
		t.Errorf("expected metadata[priority]=1 (stringified), got %s", pacto.Status.Metadata["priority"])
	}
}

func TestPopulateContractStatus_MinimalContract(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "minimal", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.Service{
				Name:    "minimal-service",
				Version: "0.1.0",
			},
		},
		ResolvedRef: "inline",
	}

	r := newReconciler()
	r.populateContractStatus(pacto, lr)

	// Verify empty slices are initialized (not nil)
	if pacto.Status.Configurations == nil {
		t.Error("expected configurations to be initialized to empty slice")
	}
	if pacto.Status.Policies == nil {
		t.Error("expected policies to be initialized to empty slice")
	}
	if pacto.Status.Capabilities == nil {
		t.Error("expected capabilities to be initialized to empty slice")
	}

	// Verify basic contract info
	if pacto.Status.Contract.ServiceName != "minimal-service" {
		t.Errorf("expected serviceName=minimal-service, got %s", pacto.Status.Contract.ServiceName)
	}
	if pacto.Status.Contract.Owner != nil {
		t.Error("expected owner to be nil for empty owner")
	}
	if pacto.Status.Contract.OwnerDisplay != "" {
		t.Error("expected ownerDisplay to be empty for empty owner")
	}
}

func TestPopulateContractStatus_ConfigurationWithSchema(t *testing.T) {
	pacto := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg-schema", Namespace: "default"},
	}

	lr := &loader.LoadResult{
		Contract: &contract.Contract{
			Service: contract.Service{Name: "svc", Version: "1.0.0"},
			Configurations: []contract.Configuration{
				{Name: "empty-values", Schema: "cfg.json", Values: map[string]any{}},
			},
		},
		ResolvedRef: "ref",
		BundleFS: fstest.MapFS{
			"cfg.json": &fstest.MapFile{
				Data: []byte(`{"properties":{"key":{"type":"string","default":"value"}}}`),
			},
		},
	}

	r := newReconciler()
	r.populateContractStatus(pacto, lr)

	if len(pacto.Status.Configurations) != 1 {
		t.Fatalf("expected 1 configuration, got %d", len(pacto.Status.Configurations))
	}
	cfg := pacto.Status.Configurations[0]
	if len(cfg.Properties) == 0 {
		t.Error("expected properties to be populated from schema when values are empty")
	}
}
