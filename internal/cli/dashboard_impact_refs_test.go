package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// TestDashboardImpactSharedRefusesRefOutsideView: a shared or open dashboard
// refuses a hidden revision's ref and a local dir as a 422 reference error,
// and resolves a visible ref through its pinned ResolvedRef.
func TestDashboardImpactSharedRefusesRefOutsideView(t *testing.T) {
	svc, pulled := pullRecorder()
	provide := impactProviderForFleet(svc, false)
	for name, refs := range map[string][2]string{
		"hidden old": {"oci://reg.example/ledger:1", "oci://reg.example/orders:2"},
		"hidden new": {"oci://reg.example/orders:1", "oci://reg.example/ledger:2"},
		"local dir":  {"./orders", "oci://reg.example/orders:2"},
	} {
		_, err := provide(context.Background(), viewSnapshot(), refs[0], refs[1], false)
		var invalid *oci.InvalidRefError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: %v, want *oci.InvalidRefError", name, err)
		}
	}
	if _, err := provide(context.Background(), nil, "oci://reg.example/orders:1", "oci://reg.example/orders:2", false); err == nil || !strings.Contains(err.Error(), "old is not a revision in your fleet view") {
		t.Errorf("no snapshot: %v", err)
	}
	if len(*pulled) != 0 {
		t.Fatalf("a refused request resolved %v", *pulled)
	}
	_, _ = provide(context.Background(), viewSnapshot(), "oci://attacker.example/x@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "oci://reg.example/orders:2", false)
	if len(*pulled) != 1 || !strings.Contains((*pulled)[0], "reg.example/orders@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("resolved %v, want the visible revision's ResolvedRef", *pulled)
	}
}

// TestDashboardImpactLocalRefsUnchanged: a local dashboard passes the
// caller's refs, local dirs included, through as given.
func TestDashboardImpactLocalRefsUnchanged(t *testing.T) {
	svc, pulled := pullRecorder()
	if _, err := impactProviderForFleet(svc, true)(context.Background(), viewSnapshot(), "/nonexistent/old", "/nonexistent/new", false); err == nil || strings.Contains(err.Error(), "fleet view") {
		t.Fatalf("local dir on a local dashboard: %v", err)
	}
	_, _ = impactProviderForFleet(svc, true)(context.Background(), viewSnapshot(), "oci://other.example/x:1", "oci://other.example/x:2", false)
	if len(*pulled) != 1 || !strings.Contains((*pulled)[0], "other.example/x:1") {
		t.Fatalf("resolved %v, want the caller's ref", *pulled)
	}
}

// TestPinVisibleRefInferredProvider: the inferred contract revision a caller
// sees on an external system pins like a fleet revision; one with no
// immutable ref is refused.
func TestPinVisibleRefInferredProvider(t *testing.T) {
	d := "sha256:" + strings.Repeat("b", 64)
	pinned := "oci://zot:5000/demo/pacto-inferred/stripe@" + d
	snap := &fleet.FleetSnapshot{Externals: map[fleet.ExternalKey]*fleet.ExternalRecord{
		"none":   {Key: "none"},
		"stripe": {Key: "stripe", Provider: &fleet.ExternalProvider{Revisions: []fleet.ExternalProviderRevision{{Version: "0.0.1", Digest: d, Ref: pinned}}}},
		"mq":     {Key: "mq", Provider: &fleet.ExternalProvider{Revisions: []fleet.ExternalProviderRevision{{Version: "0.0.1", Digest: "sha256:" + strings.Repeat("c", 64), Ref: "oci://zot:5000/demo/pacto-inferred/mq:0.0.1"}}}},
	}}
	if got, err := pinVisibleRef(snap, pinned); err != nil || got != pinned {
		t.Errorf("visible provider: %q %v", got, err)
	}
	if _, err := pinVisibleRef(snap, "oci://zot:5000/demo/pacto-inferred/mq:0.0.1"); err == nil {
		t.Error("a mutable provider ref was pinned")
	}
}
