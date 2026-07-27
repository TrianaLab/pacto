package validation

import (
	"fmt"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSchemaBytes(t *testing.T) {
	data := SchemaBytes()
	if len(data) == 0 {
		t.Fatal("expected non-empty schema bytes")
	}
}

func TestCompileSchema_InvalidJSON(t *testing.T) {
	_, err := compileSchema([]byte("{invalid json!"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCompileSchema_InvalidSchema(t *testing.T) {
	_, err := compileSchema([]byte(`{"type": 12345}`))
	if err == nil {
		t.Error("expected error for invalid schema")
	}
}

func TestMustCompileSchema_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid schema data")
		}
	}()
	mustCompileSchema([]byte("bad"))
}

func TestValidateStructural_NonValidationError(t *testing.T) {
	old := schemaValidateFn
	schemaValidateFn = func(*jsonschema.Schema, any) error { return fmt.Errorf("internal error") }
	defer func() { schemaValidateFn = old }()

	result := ValidateStructural(map[string]any{"pactoVersion": "2.0"})
	if result.IsValid() {
		t.Error("expected invalid result")
	}
	if len(result.Errors) == 0 {
		t.Fatal("expected at least one error")
	}
	if result.Errors[0].Code != "SCHEMA_ERROR" {
		t.Errorf("expected SCHEMA_ERROR, got %s", result.Errors[0].Code)
	}
}

func TestCompileSchema_AddResourceError(t *testing.T) {
	old := addResourceFn
	addResourceFn = func(_ *jsonschema.Compiler, _ string, _ any) error {
		return fmt.Errorf("injected AddResource error")
	}
	defer func() { addResourceFn = old }()

	_, err := compileSchema([]byte(`{"type": "object"}`))
	if err == nil {
		t.Fatal("expected error from AddResource")
	}
	if got := err.Error(); got != "failed to add schema resource: injected AddResource error" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestYamlToGeneric_UnmarshalError(t *testing.T) {
	old := jsonUnmarshalFn
	jsonUnmarshalFn = func([]byte, any) error {
		return fmt.Errorf("injected unmarshal error")
	}
	defer func() { jsonUnmarshalFn = old }()

	_, err := yamlToGeneric([]byte(`key: value`))
	if err == nil {
		t.Fatal("expected error from json.Unmarshal")
	}
	if got := err.Error(); got != "injected unmarshal error" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestValidateStructuralRaw_Valid(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: platform
`)
	result := ValidateStructuralRaw(yaml)
	if !result.IsValid() {
		t.Errorf("expected valid result, got errors: %v", result.Errors)
	}
}

func TestValidateStructuralRaw_InvalidEnum(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: test-svc
  version: "1.0.0"
  owner:
    team: backend
state:
  type: invalid
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
	result := ValidateStructuralRaw(yaml)
	if result.IsValid() {
		t.Error("expected invalid result for bad enum value")
	}
}

func TestValidateStructuralRaw_InvalidYAML(t *testing.T) {
	result := ValidateStructuralRaw([]byte("\t\tinvalid:\n\t -broken"))
	if result.IsValid() {
		t.Error("expected invalid result for unparseable YAML")
	}
	if result.Errors[0].Code != "YAML_PARSE_ERROR" {
		t.Errorf("expected YAML_PARSE_ERROR, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructuralRaw_V20Readiness_Valid(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: payment-api
  version: "1.4.0"
  owner:
    team: payments
readiness:
  expires: "2026-12-31"
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://grafana.company.com/payment-api
      weight: 20
      description: Main production dashboard
`)
	result := ValidateStructuralRaw(yaml)
	if !result.IsValid() {
		t.Errorf("expected valid 2.0 readiness contract, got errors: %v", result.Errors)
	}
}

func TestValidateStructuralRaw_UnsupportedVersion_V10(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.0"
service:
  name: payment-api
  version: "1.4.0"
`)
	result := ValidateStructuralRaw(yaml)
	if result.IsValid() {
		t.Fatal("expected unsupported version to be invalid")
	}
	if result.Errors[0].Code != "UNSUPPORTED_PACTO_VERSION" {
		t.Errorf("expected UNSUPPORTED_PACTO_VERSION, got %s", result.Errors[0].Code)
	}
	if result.Errors[0].Path != "pactoVersion" {
		t.Errorf("expected path pactoVersion, got %s", result.Errors[0].Path)
	}
}

func TestValidateStructuralRaw_UnsupportedVersion_V12(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: payment-api
  version: "1.4.0"
`)
	result := ValidateStructuralRaw(yaml)
	if result.IsValid() {
		t.Fatal("expected unsupported version to be invalid")
	}
	if result.Errors[0].Code != "UNSUPPORTED_PACTO_VERSION" {
		t.Errorf("expected UNSUPPORTED_PACTO_VERSION, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructural_MissingVersion_Unsupported(t *testing.T) {
	result := ValidateStructural(map[string]any{"service": map[string]any{}})
	if result.IsValid() {
		t.Fatal("expected missing version to be invalid")
	}
	if result.Errors[0].Code != "UNSUPPORTED_PACTO_VERSION" {
		t.Errorf("expected UNSUPPORTED_PACTO_VERSION, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructural_NonMapData_Unsupported(t *testing.T) {
	result := ValidateStructural([]any{"not", "a", "map"})
	if result.IsValid() {
		t.Fatal("expected non-map data to be invalid")
	}
	if result.Errors[0].Code != "UNSUPPORTED_PACTO_VERSION" {
		t.Errorf("expected UNSUPPORTED_PACTO_VERSION, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructuralRaw_V20_BadReadinessWeight(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: payment-api
  version: "1.4.0"
  owner:
    team: platform
readiness:
  expires: "2026-12-31"
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 200
`)
	result := ValidateStructuralRaw(yaml)
	if result.IsValid() {
		t.Error("expected weight 200 to be rejected by schema (max 100)")
	}
}

func TestValidateStructuralRaw_V20_ReadinessMissingClaims(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness: {}
`)
	if r := ValidateStructuralRaw(yaml); !r.IsValid() {
		for _, err := range r.Errors {
			if err.Code != "SCHEMA_VIOLATION" {
				t.Errorf("expected SCHEMA_VIOLATION, got %s", err.Code)
			}
		}
	}
}

func TestValidateStructuralRaw_V20_ReadinessEmptyClaims(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness:
  expires: "2026-12-31"
  claims: []
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected empty claims list to be rejected (minItems 1)")
	}
}

func TestValidateStructuralRaw_V20_ReadinessMissingID(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness:
  expires: "2026-12-31"
  claims:
    - type: url
      status: done
      evidence: https://x
      weight: 20
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected missing id to be rejected")
	}
}

func TestValidateStructuralRaw_V20_ReadinessInvalidType(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness:
  expires: "2026-12-31"
  claims:
    - id: dashboard
      type: dashboard
      status: done
      evidence: https://x
      weight: 20
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected invalid evidence type to be rejected")
	}
}

func TestValidateStructuralRaw_V20_MinScoreValid(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness:
  minScore: 80
  expires: "2026-12-31"
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 50
`)
	if r := ValidateStructuralRaw(yaml); !r.IsValid() {
		t.Errorf("expected minScore 80 to be valid, got %v", r.Errors)
	}
}

func TestValidateStructuralRaw_V20_MinScoreOutOfRange(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness:
  minScore: 150
  expires: "2026-12-31"
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 50
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected minScore 150 to be rejected (max 100)")
	}
}

func TestValidateStructuralRaw_V20_ReadinessTypeOtherAccepted(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness:
  expires: "2026-12-31"
  claims:
    - id: incident-channel
      type: other
      status: done
      evidence: "#payments-incidents"
      weight: 10
`)
	if r := ValidateStructuralRaw(yaml); !r.IsValid() {
		t.Errorf("expected type: other to be accepted, got %v", r.Errors)
	}
}

func TestValidateStructuralRaw_V20_ReadinessUnknownClaimFieldRejected(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
service:
  name: svc
  version: "1.0.0"
  owner:
    team: platform
readiness:
  expires: "2026-12-31"
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 20
      unknownField: bad
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected unknown readiness claim field to be rejected (additionalProperties)")
	}
}

func TestConvertYAMLToJSON_NonStringKey(t *testing.T) {
	input := map[any]any{
		42:     "int-key-value",
		"name": "string-key-value",
	}

	result := convertYAMLToJSON(input)
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", result)
	}
	if m["42"] != "int-key-value" {
		t.Errorf("expected key '42' with value 'int-key-value', got %v", m["42"])
	}
	if m["name"] != "string-key-value" {
		t.Errorf("expected key 'name' with value 'string-key-value', got %v", m["name"])
	}
}

func TestStructural_V20ReadinessAccepted(t *testing.T) {
	doc := minimalV20Contract()
	doc["readiness"] = map[string]any{
		"minScore": 80, "expires": "2026-12-31", "partialCredit": 0.5,
		"history": []any{map[string]any{"date": "2026-06-21", "version": "2.1.0", "author": "ed", "description": "init"}},
		"claims":  []any{map[string]any{"id": "sec", "type": "ticket", "category": "security", "status": "done", "evidence": "SEC-1", "weight": 30}},
	}
	if res := ValidateStructural(doc); !res.IsValid() {
		t.Fatalf("v2.0 readiness should validate: %+v", res)
	}
}

func TestStructural_V20RejectsBadEnumsAndRanges(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"id": "x", "type": "url", "status": "done", "evidence": "e", "weight": 10}
	}
	cases := []func(map[string]any){
		func(c map[string]any) { c["status"] = "wip" },
		func(c map[string]any) { c["category"] = "bogus" },
		func(c map[string]any) { c["weight"] = 101 },
	}
	for i, mutate := range cases {
		doc := minimalV20Contract()
		c := base()
		mutate(c)
		doc["readiness"] = map[string]any{"expires": "2026-12-31", "claims": []any{c}}
		res := ValidateStructural(doc)
		if res.IsValid() {
			t.Fatalf("case %d should be rejected", i)
		}
	}
}

func TestStructural_V20InterfaceTypes(t *testing.T) {
	cases := []struct {
		typ   string
		valid bool
	}{
		{"openapi", true},
		{"asyncapi", true},
		{"grpc", true},
		{"http", false},
		{"event", false},
	}
	for _, tc := range cases {
		doc := minimalV20Contract()
		doc["interfaces"] = []any{map[string]any{"name": "api", "type": tc.typ, "ref": "interfaces/spec.yaml"}}
		res := ValidateStructural(doc)
		if tc.valid && !res.IsValid() {
			t.Errorf("type %q should be valid, got errors: %v", tc.typ, res.Errors)
		}
		if !tc.valid && res.IsValid() {
			t.Errorf("type %q should be invalid", tc.typ)
		}
	}
}

func TestStructural_V20CapabilityTypes(t *testing.T) {
	cases := []struct {
		typ   string
		ref   string
		valid bool
	}{
		{"health", "", true},
		{"metrics", "", true},
		{"extension", "example.com/custom", true},
		{"monitoring", "", false},
	}
	for _, tc := range cases {
		doc := minimalV20Contract()
		cap := map[string]any{"type": tc.typ}
		if tc.ref != "" {
			cap["ref"] = tc.ref
		}
		doc["capabilities"] = []any{cap}
		res := ValidateStructural(doc)
		if tc.valid && !res.IsValid() {
			t.Errorf("capability type %q should be valid, got errors: %v", tc.typ, res.Errors)
		}
		if !tc.valid && res.IsValid() {
			t.Errorf("capability type %q should be invalid", tc.typ)
		}
	}
}

func minimalV20Contract() map[string]any {
	return map[string]any{
		"pactoVersion": "2.0",
		"service": map[string]any{
			"name":    "test-svc",
			"version": "1.0.0",
			"owner":   map[string]any{"team": "platform"},
		},
	}
}
