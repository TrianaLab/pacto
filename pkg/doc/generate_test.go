package doc

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/dashboard"
	"github.com/trianalab/pacto/v2/pkg/graph"
	"github.com/trianalab/pacto/v2/pkg/sbom"
	"github.com/trianalab/pacto/v2/pkg/schemax"
)

func ptr(v int) *int { return &v }

// ── Task 4: header / overview / runtime ────────────────────────────────

func TestGenerate_HeaderOverviewRuntime(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service:  dashboard.Service{Name: "svc", Version: "1.2.0", ContractStatus: dashboard.StatusCompliant},
		Workload: "service",
		State:    &dashboard.StateInfo{Type: "stateless", DataCriticality: "low"},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# svc", "1.2.0", "stateless", "service", "## 1. Runtime & operations"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n%s", want, md)
		}
	}
}

// ── Task 5: interfaces / configuration / policies ──────────────────────

func TestGenerate_InterfacesConfigPolicies(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		Interfaces: []dashboard.InterfaceInfo{{
			Name: "api", Type: "openapi", Visibility: "public",
			Endpoints: []dashboard.InterfaceEndpoint{{Method: "get", Path: "/things", Summary: "list"}},
		}},
		Configurations: []dashboard.ConfigurationInfo{{Name: "app", HasSchema: true,
			Values: []schemax.Property{{Key: "timeout", Value: "30s", Type: "string"}}}},
		Policies: []dashboard.PolicyInfo{{Name: "org", Ref: "oci://ghcr.io/acme/policy:1"}},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"api", "/things", "GET", "timeout", "30s", "org", "oci://ghcr.io/acme/policy:1",
		"### 2.1. OpenAPI Interface: api", "| Name | Type | Source |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n%s", want, md)
		}
	}
}

// ── Task 6: dependencies / readiness / SBOM / lock / docs / metadata ────

func TestGenerate_DepsReadinessSbomLockDocs(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service:      dashboard.Service{Name: "svc", Version: "1.0.0"},
		Dependencies: []dashboard.DependencyInfo{{Name: "db", Ref: "oci://ghcr.io/acme/db:2", Required: true, Compatibility: "^2.0.0", LockedVersion: "2.1.0"}},
		Readiness:    &dashboard.ReadinessInfo{Score: 82, MinScore: 70, Expires: "2026-12-31", Checks: []dashboard.ReadinessCheckInfo{{ID: "runbook", Status: "done", Weight: 10}}},
		SBOM:         &sbom.Document{Format: "spdx", Packages: []sbom.Package{{Name: "libfoo", Version: "1.2.3", License: "MIT"}}},
		Lock:         &dashboard.LockInfo{Present: true, RootDigest: "sha256:abc", Dependencies: []dashboard.LockDepInfo{{Name: "db", Version: "2.1.0", Digest: "sha256:def"}}},
		Docs:         []dashboard.DocInfo{{Path: "docs/runbook.md", Title: "Runbook", Content: "steps here"}},
		Metadata:     map[string]string{"team": "core"},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"db", "2.1.0", "82", "libfoo", "sha256:abc", "Runbook", "team",
		"## 2. Dependencies", "## 3. Readiness", "## 4. SBOM", "## 5. Lockfile", "## 6. Documentation",
		"`team: core`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n%s", want, md)
		}
	}
}

// ── Comprehensive snapshot (no graph) ──────────────────────────────────

func fullSnapshot() *dashboard.ServiceDetails {
	score := 90
	return &dashboard.ServiceDetails{
		Service: dashboard.Service{
			Name:           "payments-api",
			Version:        "2.1.0",
			Owner:          contract.Owner{Team: "team/payments"},
			ContractStatus: dashboard.StatusCompliant,
		},
		Compliance: &dashboard.ComplianceInfo{Status: dashboard.ComplianceOK, Score: &score},
		Workload:   "service",
		State: &dashboard.StateInfo{
			Type:                  "stateful",
			DataCriticality:       "high",
			PersistenceScope:      "shared",
			PersistenceDurability: "persistent",
		},
		Capabilities: []dashboard.CapabilityInfo{
			{Type: "health"},
			{Type: "metrics"},
		},
		Interfaces: []dashboard.InterfaceInfo{
			{Name: "api", Type: "openapi", Visibility: "public",
				Endpoints: []dashboard.InterfaceEndpoint{
					{Method: "get", Path: "/health", Summary: "Health check"},
					{Method: "post", Path: "/payments"},
				}},
			{Name: "events", Type: "asyncapi"},
		},
		Configurations: []dashboard.ConfigurationInfo{
			{Name: "default", HasSchema: true, Values: []schemax.Property{{Key: "PORT", Value: "8080", Type: "integer"}}},
		},
		Policies: []dashboard.PolicyInfo{
			{Name: "local", Schema: "policy/schema.json"},
			{Name: "platform", Ref: "oci://ghcr.io/acme/platform-policy:1.0.0"},
		},
		Dependencies: []dashboard.DependencyInfo{
			{Name: "auth", Ref: "ghcr.io/acme/auth-pacto@sha256:abc", Required: true, Compatibility: "^2.0.0", LockedVersion: "2.3.0", LockedDigest: "sha256:auth", DriftStatus: "locked"},
			{Name: "notify", Ref: "ghcr.io/acme/notify-pacto:1.0.0", Required: false, Compatibility: "~1.0.0"},
		},
		Readiness: &dashboard.ReadinessInfo{
			Score: 82, MinScore: 70, Expires: "2026-12-31",
			Checks: []dashboard.ReadinessCheckInfo{
				{ID: "dashboard", Type: "url", Category: "observability", Status: "done", Evidence: "https://grafana/x", Weight: 20, Description: "Main dashboard"},
				{ID: "runbook", Type: "document", Status: "partial", Weight: 15},
			},
			Revisions: []dashboard.ReadinessRevisionInfo{
				{Date: "2026-06-21", Version: "2.1.0", Author: "ed", Description: "Initial assessment"},
			},
		},
		SBOM: &sbom.Document{Format: "spdx", Packages: []sbom.Package{
			{Name: "libfoo", Version: "1.2.3", License: "MIT", Supplier: "ACME"},
			{Name: "libbar", Version: "0.1.0"},
		}},
		Lock: &dashboard.LockInfo{
			Present: true, RootDigest: "sha256:root",
			Dependencies: []dashboard.LockDepInfo{{Name: "auth", Version: "2.3.0", Digest: "sha256:auth"}},
			References:   []dashboard.LockRefInfo{{Kind: "config", Name: "shared-config", Version: "1.0.0", Digest: "sha256:cfg"}},
		},
		Docs: []dashboard.DocInfo{
			{Path: "docs/runbook.md", Title: "Runbook", Content: "run steps"},
			{Path: "docs/big.md", Title: "Big", Content: "cut", Truncated: true},
		},
		Metadata: map[string]string{"team": "payments", "tier": "critical"},
	}
}

func TestGenerate_FullSnapshot(t *testing.T) {
	md, err := Generate(fullSnapshot(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain := []string{
		"# payments-api",
		"**payments-api** `v2.1.0`",
		"status `Compliant`",
		"compliance `OK`",
		"readiness `82/100`",
		"| **Owner** | `team/payments` |",
		"## Table of Contents",
		"## 1. Runtime & operations",
		"| **Workload** | `service` |",
		"| **State** | `stateful` |",
		"| **Data criticality** | `high` |",
		"| **Persistence scope** | `shared` |",
		"| **Persistence durability** | `persistent` |",
		"| **Health** | `supported` |",
		"| **Metrics** | `supported` |",
		"## 2. Architecture",
		"```mermaid",
		`subgraph paymentsapi["payments-api v2.1.0"]`,
		`paymentsapi_state[("stateful · high criticality · shared persistent")]`,
		"<br/>♥ health",
		"<br/>📊 metrics",
		`external(["External User"])`,
		"external --> paymentsapi_iface_api",
		"## 3. Interfaces",
		"| `api` | `openapi` | `public` |",
		"| `events` | `asyncapi` | — |",
		"### 3.1. OpenAPI Interface: api",
		"| `GET` | `/health` | Health check |",
		"| `POST` | `/payments` | — |",
		"## 4. Configuration",
		"| `PORT` | `integer` | `8080` |",
		"## 5. Policies",
		"| `local` | Local | `policy/schema.json` |",
		"| `platform` | Remote | `oci://ghcr.io/acme/platform-policy:1.0.0` |",
		"## 6. Dependencies",
		"| `auth` | `ghcr.io/acme/auth-pacto@sha256:abc` | `^2.0.0` | Yes | `2.3.0` | `sha256:auth` | `locked` |",
		"| `notify` | `ghcr.io/acme/notify-pacto:1.0.0` | `~1.0.0` | No | — | — | — |",
		"## 7. Readiness",
		"Score `82/100` · gate `70` · expires `2026-12-31`",
		"| `dashboard` | `url` | `observability` | `done` | `https://grafana/x` | 20 | Main dashboard |",
		"| `runbook` | `document` | — | `partial` | — | 15 | — |",
		"**Revision History**",
		"| `2026-06-21` | `2.1.0` | ed | Initial assessment |",
		"## 8. SBOM",
		"Format `spdx` · 2 packages",
		"| `libfoo` | `1.2.3` | `MIT` | `ACME` |",
		"| `libbar` | `0.1.0` | — | — |",
		"## 9. Lockfile",
		"Root digest: `sha256:root`",
		"| `auth` | `2.3.0` | `sha256:auth` |",
		"| `shared-config` | `config` | `1.0.0` | `sha256:cfg` |",
		"## 10. Documentation",
		"### 10.1. Runbook",
		"_Source: `docs/runbook.md`_",
		"run steps",
		"### 10.2. Big",
		"_(truncated)_",
		"`team: payments`",
		"`tier: critical`",
		"Generated by [Pacto](https://trianalab.github.io/pacto)",
	}
	for _, s := range mustContain {
		if !strings.Contains(md, s) {
			t.Errorf("expected %q in output", s)
		}
	}
}

func TestGenerate_TOCInSync(t *testing.T) {
	md, err := Generate(fullSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	headings := headingAnchorsIn(md)
	for _, a := range tocAnchors(md) {
		if !headings[a] {
			t.Errorf("TOC anchor #%s has no matching heading\n%s", a, md)
		}
	}
}

// ── Minimal snapshot: absent-section branches ──────────────────────────

func TestGenerate_Minimal(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "wrapper", Version: "1.0.0"},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustContain := []string{
		"# wrapper",
		"**wrapper** `v1.0.0`",
		"## 1. Architecture",
		`subgraph wrapper["wrapper v1.0.0"]`,
		`wrapper_info["wrapper"]`,
	}
	for _, s := range mustContain {
		if !strings.Contains(md, s) {
			t.Errorf("expected %q in output:\n%s", s, md)
		}
	}
	mustNotContain := []string{
		"Runtime & operations", "## 2. Interfaces", ". Configuration", ". Policies",
		". Dependencies", ". Readiness", ". SBOM", ". Lockfile", ". Documentation",
		"External User", "| Field | Value |", "status `",
	}
	for _, s := range mustNotContain {
		if strings.Contains(md, s) {
			t.Errorf("unexpected %q in output:\n%s", s, md)
		}
	}
}

// ── Runtime variants ───────────────────────────────────────────────────

func TestGenerate_RuntimeWithCapabilities(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service:  dashboard.Service{Name: "svc", Version: "1.0.0"},
		Workload: "service",
		State:    &dashboard.StateInfo{Type: "stateless", DataCriticality: "low"},
		Capabilities: []dashboard.CapabilityInfo{
			{Type: "health"},
			{Type: "extension", Ref: "example.com/custom"},
		},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## 1. Runtime & operations",
		"| **Workload** | `service` |",
		"| **Health** | `supported` |",
		"| **Extension** | `example.com/custom` |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("expected %q:\n%s", want, md)
		}
	}
}

func TestGenerate_RuntimeMinimal(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service:  dashboard.Service{Name: "svc", Version: "1.0.0"},
		Workload: "job",
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "## 1. Runtime & operations") {
		t.Errorf("expected runtime section:\n%s", md)
	}
	if !strings.Contains(md, "| **Workload** | `job` |") {
		t.Errorf("expected workload row:\n%s", md)
	}
}

// ── interfaceHeadingByType: all branches ───────────────────────────────

func TestGenerate_InterfaceHeadingTypes(t *testing.T) {
	ep := []dashboard.InterfaceEndpoint{{Method: "get", Path: "/x"}}
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		Interfaces: []dashboard.InterfaceInfo{
			{Name: "api", Type: "openapi", Endpoints: ep},
			{Name: "g", Type: "grpc", Endpoints: ep},
			{Name: "e", Type: "asyncapi", Endpoints: ep},
			{Name: "w", Type: "websocket", Endpoints: ep},
		},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"OpenAPI Interface: api", "gRPC Interface: g", "AsyncAPI Interface: e", "Websocket Interface: w",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing heading %q\n%s", want, md)
		}
	}
}

func TestGenerate_InterfaceWithoutEndpoints(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service:    dashboard.Service{Name: "svc", Version: "1.0.0"},
		Interfaces: []dashboard.InterfaceInfo{{Name: "api", Type: "openapi", Visibility: "internal"}},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "### ") {
		t.Errorf("did not expect an endpoint sub-section:\n%s", md)
	}
}

// ── configuration variants ─────────────────────────────────────────────

func TestGenerate_ConfigVariants(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		Configurations: []dashboard.ConfigurationInfo{
			{Name: "app", Values: []schemax.Property{{Key: "K", Value: "V", Type: "string"}}},
			{Name: "db", Ref: "oci://ghcr.io/acme/db-config:1.0.0"},
			{Name: "", HasSchema: true}, // empty name → "default", no values → note
		},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"_app_", "| `K` | `string` | `V` |",
		"_db_", "References: `oci://ghcr.io/acme/db-config:1.0.0`",
		"_default_", "_No configurable properties._",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n%s", want, md)
		}
	}
}

// ── policies without ref (local only) already in full; readiness variants ─

func TestGenerate_ReadinessMinimal(t *testing.T) {
	// No expires, no revisions, checks present.
	d := &dashboard.ServiceDetails{
		Service:   dashboard.Service{Name: "svc", Version: "1.0.0"},
		Readiness: &dashboard.ReadinessInfo{Score: 50, MinScore: 40, Checks: []dashboard.ReadinessCheckInfo{{ID: "x", Status: "done", Weight: 5}}},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "Score `50/100` · gate `40`\n") {
		t.Errorf("expected readiness header without expires:\n%s", md)
	}
	if strings.Contains(md, "Revision History") {
		t.Errorf("did not expect revision history:\n%s", md)
	}
}

func TestGenerate_ReadinessNoChecks(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service:   dashboard.Service{Name: "svc", Version: "1.0.0"},
		Readiness: &dashboard.ReadinessInfo{Score: 0, MinScore: 0},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "## 2. Readiness") {
		t.Errorf("expected readiness section:\n%s", md)
	}
	if strings.Contains(md, "| ID | Type |") {
		t.Errorf("did not expect a checks table:\n%s", md)
	}
}

func TestGenerate_ReadinessEscapesBackticksAndPipes(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		Readiness: &dashboard.ReadinessInfo{
			Score: 10, MinScore: 5,
			Checks: []dashboard.ReadinessCheckInfo{{ID: "dash", Type: "url", Status: "done", Evidence: "https://x/q?a=`b`|c", Weight: 10}},
		},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "https://x/q?a=\\`b\\`\\|c") {
		t.Errorf("expected escaped evidence:\n%s", md)
	}
}

// ── SBOM / lock absent + minimal lock ──────────────────────────────────

func TestGenerate_SBOMEmptyPackages(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		SBOM:    &sbom.Document{Format: "spdx"},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "SBOM") {
		t.Errorf("did not expect SBOM section for empty packages:\n%s", md)
	}
}

func TestGenerate_LockMinimal(t *testing.T) {
	// Present but no root digest, no deps, no refs → heading only.
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		Lock:    &dashboard.LockInfo{Present: true},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "## 2. Lockfile") {
		t.Errorf("expected lockfile section:\n%s", md)
	}
	if strings.Contains(md, "Root digest") || strings.Contains(md, "| Dependency |") || strings.Contains(md, "| Reference |") {
		t.Errorf("did not expect lock detail tables:\n%s", md)
	}
}

func TestGenerate_LockNotPresent(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		Lock:    &dashboard.LockInfo{Present: false},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md, "Lockfile") {
		t.Errorf("did not expect lockfile section when not present:\n%s", md)
	}
}

// ── Mermaid fallback: dependency edges without a graph ──────────────────

func TestGenerate_DependencyEdgesFallback(t *testing.T) {
	d := &dashboard.ServiceDetails{
		Service: dashboard.Service{Name: "svc", Version: "1.0.0"},
		Interfaces: []dashboard.InterfaceInfo{
			{Name: "api", Type: "openapi", Visibility: "public"},
		},
		Dependencies: []dashboard.DependencyInfo{
			{Name: "auth", Ref: "reg/auth-pacto:1.0.0", Required: true, Compatibility: "^1.0.0"},
			{Name: "cache", Ref: "reg/cache-pacto:2.0.0", Required: false, Compatibility: "~2.0.0"},
		},
	}
	md, err := Generate(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`-->|"required · ^1.0.0"|`, `-.->|"optional · ~2.0.0"|`,
		`"auth-pacto"`, `"cache-pacto"`,
		// no graph → dependency table renders but no per-dep detail
		"## 3. Dependencies",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n%s", want, md)
		}
	}
	if strings.Contains(md, "<details>") {
		t.Errorf("did not expect per-dependency detail without a graph:\n%s", md)
	}
}

// ── Rich graph: subgraphs, edges, per-dependency detail ────────────────

func TestGenerate_WithGraph(t *testing.T) {
	rootC := &contract.Contract{
		Service:      contract.Service{Name: "frontend", Version: "1.0.0"},
		Interfaces:   []contract.Interface{{Name: "http", Type: "openapi", Ref: "interfaces/openapi.yaml", Visibility: "public"}},
		Workload:     "service",
		Capabilities: []contract.Capability{{Type: "health"}},
	}
	backendC := &contract.Contract{
		Service:        contract.Service{Name: "backend", Version: "1.0.0"},
		Interfaces:     []contract.Interface{{Name: "api", Type: "openapi", Ref: "interfaces/openapi.yaml", Visibility: "public"}},
		Configurations: []contract.Configuration{{Name: "default", Schema: "configuration/schema.json"}},
		Dependencies:   []contract.Dependency{{Name: "postgres", Ref: "reg/postgres:16", Required: true, Compatibility: "^16.0.0"}},
		Workload:       "service",
		State:          &contract.State{Type: "stateless", DataCriticality: "low"},
	}
	backendFS := fstest.MapFS{
		"interfaces/openapi.yaml": &fstest.MapFile{Data: []byte(`
openapi: "3.0.0"
paths:
  /items:
    get:
      summary: List items
`)},
		"configuration/schema.json": &fstest.MapFile{Data: []byte(`{
  "type": "object",
  "properties": { "PORT": { "type": "integer", "default": 8080 } }
}`)},
	}
	postgresC := &contract.Contract{
		Service:  contract.Service{Name: "postgres", Version: "16.0.0"},
		Workload: "service",
		State:    &contract.State{Type: "stateful", DataCriticality: "high", Persistence: contract.Persistence{Scope: "local", Durability: "persistent"}},
	}
	keycloakC := &contract.Contract{
		Service:    contract.Service{Name: "keycloak", Version: "26.0.0"},
		Interfaces: []contract.Interface{{Name: "http", Type: "openapi", Ref: "openapi.yaml", Visibility: "public"}},
		Workload:   "service",
		State:      &contract.State{Type: "stateless", DataCriticality: "low"},
	}
	utilsC := &contract.Contract{Service: contract.Service{Name: "utils", Version: "1.0.0"}}

	postgresNode := &graph.Node{Name: "postgres", Version: "16.0.0", Contract: postgresC}
	gr := &graph.Result{
		Root: &graph.Node{
			Name: "frontend", Version: "1.0.0", Contract: rootC,
			Dependencies: []graph.Edge{
				{Ref: "reg/backend:1.0.0", Node: &graph.Node{
					Name: "backend", Version: "1.0.0", Contract: backendC, FS: backendFS,
					Dependencies: []graph.Edge{
						{Ref: "reg/postgres:16", Node: postgresNode},
						{Ref: "reg/keycloak:26", Shared: true, Node: &graph.Node{Name: "keycloak", Version: "26.0.0"}}, // contract nil → skipped in flat walk
					},
				}},
				{Ref: "reg/keycloak:26", Node: &graph.Node{
					Name: "keycloak", Version: "26.0.0", Contract: keycloakC,
					Dependencies: []graph.Edge{
						{Ref: "reg/postgres:16", Node: postgresNode}, // seen → skipped in flat walk
					},
				}},
				{Ref: "reg/utils:1", Node: &graph.Node{Name: "utils", Version: "1.0.0", Contract: utilsC}},
				{Ref: "reg/missing:1", Node: nil}, // nil node → skipped
			},
		},
	}

	d := &dashboard.ServiceDetails{
		Service:      dashboard.Service{Name: "frontend", Version: "1.0.0"},
		Dependencies: []dashboard.DependencyInfo{{Name: "backend", Ref: "reg/backend:1.0.0", Required: true, Compatibility: "^1.0.0"}},
	}

	md, err := Generate(d, gr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain := []string{
		// subgraphs from graph nodes
		`subgraph frontend["frontend v1.0.0"]`,
		`subgraph backend["backend v1.0.0"]`,
		`subgraph postgres["postgres v16.0.0"]`,
		`postgres_state[("stateful · high criticality · local persistent")]`,
		`subgraph keycloak["keycloak v26.0.0"]`,
		`utils_info["utils"]`, // utils has no state/interfaces → info node
		// external + arrows
		`external(["External User"])`,
		"external --> frontend_iface_http",
		// transitive edges
		"frontend --> backend",
		"frontend --> keycloak",
		"frontend --> utils",
		"backend --> postgres",
		"backend --> keycloak",
		"keycloak --> postgres",
		// per-dependency collapsible detail
		"<details>",
		"<summary><strong>backend</strong> <code>v1.0.0</code></summary>",
		"**Runtime**",
		"**Interfaces**",
		"| `api` | `openapi` | `public` |",
		"**Configuration**",
		"| `PORT` | `integer` | `8080` |",
		"**Dependencies**",
		"<summary><strong>utils</strong> <code>v1.0.0</code></summary>",
	}
	for _, s := range mustContain {
		if !strings.Contains(md, s) {
			t.Errorf("expected %q in output:\n%s", s, md)
		}
	}
}

// ── Small unit / edge-case tests ───────────────────────────────────────

func TestBuildStateLabel_NoPersistence(t *testing.T) {
	c := &contract.Contract{
		State: &contract.State{Type: "stateless", DataCriticality: "low"},
	}
	label := buildStateLabel(c)
	if !strings.Contains(label, "stateless") || !strings.Contains(label, "low") {
		t.Errorf("expected basic state label, got %q", label)
	}
	if strings.Contains(label, "persistent") || strings.Contains(label, "shared") {
		t.Errorf("did not expect persistence in label without persistence set, got %q", label)
	}
}

func TestBuildStateLabel_NilState(t *testing.T) {
	c := &contract.Contract{Service: contract.Service{Name: "pure-reference", Version: "1.0.0"}}
	label := buildStateLabel(c)
	if label != "" {
		t.Errorf("expected empty label for nil state, got %q", label)
	}
}

func TestGenerate_DuplicateEdges(t *testing.T) {
	depNode := &graph.Node{Name: "dep", Version: "1.0.0"}
	gr := &graph.Result{
		Root: &graph.Node{
			Name: "svc", Version: "1.0.0",
			Contract: &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
			Dependencies: []graph.Edge{
				{Ref: "reg/dep:1.0.0", Node: depNode},
				{Ref: "reg/dep:1.0.0", Node: depNode, Shared: true},
			},
		},
	}
	d := &dashboard.ServiceDetails{Service: dashboard.Service{Name: "svc", Version: "1.0.0"}}
	md, err := Generate(d, gr)
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(md, "svc --> dep"); c != 1 {
		t.Errorf("expected 1 'svc --> dep', got %d", c)
	}
}

func TestGenerate_NilEdgeNode(t *testing.T) {
	gr := &graph.Result{
		Root: &graph.Node{
			Name: "svc", Version: "1.0.0",
			Contract:     &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}},
			Dependencies: []graph.Edge{{Ref: "reg/missing:1.0.0", Node: nil, Error: "not found"}},
		},
	}
	d := &dashboard.ServiceDetails{Service: dashboard.Service{Name: "svc", Version: "1.0.0"}}
	md, err := Generate(d, gr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "```mermaid") {
		t.Errorf("expected mermaid block:\n%s", md)
	}
}

func TestWalkMermaidEdges_NilNode(t *testing.T) {
	var b strings.Builder
	walkMermaidEdges(&b, nil, map[string]bool{})
	if b.Len() != 0 {
		t.Errorf("expected empty output for nil node, got %q", b.String())
	}
}

func TestCollectAllContracts_NilGraph(t *testing.T) {
	c := &contract.Contract{Service: contract.Service{Name: "svc", Version: "1.0.0"}}
	all := collectAllContracts(c, nil)
	if len(all) != 1 || all[0].Service.Name != "svc" {
		t.Errorf("expected single contract for nil graph, got %d", len(all))
	}
}

func TestCollectFlatDependencyNodes_NilGraph(t *testing.T) {
	if nodes := collectFlatDependencyNodes(nil); nodes != nil {
		t.Errorf("expected nil for nil graph, got %v", nodes)
	}
}

func TestDepName(t *testing.T) {
	tests := []struct{ ref, want string }{
		{"ghcr.io/acme/auth-service-pacto@sha256:abc123", "auth-service-pacto"},
		{"ghcr.io/acme/notification-service-pacto:1.0.0", "notification-service-pacto"},
		{"simple-ref", "simple-ref"},
	}
	for _, tt := range tests {
		if got := depName(tt.ref); got != tt.want {
			t.Errorf("depName(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestSanitizeMermaidID(t *testing.T) {
	tests := []struct{ in, want string }{
		{"rest-api", "restapi"},
		{"ghcr.io/acme/svc@sha256:abc", "ghcrioacmesvcsha256abc"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		if got := sanitizeMermaidID(tt.in); got != tt.want {
			t.Errorf("sanitizeMermaidID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCapitalizeFirst(t *testing.T) {
	tests := []struct{ in, want string }{{"hello", "Hello"}, {"", ""}, {"Hello", "Hello"}}
	for _, tt := range tests {
		if got := capitalizeFirst(tt.in); got != tt.want {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHeadingAnchor(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1. Runtime & operations", "1-runtime--operations"},
		{"Simple Title", "simple-title"},
		{"Section: With.Dots", "section-withdots"},
	}
	for _, tt := range tests {
		if got := headingAnchor(tt.in); got != tt.want {
			t.Errorf("headingAnchor(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── TOC helpers (shared with TestGenerate_TOCInSync) ───────────────────

func tocAnchors(md string) []string {
	var anchors []string
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "- [") {
			continue
		}
		i := strings.Index(t, "](#")
		if i < 0 {
			continue
		}
		rest := t[i+3:]
		j := strings.Index(rest, ")")
		if j < 0 {
			continue
		}
		anchors = append(anchors, rest[:j])
	}
	return anchors
}

func headingAnchorsIn(md string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimLeft(line, "#")
		if t == line || t == "" {
			continue
		}
		out[headingAnchor(strings.TrimSpace(t))] = true
	}
	return out
}
