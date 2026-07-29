package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

var fixedNow = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

// buildFleetQuery assembles a rich in-memory snapshot: orders (with an OpenAPI
// interface → tools, a skill, and a declared dependency on payments), payments,
// plus targets covering a confirmed non-compliant finding, missing evidence and
// stale evidence.
func buildFleetQuery(t *testing.T) *fleet.Query {
	t.Helper()
	ordersFS := fstest.MapFS{
		"openapi.yaml":       {Data: testutil.TestOpenAPI()},
		"skills/checkout.md": {Data: []byte("# Checkout\n")},
	}
	orders := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "orders", Version: "1.0.0", Owner: contract.Owner{Team: "commerce"}},
		Interfaces:   []contract.Interface{{Name: "api", Type: contract.InterfaceTypeOpenAPI, Ref: "openapi.yaml"}},
		Dependencies: []contract.Dependency{{Name: "payments", Ref: "oci://ghcr.io/acme/payments", Required: true, Compatibility: "^1.0.0"}},
		Workload:     contract.WorkloadService,
	}
	payments := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "payments", Version: "2.0.0", Owner: contract.Owner{Team: "billing"}},
		Workload:     contract.WorkloadService,
	}
	old := fixedNow.Add(-48 * time.Hour)
	recent := fixedNow.Add(-1 * time.Hour)

	src := fleet.NewMemorySource("local", "local", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: orders, FS: ordersFS}, ResolvedRef: "oci://ghcr.io/acme/orders@sha256:o", Digest: "sha256:o"},
			{Bundle: &contract.Bundle{Contract: payments}, Digest: "sha256:p"},
		},
		Targets: []fleet.RawTarget{
			{
				Scope: "prod", Kind: "k8s", Name: "orders", Service: "orders",
				Compliance: fleet.StatusNonCompliant, Digest: "sha256:o", EvidenceAt: &recent,
				Findings: []finding.Finding{{
					Code: finding.CodeStatelessPersistent, Severity: finding.SeverityError,
					Category: finding.CategoryStateMismatch,
					Subject:  finding.SubjectRef{Kind: "service", Name: "orders"}, Message: "drift detected",
				}},
				Coverage: &fleet.Coverage{Evaluated: 2, Required: 3},
			},
			// Missing evidence: Unknown compliance with no EvidenceAt.
			{Scope: "prod", Kind: "k8s", Name: "payments", Service: "payments", Compliance: fleet.StatusUnknown},
			// Stale: old evidence older than the freshness window.
			{Scope: "eu", Kind: "k8s", Name: "orders-eu", Service: "orders", Compliance: fleet.StatusCompliant, EvidenceAt: &old},
		},
	})

	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{
		Now: func() time.Time { return fixedNow }, FreshnessWindow: 24 * time.Hour,
	}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return fleet.NewQuery(snap)
}

// callHandler invokes a fleet handler with the given args and returns the result.
func callHandler(t *testing.T, h mcpsdk.ToolHandler, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	res, err := h(context.Background(), makeRequest(t, args))
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	return res
}

// decode parses a (non-error) handler result's JSON text into v.
func decode(t *testing.T, res *mcpsdk.CallToolResult, v any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected error result: %s", resultText(t, res))
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), v); err != nil {
		t.Fatalf("decode result: %v\ntext: %s", err, resultText(t, res))
	}
}

func TestFleetSearchHandler(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetSearchHandler(q), map[string]any{"limit": 1})
	var out fleet.SearchResult
	decode(t, res, &out)
	if out.Count > 1 {
		t.Errorf("count = %d, want <= 1 (limit honored)", out.Count)
	}
	if out.Meta.AsOf.IsZero() {
		t.Error("expected meta.asOf to be set")
	}
	if out.Meta.Completeness == "" {
		t.Error("expected meta.completeness to be set")
	}
	if out.Total < 2 {
		t.Errorf("total = %d, want >= 2 services", out.Total)
	}
}

func TestFleetSearchHandler_InvalidFilter(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetSearchHandler(q), map[string]any{"status": "Bogus"})
	if !res.IsError {
		t.Error("expected an error result for an invalid status filter")
	}
}

func TestFleetGetHandler_Service(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGetHandler(q), map[string]any{"service": "orders"})
	var out fleet.ServiceView
	decode(t, res, &out)
	if out.Service == nil || out.Service.Name != "orders" {
		t.Fatalf("unexpected service view: %+v", out.Service)
	}
	tools := 0
	for _, c := range out.Capabilities {
		tools += len(c.Tools)
	}
	if tools == 0 {
		t.Error("expected tools derived from the OpenAPI interface")
	}
	if len(out.Dependencies) == 0 {
		t.Error("expected a declared dependency edge")
	}
}

func TestFleetGetHandler_Target(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGetHandler(q), map[string]any{"target": "prod/k8s/orders"})
	var out fleet.TargetView
	decode(t, res, &out)
	if out.Target == nil || out.Target.Name != "orders" {
		t.Fatalf("unexpected target view: %+v", out.Target)
	}
	if out.Target.Compliance != fleet.StatusNonCompliant {
		t.Errorf("compliance = %q", out.Target.Compliance)
	}
}

func TestFleetGetHandler_Neither(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGetHandler(q), map[string]any{})
	if !res.IsError {
		t.Error("expected error when neither service nor target is provided")
	}
}

func TestFleetGetHandler_TargetNotFound(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGetHandler(q), map[string]any{"target": "ghost"})
	if !res.IsError {
		t.Error("expected error result for unknown target")
	}
}

func TestFleetGetHandler_ServiceNotFound(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGetHandler(q), map[string]any{"service": "ghost"})
	if !res.IsError {
		t.Error("expected error result for unknown service")
	}
}

func TestFleetGraphHandler(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGraphHandler(q), map[string]any{"service": "orders", "transitive": true})
	var out fleet.GraphResult
	decode(t, res, &out)
	if out.Root != "orders" {
		t.Errorf("root = %q", out.Root)
	}
	found := false
	for _, n := range out.Nodes {
		if n.Name == "payments" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected payments among reached nodes, got %+v", out.Nodes)
	}
}

func TestFleetGraphHandler_Dependents(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGraphHandler(q), map[string]any{"service": "payments", "direction": "dependents"})
	var out fleet.GraphResult
	decode(t, res, &out)
	if out.Direction != fleet.DirectionDependents {
		t.Errorf("direction = %q", out.Direction)
	}
}

func TestFleetGraphHandler_NotFound(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetGraphHandler(q), map[string]any{"service": "ghost"})
	if !res.IsError {
		t.Error("expected error result for unknown graph root")
	}
}

func TestFleetStatusHandler(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetStatusHandler(q), map[string]any{"needs_attention": true})
	var out fleet.StatusResult
	decode(t, res, &out)
	if len(out.Items) == 0 {
		t.Fatal("expected attention items in the union report")
	}
	codes := map[string]bool{}
	for _, it := range out.Items {
		codes[it.Code] = true
	}
	if !codes["NON_COMPLIANT"] {
		t.Errorf("expected a NON_COMPLIANT item, got codes %v", codes)
	}
}

func TestFleetExplainHandler(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetExplainHandler(q), map[string]any{"subject": "orders"})
	var out fleet.ExplainResult
	decode(t, res, &out)
	if out.Subject != "orders" || out.Kind != "service" {
		t.Errorf("unexpected explain result: %+v", out)
	}
	if len(out.Reasons) == 0 {
		t.Error("expected at least one reason for orders")
	}
}

func TestFleetExplainHandler_NotFound(t *testing.T) {
	q := buildFleetQuery(t)
	res := callHandler(t, fleetExplainHandler(q), map[string]any{"subject": "ghost"})
	if !res.IsError {
		t.Error("expected error result for unknown explain subject")
	}
}

func TestIntOrZero(t *testing.T) {
	if intOrZero(nil) != 0 {
		t.Error("intOrZero(nil) should be 0")
	}
	five := 5
	if intOrZero(&five) != 5 {
		t.Error("intOrZero(&5) should be 5")
	}
}

// --- server registration ---------------------------------------------------

// connectFleetServer wires an in-memory client to a fleet server and returns the
// session for tool/instruction inspection.
func connectFleetServer(t *testing.T, q *fleet.Query) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := NewFleetServer("test", q)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "c", Version: "1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestNewFleetServer_NilQuery_OnlyAuthoringTools(t *testing.T) {
	session := connectFleetServer(t, nil)
	names := toolNames(t, session)
	if !names["pacto_create"] {
		t.Error("expected authoring tools to be registered")
	}
	for _, ft := range fleetToolNames {
		if names[ft] {
			t.Errorf("did not expect %s when query is nil", ft)
		}
	}
	if strings.Contains(session.InitializeResult().Instructions, fleetInstructions) {
		t.Error("fleet instructions should not be appended when query is nil")
	}
}

func TestNewFleetServer_WithQuery_RegistersFleetTools(t *testing.T) {
	session := connectFleetServer(t, buildFleetQuery(t))
	names := toolNames(t, session)
	if !names["pacto_create"] {
		t.Error("expected authoring tools alongside fleet tools")
	}
	for _, ft := range fleetToolNames {
		if !names[ft] {
			t.Errorf("expected fleet tool %s to be registered", ft)
		}
	}
	if !strings.Contains(session.InitializeResult().Instructions, fleetInstructions) {
		t.Error("fleet instructions should be appended when a query is set")
	}
}

// fleetToolNames enumerates the read-only fleet tools for the safety assertions.
var fleetToolNames = []string{
	"pacto_fleet_search", "pacto_fleet_get", "pacto_fleet_graph",
	"pacto_fleet_status", "pacto_fleet_explain",
}

// --- SAFETY ----------------------------------------------------------------

// (a) Read-only: no fleet tool implies mutation, and invoking every handler
// leaves the snapshot byte-for-byte unchanged.
func TestSafety_ReadOnly(t *testing.T) {
	for _, name := range append([]string{}, fleetToolNames...) {
		lower := strings.ToLower(name)
		for _, verb := range []string{"create", "edit", "delete", "write", "deploy", "apply", "set", "update", "invoke", "grant"} {
			if strings.Contains(lower, verb) {
				t.Errorf("fleet tool %q implies mutation (%q)", name, verb)
			}
		}
	}

	q := buildFleetQuery(t)
	before, err := json.Marshal(q.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	// Exercise every handler, including read paths that build derived views.
	_ = callHandler(t, fleetSearchHandler(q), map[string]any{})
	_ = callHandler(t, fleetGetHandler(q), map[string]any{"service": "orders"})
	_ = callHandler(t, fleetGetHandler(q), map[string]any{"target": "prod/k8s/orders"})
	_ = callHandler(t, fleetGraphHandler(q), map[string]any{"service": "orders", "transitive": true})
	_ = callHandler(t, fleetGraphHandler(q), map[string]any{"service": "payments", "direction": "dependents"})
	_ = callHandler(t, fleetStatusHandler(q), map[string]any{"needs_attention": true})
	_ = callHandler(t, fleetExplainHandler(q), map[string]any{"subject": "orders"})

	after, err := json.Marshal(q.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("snapshot mutated by a read-only handler")
	}
}

// (b) No secret leakage: a source error containing a fake secret must be
// sanitized to a generic category message before it reaches a consumer.
func TestSafety_NoSecretLeakage(t *testing.T) {
	const secret = "SUPERSECRET123"
	failing := fleet.NewFailingSource("oci", "oci", errors.New("token="+secret+" unauthorized"))
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, failing)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, s := range snap.Sources {
		if s.Error != nil && strings.Contains(s.Error.Message, secret) {
			t.Fatalf("source error leaked the secret: %q", s.Error.Message)
		}
	}
	// Confirm it stays sanitized through a handler's meta.sources JSON.
	q := fleet.NewQuery(snap)
	res := callHandler(t, fleetSearchHandler(q), map[string]any{})
	if strings.Contains(resultText(t, res), secret) {
		t.Error("handler output leaked the secret")
	}
}

// (c) Bounded results: a snapshot with more than MaxSearchLimit services still
// returns at most MaxSearchLimit hits, even when the caller requests more.
func TestSafety_BoundedResults(t *testing.T) {
	n := fleet.MaxSearchLimit + 100
	revs := make([]fleet.RawRevision, 0, n)
	for i := 0; i < n; i++ {
		c := &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: fmt.Sprintf("svc-%04d", i), Version: "1.0.0"},
		}
		revs = append(revs, fleet.RawRevision{Bundle: &contract.Bundle{Contract: c}, Digest: fmt.Sprintf("sha256:%04d", i)})
	}
	src := fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: revs})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	q := fleet.NewQuery(snap)
	res := callHandler(t, fleetSearchHandler(q), map[string]any{"limit": 100000})
	var out fleet.SearchResult
	decode(t, res, &out)
	if out.Count > fleet.MaxSearchLimit {
		t.Errorf("count = %d, want <= MaxSearchLimit (%d)", out.Count, fleet.MaxSearchLimit)
	}
}

// (d) Cyclic graph safety: a dependency cycle A→B→A must terminate and record
// the cycle rather than hang or panic.
func TestSafety_CyclicGraph(t *testing.T) {
	mk := func(name, dep string) *contract.Contract {
		return &contract.Contract{
			PactoVersion: "2.0",
			Service:      contract.Service{Name: name, Version: "1.0.0"},
			Dependencies: []contract.Dependency{{Name: dep, Ref: "oci://x/" + dep, Required: true}},
		}
	}
	src := fleet.NewMemorySource("local", "local", &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: &contract.Bundle{Contract: mk("a", "b")}, Digest: "sha256:a"},
			{Bundle: &contract.Bundle{Contract: mk("b", "a")}, Digest: "sha256:b"},
		},
	})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	q := fleet.NewQuery(snap)
	res := callHandler(t, fleetGraphHandler(q), map[string]any{"service": "a", "transitive": true})
	var out fleet.GraphResult
	decode(t, res, &out)
	if len(out.Cycles) == 0 {
		t.Error("expected the A→B→A cycle to be recorded")
	}
}

// (e) Query text is a literal substring filter — never a shell command or path.
func TestSafety_QueryTextIsData(t *testing.T) {
	q := buildFleetQuery(t)
	for _, text := range []string{"$(rm -rf /)", "../../etc/passwd", "; DROP TABLE services;--"} {
		res := callHandler(t, fleetSearchHandler(q), map[string]any{"text": text})
		var out fleet.SearchResult
		decode(t, res, &out)
		if out.Count != 0 {
			t.Errorf("text %q matched %d services; expected literal (0) matches", text, out.Count)
		}
	}
}
