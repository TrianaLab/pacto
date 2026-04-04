package contract_test

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestParse_LegacyConfiguration_Singular(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
configuration:
  schema: configuration/schema.json
  values:
    maxRetries: 3
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("expected legacy configuration to parse, got: %v", err)
	}
	if len(c.Configurations) != 1 {
		t.Fatalf("expected 1 configuration, got %d", len(c.Configurations))
	}
	cfg := c.Configurations[0]
	if cfg.Name != "default" {
		t.Errorf("expected name 'default', got %q", cfg.Name)
	}
	if cfg.Schema != "configuration/schema.json" {
		t.Errorf("expected schema path, got %q", cfg.Schema)
	}
	if cfg.Values["maxRetries"] != 3 {
		t.Errorf("expected maxRetries=3, got %v", cfg.Values["maxRetries"])
	}
}

func TestParse_LegacyConfiguration_WithConfigs(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
configuration:
  configs:
    - name: app
      schema: configuration/app.json
    - name: infra
      ref: ghcr.io/org/infra-config:1.0.0
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("expected legacy configs to parse, got: %v", err)
	}
	if len(c.Configurations) != 2 {
		t.Fatalf("expected 2 configurations, got %d", len(c.Configurations))
	}
	if c.Configurations[0].Name != "app" {
		t.Errorf("expected name 'app', got %q", c.Configurations[0].Name)
	}
	if c.Configurations[1].Name != "infra" {
		t.Errorf("expected name 'infra', got %q", c.Configurations[1].Name)
	}
}

func TestParse_LegacyDependencies_WithoutName(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
dependencies:
  - ref: ghcr.io/org/repo/auth-service:1.0.0
    compatibility: exact
  - ref: ghcr.io/org/repo/payments:2.0.0
    required: true
    compatibility: compatible
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("expected legacy dependencies to parse, got: %v", err)
	}
	if len(c.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(c.Dependencies))
	}
	if c.Dependencies[0].Name != "auth-service" {
		t.Errorf("expected name derived from ref 'auth-service', got %q", c.Dependencies[0].Name)
	}
	if c.Dependencies[1].Name != "payments" {
		t.Errorf("expected name derived from ref 'payments', got %q", c.Dependencies[1].Name)
	}
}

func TestParse_LegacyPolicies_WithoutName(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
policies:
  - schema: policy/schema.json
  - ref: ghcr.io/org/governance:1.0.0
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("expected legacy policies to parse, got: %v", err)
	}
	if len(c.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(c.Policies))
	}
	if c.Policies[0].Name != "default" {
		t.Errorf("expected name 'default', got %q", c.Policies[0].Name)
	}
	if c.Policies[1].Name != "default" {
		t.Errorf("expected name 'default', got %q", c.Policies[1].Name)
	}
}

func TestParse_NewFormat_NotAffected(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
configurations:
  - name: app
    schema: configuration/schema.json
dependencies:
  - name: auth
    ref: ghcr.io/org/auth:1.0.0
    compatibility: exact
policies:
  - name: governance
    schema: policy/schema.json
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("new format should parse without issues: %v", err)
	}
	if c.Configurations[0].Name != "app" {
		t.Errorf("expected name 'app', got %q", c.Configurations[0].Name)
	}
	if c.Dependencies[0].Name != "auth" {
		t.Errorf("expected name 'auth', got %q", c.Dependencies[0].Name)
	}
	if c.Policies[0].Name != "governance" {
		t.Errorf("expected name 'governance', got %q", c.Policies[0].Name)
	}
}

func TestParse_LegacyDependency_NameNotOverwritten(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
dependencies:
  - name: custom-name
    ref: ghcr.io/org/service:1.0.0
    compatibility: exact
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dependencies[0].Name != "custom-name" {
		t.Errorf("expected existing name to be preserved, got %q", c.Dependencies[0].Name)
	}
}

func TestParse_LegacyConfiguration_Ref(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
configuration:
  ref: ghcr.io/org/shared-config:1.0.0
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("expected legacy config ref to parse, got: %v", err)
	}
	if len(c.Configurations) != 1 {
		t.Fatalf("expected 1 configuration, got %d", len(c.Configurations))
	}
	if c.Configurations[0].Ref != "ghcr.io/org/shared-config:1.0.0" {
		t.Errorf("expected ref, got %q", c.Configurations[0].Ref)
	}
}

func TestParse_LegacyDependency_DigestRef(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
dependencies:
  - ref: ghcr.io/org/repo/svc@sha256:abc123
    compatibility: exact
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dependencies[0].Name != "svc" {
		t.Errorf("expected name 'svc' derived from digest ref, got %q", c.Dependencies[0].Name)
	}
}

func TestParse_LegacyDependency_BareRef(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
dependencies:
  - ref: my-service
    compatibility: exact
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dependencies[0].Name != "my-service" {
		t.Errorf("expected name 'my-service', got %q", c.Dependencies[0].Name)
	}
}

func TestParse_LegacyDependency_EmptyRef(t *testing.T) {
	r := strings.NewReader(`
pactoVersion: "1.0"
service:
  name: my-svc
  version: "1.0.0"
dependencies:
  - ref: ""
    compatibility: exact
`)
	c, err := contract.Parse(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Dependencies[0].Name != "default" {
		t.Errorf("expected name 'default' for empty ref, got %q", c.Dependencies[0].Name)
	}
}
