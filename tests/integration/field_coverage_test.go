//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestFieldCoverage is the static half of the field coverage inventory
// (pkg/validation/schema/field-coverage.yaml): every schema path gets one valid and
// one invalid contract run through `pacto validate`, and every enum value one valid
// run. An invalid run asserts the exact finding code, so a path whose check moves
// layer (schema to cross-field) shows up here. A field with no static check beyond
// its shape still gets a valid run proving it round-trips without breaking
// validation. Each case starts from fieldCoverageBase, which declares every field.
func TestFieldCoverage(t *testing.T) {
	for _, tc := range fieldCoverageCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := fieldCoverageDoc(t)
			for _, m := range tc.ops {
				applyOp(t, doc, m)
			}
			out := validateDoc(t, doc, tc.resolver)
			if tc.code == "" {
				if !out.Valid {
					t.Fatalf("want valid, got errors %+v", out.Errors)
				}
				return
			}
			if tc.warning {
				if !out.Valid || !hasFindingCode(out.Warnings, tc.code) {
					t.Fatalf("want valid with warning %s, got valid=%v warnings=%+v errors=%+v", tc.code, out.Valid, out.Warnings, out.Errors)
				}
				return
			}
			if out.Valid || !hasFindingCode(out.Errors, tc.code) {
				t.Fatalf("want error %s, got valid=%v errors=%+v", tc.code, out.Valid, out.Errors)
			}
		})
	}
}

type fcCase struct {
	name    string
	ops     []fcOp
	code    string // "" = must validate cleanly
	warning bool   // code is expected as a warning on a valid contract
	// resolver runs the CLI with a bundle store, which is what lets it resolve policy refs.
	resolver bool
}

// fcOp sets path (dotted, numeric segments index arrays) to value, or deletes it when del is set.
type fcOp struct {
	path  string
	value any
	del   bool
}

func set(path string, v any) fcOp { return fcOp{path: path, value: v} }
func del(path string) fcOp        { return fcOp{path: path, del: true} }

func valid(name string, ops ...fcOp) fcCase { return fcCase{name: name, ops: ops} }
func invalid(name, code string, ops ...fcOp) fcCase {
	return fcCase{name: name, ops: ops, code: code}
}
func warns(name, code string, ops ...fcOp) fcCase {
	return fcCase{name: name, ops: ops, code: code, warning: true}
}

// A value of the wrong YAML shape fails decoding into the contract model before the schema runs, so it
// reports PARSE_ERROR; a well-shaped value the schema rejects reports SCHEMA_VIOLATION.
const (
	schemaViolation = "SCHEMA_VIOLATION"
	parseError      = "PARSE_ERROR"
	needs21         = "NEEDS_PACTO_2_1" // a 2.1-only part in a 2.0 contract
	needs22         = "NEEDS_PACTO_2_2" // a 2.2-only part in a 2.0 or 2.1 contract
)

var fieldCoverageCases = []fcCase{
	valid("pactoVersion/valid"),
	invalid("pactoVersion/invalid", parseError, set("pactoVersion", "1.0")),
	valid("pactoVersion=2.2", set("pactoVersion", "2.2")),
	valid("pactoVersion=2.1", set("pactoVersion", "2.1"), del("interfaces.0.lifecycle")),
	valid("pactoVersion=2.0", set("pactoVersion", "2.0"), del("publish"), del("checks"), del("interfaces.0.lifecycle")),

	valid("service/valid"),
	invalid("service/invalid", parseError, set("service", "orders")),
	valid("service.name/valid", set("service.name", "orders-v2")),
	invalid("service.name/invalid", schemaViolation, set("service.name", "Orders_Service")),
	valid("service.version/valid", set("service.version", "2.3.4-rc.1")),
	invalid("service.version/invalid", "INVALID_SEMVER", set("service.version", "abc")),
	valid("service.owner/valid", del("service.owner")),
	invalid("service.owner/invalid", schemaViolation, set("service.owner", map[string]any{})),
	valid("service.owner.team/valid", del("service.owner.dri")),
	invalid("service.owner.team/invalid", schemaViolation, set("service.owner.team", "pay ments")),
	valid("service.owner.dri/valid", del("service.owner.team")),
	invalid("service.owner.dri/invalid", schemaViolation, set("service.owner.dri", "alice smith")),
	valid("service.owner.contacts/valid", del("service.owner.contacts")),
	invalid("service.owner.contacts/invalid", parseError, set("service.owner.contacts", "team@example.com")),
	valid("service.owner.contacts[]/valid"),
	invalid("service.owner.contacts[]/invalid", parseError, set("service.owner.contacts.0", "team@example.com")),
	valid("service.owner.contacts[].type/valid"),
	invalid("service.owner.contacts[].type/invalid", schemaViolation, set("service.owner.contacts.0.type", "fax")),
	valid("service.owner.contacts[].type=email", set("service.owner.contacts.0.type", "email")),
	valid("service.owner.contacts[].type=url", set("service.owner.contacts.0.type", "url")),
	valid("service.owner.contacts[].type=group", set("service.owner.contacts.0.type", "group")),
	valid("service.owner.contacts[].type=oncall", set("service.owner.contacts.0.type", "oncall")),
	valid("service.owner.contacts[].type=chat", set("service.owner.contacts.0.type", "chat")),
	valid("service.owner.contacts[].type=other", set("service.owner.contacts.0.type", "other")),
	valid("service.owner.contacts[].value/valid", set("service.owner.contacts.0.value", "oncall@example.com")),
	invalid("service.owner.contacts[].value/invalid", schemaViolation, del("service.owner.contacts.0.value")),
	valid("service.owner.contacts[].purpose/valid", del("service.owner.contacts.0.purpose")),
	invalid("service.owner.contacts[].purpose/invalid", schemaViolation, set("service.owner.contacts.0.purpose", "billing")),
	valid("service.owner.contacts[].purpose=ownership", set("service.owner.contacts.0.purpose", "ownership")),
	valid("service.owner.contacts[].purpose=support", set("service.owner.contacts.0.purpose", "support")),
	valid("service.owner.contacts[].purpose=escalation", set("service.owner.contacts.0.purpose", "escalation")),
	valid("service.owner.contacts[].purpose=oncall", set("service.owner.contacts.0.purpose", "oncall")),
	valid("service.owner.contacts[].purpose=notifications", set("service.owner.contacts.0.purpose", "notifications")),

	valid("interfaces/valid"),
	invalid("interfaces/invalid", parseError, set("interfaces", "api")),
	valid("interfaces[]/valid"),
	invalid("interfaces[]/invalid", "DUPLICATE_INTERFACE_NAME", set("interfaces.1", map[string]any{"name": "api", "type": "openapi", "ref": "interfaces/openapi.yaml"})),
	valid("interfaces[].name/valid", set("interfaces.0.name", "api"), set("capabilities.0.binding.interface", "api")),
	invalid("interfaces[].name/invalid", schemaViolation, del("interfaces.0.name")),
	valid("interfaces[].type/valid"),
	invalid("interfaces[].type/invalid", schemaViolation, set("interfaces.0.type", "soap")),
	valid("interfaces[].type=openapi", set("interfaces.0.type", "openapi")),
	valid("interfaces[].type=asyncapi", set("interfaces.0.type", "asyncapi"), set("interfaces.0.ref", "interfaces/asyncapi.yaml")),
	valid("interfaces[].type=grpc", set("interfaces.0.type", "grpc"), set("interfaces.0.ref", "interfaces/orders.proto")),
	valid("interfaces[].type=mcp", set("interfaces.0.type", "mcp"), set("interfaces.0.ref", "interfaces/tools.json")),
	invalid("interfaces[].type=mcp/no-tools", "INVALID_MCP_TOOLS", set("interfaces.0.type", "mcp")),
	invalid("interfaces[].type=mcp/invalid-2.0", needs21, set("pactoVersion", "2.0"), del("publish"), del("checks"),
		set("interfaces.0.type", "mcp"), set("interfaces.0.ref", "interfaces/tools.json")),
	valid("interfaces[].ref/valid", set("interfaces.0.ref", "interfaces/asyncapi.yaml")),
	invalid("interfaces[].ref/invalid", "FILE_NOT_FOUND", set("interfaces.0.ref", "interfaces/missing.yaml")),
	valid("interfaces[].visibility/valid", del("interfaces.0.visibility")),
	invalid("interfaces[].visibility/invalid", schemaViolation, set("interfaces.0.visibility", "private")),
	valid("interfaces[].visibility=public", set("interfaces.0.visibility", "public")),
	valid("interfaces[].visibility=internal", set("interfaces.0.visibility", "internal")),
	valid("interfaces[].lifecycle/valid", del("interfaces.0.lifecycle")),
	invalid("interfaces[].lifecycle/invalid", schemaViolation, set("interfaces.0.lifecycle", map[string]any{})),
	invalid("interfaces[].lifecycle/invalid-2.1", needs22, set("pactoVersion", "2.1")),
	valid("interfaces[].lifecycle.deprecated/valid", set("interfaces.0.lifecycle.deprecated", "2025-12-01")),
	invalid("interfaces[].lifecycle.deprecated/invalid", "INVALID_INTERFACE_LIFECYCLE", set("interfaces.0.lifecycle.deprecated", "2026-1-15")),
	valid("interfaces[].lifecycle.sunset/valid", del("interfaces.0.lifecycle.sunset")),
	invalid("interfaces[].lifecycle.sunset/invalid", "INVALID_INTERFACE_LIFECYCLE", set("interfaces.0.lifecycle.sunset", "2025-01-01")),
	valid("interfaces[].lifecycle.successor/valid", set("interfaces.1", map[string]any{"name": "api-v2", "type": "openapi", "ref": "interfaces/openapi.yaml"}),
		set("interfaces.0.lifecycle.successor", "api-v2")),
	invalid("interfaces[].lifecycle.successor/invalid", "INTERFACE_SUCCESSOR_UNKNOWN", set("interfaces.0.lifecycle.successor", "api")),

	valid("configurations/valid", del("configurations")),
	invalid("configurations/invalid", parseError, set("configurations", "app")),
	valid("configurations[]/valid"),
	invalid("configurations[]/invalid", "DUPLICATE_CONFIGURATION_NAME", set("configurations.1", map[string]any{"name": "app", "schema": "configuration/schema.json", "required": false})),
	valid("configurations[].name/valid", set("configurations.0.name", "runtime")),
	invalid("configurations[].name/invalid", schemaViolation, del("configurations.0.name")),
	valid("configurations[].schema/valid", del("configurations.0.values")),
	invalid("configurations[].schema/invalid", "FILE_NOT_FOUND", set("configurations.0.schema", "configuration/missing.json")),
	valid("configurations[].ref/valid", del("configurations.0.schema"), del("configurations.0.values"), set("configurations.0.ref", "oci://ghcr.io/acme/shared-config-pacto:1.0.0")),
	invalid("configurations[].ref/invalid", "INVALID_CONFIG_REF", del("configurations.0.schema"), del("configurations.0.values"), set("configurations.0.ref", "oci://")),
	valid("configurations[].values/valid", set("configurations.0.values.port", 9090)),
	invalid("configurations[].values/invalid", "CONFIG_VALUES_VALIDATION_FAILED", set("configurations.0.values.port", "eighty")),
	valid("configurations[].required/valid", set("configurations.0.required", false)),
	invalid("configurations[].required/invalid", schemaViolation, del("configurations.0.required")),

	valid("policies/valid", del("policies")),
	invalid("policies/invalid", parseError, set("policies", "base")),
	valid("policies[]/valid"),
	invalid("policies[]/invalid", "DUPLICATE_POLICY_NAME", set("policies.1", map[string]any{"name": "base", "schema": "policy/schema.json"})),
	valid("policies[].name/valid", set("policies.0.name", "platform")),
	invalid("policies[].name/invalid", schemaViolation, del("policies.0.name")),
	valid("policies[].schema/valid"),
	invalid("policies[].schema/invalid", "POLICY_VIOLATION", set("policies.0.schema", "policy/strict.json")),
	{name: "policies[].ref/valid", ops: []fcOp{del("policies.0.schema"), set("policies.0.ref", "policy-bundle")}, resolver: true},
	invalid("policies[].ref/invalid", "INVALID_POLICY_REF", del("policies.0.schema"), set("policies.0.ref", "oci://")),
	valid("policies[].target/valid", del("policies.0.target")),
	invalid("policies[].target/invalid", schemaViolation, set("policies.0.target", "service")),
	valid("policies[].target=contract", set("policies.0.target", "contract")),

	valid("dependencies/valid", del("dependencies")),
	invalid("dependencies/invalid", parseError, set("dependencies", "payments")),
	valid("dependencies[]/valid"),
	invalid("dependencies[]/invalid", "DUPLICATE_DEPENDENCY_NAME", set("dependencies.1", map[string]any{"name": "payments", "ref": "oci://ghcr.io/acme/other-pacto:1.0.0", "required": false, "compatibility": "^1.0.0"})),
	valid("dependencies[].name/valid", set("dependencies.0.name", "billing")),
	invalid("dependencies[].name/invalid", schemaViolation, del("dependencies.0.name")),
	warns("dependencies[].ref/valid", "TAG_NOT_DIGEST", set("dependencies.0.ref", "oci://ghcr.io/acme/payments-pacto:1.0.0")),
	invalid("dependencies[].ref/invalid", "INVALID_OCI_REF", set("dependencies.0.ref", "oci://")),
	valid("dependencies[].required/valid", set("dependencies.0.required", false)),
	invalid("dependencies[].required/invalid", schemaViolation, del("dependencies.0.required")),
	valid("dependencies[].compatibility/valid", set("dependencies.0.compatibility", ">=1.2.0 <3.0.0")),
	invalid("dependencies[].compatibility/invalid", "INVALID_COMPATIBILITY", set("dependencies.0.compatibility", "not-a-range")),
	valid("dependencies[].external/valid", set("dependencies.0.external", true)),
	invalid("dependencies[].external/invalid", "DEPENDENCY_REF_REQUIRED", del("dependencies.0.ref")),
	invalid("dependencies[].external/invalid-2.0", needs21, set("pactoVersion", "2.0"), del("publish"), del("checks"), set("dependencies.0.external", true)),
	valid("dependencies[].version/valid", set("dependencies.1", map[string]any{"name": "model", "required": true, "external": true, "version": "claude-sonnet-4-5"})),
	invalid("dependencies[].version/invalid", "EXTERNAL_VERSION_REQUIRED", set("dependencies.1", map[string]any{"name": "model", "required": true, "external": true})),

	valid("state/valid", del("state")),
	invalid("state/invalid", parseError, set("state", "stateful")),
	valid("state.type/valid", set("state.type", "hybrid")),
	invalid("state.type/invalid", schemaViolation, set("state.type", "durable")),
	valid("state.type=stateless", set("state.type", "stateless"), set("state.persistence.durability", "ephemeral")),
	invalid("state.type=stateless/persistent", schemaViolation, set("state.type", "stateless")),
	valid("state.type=stateful", set("state.type", "stateful")),
	valid("state.type=hybrid", set("state.type", "hybrid")),
	valid("state.persistence/valid", set("state.persistence", map[string]any{"scope": "shared", "durability": "ephemeral"})),
	invalid("state.persistence/invalid", schemaViolation, del("state.persistence")),
	valid("state.persistence.scope/valid", set("state.persistence.scope", "shared")),
	invalid("state.persistence.scope/invalid", schemaViolation, set("state.persistence.scope", "global")),
	valid("state.persistence.scope=local", set("state.persistence.scope", "local")),
	valid("state.persistence.scope=shared", set("state.persistence.scope", "shared")),
	valid("state.persistence.durability/valid", set("state.persistence.durability", "ephemeral")),
	invalid("state.persistence.durability/invalid", schemaViolation, set("state.persistence.durability", "forever")),
	valid("state.persistence.durability=ephemeral", set("state.persistence.durability", "ephemeral")),
	valid("state.persistence.durability=persistent", set("state.persistence.durability", "persistent")),
	valid("state.dataCriticality/valid", set("state.dataCriticality", "low")),
	invalid("state.dataCriticality/invalid", schemaViolation, del("state.dataCriticality")),
	valid("state.dataCriticality=low", set("state.dataCriticality", "low")),
	valid("state.dataCriticality=medium", set("state.dataCriticality", "medium")),
	valid("state.dataCriticality=high", set("state.dataCriticality", "high")),

	valid("workload/valid", del("workload")),
	invalid("workload/invalid", schemaViolation, set("workload", "daemon")),
	valid("workload=service", set("workload", "service")),
	valid("workload=job", set("workload", "job")),
	valid("workload=scheduled", set("workload", "scheduled")),

	valid("capabilities/valid", del("capabilities")),
	invalid("capabilities/invalid", parseError, set("capabilities", "health")),
	valid("capabilities[]/valid"),
	invalid("capabilities[]/invalid", "DUPLICATE_CAPABILITY", set("capabilities.1", map[string]any{"type": "health"})),
	valid("capabilities[].type/valid", set("capabilities.0.type", "metrics")),
	invalid("capabilities[].type/invalid", schemaViolation, set("capabilities.0.type", "tracing")),
	valid("capabilities[].type=health", set("capabilities.0.type", "health")),
	valid("capabilities[].type=metrics", set("capabilities.0.type", "metrics"), set("capabilities.0.binding.path", "/metrics")),
	valid("capabilities[].type=extension", set("capabilities.1.type", "extension")),
	valid("capabilities[].ref/valid", set("capabilities.1.ref", "acme.io/security/scan")),
	invalid("capabilities[].ref/invalid", "CAPABILITY_REF_INVALID", set("capabilities.1.ref", "backup/plan")),
	valid("capabilities[].binding/valid", del("capabilities.0.binding")),
	invalid("capabilities[].binding/invalid", schemaViolation, set("capabilities.1.binding", map[string]any{"type": "http", "interface": "api"})),
	valid("capabilities[].binding.type/valid"),
	invalid("capabilities[].binding.type/invalid", schemaViolation, set("capabilities.0.binding.type", "grpc")),
	valid("capabilities[].binding.type=http", set("capabilities.0.binding.type", "http")),
	valid("capabilities[].binding.interface/valid", set("capabilities.0.binding.interface", "api")),
	invalid("capabilities[].binding.interface/invalid", "CAPABILITY_INTERFACE_UNKNOWN", set("capabilities.0.binding.interface", "admin")),
	valid("capabilities[].binding.path/valid", del("capabilities.0.binding.path")),
	invalid("capabilities[].binding.path/invalid", "CAPABILITY_PATH_INVALID", set("capabilities.0.binding.path", "/healthz#ready")),

	valid("readiness/valid", del("readiness")),
	invalid("readiness/invalid", parseError, set("readiness", "ready")),
	valid("readiness.minScore/valid", del("readiness.minScore")),
	invalid("readiness.minScore/invalid", schemaViolation, set("readiness.minScore", 101)),
	valid("readiness.expires/valid", set("readiness.expires", "2098-01-31")),
	invalid("readiness.expires/invalid", "INVALID_READINESS_EXPIRES", set("readiness.expires", "not-a-date")),
	valid("readiness.partialCredit/valid", del("readiness.partialCredit")),
	invalid("readiness.partialCredit/invalid", schemaViolation, set("readiness.partialCredit", 2)),
	valid("readiness.history/valid", del("readiness.history")),
	invalid("readiness.history/invalid", parseError, set("readiness.history", "first")),
	valid("readiness.history[]/valid"),
	invalid("readiness.history[]/invalid", parseError, set("readiness.history.0", "first")),
	valid("readiness.history[].date/valid", set("readiness.history.0.date", "2026-02-28")),
	invalid("readiness.history[].date/invalid", "INVALID_READINESS_REVISION", set("readiness.history.0.date", "2026-02-30")),
	valid("readiness.history[].version/valid", set("readiness.history.0.version", "1.1.0")),
	invalid("readiness.history[].version/invalid", "INVALID_READINESS_REVISION", set("readiness.history.0.version", "  ")),
	valid("readiness.history[].author/valid", set("readiness.history.0.author", "bob")),
	invalid("readiness.history[].author/invalid", "INVALID_READINESS_REVISION", set("readiness.history.0.author", "  ")),
	valid("readiness.history[].description/valid", set("readiness.history.0.description", "second assessment")),
	invalid("readiness.history[].description/invalid", "INVALID_READINESS_REVISION", set("readiness.history.0.description", "  ")),
	valid("readiness.claims/valid", set("readiness.claims.1", map[string]any{"id": "runbook", "type": "document", "status": "partial", "evidence": "docs/runbook.md", "weight": 50})),
	invalid("readiness.claims/invalid", schemaViolation, del("readiness.claims")),
	valid("readiness.claims[]/valid"),
	invalid("readiness.claims[]/invalid", "DUPLICATE_READINESS_ID", set("readiness.claims.1", map[string]any{"id": "dashboards", "type": "url", "status": "done", "evidence": "https://example.com/e", "weight": 50})),
	valid("readiness.claims[].id/valid", set("readiness.claims.0.id", "slo.dashboards")),
	invalid("readiness.claims[].id/invalid", schemaViolation, set("readiness.claims.0.id", "Dashboards!")),
	valid("readiness.claims[].type/valid", set("readiness.claims.0.type", "ticket")),
	invalid("readiness.claims[].type/invalid", schemaViolation, set("readiness.claims.0.type", "rumour")),
	valid("readiness.claims[].type=url", set("readiness.claims.0.type", "url")),
	valid("readiness.claims[].type=document", set("readiness.claims.0.type", "document")),
	valid("readiness.claims[].type=ticket", set("readiness.claims.0.type", "ticket")),
	valid("readiness.claims[].type=report", set("readiness.claims.0.type", "report")),
	valid("readiness.claims[].type=artifact", set("readiness.claims.0.type", "artifact")),
	valid("readiness.claims[].type=identifier", set("readiness.claims.0.type", "identifier")),
	valid("readiness.claims[].type=other", set("readiness.claims.0.type", "other")),
	valid("readiness.claims[].category/valid", del("readiness.claims.0.category")),
	invalid("readiness.claims[].category/invalid", schemaViolation, set("readiness.claims.0.category", "vibes")),
	valid("readiness.claims[].category=architecture", set("readiness.claims.0.category", "architecture")),
	valid("readiness.claims[].category=testing", set("readiness.claims.0.category", "testing")),
	valid("readiness.claims[].category=code-quality", set("readiness.claims.0.category", "code-quality")),
	valid("readiness.claims[].category=observability", set("readiness.claims.0.category", "observability")),
	valid("readiness.claims[].category=security", set("readiness.claims.0.category", "security")),
	valid("readiness.claims[].category=documentation", set("readiness.claims.0.category", "documentation")),
	valid("readiness.claims[].category=infrastructure", set("readiness.claims.0.category", "infrastructure")),
	valid("readiness.claims[].category=ci-cd", set("readiness.claims.0.category", "ci-cd")),
	valid("readiness.claims[].category=deployment", set("readiness.claims.0.category", "deployment")),
	valid("readiness.claims[].category=resilience", set("readiness.claims.0.category", "resilience")),
	valid("readiness.claims[].category=backup-recovery", set("readiness.claims.0.category", "backup-recovery")),
	valid("readiness.claims[].category=incident-response", set("readiness.claims.0.category", "incident-response")),
	valid("readiness.claims[].category=compliance", set("readiness.claims.0.category", "compliance")),
	valid("readiness.claims[].category=other", set("readiness.claims.0.category", "other")),
	valid("readiness.claims[].status/valid", set("readiness.claims.0.status", "partial")),
	invalid("readiness.claims[].status/invalid", schemaViolation, set("readiness.claims.0.status", "maybe")),
	valid("readiness.claims[].status=done", set("readiness.claims.0.status", "done")),
	valid("readiness.claims[].status=partial", set("readiness.claims.0.status", "partial")),
	valid("readiness.claims[].status=not-done", set("readiness.claims.0.status", "not-done")),
	valid("readiness.claims[].status=deferred", set("readiness.claims.0.status", "deferred")),
	valid("readiness.claims[].evidence/valid", set("readiness.claims.0.evidence", "JIRA-123")),
	invalid("readiness.claims[].evidence/invalid", "EMPTY_READINESS_EVIDENCE", set("readiness.claims.0.evidence", "  ")),
	valid("readiness.claims[].weight/valid", set("readiness.claims.0.weight", 0)),
	invalid("readiness.claims[].weight/invalid", schemaViolation, set("readiness.claims.0.weight", -1)),
	valid("readiness.claims[].description/valid", del("readiness.claims.0.description")),
	invalid("readiness.claims[].description/invalid", "EMPTY_READINESS_DESCRIPTION", set("readiness.claims.0.description", "  ")),

	valid("publish/valid"),
	invalid("publish/invalid", parseError, set("publish", "ghcr.io")),
	invalid("publish/invalid-2.0", needs21, set("pactoVersion", "2.0")),
	valid("publish.bundle/valid", set("publish.bundle", "oci://registry.example.com:5000/acme/pacto/orders")),
	invalid("publish.bundle/invalid", schemaViolation, set("publish.bundle", "oci://ghcr.io/acme/pacto/orders:1.0.0")),
	valid("publish.deploy/valid", del("publish.deploy")),
	invalid("publish.deploy/invalid", "PUBLISH_DEPLOY_NOT_MANIFESTS", set("publish.deploy", "api")),
	valid("publish.artifacts/valid", del("publish.deploy"), del("publish.artifacts")),
	invalid("publish.artifacts/invalid", parseError, set("publish.artifacts", "api")),
	valid("publish.artifacts[]/valid"),
	invalid("publish.artifacts[]/invalid", "DUPLICATE_PUBLISH_ARTIFACT", set("publish.artifacts.4",
		map[string]any{"name": "api", "type": "image", "ref": "oci://ghcr.io/acme/other", "build": map[string]any{"command": []any{"make"}}})),
	valid("publish.artifacts[].name/valid", set("publish.artifacts.0.name", "api-v2")),
	invalid("publish.artifacts[].name/invalid", schemaViolation, set("publish.artifacts.0.name", "Api")),
	valid("publish.artifacts[].type/valid"),
	invalid("publish.artifacts[].type/invalid", schemaViolation, set("publish.artifacts.1.type", "binary")),
	valid("publish.artifacts[].type=image", set("publish.artifacts.0.type", "image")),
	valid("publish.artifacts[].type=chart", set("publish.artifacts.1.type", "chart")),
	valid("publish.artifacts[].type=manifests", set("publish.artifacts.2.type", "manifests")),
	valid("publish.artifacts[].ref/valid", set("publish.artifacts.0.ref", "oci://ghcr.io/acme/orders-v2")),
	invalid("publish.artifacts[].ref/invalid", schemaViolation, set("publish.artifacts.0.ref", "ghcr.io/acme/orders")),
	valid("publish.artifacts[].build/valid"),
	invalid("publish.artifacts[].build/invalid", schemaViolation, set("publish.artifacts.1.build", map[string]any{})),
	valid("publish.artifacts[].build.dockerfile/valid"),
	invalid("publish.artifacts[].build.dockerfile/invalid", "PUBLISH_DOCKERFILE_NOT_IMAGE",
		set("publish.artifacts.1.build", map[string]any{"dockerfile": map[string]any{"context": "."}})),
	valid("publish.artifacts[].build.dockerfile.context/valid", set("publish.artifacts.0.build.dockerfile.context", "./src")),
	invalid("publish.artifacts[].build.dockerfile.context/invalid", schemaViolation, del("publish.artifacts.0.build.dockerfile.context")),
	valid("publish.artifacts[].build.dockerfile.file/valid", del("publish.artifacts.0.build.dockerfile.file")),
	invalid("publish.artifacts[].build.dockerfile.file/invalid", schemaViolation, set("publish.artifacts.0.build.dockerfile.file", "")),
	valid("publish.artifacts[].build.dockerfile.platforms/valid", del("publish.artifacts.0.build.dockerfile.platforms")),
	invalid("publish.artifacts[].build.dockerfile.platforms/invalid", parseError, set("publish.artifacts.0.build.dockerfile.platforms", "linux/amd64")),
	valid("publish.artifacts[].build.dockerfile.platforms[]/valid", set("publish.artifacts.0.build.dockerfile.platforms.0", "linux/arm64/v8")),
	invalid("publish.artifacts[].build.dockerfile.platforms[]/invalid", schemaViolation, set("publish.artifacts.0.build.dockerfile.platforms.0", "amd64")),
	valid("publish.artifacts[].build.command/valid", set("publish.artifacts.1.build.command", []any{"make", "chart"})),
	invalid("publish.artifacts[].build.command/invalid", schemaViolation, set("publish.artifacts.1.build.command", []any{})),
	valid("publish.artifacts[].build.command[]/valid", set("publish.artifacts.1.build.command.0", "./package.sh")),
	invalid("publish.artifacts[].build.command[]/invalid", schemaViolation, set("publish.artifacts.1.build.command.0", "")),
	valid("publish.artifacts[].sbom/valid", del("publish.artifacts.3.sbom")),
	invalid("publish.artifacts[].sbom/invalid", "PUBLISH_SBOM_NOT_IMAGE", set("publish.artifacts.1.sbom", "sbom/chart.spdx.json")),
	invalid("publish.artifacts[].sbom/dockerfile", "PUBLISH_SBOM_DOCKERFILE", set("publish.artifacts.0.sbom", "sbom/api.spdx.json")),
	valid("publish.sign/valid", del("publish.sign")),
	invalid("publish.sign/invalid", schemaViolation, set("publish.sign", map[string]any{})),
	valid("publish.sign.keyless/valid", set("publish.sign", map[string]any{"keyless": true, "identity": "ci@acme.com", "issuer": "https://accounts.google.com"})),
	invalid("publish.sign.keyless/invalid", schemaViolation, set("publish.sign", map[string]any{"keyless": false})),
	valid("publish.sign.identity/valid", set("publish.sign", map[string]any{"keyless": true, "identity": "ci@acme.com", "issuer": "https://accounts.google.com"})),
	invalid("publish.sign.identity/invalid", schemaViolation, set("publish.sign", map[string]any{"keyless": true, "issuer": "https://accounts.google.com"})),
	valid("publish.sign.issuer/valid", set("publish.sign", map[string]any{"keyless": true, "identity": "ci@acme.com", "issuer": "https://accounts.google.com"})),
	invalid("publish.sign.issuer/invalid", schemaViolation, set("publish.sign", map[string]any{"keyless": true, "identity": "ci@acme.com"})),
	valid("publish.sign.key/valid", set("publish.sign.key", "keys/release.key")),
	invalid("publish.sign.key/invalid", schemaViolation, set("publish.sign.kms", "awskms:///alias/release")),
	valid("publish.sign.kms/valid", set("publish.sign", map[string]any{"kms": "awskms:///alias/release"})),
	invalid("publish.sign.kms/invalid", schemaViolation, set("publish.sign", map[string]any{"kms": ""})),
	valid("publish.sign.transparencyLog/valid", del("publish.sign.transparencyLog")),
	invalid("publish.sign.transparencyLog/invalid", parseError, set("publish.sign.transparencyLog", 3)),

	valid("checks/valid", del("checks")),
	invalid("checks/invalid", parseError, set("checks", "evals")),
	invalid("checks/invalid-2.0", needs21, set("pactoVersion", "2.0"), del("publish")),
	valid("checks[]/valid", set("checks.1", map[string]any{"name": "smoke", "file": "evals/suite.yaml"})),
	invalid("checks[]/invalid", "DUPLICATE_CHECK_NAME", set("checks.1", map[string]any{"name": "evals", "file": "evals/suite.yaml"})),
	valid("checks[].name/valid", set("checks.0.name", "refunds")),
	invalid("checks[].name/invalid", schemaViolation, del("checks.0.name")),
	valid("checks[].file/valid", set("checks.0.file", "interfaces/tools.json")),
	invalid("checks[].file/invalid", "FILE_NOT_FOUND", set("checks.0.file", "evals/missing.yaml")),

	valid("metadata/valid", set("metadata.runbook", "https://example.com/runbook")),
	invalid("metadata/invalid", parseError, set("metadata", "gold")),
	valid("extensions/valid", set("extensions.acme", map[string]any{"nested": []any{1, "two"}})),
	invalid("extensions/invalid", parseError, set("extensions", "acme")),
}

// fieldCoverageBase declares every field of the v2.2 schema at least once
// (publish.sign declares one of its three ways; the cases cover the others).
const fieldCoverageBase = `pactoVersion: "2.2"
service:
  name: orders
  version: 1.0.0
  owner:
    team: payments
    dri: alice
    contacts:
      - type: email
        value: team@example.com
        purpose: ownership
interfaces:
  - name: api
    type: openapi
    ref: interfaces/openapi.yaml
    visibility: internal
    lifecycle:
      deprecated: "2026-01-15"
      sunset: "2099-12-31"
      successor: oci://ghcr.io/acme/orders-v2
configurations:
  - name: app
    schema: configuration/schema.json
    values:
      port: 8080
    required: true
policies:
  - name: base
    schema: policy/schema.json
    target: contract
dependencies:
  - name: payments
    ref: oci://ghcr.io/acme/payments-pacto@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    required: true
    compatibility: ^1.0.0
state:
  type: stateful
  persistence:
    scope: local
    durability: persistent
  dataCriticality: high
workload: service
capabilities:
  - type: health
    binding:
      type: http
      interface: api
      path: /healthz
  - type: extension
    ref: acme.io/backup
readiness:
  minScore: 50
  expires: "2099-12-31"
  partialCredit: 0.5
  history:
    - date: "2026-01-01"
      version: 1.0.0
      author: alice
      description: first assessment
  claims:
    - id: dashboards
      type: url
      category: observability
      status: done
      evidence: https://example.com/d
      weight: 50
      description: dashboards exist
publish:
  bundle: oci://ghcr.io/acme/pacto/orders
  deploy: manifests
  artifacts:
    - name: api
      type: image
      ref: oci://ghcr.io/acme/orders
      build:
        dockerfile:
          context: .
          file: Dockerfile
          platforms: [linux/amd64]
    - name: chart
      type: chart
      ref: oci://ghcr.io/acme/charts/orders
      build:
        command: [./package-chart.sh]
    - name: manifests
      type: manifests
      ref: oci://ghcr.io/acme/deploy/orders
      build:
        command: [./render.sh]
    - name: worker
      type: image
      ref: oci://ghcr.io/acme/orders-worker
      build:
        command: [./build-worker.sh]
      sbom: sbom/worker.spdx.json
  sign:
    key: cosign.key
    transparencyLog: false
checks:
  - name: evals
    file: evals/suite.yaml
metadata:
  tier: gold
extensions:
  acme:
    enabled: true
`

var fieldCoverageFiles = map[string]string{
	"interfaces/openapi.yaml":          "openapi: 3.0.3\ninfo: {title: orders, version: 1.0.0}\npaths: {}\n",
	"interfaces/asyncapi.yaml":         "asyncapi: 2.6.0\ninfo: {title: orders, version: 1.0.0}\nchannels: {}\n",
	"interfaces/orders.proto":          "syntax = \"proto3\";\npackage orders;\n",
	"interfaces/tools.json":            `{"tools":[{"name":"refund","inputSchema":{"type":"object"}}]}`,
	"evals/suite.yaml":                 "cases: []\n",
	"configuration/schema.json":        `{"type":"object","properties":{"port":{"type":"integer"}}}`,
	"policy/schema.json":               `{"type":"object","required":["service"]}`,
	"policy-bundle/pacto.yaml":         "pactoVersion: \"2.0\"\nservice:\n  name: platform-policy\n  version: 1.0.0\n",
	"policy-bundle/policy/schema.json": `{"type":"object","required":["service"]}`,
	"policy/strict.json":               `{"type":"object","required":["service"],"properties":{"service":{"required":["owner"],"properties":{"owner":{"required":["oncall"]}}}}}`,
}

func fieldCoverageDoc(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(fieldCoverageBase), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// applyOp walks a dotted path through decoded YAML. A numeric segment indexes an array and may append
// one element past the end; the last segment is set or deleted.
func applyOp(t *testing.T, doc map[string]any, op fcOp) {
	t.Helper()
	segs := strings.Split(op.path, ".")
	var parent any = doc
	for _, s := range segs[:len(segs)-1] {
		switch p := parent.(type) {
		case map[string]any:
			parent = p[s]
		case []any:
			i, _ := strconv.Atoi(s)
			parent = p[i]
		}
		if parent == nil {
			t.Fatalf("path %q: segment %q does not exist", op.path, s)
		}
	}
	last := segs[len(segs)-1]
	switch p := parent.(type) {
	case map[string]any:
		if op.del {
			delete(p, last)
		} else {
			p[last] = op.value
		}
	case []any:
		i, _ := strconv.Atoi(last)
		if op.del {
			t.Fatalf("path %q: deleting array items is not supported", op.path)
		}
		if i == len(p) {
			// Appending changes the slice header, so write it back through the grandparent.
			applyOp(t, doc, fcOp{path: strings.Join(segs[:len(segs)-1], "."), value: append(p, op.value)})
			return
		}
		p[i] = op.value
	}
}

type fcResult struct {
	Valid    bool
	Errors   []fcFinding
	Warnings []fcFinding
}

type fcFinding struct {
	Path, Code, Message string
}

func hasFindingCode(fs []fcFinding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func validateDoc(t *testing.T, doc map[string]any, resolver bool) fcResult {
	t.Helper()
	dir := t.TempDir()
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"pacto.yaml": string(data)}
	for k, v := range fieldCoverageFiles {
		files[k] = v
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var reg *testRegistry
	if resolver {
		reg = newTestRegistry(t)
	}
	out, _ := runCommand(t, reg, "validate", "--output-format", "json", dir)
	var res fcResult
	if err := json.NewDecoder(bytes.NewReader([]byte(out))).Decode(&res); err != nil {
		t.Fatalf("decode validate output: %v\n%s", err, out)
	}
	return res
}
