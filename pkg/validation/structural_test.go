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
	schemaValidateFn = func(*jsonschema.Schema, interface{}) error { return fmt.Errorf("internal error") }
	defer func() { schemaValidateFn = old }()

	// Use a recognized pactoVersion so selection succeeds and the injected
	// schema-validate error is reached.
	result := ValidateStructural(map[string]interface{}{"pactoVersion": "1.0"})
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
	jsonUnmarshalFn = func([]byte, interface{}) error {
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
	yaml := []byte("pactoVersion: \"1.0\"\nservice:\n  name: test-svc\n  version: \"1.0.0\"\n")
	result := ValidateStructuralRaw(yaml)
	if !result.IsValid() {
		t.Errorf("expected valid result, got errors: %v", result.Errors)
	}
}

func TestValidateStructuralRaw_InvalidEnum(t *testing.T) {
	// runtime.state.type has an enum constraint; "invalid" should fail.
	yaml := []byte(`pactoVersion: "1.0"
service:
  name: test-svc
  version: "1.0.0"
runtime:
  workload: service
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

func TestValidateStructuralRaw_V12Readiness_Valid(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: payment-api
  version: "1.4.0"
readiness:
  expires: "2026-12-31"
  checks:
    - id: dashboard
      type: url
      status: done
      evidence: https://grafana.company.com/payment-api
      weight: 20
      description: Main production dashboard
`)
	result := ValidateStructuralRaw(yaml)
	if !result.IsValid() {
		t.Errorf("expected valid 1.2 readiness contract, got errors: %v", result.Errors)
	}
}

func TestValidateStructuralRaw_V11WithoutReadiness_Valid(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.1"
service:
  name: payment-api
  version: "1.4.0"
`)
	result := ValidateStructuralRaw(yaml)
	if !result.IsValid() {
		t.Errorf("expected valid 1.1 contract without readiness, got errors: %v", result.Errors)
	}
}

func TestValidateStructuralRaw_V10WithReadiness_Rejected(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.0"
service:
  name: payment-api
  version: "1.4.0"
readiness:
  checks:
    - id: dashboard
      type: url
      evidence: https://x
      weight: 20
      expires: "2026-12-31"
`)
	result := ValidateStructuralRaw(yaml)
	if result.IsValid() {
		t.Error("expected readiness to be rejected under pactoVersion 1.0")
	}
}

func TestValidateStructuralRaw_UnsupportedVersion(t *testing.T) {
	yaml := []byte(`pactoVersion: "2.0"
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

func TestValidateStructural_MissingVersion_Unsupported(t *testing.T) {
	// A generic doc with no pactoVersion at all.
	result := ValidateStructural(map[string]interface{}{"service": map[string]interface{}{}})
	if result.IsValid() {
		t.Fatal("expected missing version to be invalid")
	}
	if result.Errors[0].Code != "UNSUPPORTED_PACTO_VERSION" {
		t.Errorf("expected UNSUPPORTED_PACTO_VERSION, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructural_NonMapData_Unsupported(t *testing.T) {
	result := ValidateStructural([]interface{}{"not", "a", "map"})
	if result.IsValid() {
		t.Fatal("expected non-map data to be invalid")
	}
	if result.Errors[0].Code != "UNSUPPORTED_PACTO_VERSION" {
		t.Errorf("expected UNSUPPORTED_PACTO_VERSION, got %s", result.Errors[0].Code)
	}
}

func TestValidateStructuralRaw_V12_BadReadinessWeight(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: payment-api
  version: "1.4.0"
readiness:
  expires: "2026-12-31"
  checks:
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

func TestValidateStructuralRaw_V12_ReadinessMissingChecks(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness: {}
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected readiness without checks to be rejected")
	}
}

func TestValidateStructuralRaw_V12_ReadinessEmptyChecks(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness:
  expires: "2026-12-31"
  checks: []
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected empty checks list to be rejected (minItems 1)")
	}
}

func TestValidateStructuralRaw_V12_ReadinessMissingID(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness:
  expires: "2026-12-31"
  checks:
    - type: url
      status: done
      evidence: https://x
      weight: 20
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected missing id to be rejected")
	}
}

func TestValidateStructuralRaw_V12_ReadinessInvalidType(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness:
  expires: "2026-12-31"
  checks:
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

func TestValidateStructuralRaw_V12_MinScoreValid(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness:
  minScore: 80
  expires: "2026-12-31"
  checks:
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

func TestValidateStructuralRaw_V12_MinScoreOutOfRange(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness:
  minScore: 150
  expires: "2026-12-31"
  checks:
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

func TestValidateStructuralRaw_V12_ReadinessTypeOtherAccepted(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness:
  expires: "2026-12-31"
  checks:
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

func TestValidateStructuralRaw_V12_ReadinessUnknownCheckFieldRejected(t *testing.T) {
	yaml := []byte(`pactoVersion: "1.2"
service:
  name: svc
  version: "1.0.0"
readiness:
  expires: "2026-12-31"
  checks:
    - id: dashboard
      type: url
      status: done
      evidence: https://x
      weight: 20
      unknownField: bad
`)
	if r := ValidateStructuralRaw(yaml); r.IsValid() {
		t.Error("expected unknown readiness check field to be rejected (additionalProperties)")
	}
}

func TestConvertYAMLToJSON_NonStringKey(t *testing.T) {
	// Simulate a map[interface{}]interface{} with a non-string key,
	// which can occur in YAML when keys are integers or booleans.
	input := map[interface{}]interface{}{
		42:     "int-key-value",
		"name": "string-key-value",
	}

	result := convertYAMLToJSON(input)
	m, ok := result.(map[string]interface{})
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

func TestStructural_StringOwnerRejected_AllVersions(t *testing.T) {
	for _, v := range []string{"1.0", "1.1", "1.2"} {
		doc := minimalContract(v)
		doc["service"].(map[string]any)["owner"] = "team/x"
		res := ValidateStructural(doc)
		if res.IsValid() {
			t.Fatalf("string owner must be rejected under %s", v)
		}
	}
}

func TestStructural_ReadinessRejectedUnder10And11(t *testing.T) {
	for _, v := range []string{"1.0", "1.1"} {
		doc := minimalContract(v)
		doc["readiness"] = map[string]any{
			"expires": "2026-12-31",
			"checks":  []any{map[string]any{"id": "a", "type": "url", "status": "done", "evidence": "e", "weight": 10}},
		}
		res := ValidateStructural(doc)
		if res.IsValid() {
			t.Fatalf("readiness must be rejected under %s", v)
		}
	}
}

// Helper to create a minimal valid contract for a given version
func minimalContract(version string) map[string]any {
	return map[string]any{
		"pactoVersion": version,
		"service": map[string]any{
			"name":    "test-svc",
			"version": "1.0.0",
		},
	}
}

func TestStructural_V12ReadinessAccepted(t *testing.T) {
	doc := minimalContract("1.2")
	doc["readiness"] = map[string]any{
		"minScore": 80, "expires": "2026-12-31", "partialCredit": 0.5,
		"history": []any{map[string]any{"date": "2026-06-21", "version": "2.1.0", "author": "ed", "description": "init"}},
		"checks":  []any{map[string]any{"id": "sec", "type": "ticket", "category": "security", "status": "done", "evidence": "SEC-1", "weight": 30}},
	}
	if res := ValidateStructural(doc); !res.IsValid() {
		t.Fatalf("v1.2 readiness should validate: %+v", res)
	}
}

func TestStructural_V12RejectsBadEnumsAndRanges(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{"id": "x", "type": "url", "status": "done", "evidence": "e", "weight": 10}
	}
	cases := []func(map[string]any){
		func(c map[string]any) { c["status"] = "wip" },
		func(c map[string]any) { c["category"] = "bogus" },
		func(c map[string]any) { c["weight"] = 101 },
	}
	for i, mutate := range cases {
		doc := minimalContract("1.2")
		c := base()
		mutate(c)
		doc["readiness"] = map[string]any{"expires": "2026-12-31", "checks": []any{c}}
		res := ValidateStructural(doc)
		if res.IsValid() {
			t.Fatalf("case %d should be rejected", i)
		}
	}
}
