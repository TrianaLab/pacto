package doc

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/pkg/contract"
)

func intPtr(v int) *int { return &v }

func fullContract() *contract.Contract {
	return &contract.Contract{
		PactoVersion: "1.0",
		Service: contract.ServiceIdentity{
			Name:    "payments-api",
			Version: "2.1.0",
			Owner:   "team/payments",
			Image:   &contract.Image{Ref: "ghcr.io/acme/payments-api:2.1.0", Private: true},
		},
		Interfaces: []contract.Interface{
			{
				Name:       "rest-api",
				Type:       "http",
				Port:       intPtr(8080),
				Visibility: "public",
				Contract:   "interfaces/openapi.yaml",
			},
			{
				Name:       "grpc-api",
				Type:       "grpc",
				Port:       intPtr(9090),
				Visibility: "internal",
				Contract:   "interfaces/service.proto",
			},
			{
				Name:       "order-events",
				Type:       "event",
				Visibility: "internal",
				Contract:   "interfaces/events.yaml",
			},
		},
		Configuration: &contract.Configuration{
			Schema: "configuration/schema.json",
		},
		Dependencies: []contract.Dependency{
			{
				Ref:           "ghcr.io/acme/auth-service-pacto@sha256:abc123",
				Required:      true,
				Compatibility: "^2.0.0",
			},
			{
				Ref:           "ghcr.io/acme/notification-service-pacto:1.0.0",
				Required:      false,
				Compatibility: "~1.0.0",
			},
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:            "stateful",
				DataCriticality: "high",
				Persistence: contract.Persistence{
					Scope:      "shared",
					Durability: "persistent",
				},
			},
			Lifecycle: &contract.Lifecycle{
				UpgradeStrategy:         "rolling",
				GracefulShutdownSeconds: intPtr(30),
			},
			Health: contract.Health{
				Interface:           "rest-api",
				Path:                "/health",
				InitialDelaySeconds: intPtr(15),
			},
		},
		Scaling: &contract.Scaling{Min: 2, Max: 10},
		Metadata: map[string]interface{}{
			"team": "payments",
			"tier": "critical",
		},
	}
}

func fullFS() fstest.MapFS {
	return fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`
openapi: "3.0.0"
paths:
  /health:
    get:
      summary: Health check
  /payments:
    post:
      summary: Create a payment
`)},
		"interfaces/events.yaml": &fstest.MapFile{Data: []byte(`
description: Order placement events
`)},
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{
  "type": "object",
  "properties": {
    "PORT": {
      "type": "integer",
      "description": "HTTP server port",
      "default": 8080
    },
    "REDIS_URL": {
      "type": "string",
      "description": "Redis connection string"
    }
  },
  "required": ["PORT", "REDIS_URL"]
}`)},
	}
}

func TestGenerate_Full(t *testing.T) {
	c := fullContract()
	fsys := fullFS()

	md, err := Generate(c, fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Service heading
	if !strings.Contains(md, "# payments-api") {
		t.Error("expected service heading")
	}

	// Description paragraph with version, state, interfaces, dependencies
	if !strings.Contains(md, "**payments-api** `v2.1.0` is a `stateful` `service` workload exposing 3 interfaces with 2 dependencies.") {
		t.Error("expected description paragraph with version and summary")
	}
	if !strings.Contains(md, "Owned by `team/payments`") {
		t.Error("expected owner in description")
	}
	if !strings.Contains(md, "scales from `2` to `10` replicas") {
		t.Error("expected scaling in description")
	}

	// Concept explanations table from schema
	if !strings.Contains(md, "| Concept | Value | Description |") {
		t.Error("expected concept explanations table header")
	}
	if !strings.Contains(md, "| **Workload** | `service` |") {
		t.Error("expected workload row in concepts table")
	}
	if !strings.Contains(md, "| **State** | `stateful` |") {
		t.Error("expected state row in concepts table")
	}
	if !strings.Contains(md, "Retains data between requests") {
		t.Error("expected stateful explanation text")
	}
	if !strings.Contains(md, "| **Persistence scope** | `shared` |") {
		t.Error("expected persistence scope row in concepts table")
	}
	if !strings.Contains(md, "| **Persistence durability** | `persistent` |") {
		t.Error("expected persistence durability row in concepts table")
	}
	if !strings.Contains(md, "must survive restarts") {
		t.Error("expected persistent explanation text")
	}
	if !strings.Contains(md, "| **Data criticality** | `high` |") {
		t.Error("expected data criticality row in concepts table")
	}
	if !strings.Contains(md, "severe business impact") {
		t.Error("expected high criticality explanation text")
	}

	if !strings.Contains(md, "| **Upgrade strategy** | `rolling` |") {
		t.Error("expected upgrade strategy row in concepts table")
	}

	// Contract reference link
	if !strings.Contains(md, "[Contract Reference](https://trianalab.github.io/pacto/contract-reference/)") {
		t.Error("expected contract reference link after concepts table")
	}

	// Table of contents — no Overview link (removed)
	if !strings.Contains(md, "## Table of Contents") {
		t.Error("expected table of contents heading")
	}
	if strings.Contains(md, "- [Overview]") {
		t.Error("should not contain Overview link in TOC")
	}
	if !strings.Contains(md, "- [Architecture](#architecture)") {
		t.Error("expected TOC link to Architecture")
	}
	if !strings.Contains(md, "- [Interfaces](#interfaces)") {
		t.Error("expected TOC link to Interfaces")
	}
	if !strings.Contains(md, "- [Configuration](#configuration)") {
		t.Error("expected TOC link to Configuration")
	}
	if !strings.Contains(md, "- [Dependencies](#dependencies)") {
		t.Error("expected TOC link to Dependencies")
	}
	if !strings.Contains(md, "  - [HTTP Interface: rest-api](#http-interface-rest-api)") {
		t.Error("expected TOC sub-link to HTTP Interface")
	}
	if !strings.Contains(md, "  - [gRPC Interface: grpc-api](#grpc-interface-grpc-api)") {
		t.Error("expected TOC sub-link to gRPC Interface")
	}
	if !strings.Contains(md, "  - [Event Interface: order-events](#event-interface-order-events)") {
		t.Error("expected TOC sub-link to Event Interface")
	}

	// No overview table
	if strings.Contains(md, "## Overview") {
		t.Error("should not contain overview section (replaced by description)")
	}

	// Mermaid architecture diagram
	if !strings.Contains(md, "## Architecture") {
		t.Error("expected architecture section")
	}
	if !strings.Contains(md, "```mermaid") {
		t.Error("expected mermaid fenced block")
	}
	if !strings.Contains(md, "subgraph") {
		t.Error("expected subgraph in mermaid diagram")
	}
	if !strings.Contains(md, "payments-api v2.1.0") {
		t.Error("expected service name+version in subgraph")
	}
	// State as cylinder with persistence and replicas info
	if !strings.Contains(md, `state[("stateful`) {
		t.Error("expected cylinder shape for state in mermaid diagram")
	}
	if !strings.Contains(md, "\u00b7 shared persistent") {
		t.Error("expected persistence scope in mermaid state cylinder")
	}
	if !strings.Contains(md, "\u00b7 2\u201310 replicas") {
		t.Error("expected replica range in state cylinder")
	}
	// External user node for public interfaces
	if !strings.Contains(md, `external(["External User"])`) {
		t.Error("expected external user node in mermaid")
	}
	if !strings.Contains(md, "external --> iface_restapi") {
		t.Error("expected external user arrow to public interface")
	}
	// Interface nodes with <br/> line breaks
	if !strings.Contains(md, "iface_restapi") {
		t.Error("expected rest-api interface node in mermaid")
	}
	if !strings.Contains(md, "<br/>") {
		t.Error("expected <br/> line breaks in mermaid interface nodes")
	}
	if !strings.Contains(md, "iface_grpcapi") {
		t.Error("expected grpc-api interface node in mermaid")
	}
	if !strings.Contains(md, "iface_orderevents") {
		t.Error("expected order-events interface node in mermaid")
	}
	// Dependency edges
	if !strings.Contains(md, `-->|"required`) {
		t.Error("expected solid arrow for required dependency")
	}
	if !strings.Contains(md, `-.->|"optional`) {
		t.Error("expected dashed arrow for optional dependency")
	}
	if !strings.Contains(md, `"auth-service-pacto"`) {
		t.Error("expected auth-service-pacto dep name in mermaid")
	}
	if !strings.Contains(md, `"notification-service-pacto"`) {
		t.Error("expected notification-service-pacto dep name in mermaid")
	}

	// Interfaces table
	if !strings.Contains(md, "## Interfaces") {
		t.Error("expected interfaces section")
	}
	if !strings.Contains(md, "| `rest-api` | `http` | `8080` | `public` |") {
		t.Error("expected rest-api in interfaces table")
	}

	// Configuration table
	if !strings.Contains(md, "## Configuration") {
		t.Error("expected configuration section")
	}
	if !strings.Contains(md, "| `PORT` | `integer` | HTTP server port | `8080` | Yes |") {
		t.Error("expected PORT property in configuration")
	}

	// HTTP interface detail subsection
	if !strings.Contains(md, "### HTTP Interface: rest-api") {
		t.Error("expected HTTP interface subsection")
	}
	if !strings.Contains(md, "#### Endpoints") {
		t.Error("expected endpoints heading")
	}
	if !strings.Contains(md, "| `GET` | `/health` | Health check |") {
		t.Error("expected GET /health endpoint")
	}
	if !strings.Contains(md, "| `POST` | `/payments` | Create a payment |") {
		t.Error("expected POST /payments endpoint")
	}

	// gRPC interface detail subsection
	if !strings.Contains(md, "### gRPC Interface: grpc-api") {
		t.Error("expected gRPC interface subsection")
	}
	if !strings.Contains(md, "Its contract is defined in `interfaces/service.proto`") {
		t.Error("expected gRPC contract reference")
	}

	// Event interface detail subsection
	if !strings.Contains(md, "### Event Interface: order-events") {
		t.Error("expected Event interface subsection")
	}

	// Dependencies
	if !strings.Contains(md, "## Dependencies") {
		t.Error("expected dependencies section")
	}
	if !strings.Contains(md, "| `ghcr.io/acme/auth-service-pacto@sha256:abc123` | `^2.0.0` | Yes |") {
		t.Error("expected auth dependency")
	}
	if !strings.Contains(md, "| `ghcr.io/acme/notification-service-pacto:1.0.0` | `~1.0.0` | No |") {
		t.Error("expected notification dependency")
	}

	// Image
	if !strings.Contains(md, "packaged as `ghcr.io/acme/payments-api:2.1.0`") {
		t.Error("expected image ref in description")
	}
	if !strings.Contains(md, "## Container Image") {
		t.Error("expected container image section")
	}
	if !strings.Contains(md, "**Ref:** `ghcr.io/acme/payments-api:2.1.0`") {
		t.Error("expected image ref")
	}
	if !strings.Contains(md, "**Private:** Yes") {
		t.Error("expected private flag")
	}
	if !strings.Contains(md, "- [Container Image](#container-image)") {
		t.Error("expected container image in TOC")
	}

	// Health check in interface section (verbal format)
	if !strings.Contains(md, "owns the health path under `/health`") {
		t.Error("expected health path in interface section")
	}
	if !strings.Contains(md, "requires an initial delay of `15s`") {
		t.Error("expected initial delay in interface section")
	}

	// Verbal interface descriptions
	if !strings.Contains(md, "The `rest-api` interface is `public` and exposes port `8080`.") {
		t.Error("expected verbal description for rest-api")
	}
	if !strings.Contains(md, "The `grpc-api` interface is `internal` and exposes port `9090`.") {
		t.Error("expected verbal description for grpc-api")
	}
	if !strings.Contains(md, "The `order-events` interface is `internal`.") {
		t.Error("expected verbal description for order-events")
	}

	// Health label in mermaid diagram
	if !strings.Contains(md, "<br/>♥ health") {
		t.Error("expected health label in mermaid diagram")
	}

	// Graceful shutdown in concepts table
	if !strings.Contains(md, "| **Graceful shutdown** | `30s` |") {
		t.Error("expected graceful shutdown in concepts table")
	}

	// Metadata tags in footer (no heading)
	if strings.Contains(md, "## Metadata") {
		t.Error("metadata should not have a heading")
	}
	if !strings.Contains(md, "`team: payments`") {
		t.Error("expected team metadata tag in footer")
	}
	if !strings.Contains(md, "`tier: critical`") {
		t.Error("expected tier metadata tag in footer")
	}

	// Footer
	if !strings.Contains(md, "Generated by [Pacto](https://trianalab.github.io/pacto)") {
		t.Error("expected Pacto footer")
	}
}

func TestGenerate_Minimal(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service: contract.ServiceIdentity{
			Name:    "simple-svc",
			Version: "1.0.0",
		},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080)},
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State: contract.State{
				Type:            "stateless",
				DataCriticality: "low",
			},
		},
	}

	md, err := Generate(c, fstest.MapFS{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(md, "# simple-svc") {
		t.Error("expected service heading")
	}

	// Description paragraph with version
	if !strings.Contains(md, "**simple-svc** `v1.0.0` is a `stateless` `service` workload exposing 1 interface.") {
		t.Error("expected description paragraph")
	}

	// Concept explanations table
	if !strings.Contains(md, "| **Workload** | `service` |") {
		t.Error("expected workload row in concepts table")
	}
	if !strings.Contains(md, "| **State** | `stateless` |") {
		t.Error("expected state row in concepts table")
	}
	if !strings.Contains(md, "Does not retain data") {
		t.Error("expected stateless explanation text")
	}

	// No persistence rows when scope/durability are empty
	if strings.Contains(md, "| **Persistence durability**") {
		t.Error("should not contain persistence durability row when durability is empty")
	}
	if strings.Contains(md, "| **Persistence scope**") {
		t.Error("should not contain persistence scope row when scope is empty")
	}

	// TOC — no Dependencies, no Configuration, no Overview
	if !strings.Contains(md, "## Table of Contents") {
		t.Error("expected table of contents")
	}
	if strings.Contains(md, "- [Overview]") {
		t.Error("should not contain Overview link in TOC")
	}
	if strings.Contains(md, "- [Dependencies]") {
		t.Error("should not contain Dependencies link in TOC for minimal contract")
	}
	if strings.Contains(md, "- [Configuration]") {
		t.Error("should not contain Configuration link in TOC for minimal contract")
	}

	// Mermaid exists but no dependency edges, scaling, or external user
	if !strings.Contains(md, "```mermaid") {
		t.Error("expected mermaid block")
	}
	if strings.Contains(md, "-->|") || strings.Contains(md, "-.->|") {
		t.Error("should not contain dependency edges in mermaid for minimal contract")
	}
	if strings.Contains(md, "replicas") {
		t.Error("should not contain scaling in mermaid for minimal contract")
	}
	if strings.Contains(md, "External User") {
		t.Error("should not contain external user node when no public interfaces")
	}

	// No optional sections
	if strings.Contains(md, "## Configuration") {
		t.Error("should not contain Configuration for minimal contract")
	}
	if strings.Contains(md, "## Dependencies") {
		t.Error("should not contain Dependencies for minimal contract")
	}
}

func TestGenerate_MissingSpecFiles(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service: contract.ServiceIdentity{
			Name:    "svc",
			Version: "1.0.0",
		},
		Interfaces: []contract.Interface{
			{
				Name:     "api",
				Type:     "http",
				Port:     intPtr(8080),
				Contract: "interfaces/openapi.yaml",
			},
		},
		Configuration: &contract.Configuration{
			Schema: "configuration/schema.json",
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
		},
	}

	// Empty FS — spec files don't exist.
	md, err := Generate(c, fstest.MapFS{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should produce inline notes, not fatal errors.
	if !strings.Contains(md, "_Could not read") {
		t.Error("expected inline error note for missing spec files")
	}
}

func TestGenerate_NoInterfaces(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
		},
	}

	md, err := Generate(c, fstest.MapFS{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(md, "## Interfaces") {
		t.Error("should not contain Interfaces section when there are none")
	}
}

func TestGenerate_InterfaceWithoutPort(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "events", Type: "event", Visibility: "internal"},
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
		},
	}

	md, err := Generate(c, fstest.MapFS{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Port should show as em-dash in interfaces table
	if !strings.Contains(md, "| `events` | `event` | \u2014 | `internal` |") {
		t.Errorf("expected em-dash for missing port, got:\n%s", md)
	}
}

func TestLoadSchemaDescriptions_InvalidJSON(t *testing.T) {
	dst := loadSchemaDescriptions([]byte("{invalid"))
	if len(dst) != 0 {
		t.Errorf("expected empty map for invalid JSON, got %v", dst)
	}
}

func TestCapitalizeFirst(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "Hello"},
		{"", ""},
		{"Hello", "Hello"},
	}
	for _, tt := range tests {
		got := capitalizeFirst(tt.input)
		if got != tt.want {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestInterfaceHeading_UnknownType(t *testing.T) {
	iface := contract.Interface{Name: "custom", Type: "websocket"}
	heading := interfaceHeading(iface)
	expected := "Websocket Interface: custom"
	if heading != expected {
		t.Errorf("expected %q, got %q", expected, heading)
	}
}

func TestGenerate_LifecycleWithEmptyUpgradeStrategy(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: intPtr(8080)}},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
			Lifecycle: &contract.Lifecycle{
				GracefulShutdownSeconds: intPtr(30),
			},
		},
	}

	md, err := Generate(c, fstest.MapFS{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not contain upgrade strategy row when empty
	if strings.Contains(md, "**Upgrade strategy**") {
		t.Error("should not contain upgrade strategy row when empty")
	}
}

func TestGenerate_HTTPInterfaceWithoutContract(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080), Visibility: "public"},
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
			Health:   contract.Health{Interface: "api"},
		},
	}

	md, err := Generate(c, fstest.MapFS{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have the interface heading but no endpoints section
	if !strings.Contains(md, "### HTTP Interface: api") {
		t.Error("expected HTTP interface subsection")
	}
	if strings.Contains(md, "#### Endpoints") {
		t.Error("should not contain endpoints section when no contract")
	}
}

func TestGenerate_HTTPInterfaceWithEmptySpec(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080), Contract: "interfaces/openapi.yaml"},
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
		},
	}

	// Spec exists but has no paths
	fsys := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: Empty API
`)},
	}

	md, err := Generate(c, fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(md, "### HTTP Interface: api") {
		t.Error("expected HTTP interface subsection")
	}
	if strings.Contains(md, "#### Endpoints") {
		t.Error("should not contain endpoints section for empty spec")
	}
}

func TestExtractEnumDescriptions_NonObjectValue(t *testing.T) {
	props := map[string]interface{}{
		"name":    "not an object",
		"version": 42,
	}
	dst := make(map[string]string)
	extractEnumDescriptions(props, "", dst)
	if len(dst) != 0 {
		t.Errorf("expected empty map for non-object values, got %v", dst)
	}
}

func TestGenerate_ConfigurationSchemaError(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: intPtr(8080)}},
		Configuration: &contract.Configuration{
			Schema: "configuration/schema.json",
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
		},
	}

	// FS with an empty schema that has properties: {}
	fsys := fstest.MapFS{
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{"type":"object","properties":{}}`)},
	}

	md, err := Generate(c, fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty properties should not produce a Configuration section
	if strings.Contains(md, "## Configuration") {
		t.Error("should not contain Configuration section when properties are empty")
	}
}

func TestGenerate_ConfigPropertyWithoutDescription(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "api", Type: "http", Port: intPtr(8080)}},
		Configuration: &contract.Configuration{
			Schema: "configuration/schema.json",
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
		},
	}

	fsys := fstest.MapFS{
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{
  "type": "object",
  "properties": {
    "DEBUG": {"type": "boolean"}
  }
}`)},
	}

	md, err := Generate(c, fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(md, "## Configuration") {
		t.Error("expected configuration section")
	}
	// Property without description should show em-dash
	if !strings.Contains(md, "| `DEBUG` | `boolean` | \u2014 |") {
		t.Errorf("expected DEBUG with em-dash description, got:\n%s", md)
	}
}

func TestGenerate_EndpointWithoutSummary(t *testing.T) {
	c := &contract.Contract{
		PactoVersion: "1.0",
		Service:      contract.ServiceIdentity{Name: "svc", Version: "1.0.0"},
		Interfaces: []contract.Interface{
			{Name: "api", Type: "http", Port: intPtr(8080), Contract: "interfaces/openapi.yaml"},
		},
		Runtime: contract.Runtime{
			Workload: "service",
			State:    contract.State{Type: "stateless", DataCriticality: "low"},
		},
	}

	fsys := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`
openapi: "3.0.0"
paths:
  /items:
    get: {}
`)},
	}

	md, err := Generate(c, fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Endpoint without summary should show em-dash
	if !strings.Contains(md, "| `GET` | `/items` | \u2014 |") {
		t.Errorf("expected em-dash for missing summary, got:\n%s", md)
	}
}

func TestDepName(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"ghcr.io/acme/auth-service-pacto@sha256:abc123", "auth-service-pacto"},
		{"ghcr.io/acme/notification-service-pacto:1.0.0", "notification-service-pacto"},
		{"simple-ref", "simple-ref"},
	}
	for _, tt := range tests {
		got := depName(tt.ref)
		if got != tt.want {
			t.Errorf("depName(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestSanitizeMermaidID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"rest-api", "restapi"},
		{"ghcr.io/acme/svc@sha256:abc", "ghcrioacmesvcsha256abc"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		got := sanitizeMermaidID(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeMermaidID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
