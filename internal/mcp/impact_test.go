package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/impact"
)

// stubImpact returns a provider yielding res/err, recording the arguments it saw.
// It records the observed flag in seen.IncludeObserved and the traces path in
// seenTraces (when non-nil).
func stubImpact(res *impact.Result, err error, seen *impact.Options) impactProvider {
	return stubImpactTraces(res, err, seen, nil)
}

func stubImpactTraces(res *impact.Result, err error, seen *impact.Options, seenTraces *string) impactProvider {
	return func(_ context.Context, _, _ string, includeObserved bool, tracesPath string) (*impact.Result, error) {
		if seen != nil {
			seen.IncludeObserved = includeObserved
		}
		if seenTraces != nil {
			*seenTraces = tracesPath
		}
		return res, err
	}
}

func TestImpactHandler_TracesReachProvider(t *testing.T) {
	var seenTraces string
	callHandler(t, impactHandler(stubImpactTraces(&impact.Result{}, nil, nil, &seenTraces)), map[string]any{
		"old_ref": "a", "new_ref": "b", "traces": "/tmp/traces.json",
	})
	if seenTraces != "/tmp/traces.json" {
		t.Errorf("traces path = %q, want /tmp/traces.json", seenTraces)
	}
}

func TestImpactHandler_Success(t *testing.T) {
	want := &impact.Result{
		SchemaVersion:  impact.SchemaVersion,
		Service:        "orders",
		Classification: "BREAKING",
		Consumers: []impact.AffectedConsumer{
			{Service: "checkout", Depth: 1, Direct: true, Confidence: impact.ConfidenceContractual},
		},
	}
	var seen impact.Options
	res := callHandler(t, impactHandler(stubImpact(want, nil, &seen)), map[string]any{
		"old_ref": "oci://x/svc:1.0.0", "new_ref": "oci://x/svc:2.0.0", "include_observed": true,
	})
	var out impact.Result
	decode(t, res, &out)
	if out.Service != "orders" || out.Classification != "BREAKING" {
		t.Fatalf("unexpected result: %+v", out)
	}
	if len(out.Consumers) != 1 || out.Consumers[0].Service != "checkout" {
		t.Fatalf("unexpected consumers: %+v", out.Consumers)
	}
	if !seen.IncludeObserved {
		t.Error("expected include_observed=true to reach the provider")
	}
}

func TestImpactHandler_Error(t *testing.T) {
	res := callHandler(t, impactHandler(stubImpact(nil, errors.New("old revision: boom"), nil)), map[string]any{
		"old_ref": "bad", "new_ref": "bad",
	})
	if !res.IsError {
		t.Error("expected an error result when the provider fails")
	}
}

func TestNewFleetServer_WithImpact_RegistersImpactTool(t *testing.T) {
	session := connectFleetServer(t, buildFleetQuery(t), stubImpact(&impact.Result{}, nil, nil))
	names := toolNames(t, session)
	if !names["pacto_impact"] {
		t.Error("expected pacto_impact to be registered when an impact provider is set")
	}
	// Fleet tools remain registered alongside it.
	if !names["pacto_fleet_search"] {
		t.Error("expected fleet tools alongside pacto_impact")
	}
}

func TestNewFleetServer_NilImpact_NoImpactTool(t *testing.T) {
	session := connectFleetServer(t, buildFleetQuery(t), nil)
	if toolNames(t, session)["pacto_impact"] {
		t.Error("did not expect pacto_impact when the impact provider is nil")
	}
}
