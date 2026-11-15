package mcp

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// deprecatedOfferingFleet: postgresql 16.1.0 is deprecated and app depends on
// it; redis is supported and nothing consumes it.
func deprecatedOfferingFleet(t *testing.T) *fleet.Query {
	t.Helper()
	pg := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "postgresql", Version: "16.1.0", Offering: "postgresql",
		Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-01"}}}
	redis := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "redis", Version: "7.0.0", Offering: "redis"}}
	app := &contract.Contract{PactoVersion: "2.4", Workload: contract.WorkloadService, Service: contract.Service{Name: "app", Version: "1.0.0"},
		Dependencies: []contract.Dependency{{Name: "postgresql", Ref: "oci://ghcr.io/acme/postgresql", Required: true, Compatibility: "^16.0.0"}}}
	src := fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: &contract.Bundle{Contract: pg}, Digest: "sha256:p"},
		{Bundle: &contract.Bundle{Contract: redis}, Digest: "sha256:r"},
		{Bundle: &contract.Bundle{Contract: app}, Digest: "sha256:a"},
	}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return fixedNow }}, src)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// TestFleetEntitiesLifecycleFailedArgs: lifecycle keeps the deprecated
// offering, failed keeps none here, and a bad lifecycle is an error result.
func TestFleetEntitiesLifecycleFailedArgs(t *testing.T) {
	q := deprecatedOfferingFleet(t)
	call := func(args map[string]any) []string {
		return listedKeys(t, callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), args))
	}
	if got := call(map[string]any{"kind": "offering", "lifecycle": "deprecated"}); !slices.Equal(got, []string{"offering/postgresql"}) {
		t.Errorf("lifecycle=deprecated = %v, want postgresql only", got)
	}
	if got := call(map[string]any{"kind": "offering", "failed": true}); len(got) != 0 {
		t.Errorf("failed = %v, want none", got)
	}
	if res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"lifecycle": "bogus"}); !res.IsError {
		t.Errorf("lifecycle=bogus = %s, want an error result", resultText(t, res))
	}
}

// TestFleetStatusLifecycleArg: lifecycle selects the deprecated-provider
// items and nothing else.
func TestFleetStatusLifecycleArg(t *testing.T) {
	var out fleet.StatusResult
	decode(t, callHandler(t, fleetStatusTool(), fleetStatusHandler(deprecatedOfferingFleet(t)), map[string]any{"lifecycle": true}), &out)
	if out.Total != 1 || out.Items[0].Code != "DEPENDENCY_SERVICE_DEPRECATED" {
		t.Errorf("lifecycle = %+v", out)
	}
}

// TestImpactToolOfferingArg: offering reaches the provider as Event.Offering.
func TestImpactToolOfferingArg(t *testing.T) {
	var seen impact.Options
	res := callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, &seen), nil), map[string]any{
		"event": "unavailable", "offering": "postgresql",
	})
	if res.IsError || seen.Event == nil || *seen.Event != (impact.Event{Kind: "unavailable", Offering: "postgresql"}) {
		t.Fatalf("event = %+v error %v", seen.Event, res.IsError)
	}
}

// TestImpactToolOfferingXorService: an event names exactly one subject.
func TestImpactToolOfferingXorService(t *testing.T) {
	for _, args := range []map[string]any{
		{"event": "unavailable", "offering": "postgresql", "service": "postgresql"},
		{"offering": "postgresql"},
	} {
		if res := callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, nil), nil), args); !res.IsError {
			t.Errorf("%v: want an error result", args)
		}
	}
}

// TestImpactToolOfferingRegressedRefused: a regression belongs to one revision.
func TestImpactToolOfferingRegressedRefused(t *testing.T) {
	res := callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, nil), nil), map[string]any{
		"event": "regressed", "offering": "postgresql", "revision": "1.0.0",
	})
	if !res.IsError {
		t.Errorf("regressed offering = %s, want an error result", resultText(t, res))
	}
}
