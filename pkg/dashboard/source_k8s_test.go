package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v2/pkg/contract"
)

// ---------------------------------------------------------------------------
// serviceFromK8sStatus
// ---------------------------------------------------------------------------

func TestK8s_serviceFromK8sStatus_Minimal(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "my-svc"
	r.Status.ContractStatus = "Compliant"

	svc := serviceFromK8sStatus(r)

	if svc.Name != "my-svc" {
		t.Errorf("expected name 'my-svc', got %q", svc.Name)
	}
	if svc.ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant, got %q", svc.ContractStatus)
	}
	if svc.Source != "k8s" {
		t.Errorf("expected source 'k8s', got %q", svc.Source)
	}
	if svc.Version != "" {
		t.Errorf("expected empty version, got %q", svc.Version)
	}
	if !svc.Owner.IsEmpty() {
		t.Errorf("expected empty owner, got %q", svc.Owner.DisplayString())
	}
}

func TestK8s_serviceFromK8sStatus_WithContract(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "k8s-name"
	r.Status.ContractStatus = "Warning"
	r.Status.Contract = &k8sContractInfo{
		ServiceName: "api-gateway",
		Version:     "2.0.0",
		Owner:       contract.Owner{Team: "platform-team"},
	}

	svc := serviceFromK8sStatus(r)

	if svc.Name != "api-gateway" {
		t.Errorf("expected name 'api-gateway', got %q", svc.Name)
	}
	if svc.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", svc.Version)
	}
	if svc.Owner.DisplayString() != "platform-team" {
		t.Errorf("expected owner 'platform-team', got %q", svc.Owner.DisplayString())
	}
}

func TestK8s_serviceFromK8sStatus_StructuredOwner(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "k8s-name"
	r.Status.ContractStatus = "Compliant"
	r.Status.Contract = &k8sContractInfo{
		ServiceName: "api-gateway",
		Version:     "2.0.0",
		Owner:       contract.Owner{Team: "foundations", DRI: "alice"},
	}

	svc := serviceFromK8sStatus(r)

	if svc.Owner.DisplayString() != "foundations" {
		t.Errorf("expected owner display 'foundations', got %q", svc.Owner.DisplayString())
	}
	if svc.Owner.DRI != "alice" {
		t.Errorf("expected dri 'alice', got %q", svc.Owner.DRI)
	}
}

func TestK8s_serviceFromK8sStatus_StructuredOwnerFromJSON(t *testing.T) {
	// Simulate the actual K8s API path: JSON unmarshal of operator payload.
	payload := `{
		"metadata": {"name": "k8s-name"},
		"status": {
			"contractStatus": "Compliant",
			"contract": {
				"serviceName": "billing",
				"version": "1.0.0",
				"owner": {
					"team": "payments",
					"dri": "bob.smith",
					"contacts": [
						{"type": "email", "value": "payments@acme.com", "purpose": "ownership"},
						{"type": "chat", "value": "#payments"}
					]
				}
			}
		}
	}`
	var r pactoResource
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	svc := serviceFromK8sStatus(r)

	if svc.Owner.DisplayString() != "payments" {
		t.Errorf("expected display 'payments', got %q", svc.Owner.DisplayString())
	}
	if svc.Owner.DRI != "bob.smith" {
		t.Errorf("expected dri 'bob.smith', got %q", svc.Owner.DRI)
	}
	contacts := svc.Owner.Contacts
	if len(contacts) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(contacts))
	}
	if contacts[0].Type != "email" || contacts[0].Value != "payments@acme.com" || contacts[0].Purpose != "ownership" {
		t.Errorf("contact[0] mismatch: %+v", contacts[0])
	}
	if contacts[1].Type != "chat" || contacts[1].Value != "#payments" {
		t.Errorf("contact[1] mismatch: %+v", contacts[1])
	}
}

func TestK8s_serviceFromK8sStatus_LegacyStringOwnerFromJSON(t *testing.T) {
	// String owner format is no longer supported - expect unmarshal to fail.
	payload := `{
		"metadata": {"name": "svc"},
		"status": {
			"contractStatus": "Compliant",
			"contract": {
				"serviceName": "svc",
				"version": "1.0.0",
				"owner": "team/platform"
			}
		}
	}`
	var r pactoResource
	err := json.Unmarshal([]byte(payload), &r)
	if err == nil {
		t.Fatal("expected unmarshal to fail for string owner, but it succeeded")
	}
}

func TestK8s_serviceFromK8sStatus_EmptyOwnerFromJSON(t *testing.T) {
	// Confirm missing owner field works.
	payload := `{
		"metadata": {"name": "svc"},
		"status": {
			"contractStatus": "Compliant",
			"contract": {
				"serviceName": "svc",
				"version": "1.0.0"
			}
		}
	}`
	var r pactoResource
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	svc := serviceFromK8sStatus(r)

	if !svc.Owner.IsEmpty() {
		t.Errorf("expected empty owner, got %q", svc.Owner.DisplayString())
	}
}

func TestK8s_serviceFromK8sStatus_NullOwnerFromJSON(t *testing.T) {
	// Confirm explicit null owner works.
	payload := `{
		"metadata": {"name": "svc"},
		"status": {
			"contractStatus": "Compliant",
			"contract": {
				"serviceName": "svc",
				"version": "1.0.0",
				"owner": null
			}
		}
	}`
	var r pactoResource
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	svc := serviceFromK8sStatus(r)

	if !svc.Owner.IsEmpty() {
		t.Errorf("expected empty owner for null, got %q", svc.Owner.DisplayString())
	}
}

func TestK8s_serviceDetailsFromK8sStatus_StructuredOwnerPreserved(t *testing.T) {
	// Verify structured owner flows through to ServiceDetails (the full detail path).
	payload := `{
		"metadata": {"name": "svc", "namespace": "prod"},
		"status": {
			"contractStatus": "Compliant",
			"contract": {
				"serviceName": "billing",
				"version": "2.0.0",
				"owner": {"team": "finops", "dri": "carol"},
				"imageRef": "ghcr.io/org/billing:2.0.0"
			}
		}
	}`
	var r pactoResource
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	details := serviceDetailsFromK8sStatus(&r)

	if details.Owner.DisplayString() != "finops" {
		t.Errorf("expected 'finops', got %q", details.Owner.DisplayString())
	}
	if details.Owner.DRI != "carol" {
		t.Errorf("expected dri 'carol', got %q", details.Owner.DRI)
	}

	// Verify JSON marshaling preserves structure.
	data, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal output failed: %v", err)
	}
	ownerMap, ok := m["owner"].(map[string]any)
	if !ok {
		t.Fatalf("expected owner to be object in JSON, got %T: %v", m["owner"], m["owner"])
	}
	if ownerMap["team"] != "finops" {
		t.Errorf("expected team=finops in JSON, got %v", ownerMap["team"])
	}
	if ownerMap["dri"] != "carol" {
		t.Errorf("expected dri=carol in JSON, got %v", ownerMap["dri"])
	}
}

func TestK8s_serviceFromK8sStatus_ContractVersionOverride(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "svc"
	r.Status.Contract = &k8sContractInfo{Version: "1.0.0"}
	r.Status.ContractVersion = "3.0.0"

	svc := serviceFromK8sStatus(r)

	// ContractVersion takes precedence over Contract.Version.
	if svc.Version != "3.0.0" {
		t.Errorf("expected version '3.0.0', got %q", svc.Version)
	}
}

func TestK8s_serviceFromK8sStatus_EmptyStatusDefaultsToUnknown(t *testing.T) {
	r := pactoResource{}
	r.Metadata.Name = "svc"

	svc := serviceFromK8sStatus(r)

	if svc.ContractStatus != StatusUnknown {
		t.Errorf("expected Unknown, got %q", svc.ContractStatus)
	}
}

// ---------------------------------------------------------------------------
// serviceDetailsFromK8sStatus — comprehensive
// ---------------------------------------------------------------------------

func TestK8s_serviceDetailsFromK8sStatus_Comprehensive(t *testing.T) {
	d := buildComprehensiveK8sDetails(t)
	assertDetailsServiceFields(t, d)
	assertDetailsInterfaces(t, d)
	assertDetailsConfig(t, d)
	assertDetailsPolicy(t, d)
	assertDetailsDeps(t, d)
	assertDetailsRuntime(t, d)
	assertDetailsScaling(t, d)
	assertDetailsValidation(t, d)
	assertDetailsResources(t, d)
	assertDetailsPorts(t, d)
	assertDetailsConditions(t, d)
	assertDetailsEndpoints(t, d)
	assertDetailsInsights(t, d)
	assertDetailsChecksSummary(t, d)
	assertDetailsReadiness(t, d)
}

func assertDetailsReadiness(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Readiness == nil {
		t.Fatal("expected readiness")
	}
	r := d.Readiness
	type check struct {
		label string
		got   any
		want  any
	}
	checks := []check{
		{"score", r.Score, 71},
		{"totalWeight", r.TotalWeight, 85},
		{"earnedWeight", r.EarnedWeight, 60},
		{"minScore", r.MinScore, 80},
		{"passing", r.Passing, false},
		{"expires", r.Expires, "2026-12-31"},
		{"expired", r.Expired, false},
		{"doneCount", r.DoneCount, 1},
		{"notDoneCount", r.NotDoneCount, 1},
		{"deferredCount", r.DeferredCount, 1},
		{"checks length", len(r.Checks), 3},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.label, c.got, c.want)
		}
	}
	if r.DaysRemaining == nil {
		t.Error("expected daysRemaining to be non-nil")
	}
	if len(r.Checks) > 0 && (r.Checks[0].Status != "done" || r.Checks[0].Category != "observability" || r.Checks[0].EarnedWeight != 60) {
		t.Errorf("expected first check done observability earned 60, got %+v", r.Checks[0])
	}
	if len(r.Checks) > 2 && (r.Checks[2].Status != "deferred" || !r.Checks[2].Excluded) {
		t.Errorf("expected third check deferred and excluded, got %+v", r.Checks[2])
	}
	if len(r.Revisions) != 1 || r.Revisions[0].Author != "ed" {
		t.Errorf("expected mapped revision, got %+v", r.Revisions)
	}
}

func TestK8s_readinessFromK8s_Nil(t *testing.T) {
	if got := readinessFromK8s(nil); got != nil {
		t.Errorf("expected nil for nil readiness, got %+v", got)
	}
}

func assertDetailsServiceFields(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Name != "billing" {
		t.Errorf("name: got %q", d.Name)
	}
	if d.Version != "1.2.3" {
		t.Errorf("version: got %q", d.Version)
	}
	if d.Owner.DisplayString() != "payments" {
		t.Errorf("owner: got %q", d.Owner.DisplayString())
	}
	if d.ContractStatus != StatusCompliant {
		t.Errorf("contractStatus: got %q", d.ContractStatus)
	}
	// ImageRef removed in v2
	if d.Metadata["team"] != "platform" {
		t.Errorf("metadata team: got %q", d.Metadata["team"])
	}
	if d.LastReconciledAt == "" || !strings.HasSuffix(d.LastReconciledAt, "ago") {
		t.Errorf("lastReconciledAt: got %q", d.LastReconciledAt)
	}
}

func assertDetailsInterfaces(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Interfaces) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(d.Interfaces))
	}
	iface := d.Interfaces[0]
	// Port removed in v2
	if iface.Name != "http" || iface.Type != "http" || iface.Visibility != "public" || !iface.HasContractFile {
		t.Errorf("interface mismatch: %+v", iface)
	}
}

func assertDetailsConfig(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Configurations) != 1 {
		t.Fatal("expected 1 configuration entry")
	}
	cfg := d.Configurations[0]
	if cfg.Name != "default" {
		t.Errorf("config name: got %q, want %q", cfg.Name, "default")
	}
	if !cfg.HasSchema || cfg.Ref != "config-ref" {
		t.Errorf("configuration mismatch: %+v", cfg)
	}
	if len(cfg.ValueKeys) != 1 || cfg.ValueKeys[0] != "key1" {
		t.Errorf("config valueKeys: %v", cfg.ValueKeys)
	}
}

func assertDetailsPolicy(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Policies) != 1 {
		t.Fatal("expected 1 policy entry")
	}
	pol := d.Policies[0]
	if pol.Name != "security" {
		t.Errorf("policy name: got %q, want %q", pol.Name, "security")
	}
	if !pol.HasSchema || pol.Schema != "policy.json" || pol.Ref != "policy-ref" {
		t.Errorf("policy mismatch: %+v", pol)
	}
}

func assertDetailsDeps(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(d.Dependencies))
	}
	dep := d.Dependencies[0]
	if dep.Name != "auth" {
		t.Errorf("dependency name: got %q, want %q", dep.Name, "auth")
	}
	if dep.Ref != "auth@^1.0.0" || !dep.Required || dep.Compatibility != "strict" {
		t.Errorf("dependency mismatch: %+v", dep)
	}
}

func assertDetailsRuntime(t *testing.T, d *ServiceDetails) {
	t.Helper()
	// Runtime is not in v2 model; provisionally check Workload + State
	if d.Workload != "service" {
		t.Errorf("workload: got %q, want service", d.Workload)
	}
	if d.State == nil || d.State.Type != "stateless" {
		t.Errorf("state: got %+v", d.State)
	}
}

func assertDetailsScaling(t *testing.T, d *ServiceDetails) {
	t.Helper()
	// Scaling removed in v2 model
}

func assertDetailsValidation(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Validation == nil {
		t.Fatal("expected validation")
	}
	if d.Validation.Valid {
		t.Error("expected valid=false")
	}
	if len(d.Validation.Errors) != 1 || d.Validation.Errors[0].Code != "E001" {
		t.Errorf("errors: %v", d.Validation.Errors)
	}
	if len(d.Validation.Warnings) != 1 || d.Validation.Warnings[0].Code != "W001" {
		t.Errorf("warnings: %v", d.Validation.Warnings)
	}
}

func assertDetailsResources(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Resources == nil {
		t.Fatal("expected resources")
	}
	if d.Resources.ServiceExists == nil || !*d.Resources.ServiceExists {
		t.Error("expected serviceExists=true")
	}
	if d.Resources.WorkloadExists == nil || *d.Resources.WorkloadExists {
		t.Error("expected workloadExists=false")
	}
}

func assertDetailsPorts(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.Ports == nil {
		t.Fatal("expected ports")
	}
	if len(d.Ports.Unexpected) != 1 || d.Ports.Unexpected[0] != 9090 {
		t.Errorf("unexpected ports: %v", d.Ports.Unexpected)
	}
}

func assertDetailsConditions(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Conditions) != 1 {
		t.Fatalf("expected 1, got %d", len(d.Conditions))
	}
	cond := d.Conditions[0]
	if cond.Type != "Ready" || cond.Status != "True" || cond.Reason != "AllChecks" {
		t.Errorf("condition mismatch: %+v", cond)
	}
	if cond.LastTransitionAgo == "" || !strings.HasSuffix(cond.LastTransitionAgo, "ago") {
		t.Errorf("lastTransitionAgo: got %q", cond.LastTransitionAgo)
	}
}

func assertDetailsEndpoints(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Endpoints) != 1 {
		t.Fatalf("expected 1, got %d", len(d.Endpoints))
	}
	ep := d.Endpoints[0]
	if ep.Interface != "http" || ep.Type != "health" || ep.URL != "http://billing:8080/healthz" {
		t.Errorf("endpoint mismatch: %+v", ep)
	}
	if ep.Healthy == nil || !*ep.Healthy {
		t.Error("expected healthy=true")
	}
	if ep.StatusCode == nil || *ep.StatusCode != 200 {
		t.Error("expected statusCode=200")
	}
	if ep.LatencyMs == nil || *ep.LatencyMs != 42 {
		t.Error("expected latencyMs=42")
	}
}

func assertDetailsInsights(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if len(d.Insights) != 1 {
		t.Fatalf("expected 1, got %d", len(d.Insights))
	}
	if d.Insights[0].Severity != "warning" || d.Insights[0].Title != "High latency" {
		t.Errorf("insight mismatch: %+v", d.Insights[0])
	}
}

func assertDetailsChecksSummary(t *testing.T, d *ServiceDetails) {
	t.Helper()
	if d.ChecksSummary == nil {
		t.Fatal("expected checksSummary")
	}
	if d.ChecksSummary.Total != 10 || d.ChecksSummary.Passed != 8 || d.ChecksSummary.Failed != 2 {
		t.Errorf("checksSummary mismatch: %+v", d.ChecksSummary)
	}
}

func buildComprehensiveK8sDetails(t *testing.T) *ServiceDetails {
	t.Helper()
	healthy := true
	statusCode := 200
	latency := int64(42)

	r := &pactoResource{}
	r.Metadata.Name = "k8s-name"
	r.Status.ContractStatus = "Compliant"
	r.Status.ContractVersion = "1.2.3"
	r.Status.LastReconciledAt = time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
	r.Status.Contract = &k8sContractInfo{ServiceName: "billing", Version: "1.0.0", Owner: contract.Owner{Team: "payments"}, ResolvedRef: "sha256:abc"}
	r.Status.State = &k8sState{Type: "stateless", PersistenceScope: "local", PersistenceDurability: "ephemeral", DataCriticality: "low"}
	r.Status.Metadata = map[string]string{"team": "platform", "env": "prod"}
	r.Status.Interfaces = flexSlice[k8sInterface]{{Name: "http", Type: "http", Visibility: "public", HasContractFile: true}}
	r.Status.Configurations = flexSlice[k8sConfig]{{Name: "default", HasSchema: true, Ref: "config-ref", ValueKeys: []string{"key1"}, SecretKeys: []string{"secret1"}}}
	r.Status.Policies = flexSlice[k8sPolicy]{{Name: "security", HasSchema: true, Schema: "policy.json", Ref: "policy-ref"}}
	r.Status.Dependencies = flexSlice[k8sDependency]{{Name: "auth", Ref: "auth@^1.0.0", Required: true, Compatibility: "strict"}}
	days := int32(206)
	r.Status.Readiness = &k8sReadiness{
		Score: 71, MinScore: 80, Passing: false, TotalWeight: 85, EarnedWeight: 60, PartialCredit: 0.5,
		Expires: "2026-12-31", Expired: false, DaysRemaining: &days,
		DoneCount: 1, PartialCount: 0, NotDoneCount: 1, DeferredCount: 1,
		Revisions: flexSlice[k8sReadinessRevision]{
			{Date: "2026-06-21", Version: "2.1.0", Author: "ed", Description: "initial"},
		},
		Checks: flexSlice[k8sReadinessCheck]{
			{ID: "dashboard", Type: "url", Category: "observability", Status: "done", Evidence: "https://x", Weight: 60, EarnedWeight: 60},
			{ID: "security-review", Type: "ticket", Status: "not-done", Evidence: "SEC-1", Weight: 25, EarnedWeight: 0},
			{ID: "dr-plan", Type: "document", Status: "deferred", Evidence: "docs/dr.md", Weight: 15, EarnedWeight: 0, Excluded: true},
		},
	}
	r.Status.Validation = &k8sValidation{Valid: false, Errors: []k8sIssue{{Code: "E001", Path: "/service/name", Message: "name is required"}}, Warnings: []k8sIssue{{Code: "W001", Path: "/runtime", Message: "deprecated field"}}}
	r.Status.Resources = &k8sResources{Service: &k8sResourceStatus{Exists: true}, Workload: &k8sResourceStatus{Exists: false}}
	r.Status.Ports = &k8sPorts{Expected: []int{8080}, Observed: []int{8080, 9090}, Missing: nil, Unexpected: []int{9090}}
	condTime := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	r.Status.Conditions = flexSlice[k8sCondition]{{Type: "Ready", Status: "True", Reason: "AllChecks", Message: "all good", LastTransitionTime: condTime}}
	r.Status.Endpoints = k8sEndpoints{{Interface: "http", Type: "health", URL: "http://billing:8080/healthz", Healthy: &healthy, StatusCode: &statusCode, LatencyMs: &latency, Error: "", Message: "OK"}}
	r.Status.Insights = flexSlice[k8sInsight]{{Severity: "warning", Title: "High latency", Description: "p99 > 500ms"}}
	r.Status.Summary = &k8sSummary{Total: 10, Passed: 8, Failed: 2}

	return serviceDetailsFromK8sStatus(r)
}

func TestK8s_serviceDetailsFromK8sStatus_ConfigPolicyContent(t *testing.T) {
	r := &pactoResource{}
	r.Metadata.Name = "redis"
	r.Status.ContractStatus = "Reference"
	r.Status.Configurations = flexSlice[k8sConfig]{{
		Name:      "provisioning",
		HasSchema: true,
		Properties: []k8sSchemaProperty{
			{Key: "maxMemory", Value: "256mb", Type: "string"},
			{Key: "evictionPolicy", Value: "(any)", Type: "string"},
		},
	}}
	r.Status.Policies = flexSlice[k8sPolicy]{{
		Name:        "redis",
		HasSchema:   true,
		Schema:      "policy/schema.json",
		Title:       "Redis Policy",
		Description: "Redis hardening rules",
		Properties: []k8sSchemaProperty{
			{Key: "tlsRequired", Value: "true", Type: "boolean"},
		},
	}}

	d := serviceDetailsFromK8sStatus(r)

	if len(d.Configurations) != 1 || len(d.Configurations[0].Values) != 2 {
		t.Fatalf("expected config with 2 values, got %+v", d.Configurations)
	}
	if d.Configurations[0].Values[0].Key != "maxMemory" || d.Configurations[0].Values[0].Value != "256mb" {
		t.Errorf("config value mismatch: %+v", d.Configurations[0].Values)
	}
	if len(d.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(d.Policies))
	}
	pol := d.Policies[0]
	if pol.Title != "Redis Policy" || pol.Description != "Redis hardening rules" {
		t.Errorf("policy meta mismatch: %+v", pol)
	}
	if len(pol.Values) != 1 || pol.Values[0].Key != "tlsRequired" || pol.Values[0].Type != "boolean" {
		t.Errorf("policy values mismatch: %+v", pol.Values)
	}
}

func TestK8s_serviceDetailsFromK8sStatus_ResolutionPolicy(t *testing.T) {
	tests := []struct {
		name             string
		resolutionPolicy string
		wantPolicy       string
	}{
		{"Latest", "Latest", VersionPolicyTracking},
		{"PinnedTag", "PinnedTag", VersionPolicyPinnedTag},
		{"PinnedDigest", "PinnedDigest", VersionPolicyPinnedDigest},
		{"absent", "", ""},
		{"unknown value", "SomeOther", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &pactoResource{}
			r.Status.ContractStatus = "Compliant"
			r.Status.Contract = &k8sContractInfo{
				ServiceName:      "svc",
				Version:          "1.0.0",
				ResolutionPolicy: tt.resolutionPolicy,
			}
			d := serviceDetailsFromK8sStatus(r)
			if d.VersionPolicy != tt.wantPolicy {
				t.Errorf("resolutionPolicy=%q → versionPolicy=%q, want %q", tt.resolutionPolicy, d.VersionPolicy, tt.wantPolicy)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// timeAgoFromRFC3339
// ---------------------------------------------------------------------------

func TestK8s_timeAgoFromRFC3339_Valid(t *testing.T) {
	ts := time.Now().Add(-30 * time.Second).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.HasSuffix(result, "ago") {
		t.Errorf("expected 'ago' suffix, got %q", result)
	}
	if !strings.Contains(result, "s") {
		t.Errorf("expected seconds unit, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Invalid(t *testing.T) {
	result := timeAgoFromRFC3339("not-a-timestamp")
	if result != "" {
		t.Errorf("expected empty string for invalid input, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Minutes(t *testing.T) {
	ts := time.Now().Add(-5 * time.Minute).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.Contains(result, "m ago") {
		t.Errorf("expected minutes format, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Hours(t *testing.T) {
	ts := time.Now().Add(-3 * time.Hour).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.Contains(result, "h ago") {
		t.Errorf("expected hours format, got %q", result)
	}
}

func TestK8s_timeAgoFromRFC3339_Days(t *testing.T) {
	ts := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	result := timeAgoFromRFC3339(ts)
	if !strings.Contains(result, "d ago") {
		t.Errorf("expected days format, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// flexSlice.UnmarshalJSON
// ---------------------------------------------------------------------------

func TestK8s_flexSlice_Array(t *testing.T) {
	input := `[{"name":"a"},{"name":"b"}]`
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte(input), &fs); err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("expected 2 items, got %d", len(fs))
	}
	if fs[0].Name != "a" || fs[1].Name != "b" {
		t.Errorf("unexpected items: %+v", fs)
	}
}

func TestK8s_flexSlice_SingleObject(t *testing.T) {
	input := `{"name":"only"}`
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte(input), &fs); err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 {
		t.Fatalf("expected 1 item, got %d", len(fs))
	}
	if fs[0].Name != "only" {
		t.Errorf("expected name 'only', got %q", fs[0].Name)
	}
}

func TestK8s_policiesFromJSON_Array(t *testing.T) {
	payload := `{
		"metadata": {"name": "svc"},
		"status": {
			"contractStatus": "Compliant",
			"policies": [
				{"hasSchema": true, "schema": "policy/schema.json"},
				{"hasSchema": false, "ref": "ghcr.io/org/shared-policy:v1"}
			]
		}
	}`
	var r pactoResource
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	d := serviceDetailsFromK8sStatus(&r)
	if len(d.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(d.Policies))
	}
	if !d.Policies[0].HasSchema || d.Policies[0].Schema != "policy/schema.json" {
		t.Errorf("policy[0] mismatch: %+v", d.Policies[0])
	}
	if d.Policies[1].HasSchema || d.Policies[1].Ref != "ghcr.io/org/shared-policy:v1" {
		t.Errorf("policy[1] mismatch: %+v", d.Policies[1])
	}
}

func TestK8s_policiesFromJSON_SingleObject(t *testing.T) {
	payload := `{
		"metadata": {"name": "svc"},
		"status": {
			"contractStatus": "Compliant",
			"policies": {"hasSchema": true, "schema": "policy.json", "ref": "policy-ref"}
		}
	}`
	var r pactoResource
	if err := json.Unmarshal([]byte(payload), &r); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	d := serviceDetailsFromK8sStatus(&r)
	if len(d.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(d.Policies))
	}
	if !d.Policies[0].HasSchema || d.Policies[0].Schema != "policy.json" || d.Policies[0].Ref != "policy-ref" {
		t.Errorf("policy mismatch: %+v", d.Policies[0])
	}
}

func TestK8s_flexSlice_InvalidJSON(t *testing.T) {
	input := `not json`
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte(input), &fs); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestK8s_flexSlice_NullInput(t *testing.T) {
	var fs flexSlice[k8sInterface]
	if err := json.Unmarshal([]byte("null"), &fs); err != nil {
		t.Fatalf("unexpected error for null input: %v", err)
	}
	if len(fs) != 0 {
		t.Errorf("expected empty slice for null input, got %d items", len(fs))
	}
}

func TestK8s_flexSlice_InvalidJSON_ViaStatus(t *testing.T) {
	// Test the error path through full pactoStatus unmarshal to ensure coverage
	// hits the concrete instantiation used in production code.
	input := `{"interfaces": 42}` // 42 is not an array or object
	var status pactoStatus
	if err := json.Unmarshal([]byte(input), &status); err == nil {
		t.Error("expected error for invalid interfaces value")
	}
}

// ---------------------------------------------------------------------------
// k8sEndpoints unmarshal tests
// ---------------------------------------------------------------------------

func TestK8s_k8sEndpoints_Array(t *testing.T) {
	input := `[{"interface":"http","url":"http://svc:8080/health","statusCode":200}]`
	var ep k8sEndpoints
	if err := json.Unmarshal([]byte(input), &ep); err != nil {
		t.Fatal(err)
	}
	if len(ep) != 1 || ep[0].Interface != "http" || ep[0].URL != "http://svc:8080/health" {
		t.Errorf("unexpected: %+v", ep)
	}
}

func TestK8s_k8sEndpoints_SingleObject(t *testing.T) {
	input := `{"interface":"http","url":"http://svc:8080/health"}`
	var ep k8sEndpoints
	if err := json.Unmarshal([]byte(input), &ep); err != nil {
		t.Fatal(err)
	}
	if len(ep) != 1 || ep[0].Interface != "http" {
		t.Errorf("unexpected: %+v", ep)
	}
}

func TestK8s_k8sEndpoints_Map(t *testing.T) {
	input := `{"health":{"url":"http://svc:8080/health","reachable":true,"statusCode":200,"latencyMs":5}}`
	var ep k8sEndpoints
	if err := json.Unmarshal([]byte(input), &ep); err != nil {
		t.Fatal(err)
	}
	if len(ep) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(ep))
	}
	if ep[0].Interface != "health" {
		t.Errorf("expected interface 'health', got %q", ep[0].Interface)
	}
	if ep[0].URL != "http://svc:8080/health" {
		t.Errorf("expected url, got %q", ep[0].URL)
	}
	if ep[0].Healthy == nil || !*ep[0].Healthy {
		t.Error("expected healthy=true")
	}
	if ep[0].StatusCode == nil || *ep[0].StatusCode != 200 {
		t.Error("expected statusCode=200")
	}
	if ep[0].LatencyMs == nil || *ep[0].LatencyMs != 5 {
		t.Error("expected latencyMs=5")
	}
}

func TestK8s_k8sEndpoints_MapMultiple(t *testing.T) {
	input := `{"health":{"url":"http://svc:8080/health"},"metrics":{"url":"http://svc:9090/metrics"}}`
	var ep k8sEndpoints
	if err := json.Unmarshal([]byte(input), &ep); err != nil {
		t.Fatal(err)
	}
	if len(ep) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(ep))
	}
	// Should be sorted alphabetically.
	if ep[0].Interface != "health" || ep[1].Interface != "metrics" {
		t.Errorf("expected sorted [health, metrics], got [%s, %s]", ep[0].Interface, ep[1].Interface)
	}
}

func TestK8s_k8sEndpoints_InvalidJSON(t *testing.T) {
	var ep k8sEndpoints
	if err := json.Unmarshal([]byte(`not json`), &ep); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestK8s_k8sEndpoints_InvalidType(t *testing.T) {
	// Valid JSON that is neither array, map, nor object — triggers final fallback error.
	var ep k8sEndpoints
	if err := json.Unmarshal([]byte(`42`), &ep); err == nil {
		t.Error("expected error for non-object/array JSON value")
	}
}

// ---------------------------------------------------------------------------
// Mock K8sClient for source tests
// ---------------------------------------------------------------------------

type mockK8sClient struct {
	probeErr     error
	crdDiscovery *CRDDiscovery
	crdErr       error
	listJSON     []byte
	listErr      error
	listByRes    map[string][]byte // resource-specific overrides for ListJSON
	listByResErr map[string]error
	getJSON      []byte
	getErr       error
	countResult  int
	countErr     error
}

func (m *mockK8sClient) Probe(context.Context) error { return m.probeErr }
func (m *mockK8sClient) DiscoverCRD(context.Context) (*CRDDiscovery, error) {
	return m.crdDiscovery, m.crdErr
}
func (m *mockK8sClient) ListJSON(_ context.Context, resource, _ string) ([]byte, error) {
	if m.listByRes != nil {
		if err, hasErr := m.listByResErr[resource]; hasErr && err != nil {
			return nil, err
		}
		if data, ok := m.listByRes[resource]; ok {
			return data, nil
		}
	}
	return m.listJSON, m.listErr
}
func (m *mockK8sClient) GetJSON(context.Context, string, string, string) ([]byte, error) {
	return m.getJSON, m.getErr
}
func (m *mockK8sClient) CountResources(context.Context, string, string) (int, error) {
	return m.countResult, m.countErr
}

// ---------------------------------------------------------------------------
// NewK8sSource
// ---------------------------------------------------------------------------

func TestK8s_NewK8sSource_DefaultResourceName(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "")
	if src.resourceName != "pactos" {
		t.Errorf("expected default resource name 'pactos', got %q", src.resourceName)
	}
	if src.namespace != "default" {
		t.Errorf("expected namespace 'default', got %q", src.namespace)
	}
}

func TestK8s_NewK8sSource_CustomResourceName(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "prod", "pactocontracts")
	if src.resourceName != "pactocontracts" {
		t.Errorf("expected resource name 'pactocontracts', got %q", src.resourceName)
	}
	if src.namespace != "prod" {
		t.Errorf("expected namespace 'prod', got %q", src.namespace)
	}
}

// ---------------------------------------------------------------------------
// setListCache
// ---------------------------------------------------------------------------

func TestK8s_setListCache(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos")

	items := []pactoResource{
		{Status: pactoStatus{ContractStatus: "Compliant"}},
	}
	items[0].Metadata.Name = "svc-a"

	src.setListCache(items, nil)

	src.listMu.Lock()
	defer src.listMu.Unlock()

	if len(src.listCache) != 1 {
		t.Fatalf("expected 1 cached item, got %d", len(src.listCache))
	}
	if src.listCache[0].Metadata.Name != "svc-a" {
		t.Errorf("expected cached name 'svc-a', got %q", src.listCache[0].Metadata.Name)
	}
	if src.listErr != nil {
		t.Errorf("expected nil error, got %v", src.listErr)
	}
	if src.listAt.IsZero() {
		t.Error("expected listAt to be set")
	}
}

func TestK8s_setListCache_WithError(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos")

	testErr := fmt.Errorf("connection refused")
	src.setListCache(nil, testErr)

	src.listMu.Lock()
	defer src.listMu.Unlock()

	if src.listCache != nil {
		t.Errorf("expected nil cache, got %v", src.listCache)
	}
	if src.listErr == nil || src.listErr.Error() != "connection refused" {
		t.Errorf("expected 'connection refused', got %v", src.listErr)
	}
}

// ---------------------------------------------------------------------------
// GetVersions — not supported
// ---------------------------------------------------------------------------

func TestK8s_GetVersions_ReturnsEmptyForUnknownService(t *testing.T) {
	client := &mockK8sClient{
		listByRes: map[string][]byte{
			"pactorevisions": []byte(`{"items":[]}`),
		},
		listByResErr: map[string]error{},
	}
	src := NewK8sSource(client, "default", "pactos")
	versions, err := src.GetVersions(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("expected empty versions, got %d", len(versions))
	}
}

// ---------------------------------------------------------------------------
// GetDiff — not supported
// ---------------------------------------------------------------------------

func TestK8s_GetDiff_ReturnsError(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos")
	_, err := src.GetDiff(context.Background(), Ref{Name: "a", Version: "1"}, Ref{Name: "a", Version: "2"})
	if err == nil {
		t.Fatal("expected error from GetDiff")
	}
	if !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestK8s_GetServiceVersion_ReturnsError(t *testing.T) {
	client := &mockK8sClient{}
	src := NewK8sSource(client, "default", "pactos")
	_, err := src.GetServiceVersion(context.Background(), Ref{Name: "a", Version: "1"})
	if err == nil {
		t.Fatal("expected error from GetServiceVersion")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ListServices
// ---------------------------------------------------------------------------

func TestK8s_ListServices(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-b", "namespace": "default"}, "status": {"contractStatus": "Compliant", "contract": {"serviceName": "svc-b", "version": "1.0.0"}}}, {"metadata": {"name": "svc-a"}, "status": {"contractStatus": "Progressing"}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "default", "pactos")
	services, err := src.ListServices(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	// Should be sorted by name.
	if services[0].Name != "svc-a" {
		t.Errorf("expected first service 'svc-a', got %q", services[0].Name)
	}
	if services[1].Name != "svc-b" {
		t.Errorf("expected second service 'svc-b', got %q", services[1].Name)
	}
	if services[1].Version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", services[1].Version)
	}
	if services[0].ContractStatus != StatusUnknown {
		t.Errorf("expected Unknown (normalized from Progressing), got %q", services[0].ContractStatus)
	}
	if services[1].ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant, got %q", services[1].ContractStatus)
	}
}

// ---------------------------------------------------------------------------
// GetService with namespace (direct API get)
// ---------------------------------------------------------------------------

func TestK8s_GetService_WithNamespace(t *testing.T) {
	singleJSON := `{"metadata": {"name": "my-svc", "namespace": "default"}, "status": {"contractStatus": "Compliant", "contract": {"serviceName": "my-svc", "version": "2.0.0"}}}`
	client := &mockK8sClient{getJSON: []byte(singleJSON)}

	src := NewK8sSource(client, "default", "pactos")
	details, err := src.GetService(context.Background(), "my-svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details.Name != "my-svc" {
		t.Errorf("expected name 'my-svc', got %q", details.Name)
	}
	if details.Version != "2.0.0" {
		t.Errorf("expected version '2.0.0', got %q", details.Version)
	}
	if details.ContractStatus != StatusCompliant {
		t.Errorf("expected Compliant, got %q", details.ContractStatus)
	}
}

// ---------------------------------------------------------------------------
// GetService without namespace (lists all, filters by name)
// ---------------------------------------------------------------------------

func TestK8s_GetService_WithoutNamespace(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-a"}, "status": {"contractStatus": "Compliant"}}, {"metadata": {"name": "target-svc"}, "status": {"contractStatus": "Warning", "contract": {"serviceName": "target-svc", "version": "3.0.0"}}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "", "pactos")
	details, err := src.GetService(context.Background(), "target-svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details.Name != "target-svc" {
		t.Errorf("expected name 'target-svc', got %q", details.Name)
	}
	if details.Version != "3.0.0" {
		t.Errorf("expected version '3.0.0', got %q", details.Version)
	}
	if details.ContractStatus != StatusWarning {
		t.Errorf("expected Warning, got %q", details.ContractStatus)
	}
}

// ---------------------------------------------------------------------------
// listPactos error (API call fails)
// ---------------------------------------------------------------------------

func TestK8s_listPactos_Error(t *testing.T) {
	client := &mockK8sClient{listErr: fmt.Errorf("connection refused")}

	src := NewK8sSource(client, "default", "pactos")
	_, err := src.ListServices(context.Background())
	if err == nil {
		t.Fatal("expected error from ListServices when API call fails")
	}
	if !strings.Contains(err.Error(), "listing") {
		t.Errorf("expected error to mention 'listing', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// listPactos bad JSON
// ---------------------------------------------------------------------------

func TestK8s_listPactos_BadJSON(t *testing.T) {
	client := &mockK8sClient{listJSON: []byte("this is not valid json")}

	src := NewK8sSource(client, "default", "pactos")
	_, err := src.ListServices(context.Background())
	if err == nil {
		t.Fatal("expected error from ListServices when API returns bad JSON")
	}
	if !strings.Contains(err.Error(), "parsing API response") {
		t.Errorf("expected error to mention 'parsing API response', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getPacto not found (list returns items but none match)
// ---------------------------------------------------------------------------

func TestK8s_getPacto_NotFound(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-a"}, "status": {"contractStatus": "Compliant"}}, {"metadata": {"name": "svc-b"}, "status": {"contractStatus": "Compliant"}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "", "pactos")
	_, err := src.GetService(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error when service is not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected error to mention 'not found', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// listPactos cache hit — second call within TTL returns cached result
// ---------------------------------------------------------------------------

func TestK8s_listPactos_CacheHit(t *testing.T) {
	listJSON := `{"items": [{"metadata": {"name": "svc-a"}, "status": {"contractStatus": "Compliant"}}]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "default", "pactos")

	// First call populates cache.
	items1, err := src.listPactos(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(items1) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items1))
	}

	// Replace client response with different data to prove cache is used.
	client.listJSON = []byte(`{"items": []}`)

	// Second call should return cached data (within TTL).
	items2, err := src.listPactos(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(items2) != 1 {
		t.Fatalf("expected cached 1 item, got %d (cache not hit)", len(items2))
	}
}

// ---------------------------------------------------------------------------
// getPacto matches by contract.ServiceName in all-namespaces mode
// ---------------------------------------------------------------------------

func TestK8s_getPacto_MatchByServiceName(t *testing.T) {
	listJSON := `{"items": [
		{"metadata": {"name": "k8s-resource-name"}, "status": {"contractStatus": "Compliant", "contract": {"serviceName": "my-service", "version": "1.0.0"}}}
	]}`
	client := &mockK8sClient{listJSON: []byte(listJSON)}

	src := NewK8sSource(client, "", "pactos") // all-namespaces mode
	details, err := src.GetService(context.Background(), "my-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details.Name != "my-service" {
		t.Errorf("expected name 'my-service', got %q", details.Name)
	}
}

// ---------------------------------------------------------------------------
// getPacto API error (with namespace, API call fails)
// ---------------------------------------------------------------------------

func TestK8s_getPacto_APIError_WithNamespace(t *testing.T) {
	client := &mockK8sClient{getErr: fmt.Errorf("connection refused")}

	src := NewK8sSource(client, "default", "pactos")
	_, err := src.GetService(context.Background(), "my-svc")
	if err == nil {
		t.Fatal("expected error when API call fails for direct get")
	}
	if !strings.Contains(err.Error(), "getting") {
		t.Errorf("expected error to mention 'getting', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getPacto bad JSON (with namespace, direct get returns invalid JSON)
// ---------------------------------------------------------------------------

func TestK8s_getPacto_BadJSON_WithNamespace(t *testing.T) {
	client := &mockK8sClient{getJSON: []byte("not valid json at all")}

	src := NewK8sSource(client, "default", "pactos")
	_, err := src.GetService(context.Background(), "my-svc")
	if err == nil {
		t.Fatal("expected error when API returns bad JSON for direct get")
	}
	if !strings.Contains(err.Error(), "parsing API response") {
		t.Errorf("expected error to mention 'parsing API response', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// getPacto listPactos error in all-namespaces mode
// ---------------------------------------------------------------------------

func TestK8s_getPacto_ListError_AllNamespaces(t *testing.T) {
	client := &mockK8sClient{listErr: fmt.Errorf("connection refused")}

	src := NewK8sSource(client, "", "pactos") // all-namespaces mode
	_, err := src.GetService(context.Background(), "my-svc")
	if err == nil {
		t.Fatal("expected error when listPactos fails in all-namespaces mode")
	}
	if !strings.Contains(err.Error(), "listing") {
		t.Errorf("expected error to mention 'listing', got: %v", err)
	}
}

func TestObservedRuntimeFromK8s(t *testing.T) {
	// nil input
	if got := observedRuntimeFromK8s(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}

	// non-nil input
	grace := 30
	hasPVC := true
	hasEmpty := false
	delay := 5
	obs := &k8sObservedRuntime{
		WorkloadKind:                   "Deployment",
		DeploymentStrategy:             "RollingUpdate",
		PodManagementPolicy:            "OrderedReady",
		TerminationGracePeriodSeconds:  &grace,
		ContainerImages:                []string{"img:v1"},
		HasPVC:                         &hasPVC,
		HasEmptyDir:                    &hasEmpty,
		HealthProbeInitialDelaySeconds: &delay,
	}
	got := observedRuntimeFromK8s(obs)
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.WorkloadKind != "Deployment" {
		t.Errorf("expected Deployment, got %q", got.WorkloadKind)
	}
	if got.DeploymentStrategy != "RollingUpdate" {
		t.Errorf("expected RollingUpdate, got %q", got.DeploymentStrategy)
	}
	if got.TerminationGracePeriodSeconds == nil || *got.TerminationGracePeriodSeconds != 30 {
		t.Errorf("expected 30, got %v", got.TerminationGracePeriodSeconds)
	}
	if got.HasPVC == nil || !*got.HasPVC {
		t.Error("expected HasPVC=true")
	}
	if got.HealthProbeInitialDelay == nil || *got.HealthProbeInitialDelay != 5 {
		t.Errorf("expected 5, got %v", got.HealthProbeInitialDelay)
	}
}

// ---------------------------------------------------------------------------
// GetVersions (from PactoRevision CRDs)
// ---------------------------------------------------------------------------

func TestK8s_GetVersions(t *testing.T) {
	revisionsJSON := mustJSON(t, map[string]any{
		"items": []map[string]any{
			{
				"metadata": map[string]any{"name": "payments-service-1-0-0", "namespace": "default"},
				"spec": map[string]any{
					"serviceName": "payments-service",
					"version":     "1.0.0",
					"source":      map[string]any{"oci": "ghcr.io/org/payments-service:1.0.0"},
				},
				"status": map[string]any{
					"contractHash": "abc123",
					"createdAt":    "2026-01-01T00:00:00Z",
					"resolved":     true,
				},
			},
			{
				"metadata": map[string]any{"name": "payments-service-2-0-0", "namespace": "default"},
				"spec": map[string]any{
					"serviceName": "payments-service",
					"version":     "2.0.0",
					"source":      map[string]any{"oci": "ghcr.io/org/payments-service:2.0.0"},
				},
				"status": map[string]any{
					"contractHash": "def456",
					"createdAt":    "2026-02-01T00:00:00Z",
					"resolved":     true,
				},
			},
			{
				"metadata": map[string]any{"name": "other-service-1-0-0", "namespace": "default"},
				"spec": map[string]any{
					"serviceName": "other-service",
					"version":     "1.0.0",
					"source":      map[string]any{"oci": "ghcr.io/org/other-service:1.0.0"},
				},
				"status": map[string]any{"contractHash": "xyz"},
			},
		},
	})

	client := &mockK8sClient{
		listByRes: map[string][]byte{
			"pactorevisions": revisionsJSON,
		},
		listByResErr: map[string]error{},
	}
	src := NewK8sSource(client, "default", "pactos")

	versions, err := src.GetVersions(context.Background(), "payments-service")
	if err != nil {
		t.Fatal(err)
	}

	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	// Should be sorted descending (latest first).
	if versions[0].Version != "2.0.0" {
		t.Errorf("expected first version 2.0.0, got %q", versions[0].Version)
	}
	if versions[1].Version != "1.0.0" {
		t.Errorf("expected second version 1.0.0, got %q", versions[1].Version)
	}

	// Check fields are populated.
	if versions[0].Ref != "ghcr.io/org/payments-service:2.0.0" {
		t.Errorf("expected OCI ref, got %q", versions[0].Ref)
	}
	if versions[0].ContractHash != "def456" {
		t.Errorf("expected contract hash, got %q", versions[0].ContractHash)
	}
	if versions[0].CreatedAt == nil {
		t.Error("expected createdAt to be set")
	}
}

func TestK8s_GetVersions_NoRevisions(t *testing.T) {
	client := &mockK8sClient{
		listByRes: map[string][]byte{
			"pactorevisions": []byte(`{"items":[]}`),
		},
		listByResErr: map[string]error{},
	}
	src := NewK8sSource(client, "", "pactos")

	versions, err := src.GetVersions(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Errorf("expected 0 versions, got %d", len(versions))
	}
}

func TestK8s_GetVersions_ListError(t *testing.T) {
	client := &mockK8sClient{
		listByRes:    map[string][]byte{},
		listByResErr: map[string]error{"pactorevisions": fmt.Errorf("forbidden")},
	}
	src := NewK8sSource(client, "", "pactos")

	_, err := src.GetVersions(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error")
	}
}

// Semver ordering is now tested in pkg/semver (shared with the CLI resolver).

func TestListRevisions_InvalidJSON(t *testing.T) {
	client := &mockK8sClient{
		crdDiscovery: &CRDDiscovery{Found: true, ResourceName: revisionResourceName},
		listByRes: map[string][]byte{
			revisionResourceName: []byte(`not valid json`),
		},
	}
	setupMockK8sClient(t, client)

	src := NewK8sSource(client, "default", "")
	_, err := src.GetVersions(context.Background(), "any-service")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
