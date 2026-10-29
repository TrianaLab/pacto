package validation

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestSchemaPerSupportedVersion: the versions Parse accepts are the versions
// with a schema, so a new pactoVersion is one list in pkg/contract plus its
// schema file, and neither can be forgotten.
func TestSchemaPerSupportedVersion(t *testing.T) {
	if got := slices.Sorted(maps.Keys(schemaBytes)); !slices.Equal(got, contract.SupportedVersions) {
		t.Fatalf("schemas for %v, contract.SupportedVersions %v", got, contract.SupportedVersions)
	}
}

func TestSchemaBytes(t *testing.T) {
	data := SchemaBytes()
	if len(data) == 0 {
		t.Fatal("expected non-empty schema bytes")
	}
}

func TestCompileSchema_InvalidJSON(t *testing.T) {
	_, err := compileSchema([]byte("{invalid json!"), schemaResourceURL)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestCompileSchema_InvalidSchema(t *testing.T) {
	_, err := compileSchema([]byte(`{"type": 12345}`), schemaResourceURL)
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
	var result ValidationResult
	addSchemaError(&result, fmt.Errorf("internal error"))
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
	// A meta-schema URL is already registered, so AddResource refuses it.
	_, err := compileSchema([]byte(`{"type": "object"}`), "https://json-schema.org/draft/2020-12/schema")
	if err == nil {
		t.Fatal("expected error from AddResource")
	}
	if got := err.Error(); !strings.HasPrefix(got, "failed to add schema resource: ") {
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

// service.version is interpolated straight into `pacto pack`'s output filename,
// so a version carrying a path separator or naming the parent directory would
// let a PR-authored pacto.yaml write its archive outside the build root. The
// schema is where that closes, because every consumer runs it -- a guard in the
// pack command would leave the operator, the dashboard and the MCP server open.
func TestValidateStructuralRaw_VersionMustBeOnePathComponent(t *testing.T) {
	contract := func(version string) []byte {
		return []byte("pactoVersion: \"2.0\"\nservice:\n  name: test-svc\n  version: \"" + version + "\"\n")
	}
	for _, version := range []string{"../../etc/cron.d/x", "..", ".", "a/b", `a\b`, "1.0.0-", "-1.0.0"} {
		if r := ValidateStructuralRaw(contract(version)); r.IsValid() {
			t.Errorf("version %q was accepted; it is not safe as a path component", version)
		}
	}
	// Every shape semver produces stays legal, including prerelease and build
	// metadata, and so does a plain non-semver label.
	for _, version := range []string{"1.0.0", "1.0.0-rc.1", "1.0.0+build.5", "1.0.0-rc.1+build.5", "v1.2.3", "2", "latest"} {
		if r := ValidateStructuralRaw(contract(version)); !r.IsValid() {
			t.Errorf("version %q was rejected: %+v", version, r.Errors)
		}
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

// TestValidateStructural_V21Publish: publish is accepted at 2.1 only, and its
// shape rules come from the 2.1 schema.
func TestValidateStructural_V21Publish(t *testing.T) {
	const head = "pactoVersion: \"%s\"\nservice: {name: orders, version: 1.0.0}\n"
	full := `publish:
  bundle: oci://registry.example.com:5000/acme/pacto/orders
  deploy: manifests
  artifacts:
    - {name: api, type: image, ref: "oci://ghcr.io/acme/orders", sbom: sbom/api.spdx.json,
       build: {dockerfile: {context: ., file: Dockerfile, platforms: [linux/amd64, linux/arm64/v8]}}}
    - {name: manifests, type: manifests, ref: "oci://ghcr.io/acme/deploy/orders", build: {command: [./render.sh]}}
  sign: {key: cosign.key, transparencyLog: false}
`
	if r := ValidateStructuralRaw([]byte(fmt.Sprintf(head, "2.1") + full)); !r.IsValid() {
		t.Fatalf("2.1 with publish: %+v", r.Errors)
	}
	keyless := "publish: {bundle: \"oci://r.io/a\", sign: {keyless: true, identity: ci@acme.com, issuer: \"https://i\"}}\n"
	if r := ValidateStructuralRaw([]byte(fmt.Sprintf(head, "2.1") + keyless)); !r.IsValid() {
		t.Fatalf("keyless with its identity: %+v", r.Errors)
	}
	if r := ValidateStructuralRaw([]byte(fmt.Sprintf(head, "2.0") + full)); r.IsValid() {
		t.Fatal("publish under 2.0 must be refused")
	}
	bad := map[string]string{
		"tagged bundle":       "publish: {bundle: \"oci://ghcr.io/acme/orders:1.0.0\"}\n",
		"no oci scheme":       "publish: {bundle: ghcr.io/acme/orders}\n",
		"two builders":        "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: image, ref: \"oci://r.io/a\", build: {command: [x], dockerfile: {context: .}}}]}\n",
		"no builder":          "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: image, ref: \"oci://r.io/a\", build: {}}]}\n",
		"bad type":            "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: binary, ref: \"oci://r.io/a\", build: {command: [x]}}]}\n",
		"bad name":            "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: Api, type: image, ref: \"oci://r.io/a\", build: {command: [x]}}]}\n",
		"bad platform":        "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: image, ref: \"oci://r.io/a\", build: {dockerfile: {context: ., platforms: [amd64]}}}]}\n",
		"empty command":       "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: image, ref: \"oci://r.io/a\", build: {command: []}}]}\n",
		"two sign ways":       "publish: {bundle: \"oci://r.io/a\", sign: {key: k, kms: \"awskms:///a\"}}\n",
		"keyless false":       "publish: {bundle: \"oci://r.io/a\", sign: {keyless: false}}\n",
		"keyless no identity": "publish: {bundle: \"oci://r.io/a\", sign: {keyless: true, issuer: \"https://i\"}}\n",
		"empty sign":          "publish: {bundle: \"oci://r.io/a\", sign: {}}\n",
		"sbom not json":       "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: image, ref: \"oci://r.io/a\", sbom: sbom.txt, build: {command: [x]}}]}\n",
		"sbom as a flag":      "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: image, ref: \"oci://r.io/a\", sbom: true, build: {command: [x]}}]}\n",
		"missing context":     "publish: {bundle: \"oci://r.io/a\", artifacts: [{name: a, type: image, ref: \"oci://r.io/a\", build: {dockerfile: {file: D}}}]}\n",
	}
	for name, doc := range bad {
		if r := ValidateStructuralRaw([]byte(fmt.Sprintf(head, "2.1") + doc)); r.IsValid() {
			t.Errorf("%s: must be refused", name)
		}
	}
}

// TestNeeds22Epoch: a 2.0 or 2.1 contract that declares an interface's
// compatibilityEpoch is told to set pactoVersion 2.2, the rest of the contract
// is still checked, and a 2.2 contract accepts the field.
func TestNeeds22Epoch(t *testing.T) {
	for _, version := range []string{"2.0", "2.1"} {
		data := map[string]any{
			"pactoVersion": version,
			"service":      map[string]any{"name": "svc", "version": "1.0.0"},
			"interfaces": []any{
				map[string]any{"name": "api", "type": "openapi", "ref": "api.yaml", "compatibilityEpoch": 2},
				map[string]any{"name": "rpc", "type": "grpc", "ref": "api.proto", "visibility": "nonsense"},
			},
		}
		var got []string
		for _, e := range ValidateStructural(data).Errors {
			got = append(got, e.Code+" "+e.Path)
		}
		joined := strings.Join(got, "\n")
		for _, want := range []string{"NEEDS_PACTO_2_2 interfaces.0.compatibilityEpoch", "SCHEMA_VIOLATION interfaces.1.visibility"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("%s: missing %q in:\n%s", version, want, joined)
			}
		}
		if len(got) != 2 {
			t.Fatalf("%s: expected only those two errors:\n%s", version, joined)
		}
		if data["interfaces"].([]any)[0].(map[string]any)["compatibilityEpoch"] != 2 {
			t.Fatal("the caller's data was changed")
		}
	}
	ok := map[string]any{
		"pactoVersion": "2.2",
		"service":      map[string]any{"name": "svc", "version": "1.0.0"},
		"interfaces":   []any{map[string]any{"name": "api", "type": "openapi", "ref": "api.yaml", "compatibilityEpoch": 2}},
	}
	if res := ValidateStructural(ok); !res.IsValid() {
		t.Fatalf("a 2.2 contract with an epoch: %+v", res.Errors)
	}
	ok["interfaces"] = []any{map[string]any{"name": "api", "type": "openapi", "ref": "api.yaml", "compatibilityEpoch": -1}}
	if res := ValidateStructural(ok); res.IsValid() {
		t.Fatal("a negative epoch must be refused")
	}
}

// TestNeeds21: a 2.0 contract using what only 2.1 has is told to set
// pactoVersion 2.1, not given the schema's generic "not allowed", and the rest
// of the contract is still checked.
func TestNeeds21(t *testing.T) {
	data := map[string]any{
		"pactoVersion": "2.0",
		"service":      map[string]any{"name": "svc", "version": "1.0.0"},
		"publish":      map[string]any{},
		"checks":       []any{},
		"interfaces":   []any{map[string]any{"name": "tools", "type": "mcp"}, map[string]any{"name": "api", "type": "bogus", "visibility": "nonsense"}},
		"dependencies": []any{
			map[string]any{"name": "db", "ref": "oci://r/db:1.0.0", "required": true, "compatibility": "^1.0.0", "external": true},
			map[string]any{"name": "model", "required": true, "external": true, "version": "2025-01"},
		},
	}
	res := ValidateStructural(data)
	var got []string
	for _, e := range res.Errors {
		got = append(got, e.Code+" "+e.Path)
		if strings.Contains(e.Message, "not allowed") {
			t.Fatalf("a generic schema error: %+v", e)
		}
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"NEEDS_PACTO_2_1 publish", "NEEDS_PACTO_2_1 checks", "NEEDS_PACTO_2_1 interfaces.0.type",
		"NEEDS_PACTO_2_1 dependencies.0.external", "NEEDS_PACTO_2_1 dependencies.1.external", "NEEDS_PACTO_2_1 dependencies.1.version",
		"SCHEMA_VIOLATION dependencies.1", "SCHEMA_VIOLATION interfaces.1.visibility"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "SCHEMA_VIOLATION interfaces.0") {
		t.Fatalf("the api interface's errors are reported at the mcp interface's index:\n%s", joined)
	}
	if strings.Contains(joined, "SCHEMA_VIOLATION dependencies.0") {
		t.Fatalf("a 2.0 dependency with a ref is wrong only in being external:\n%s", joined)
	}
	if _, ok := data["publish"]; !ok || data["dependencies"].([]any)[0].(map[string]any)["external"] != true {
		t.Fatal("the caller's data was changed")
	}
	data["interfaces"] = []any{map[string]any{"name": "api", "type": "openapi", "ref": "api.yaml"}}
	delete(data, "publish")
	delete(data, "checks")
	delete(data, "dependencies")
	if res := ValidateStructural(data); !res.IsValid() {
		t.Fatalf("a plain 2.0 contract: %+v", res.Errors)
	}
}

// TestNeeds22: an interface lifecycle and guarantees (both on one interface)
// and dependency requires in a 2.0 or 2.1 contract are told to set
// pactoVersion 2.2, the rest of each entry is still checked at its own index,
// and the caller's data is left alone.
func TestNeeds22(t *testing.T) {
	for _, version := range []string{"2.0", "2.1"} {
		data := map[string]any{
			"pactoVersion": version,
			"service":      map[string]any{"name": "svc", "version": "1.0.0"},
			"interfaces": []any{
				map[string]any{"name": "events", "type": "asyncapi", "ref": "e.yaml", "guarantees": map[string]any{"delivery": "at-least-once"},
					"lifecycle": map[string]any{"deprecated": "2026-01-15"}},
				map[string]any{"name": "api", "type": "openapi", "ref": "a.yaml", "visibility": "nonsense"},
			},
			"dependencies": []any{
				map[string]any{"name": "orders", "ref": "oci://r/orders", "required": true, "compatibility": "^1.0.0",
					"requires": []any{map[string]any{"interface": "events", "delivery": "at-least-once"}}},
			},
		}
		var got []string
		for _, e := range ValidateStructural(data).Errors {
			got = append(got, e.Code+" "+e.Path)
		}
		joined := strings.Join(got, "\n")
		want := []string{"NEEDS_PACTO_2_2 interfaces.0.lifecycle", "NEEDS_PACTO_2_2 interfaces.0.guarantees", "NEEDS_PACTO_2_2 dependencies.0.requires", "SCHEMA_VIOLATION interfaces.1.visibility"}
		if len(got) != len(want) {
			t.Fatalf("%s: got\n%s", version, joined)
		}
		for _, w := range want {
			if !strings.Contains(joined, w) {
				t.Fatalf("%s: missing %q in:\n%s", version, w, joined)
			}
		}
		if _, ok := data["interfaces"].([]any)[0].(map[string]any)["guarantees"]; !ok {
			t.Fatal("the caller's data was changed")
		}
	}
	data := map[string]any{"pactoVersion": "2.1", "service": map[string]any{"name": "svc", "version": "1.0.0"}, "interfaces": "not a list"}
	if res := ValidateStructural(data); res.IsValid() {
		t.Fatal("a malformed interfaces list must still be refused")
	}
}

func TestValidateStructural_V22Guarantees(t *testing.T) {
	const head = "pactoVersion: \"2.2\"\nservice: {name: svc, version: 1.0.0}\n"
	ok := head + `interfaces:
  - {name: events, type: asyncapi, ref: e.yaml, guarantees: {profile: "1", delivery: at-least-once, ordering: per-key, dedupeWindow: 5m, retention: 168h}}
  - {name: api, type: openapi, ref: a.yaml, guarantees: {idempotent: true, timeout: 1m30s}}
dependencies:
  - name: orders
    ref: oci://r/orders
    required: true
    compatibility: ^1.0.0
    requires: [{interface: events, delivery: at-least-once, dedupeWindow: 1m}]
`
	if r := ValidateStructuralRaw([]byte(ok)); !r.IsValid() {
		t.Fatalf("valid 2.2 contract refused: %+v", r.Errors)
	}
	bad := map[string]string{
		"duration":     "interfaces: [{name: e, type: asyncapi, ref: e.yaml, guarantees: {retention: 7d}}]\n",
		"delivery":     "interfaces: [{name: e, type: asyncapi, ref: e.yaml, guarantees: {delivery: exactly-once}}]\n",
		"profile":      "interfaces: [{name: e, type: asyncapi, ref: e.yaml, guarantees: {profile: \"2\"}}]\n",
		"unknown":      "interfaces: [{name: e, type: asyncapi, ref: e.yaml, guarantees: {latency: 1s}}]\n",
		"no interface": "dependencies: [{name: o, ref: oci://r/o, required: true, compatibility: ^1.0.0, requires: [{idempotent: true}]}]\n",
		"bad requires": "dependencies: [{name: o, ref: oci://r/o, required: true, compatibility: ^1.0.0, requires: [{interface: e, ordering: total}]}]\n",
	}
	for name, doc := range bad {
		if r := ValidateStructuralRaw([]byte(head + doc)); r.IsValid() {
			t.Errorf("%s: must be refused", name)
		}
	}
}
