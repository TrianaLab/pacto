package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

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
