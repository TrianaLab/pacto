package validation_test

import (
	"testing"

	"github.com/trianalab/pacto/v2/pkg/validation"
)

const schemaBase = `pactoVersion: "2.0"
service:
  name: orders
  version: "1.0.0"
interfaces:
  - name: public-api
    type: openapi
    ref: interfaces/openapi.yaml
`

func TestSchema_CapabilityBinding_Valid(t *testing.T) {
	data := []byte(schemaBase + `capabilities:
  - type: health
    binding:
      type: http
      interface: public-api
      path: /healthz
  - type: extension
    ref: example.com/custom
verification:
  conformance:
    - public-api
`)
	r := validation.ValidateStructuralRaw(data)
	if hasErrorCode(r, "SCHEMA_VIOLATION") {
		t.Errorf("valid discriminated binding + verification must pass schema, got %+v", r.Errors)
	}
}

func TestSchema_ExtensionWithBinding_Rejected(t *testing.T) {
	data := []byte(schemaBase + `capabilities:
  - type: extension
    ref: example.com/custom
    binding:
      type: http
      interface: public-api
`)
	if r := validation.ValidateStructuralRaw(data); !hasErrorCode(r, "SCHEMA_VIOLATION") {
		t.Errorf("extension+binding must be rejected, got %+v", r.Errors)
	}
}

func TestSchema_HealthWithRef_Rejected(t *testing.T) {
	data := []byte(schemaBase + `capabilities:
  - type: health
    ref: example.com/custom
`)
	if r := validation.ValidateStructuralRaw(data); !hasErrorCode(r, "SCHEMA_VIOLATION") {
		t.Errorf("health+ref must be rejected, got %+v", r.Errors)
	}
}

func TestSchema_BindingBadTransport_Rejected(t *testing.T) {
	data := []byte(schemaBase + `capabilities:
  - type: health
    binding:
      type: grpc
      interface: public-api
`)
	if r := validation.ValidateStructuralRaw(data); !hasErrorCode(r, "SCHEMA_VIOLATION") {
		t.Errorf("binding transport grpc must be rejected this release, got %+v", r.Errors)
	}
}

func TestSchema_ConfigurationMissingRequired_Rejected(t *testing.T) {
	data := []byte(schemaBase + `configurations:
  - name: app
    schema: configuration/app.schema.json
`)
	if r := validation.ValidateStructuralRaw(data); !hasErrorCode(r, "SCHEMA_VIOLATION") {
		t.Errorf("configuration without required must be rejected, got %+v", r.Errors)
	}
}

func TestSchema_DependencyMissingRequired_Rejected(t *testing.T) {
	data := []byte(schemaBase + `dependencies:
  - name: db
    ref: oci://ghcr.io/acme/db@sha256:abc
    compatibility: ^1.0.0
`)
	if r := validation.ValidateStructuralRaw(data); !hasErrorCode(r, "SCHEMA_VIOLATION") {
		t.Errorf("dependency without required must be rejected, got %+v", r.Errors)
	}
}
