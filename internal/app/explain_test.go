package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// writeReadinessBundle writes a pactoVersion 1.2 bundle with a readiness section
// (one current check, one expired check) and returns its directory.
func writeReadinessBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := []byte(`pactoVersion: "2.0"
service:
  name: payment-api
  version: "1.4.0"
  owner:
    team: payments-team
interfaces:
  - name: api
    type: openapi
    ref: openapi.yaml
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
capabilities:
  - type: health
readiness:
  expires: "2026-01-15"
  history:
    - date: "2026-06-21"
      version: "2.1.0"
      author: ed
      description: initial assessment
  claims:
    - id: dashboard
      type: url
      status: done
      evidence: https://grafana.company.com/payment-api
      weight: 60
      description: Main production dashboard
    - id: security-review
      type: ticket
      status: done
      evidence: SEC-1842
      weight: 40
`)
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openapi.yaml"), []byte("openapi: \"3.0.0\"\ninfo:\n  title: API\n  version: 1.0.0\npaths: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pinnedService is a Service whose readiness clock reads at.
func pinnedService(at time.Time) *Service {
	return &Service{sys: sysDeps{now: func() time.Time { return at }}}
}

func TestExplain_WithReadiness(t *testing.T) {
	path := writeReadinessBundle(t)
	svc := pinnedService(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Readiness == nil {
		t.Fatal("expected readiness summary to be present")
	}
	r := result.Readiness
	type field struct {
		label string
		got   any
		want  any
	}
	fields := []field{
		{"totalWeight", r.TotalWeight, 100},
		{"expired", r.Expired, true},
		{"earnedWeight", r.EarnedWeight, 0},
		{"score", r.Score, 0},
		{"minScore", r.MinScore, 100},
		{"passing", r.Passing, false},
		{"doneCount", r.DoneCount, 2},
		{"notDoneCount", r.NotDoneCount, 0},
		{"checks length", len(r.Checks), 2},
	}
	for _, f := range fields {
		if f.got != f.want {
			t.Errorf("%s: got %v, want %v", f.label, f.got, f.want)
		}
	}
	if len(r.Checks) > 0 && (r.Checks[0].ID != "dashboard" || r.Checks[0].Status != "done") {
		t.Errorf("unexpected first check: %+v", r.Checks[0])
	}
	if len(r.Checks) > 1 && r.Checks[1].Status != "done" {
		t.Errorf("expected second check done, got %s", r.Checks[1].Status)
	}
	if len(r.Revisions) != 1 || r.Revisions[0].Author != "ed" || r.Revisions[0].Version != "2.1.0" {
		t.Errorf("expected mapped revision history, got %+v", r.Revisions)
	}
}

func TestExplain_WithoutReadiness(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Readiness != nil {
		t.Errorf("expected no readiness summary, got %+v", result.Readiness)
	}
}

func TestExplain_Local(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test-svc" {
		t.Errorf("expected Name=test-svc, got %s", result.Name)
	}
	if result.Version != "1.0.0" {
		t.Errorf("expected Version=1.0.0, got %s", result.Version)
	}
	if result.PactoVersion != "2.0" {
		t.Errorf("expected PactoVersion=2.0, got %s", result.PactoVersion)
	}
	if result.Workload != "service" {
		t.Errorf("expected Workload=service, got %s", result.Workload)
	}
}

func TestExplain_WithInterfaces(t *testing.T) {
	path := writeTestBundle(t)
	svc := NewService(nil, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(result.Interfaces))
	}
	iface := result.Interfaces[0]
	if iface.Name != "api" {
		t.Errorf("expected interface Name=api, got %s", iface.Name)
	}
	if iface.Type != "openapi" {
		t.Errorf("expected interface Type=openapi, got %s", iface.Type)
	}
	if iface.Ref != "openapi.yaml" {
		t.Errorf("expected interface Ref=openapi.yaml, got %s", iface.Ref)
	}
}

func TestExplain_OCIRef(t *testing.T) {
	store := &mockBundleStore{}
	svc := NewService(store, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Name != "test-svc" {
		t.Errorf("expected Name=test-svc, got %s", result.Name)
	}
}

func TestExplain_WithDependencies(t *testing.T) {
	store := &mockBundleStore{
		PullFn: func(_ context.Context, _ string) (*contract.Bundle, error) {
			b := testBundle()
			b.Contract.Dependencies = []contract.Dependency{
				{Ref: "ghcr.io/acme/dep:1.0.0", Required: true, Compatibility: "^1.0.0"},
			}
			return b, nil
		},
	}
	svc := NewService(store, nil)
	result, err := svc.Explain(context.Background(), ExplainOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(result.Dependencies))
	}
	dep := result.Dependencies[0]
	if dep.Ref != "ghcr.io/acme/dep:1.0.0" {
		t.Errorf("expected Ref=ghcr.io/acme/dep:1.0.0, got %s", dep.Ref)
	}
	if !dep.Required {
		t.Error("expected Required=true")
	}
}

func TestExplain_NotFound(t *testing.T) {
	svc := NewService(nil, nil)
	_, err := svc.Explain(context.Background(), ExplainOptions{Path: "/nonexistent/pacto.yaml"})
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestExplain_WithEnvironments(t *testing.T) {
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: writeEnvironmentBundle(t, "")})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Environments) != 1 || result.Environments[0].Name != "production" || result.Environments[0].File != "environments/production.yaml" {
		t.Fatalf("environments = %+v", result.Environments)
	}
}

func TestExplain_WithGuaranteesEpochAndRequires(t *testing.T) {
	dir := t.TempDir()
	doc := `pactoVersion: "2.2"
service: {name: checkout, version: 1.0.0}
interfaces:
  - {name: events, type: asyncapi, ref: events.yaml, compatibilityEpoch: 2, guarantees: {delivery: at-least-once}}
dependencies:
  - {name: orders, ref: oci://example.com/orders, compatibility: ^1.0.0, required: true, requires: [{interface: http, idempotent: true}]}
`
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.yaml"), []byte("asyncapi: 3.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	iface := r.Interfaces[0]
	if iface.CompatibilityEpoch != 2 || iface.Guarantees == nil || iface.Guarantees.Delivery != "at-least-once" {
		t.Errorf("interface = %+v", iface)
	}
	if req := r.Dependencies[0].Requires; len(req) != 1 || req[0].Interface != "http" || !req[0].Idempotent {
		t.Errorf("requires = %+v", req)
	}
}

func TestExplain_Domain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.4\"\nservice:\n  name: svc\n  version: 1.0.0\n  domain: payments\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Domain != "payments" {
		t.Errorf("Domain = %q", result.Domain)
	}
}

func TestExplain_OfferingAndSummary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.4\"\nservice:\n  name: svc\n  version: 1.0.0\n  offering: postgresql\n  summary: Primary database\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Offering != "postgresql" || result.Summary != "Primary database" {
		t.Errorf("Offering = %q, Summary = %q", result.Offering, result.Summary)
	}
}

func TestExplain_Lifecycle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.4\"\nservice:\n  name: svc\n  version: 1.0.0\n  lifecycle:\n    deprecated: \"2026-01-01\"\n    sunset: \"2027-06-30\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l := result.Lifecycle; l == nil || l.String() != "deprecated 2026-01-01, sunset 2027-06-30" {
		t.Errorf("Lifecycle = %+v", l)
	}
}

func TestExplain_TemplateAndOrigin(t *testing.T) {
	dir := t.TempDir()
	digest := "sha256:" + strings.Repeat("a", 64)
	yaml := "pactoVersion: \"2.4\"\nservice:\n  name: svc\n  version: 1.0.0\n  template:\n    plugins:\n      - name: dockerfile\n" +
		"  origin:\n    ref: oci://ghcr.io/acme/http-service:1.0.0\n    digest: " + digest + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tpl := result.Template; tpl == nil || len(tpl.Plugins) != 1 || tpl.Plugins[0].Name != "dockerfile" {
		t.Errorf("Template = %+v", tpl)
	}
	if o := result.Origin; o == nil || o.Ref != "oci://ghcr.io/acme/http-service:1.0.0" || o.Digest != digest {
		t.Errorf("Origin = %+v", o)
	}
}

// writeRequestBundle writes a 2.4 consumer whose configuration reference
// requests values, with two environment files merged over them.
func writeRequestBundle(t *testing.T, staging string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"pacto.yaml": `pactoVersion: "2.4"
service:
  name: orders
  version: 1.0.0
configurations:
  - name: database
    ref: oci://ghcr.io/acme/postgresql:16.1.0
    configuration: provisioning
    required: true
    values:
      size: medium
      backups: { enabled: true, retentionDays: 7 }
environments:
  - name: production
    file: environments/production.yaml
  - name: staging
    file: environments/staging.yaml
`,
		"environments/production.yaml": "database:\n  backups:\n    retentionDays: 14\n",
		"environments/staging.yaml":    staging,
	}
	for name, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Explain gives an adapter each environment's effective request: the inline
// values with the environment's file deep-merged on top, a null deleting.
func TestExplain_ConfigurationEnvironmentValues(t *testing.T) {
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: writeRequestBundle(t, "database:\n  backups: null\n")})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Configurations) != 1 {
		t.Fatalf("configurations = %+v", result.Configurations)
	}
	c := result.Configurations[0]
	if c.Ref != "oci://ghcr.io/acme/postgresql:16.1.0" || c.Configuration != "provisioning" || !c.Required {
		t.Errorf("configuration = %+v", c)
	}
	got, _ := json.Marshal(c.EnvironmentValues)
	want := `{"production":{"backups":{"enabled":true,"retentionDays":14},"size":"medium"},"staging":{"size":"medium"}}`
	if string(got) != want {
		t.Errorf("environmentValues = %s, want %s", got, want)
	}
	// The inline values are the request where no environment applies, untouched
	// by the merges.
	if inline, _ := json.Marshal(c.Values); string(inline) != `{"backups":{"enabled":true,"retentionDays":7},"size":"medium"}` {
		t.Errorf("values = %s", inline)
	}
	if s := c.Summary(); s != "database: oci://ghcr.io/acme/postgresql:16.1.0, configuration provisioning (required)" {
		t.Errorf("summary = %q", s)
	}
	if lines := c.ValueLines(); len(lines) != 3 || lines[2] != `staging: {"size":"medium"}` {
		t.Errorf("value lines = %q", lines)
	}
}

// A values file that cannot be read fails the explain rather than reporting
// the inline values alone as the request.
func TestExplain_UnreadableEnvironmentFile(t *testing.T) {
	_, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: writeRequestBundle(t, "database: [unclosed\n")})
	if err == nil || !strings.Contains(err.Error(), "environment staging: cannot read environments/staging.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestExplainConfiguration_SchemaSummary(t *testing.T) {
	c := ExplainConfiguration{Name: "chart", Schema: "chart/values.schema.json"}
	if s := c.Summary(); s != "chart: chart/values.schema.json (optional)" {
		t.Errorf("summary = %q", s)
	}
	if lines := c.ValueLines(); len(lines) != 0 {
		t.Errorf("value lines = %q", lines)
	}
}

func TestExplainConfigurations_NoBundleFiles(t *testing.T) {
	c := &contract.Contract{
		Configurations: []contract.Configuration{{Name: "database", Values: map[string]any{"size": "medium"}}},
		Environments:   []contract.Environment{{Name: "production", File: "environments/production.yaml"}},
	}
	if _, err := explainConfigurations(c, nil); err == nil || !strings.Contains(err.Error(), "no bundle files") {
		t.Fatalf("err = %v", err)
	}
	c.Environments = nil
	got, err := explainConfigurations(c, nil)
	if err != nil || len(got) != 1 || got[0].EnvironmentValues != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// Explain compares every declared environment, or the ones named.
func TestExplainEnvironmentDifferences(t *testing.T) {
	dir := writeRequestBundle(t, "database:\n  size: small\n")
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(result.EnvironmentDifferences)
	want := `[{"configuration":"database","path":"backups.retentionDays","values":{"production":14,"staging":7}},` +
		`{"configuration":"database","path":"size","values":{"production":"medium","staging":"small"}}]`
	if string(got) != want || strings.Join(result.ComparedEnvironments, ",") != "production,staging" {
		t.Fatalf("differences = %s, compared %v", got, result.ComparedEnvironments)
	}
	result, err = NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir, Environments: []string{"staging", "production"}})
	if err != nil || len(result.EnvironmentDifferences) != 2 || strings.Join(result.ComparedEnvironments, ",") != "staging,production" {
		t.Fatalf("named: %+v, %v", result, err)
	}
	if _, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir, Environments: []string{"nope", "staging"}}); err == nil ||
		!strings.Contains(err.Error(), "declared: production, staging") {
		t.Fatalf("an undeclared name fails explain: %v", err)
	}
}

// Without names, a comparison that cannot run is reported and explain still
// answers.
func TestExplainDifferencesErrorWithoutNames(t *testing.T) {
	dir := t.TempDir()
	content := "pactoVersion: \"2.3\"\nservice:\n  name: svc\n  version: 1.0.0\nenvironments:\n  - name: a\n    file: a.yaml\n  - name: b\n    file: b.yaml\n"
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir})
	if err != nil || !strings.Contains(result.EnvironmentDifferencesError, "environment a: cannot read a.yaml") || result.EnvironmentDifferences != nil {
		t.Fatalf("got %+v, %v", result, err)
	}
	if _, err := NewService(nil, nil).Explain(context.Background(), ExplainOptions{Path: dir, Environments: []string{"a", "b"}}); err == nil {
		t.Fatal("with names, the error fails explain")
	}
}
