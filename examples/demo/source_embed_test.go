package main

import (
	"context"
	"os"
	"testing"

	"github.com/trianalab/pacto/pkg/dashboard"
)

// bundlesFS loads the same bundles the wasm binary embeds. Bundles live in
// ./bundles relative to this package, so go test (cwd = package dir) finds them.
func bundlesFS(t *testing.T) *EmbedSource {
	t.Helper()
	src, err := NewEmbedSource(os.DirFS("bundles"))
	if err != nil {
		t.Fatalf("NewEmbedSource: %v", err)
	}
	return src
}

func TestListServices(t *testing.T) {
	src := bundlesFS(t)
	svcs, err := src.ListServices(context.Background())
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(svcs) < 10 {
		t.Fatalf("expected the demo's full fleet, got %d services", len(svcs))
	}
	want := map[string]bool{"pacto-demo": false, "frontend": false, "payments-service": false}
	for _, s := range svcs {
		if _, ok := want[s.Name]; ok {
			want[s.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected service %q in fleet", name)
		}
	}
}

func TestGetServiceReturnsLatest(t *testing.T) {
	src := bundlesFS(t)
	d, err := src.GetService(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetService: %v", err)
	}
	if d.Version != "2.1.0" {
		t.Errorf("payments-service current version = %q, want 2.1.0", d.Version)
	}
}

func TestGetVersionsDescendingWithClassification(t *testing.T) {
	src := bundlesFS(t)
	vs, err := src.GetVersions(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetVersions: %v", err)
	}
	wantOrder := []string{"2.1.0", "2.0.0", "1.2.0", "1.1.0", "1.0.0"}
	if len(vs) != len(wantOrder) {
		t.Fatalf("got %d versions, want %d", len(vs), len(wantOrder))
	}
	for i, w := range wantOrder {
		if vs[i].Version != w {
			t.Errorf("version[%d] = %q, want %q", i, vs[i].Version, w)
		}
		if vs[i].ContractHash == "" {
			t.Errorf("version %q missing contract hash", vs[i].Version)
		}
	}
	for _, v := range vs {
		if v.Version == "2.0.0" && v.Classification != "BREAKING" {
			t.Errorf("2.0.0 classification = %q, want BREAKING", v.Classification)
		}
	}
}

func TestGetDiffBreaking(t *testing.T) {
	src := bundlesFS(t)
	d, err := src.GetDiff(context.Background(),
		dashboard.Ref{Name: "payments-service", Version: "1.2.0"},
		dashboard.Ref{Name: "payments-service", Version: "2.0.0"})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if d.Classification != "BREAKING" {
		t.Errorf("classification = %q, want BREAKING", d.Classification)
	}
	var sawChargesRemoved bool
	for _, c := range d.Changes {
		if c.Path == "openapi.paths[/charges]" {
			sawChargesRemoved = true
		}
	}
	if !sawChargesRemoved {
		t.Errorf("expected removal of /charges path among %d changes", len(d.Changes))
	}
}

func TestGetDiffDefaultsToLatest(t *testing.T) {
	src := bundlesFS(t)
	d, err := src.GetDiff(context.Background(),
		dashboard.Ref{Name: "payments-service", Version: "1.0.0"},
		dashboard.Ref{Name: "payments-service", Version: ""})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if d.To.Version != "2.1.0" {
		t.Errorf("to version = %q, want resolved latest 2.1.0", d.To.Version)
	}
}

// TestReadinessShowcase pins the two readiness fixtures: payments-service 2.1.0
// fails its gate (Score 70 < minScore 80) and orders-service 1.2.0 passes. Dates
// are durable sentinels, so these hold regardless of when the test runs.
func TestReadinessShowcase(t *testing.T) {
	src := bundlesFS(t)

	pay, err := src.GetService(context.Background(), "payments-service")
	if err != nil {
		t.Fatalf("GetService(payments-service): %v", err)
	}
	if pay.Readiness == nil {
		t.Fatal("payments-service 2.1.0 should expose readiness")
	}
	if pay.Readiness.MinScore != 80 {
		t.Errorf("payments minScore = %d, want 80", pay.Readiness.MinScore)
	}
	if pay.Readiness.Score != 70 {
		t.Errorf("payments score = %d, want 70", pay.Readiness.Score)
	}
	if pay.Readiness.Passing {
		t.Error("payments readiness gate should FAIL (70 < 80)")
	}

	ord, err := src.GetService(context.Background(), "orders-service")
	if err != nil {
		t.Fatalf("GetService(orders-service): %v", err)
	}
	if ord.Readiness == nil || !ord.Readiness.Passing {
		t.Errorf("orders-service 1.2.0 readiness should PASS, got %+v", ord.Readiness)
	}
}
