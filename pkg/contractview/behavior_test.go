package contractview

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	depgraph "github.com/trianalab/pacto/v4/pkg/graph"
)

// TestServiceDetails_EnvironmentsAndStages: the offline view carries how each
// validation stage ended and each environment's check, located in its file.
func TestServiceDetails_EnvironmentsAndStages(t *testing.T) {
	raw := []byte(`pactoVersion: "2.3"
service: {name: svc, version: 1.0.0}
configurations:
  - {name: chart, schema: chart.json, required: false}
environments:
  - {name: staging, file: staging.yaml}
  - {name: prod, file: prod.yaml}
`)
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"pacto.yaml":   {Data: raw},
		"chart.json":   {Data: []byte(`{"type":"object","properties":{"replicas":{"type":"integer"}}}`)},
		"staging.yaml": {Data: []byte("chart:\n  replicas: 2\n")},
		"prod.yaml":    {Data: []byte("chart:\n  replicas: many\n")},
	}
	d := ServiceDetailsFromBundle(&contract.Bundle{Contract: c, RawYAML: raw, FS: fsys}, "local")
	if n := len(d.Stages); n != 4 || d.Stages[3].Name != "environments" || d.Stages[3].Status != "fail" {
		t.Fatalf("stages: %+v", d.Stages)
	}
	if len(d.Environments) != 2 || d.Environments[0].Status != fleet.EnvironmentPass || d.Environments[1].Status != fleet.EnvironmentFail {
		t.Fatalf("environments: %+v", d.Environments)
	}
	if f := d.Environments[1].Findings.Items; len(f) != 1 || f[0].File != "prod.yaml" || f[0].Line != 2 {
		t.Fatalf("prod findings: %+v", f)
	}
}

// TestServiceDetails_EnvironmentsUnread: a document that cannot be read checks
// nothing, so each environment is not checked, with the reason.
func TestServiceDetails_EnvironmentsUnread(t *testing.T) {
	c := &contract.Contract{Service: contract.Service{Name: "svc"}, Environments: []contract.Environment{{Name: "staging", File: "s.yaml"}}}
	d := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	if len(d.Stages) != 0 || len(d.Environments) != 1 || d.Environments[0].Status != fleet.EnvironmentSkip || d.Environments[0].Reason == "" {
		t.Fatalf("got stages %+v environments %+v", d.Stages, d.Environments)
	}
}

// TestServiceDetails_InterfaceBehavior: lifecycle, guarantees and epoch reach
// the interface row.
func TestServiceDetails_InterfaceBehavior(t *testing.T) {
	c := &contract.Contract{Service: contract.Service{Name: "svc"}, Interfaces: []contract.Interface{{
		Name: "events", Type: contract.InterfaceTypeAsyncAPI,
		Lifecycle:          &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-06-01", Successor: "events-v2"},
		Guarantees:         &contract.Guarantees{Delivery: "at-least-once"},
		CompatibilityEpoch: 2,
	}}}
	i := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local").Interfaces[0]
	if i.Lifecycle.Successor != "events-v2" || i.Guarantees.Delivery != "at-least-once" || i.CompatibilityEpoch != 2 {
		t.Fatalf("got %+v", i)
	}
}

// TestApplyDependencyGraph: a requirement is met or refused only against a
// provider contract the resolver fetched; anything else is Not verified with
// the reason, never a refusal.
func TestApplyDependencyGraph(t *testing.T) {
	req := []contract.Requirement{{Interface: "api", Guarantees: contract.Guarantees{Idempotent: true}}}
	c := &contract.Contract{Service: contract.Service{Name: "svc"}, Dependencies: []contract.Dependency{
		{Name: "met", Ref: "oci://r/met", Requires: req},
		{Name: "refused", Ref: "oci://r/refused", Requires: req},
		{Name: "failed", Ref: "oci://r/failed", Requires: req},
		{Name: "unfetched", Ref: "oci://r/unfetched", Requires: req},
		{Name: "external", Version: "v1", Requires: req},
		{Name: "plain", Ref: "oci://r/plain"},
	}}
	d := ServiceDetailsFromBundle(&contract.Bundle{Contract: c}, "local")
	for _, dep := range d.Dependencies[:5] {
		if dep.GuaranteeStatus != fleet.GuaranteeUnknown || dep.GuaranteeReason == "" {
			t.Fatalf("before the graph %s: %+v", dep.Name, dep)
		}
	}
	if d.Dependencies[4].GuaranteeReason != reasonNoContract {
		t.Fatalf("external: %+v", d.Dependencies[4])
	}
	ApplyDependencyGraph(d, nil) // no graph changes nothing
	ApplyDependencyGraph(d, &depgraph.Result{})

	deprecated := &contract.Contract{Interfaces: []contract.Interface{
		{Name: "api"}, {Name: "old", Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Successor: "api"}},
	}}
	mismatch := contract.Mismatch{Consumer: "svc", Provider: "refused", Interface: "api", Guarantee: "idempotent", Required: "true"}
	ApplyDependencyGraph(d, &depgraph.Result{Root: &depgraph.Node{Dependencies: []depgraph.Edge{
		{Node: &depgraph.Node{Contract: deprecated}},
		{Node: &depgraph.Node{}, Unmet: []contract.Mismatch{mismatch}},
		{Error: "unauthorized"},
		{},
		{Type: depgraph.EdgeExternal},
		// The plain dependency's edge is missing: a shorter graph stops early.
	}}})
	want := []struct{ status, reason string }{
		{fleet.GuaranteeMet, ""},
		{fleet.GuaranteeUnmet, ""},
		{fleet.GuaranteeUnknown, reasonFetchFailed + "unauthorized"},
		{fleet.GuaranteeUnknown, reasonNotFetched},
		{fleet.GuaranteeUnknown, reasonNoContract},
		{"", ""},
	}
	for i, w := range want {
		if dep := d.Dependencies[i]; dep.GuaranteeStatus != w.status || dep.GuaranteeReason != w.reason {
			t.Errorf("%s: got %q %q, want %q %q", dep.Name, dep.GuaranteeStatus, dep.GuaranteeReason, w.status, w.reason)
		}
	}
	if di := d.Dependencies[0].DeprecatedInterfaces; len(di) != 1 || di[0].Interface != "old" || di[0].Successor != "api" {
		t.Fatalf("deprecated: %+v", di)
	}
	if u := d.Dependencies[1].Unmet; len(u) != 1 || u[0] != mismatch {
		t.Fatalf("refused: %+v", u)
	}
}
