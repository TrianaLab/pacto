package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// stubImpact returns a provider yielding res/err, recording the arguments it saw.
// It records the observed flag in seen.IncludeObserved and the traces path in
// seenTraces (when non-nil).
func stubImpact(res *impact.Result, err error, seen *impact.Options) impactProvider {
	return stubImpactTraces(res, err, seen, nil)
}

func stubImpactTraces(res *impact.Result, err error, seen *impact.Options, seenTraces *string) impactProvider {
	return func(_ context.Context, _ *fleet.FleetSnapshot, req ImpactRequest) (*impact.Result, error) {
		if seen != nil {
			seen.IncludeObserved = req.IncludeObserved
			seen.Event = req.Event
		}
		if seenTraces != nil {
			*seenTraces = req.TracesPath
		}
		return res, err
	}
}

func TestImpactHandler_TracesReachProvider(t *testing.T) {
	var seenTraces string
	callHandler(t, impactTool(), impactHandler(stubImpactTraces(&impact.Result{}, nil, nil, &seenTraces), nil), map[string]any{
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
	res := callHandler(t, impactTool(), impactHandler(stubImpact(want, nil, &seen), nil), map[string]any{
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
	res := callHandler(t, impactTool(), impactHandler(stubImpact(nil, errors.New("old revision: boom"), nil), nil), map[string]any{
		"old_ref": "bad", "new_ref": "bad",
	})
	if !res.IsError {
		t.Error("expected an error result when the provider fails")
	}
}

func TestFleetMode_WithImpact_RegistersImpactTool(t *testing.T) {
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

func TestFleetMode_NilImpact_NoImpactTool(t *testing.T) {
	session := connectFleetServer(t, buildFleetQuery(t), nil)
	if toolNames(t, session)["pacto_impact"] {
		t.Error("did not expect pacto_impact when the impact provider is nil")
	}
}

func TestImpactToolEventArgs(t *testing.T) {
	var seen impact.Options
	res := callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, &seen), nil), map[string]any{
		"event": "regressed", "service": "payments", "revision": "sha256:a",
	})
	if res.IsError || seen.Event == nil || *seen.Event != (impact.Event{Kind: "regressed", Service: "payments", Revision: "sha256:a"}) {
		t.Fatalf("event = %+v error %v", seen.Event, res.IsError)
	}
	if callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, nil), nil), map[string]any{
		"event": "regressed", "service": "payments",
	}).IsError != true {
		t.Error("a regressed event without a revision is refused")
	}
}

func TestImpactToolNeedsPairOrEvent(t *testing.T) {
	for _, args := range []map[string]any{
		{},
		{"old_ref": "a"},
		{"old_ref": "a", "new_ref": "b", "event": "unavailable", "service": "x"},
		{"event": "unavailable"},
		{"service": "x"},
		{"old_ref": "a", "event": "removed", "service": "x"},
		{"event": "unavailable", "service": "redis", "revision": "sha256:abc"},
		{"event": "removed", "service": "redis", "revision": "1.0.0"},
		{"event": "unavailable", "service": "redis", "include_observed": true},
		{"running": true},
		{"running": true, "old_ref": "a", "new_ref": "b"},
	} {
		called := false
		provide := func(context.Context, *fleet.FleetSnapshot, ImpactRequest) (*impact.Result, error) {
			called = true
			return &impact.Result{}, nil
		}
		if res := callHandler(t, impactTool(), impactHandler(provide, nil), args); !res.IsError || called {
			t.Errorf("%v: want an error before the provider runs", args)
		}
	}
}

// TestImpactEventHrefs: event consumers link to their entity pages like any
// consumer, and an uncontracted caller does not.
func TestImpactEventHrefs(t *testing.T) {
	provide := stubImpact(&impact.Result{Event: &impact.Event{Kind: impact.EventUnavailable, Service: "redis"}, Consumers: []impact.AffectedConsumer{
		{Service: "web", Domain: "shop", Effect: impact.EffectBreaks}, {Service: "10.0.0.1", Uncontracted: true, Effect: impact.EffectUnknown},
	}}, nil, nil)
	linked := mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(hrefFleet(t)), Opts: FleetOptions{DashboardURL: "https://dash.example/", Impact: provide}}})
	text := resultText(t, callToolOn(t, linked, "pacto_impact", map[string]any{"event": "unavailable", "service": "redis"}))
	if strings.Count(text, `"href"`) != 1 || !strings.Contains(text, `/fleet/services/shop%2Fweb"`) {
		t.Fatalf("event hrefs:\n%s", text)
	}
}

// An event kind the tool does not know is refused before any analysis, even
// by a client that skips the input schema.
func TestImpactArgsRefuseUnknownEvent(t *testing.T) {
	if _, err := (impactArgs{Event: "exploded", Service: "x"}).request(); err == nil || err.Error() != "event is one of unavailable, removed or regressed" {
		t.Errorf("err = %v", err)
	}
}

// TestImpactToolSaysServiceIsOfferingName: an agent reading .service on an
// offering answer is told it holds the offering's name.
func TestImpactToolSaysServiceIsOfferingName(t *testing.T) {
	if d := impactTool().Description; !strings.Contains(d, "'service' then holds the offering's name, the same as 'event.offering'") {
		t.Errorf("description = %s", d)
	}
}

// TestImpactToolSaysHTTPReadsNoTargetSnapshot: the description says which
// servers read the change's target snapshot.
func TestImpactToolSaysHTTPReadsNoTargetSnapshot(t *testing.T) {
	d := impactTool().Description
	if !strings.Contains(d, "'registry', 'targetSnapshots'") || !strings.Contains(d, "an HTTP server reads none until per-cluster review exists") {
		t.Fatal(d)
	}
}

// TestImpactToolDescriptionNamesIndex: the description says how the
// organization index is read and which servers never read it.
func TestImpactToolDescriptionNamesIndex(t *testing.T) {
	d := impactTool().Description
	for _, want := range []string{"consumerIndex", "CONSUMER_NOT_INDEXED", "CONSUMER_INDEX_UNCHECKED", "Over HTTP the index is never read"} {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q", want)
		}
	}
}

func TestMCPImpactRunningRejectsOldRef(t *testing.T) {
	if req, err := (impactArgs{Running: true, OldRef: "a", NewRef: "b"}).request(); err == nil || !strings.Contains(err.Error(), "leave old_ref empty") || req != (ImpactRequest{}) {
		t.Errorf("err = %v", err)
	}
}

func TestMCPImpactRunningRejectsEvent(t *testing.T) {
	for _, a := range []impactArgs{{Event: "removed"}, {Service: "x"}, {Offering: "o"}, {Revision: "1"}} {
		a.Running, a.NewRef = true, "b"
		if _, err := a.request(); err == nil || !strings.Contains(err.Error(), "pass no event, service, offering or revision") {
			t.Errorf("%+v: %v", a, err)
		}
	}
}

// TestMCPImpactRunningRequest: running with new_ref alone reaches the
// provider as a running request.
func TestMCPImpactRunningRequest(t *testing.T) {
	var seen ImpactRequest
	provide := func(_ context.Context, _ *fleet.FleetSnapshot, req ImpactRequest) (*impact.Result, error) {
		seen = req
		return &impact.Result{}, nil
	}
	if res := callHandler(t, impactTool(), impactHandler(provide, nil), map[string]any{"running": true, "new_ref": "oci://r/p:2"}); res.IsError || !seen.Running || seen.NewRef != "oci://r/p:2" || seen.OldRef != "" {
		t.Fatalf("seen %+v error %v", seen, res.IsError)
	}
}
