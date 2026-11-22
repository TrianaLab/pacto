package mcp

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/impact"
)

// TestImpactExternalEvent: external reaches the provider as Event.External.
func TestImpactExternalEvent(t *testing.T) {
	for _, kind := range []string{impact.EventChanged, impact.EventUnavailable} {
		var seen impact.Options
		res := callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, &seen), nil), map[string]any{
			"event": kind, "external": "stripe",
		})
		if res.IsError || seen.Event == nil || *seen.Event != (impact.Event{Kind: kind, External: "stripe"}) {
			t.Fatalf("%s: event = %+v error %s", kind, seen.Event, resultText(t, res))
		}
	}
}

// TestImpactExternalExclusive: an event names exactly one subject, and an
// external system is never removed or regressed.
func TestImpactExternalExclusive(t *testing.T) {
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"event": "changed", "external": "stripe", "service": "payments"}, "exactly one of 'service', 'offering' and 'external'"},
		{map[string]any{"event": "unavailable", "external": "stripe", "offering": "postgresql"}, "exactly one of"},
		{map[string]any{"event": "removed", "external": "stripe"}, "not yours to remove or regress"},
		{map[string]any{"event": "regressed", "external": "stripe", "revision": "1.0.0"}, "not yours to remove or regress"},
		{map[string]any{"event": "changed", "external": "Stripe"}, "pacto fleet search"},
		{map[string]any{"external": "stripe"}, "pass either old_ref and new_ref, or event and service (or offering, or external)"},
	} {
		res := callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, nil), nil), tc.args)
		if !res.IsError || !strings.Contains(resultText(t, res), tc.want) {
			t.Errorf("%v = %s, want an error naming %q", tc.args, resultText(t, res), tc.want)
		}
	}
}

// TestImpactChangedNeedsExternal: changed is a change of an external system.
func TestImpactChangedNeedsExternal(t *testing.T) {
	res := callHandler(t, impactTool(), impactHandler(stubImpact(&impact.Result{}, nil, nil), nil), map[string]any{
		"event": "changed", "service": "payments",
	})
	if !res.IsError || !strings.Contains(resultText(t, res), "a changed event names an 'external' system") {
		t.Errorf("changed service = %s", resultText(t, res))
	}
}

// TestImpactRunningRefusesExternal: a running question asks about a
// contract change, so an external is refused like any event subject.
func TestImpactRunningRefusesExternal(t *testing.T) {
	_, err := impactArgs{Running: true, NewRef: "oci://r/a:1", External: "stripe"}.request()
	if err == nil || err.Error() != "running asks about a contract change: pass no event, service, offering, external or revision" {
		t.Errorf("running external = %v", err)
	}
}
