package delivery

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// externalsPacto is payments' Pacto at status format 3 with the externals and
// coverage the operator wrote.
func externalsPacto(cov *CallerCoverageView, externals ...ExternalView) PactoView {
	p := paymentsPacto(complete())
	p.Status.StatusFormat = StatusFormatExternals
	p.Status.Externals = externals
	p.Status.ExternalsCoverage = cov
	p.Status.ExternalCatalog = externalcatalog.DefaultDigest() + " (embedded)"
	return p
}

var stripeEnv = ExternalView{Identity: "stripe", Class: "vendor-api", Confidence: "likely", Kind: "env",
	Where: "deployment/payments env STRIPE_API_BASE", Host: "api.stripe.com", Entry: "stripe"}

func TestExternalUsesFromStatus(t *testing.T) {
	reconciled := now
	uses := ExternalUses(externalsPacto(complete(), stripeEnv), &reconciled)
	want := fleet.RawExternalUse{Service: "payments", Domain: "reg.example/pactos", Namespace: "prod", Identity: "stripe",
		Class: "vendor-api", Confidence: "likely", Kind: "env", Where: stripeEnv.Where, Host: "api.stripe.com", Entry: "stripe",
		Catalog: externalcatalog.DefaultDigest(), ObservedAt: &reconciled}
	if len(uses) != 1 || uses[0].ObservedAt == nil || !uses[0].ObservedAt.Equal(now) {
		t.Fatalf("uses = %+v", uses)
	}
	got := uses[0]
	got.ObservedAt = want.ObservedAt
	if got != want {
		t.Fatalf("use = %+v\nwant %+v", got, want)
	}
	if ExternalLimitations(externalsPacto(complete(), stripeEnv), &reconciled, now) != nil {
		t.Fatal("a fresh complete coverage adds no limitation")
	}
	// The embedded catalog the operator names is the snapshot's own: no mismatch.
	snap, err := Snapshot(context.Background(), []PactoView{externalsPacto(complete(), stripeEnv)}, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationExternalCatalogMismatch || l.Code == fleet.LimitationExternalUseUnattributed {
			t.Fatalf("snapshot limitation = %+v", l)
		}
	}
	found := false
	for _, rel := range snap.Relationships {
		found = found || (rel.FromService == fleet.NewServiceKeyDomain("reg.example/pactos", "payments") && rel.ToExternal == "stripe" && rel.Provenance == fleet.ProvenanceInferred)
	}
	if !found {
		t.Fatalf("no inferred edge payments -> stripe: %+v", snap.Relationships)
	}
}

// unreported asserts one EXTERNALS_UNREPORTED limitation citing reason and
// saying why in plain words.
func unreported(t *testing.T, lims []fleet.Limitation, reason, words string) {
	t.Helper()
	if len(lims) != 1 || lims[0].Code != fleet.LimitationExternalsUnreported || lims[0].Source != SnapshotSourceID ||
		!strings.Contains(lims[0].Message, reason) || !strings.Contains(lims[0].Message, words) {
		t.Fatalf("limitations = %+v, want %s (%s)", lims, reason, words)
	}
}

func TestExternalUsesOldStatusFormatUnreported(t *testing.T) {
	reconciled := now
	p := externalsPacto(complete(), stripeEnv)
	p.Status.StatusFormat = StatusFormatDomain
	unreported(t, ExternalLimitations(p, &reconciled, now), "status-format", "does not report")
	// Format 3 with no coverage cannot say it is complete either.
	unreported(t, ExternalLimitations(externalsPacto(nil), &reconciled, now), "not-evaluated", "could not evaluate")
	// The old status reaches the target, so the External answer turns partial.
	snap, err := Snapshot(context.Background(), []PactoView{p}, nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range snap.Targets {
		if !slices.ContainsFunc(tg.Limitations, func(l fleet.Limitation) bool { return l.Code == fleet.LimitationExternalsUnreported }) {
			t.Fatalf("target limitations = %+v", tg.Limitations)
		}
	}
	v, err := fleet.NewQuery(snap).GetExternal("stripe")
	if err != nil || v.Meta.Completeness != fleet.CompletenessPartial {
		t.Fatalf("external = %+v, %v", v, err)
	}
}

func TestExternalUsesTruncatedPartial(t *testing.T) {
	reconciled := now
	p := externalsPacto(&CallerCoverageView{State: "partial", Reason: "truncated", RefreshSeconds: 300}, stripeEnv)
	unreported(t, ExternalLimitations(p, &reconciled, now), "partial, truncated", "more entries than the operator keeps")
	if len(ExternalUses(p, &reconciled)) != 1 {
		t.Fatal("a truncated list still reports what it kept")
	}
}

func TestExternalUsesStaleStatusUnavailable(t *testing.T) {
	old := now.Add(-11 * time.Minute)
	unreported(t, ExternalLimitations(externalsPacto(complete(), stripeEnv), &old, now), "unavailable, stale-status", "has not updated")
	unreported(t, ExternalLimitations(externalsPacto(complete()), nil, now), "stale-status", "has not updated")
	unreported(t, ExternalLimitations(externalsPacto(&CallerCoverageView{State: "complete"}), &now, now), "stale-status", "has not updated")
}

func TestExternalUsesServiceEntryOffPartial(t *testing.T) {
	reconciled := now
	for reason, words := range map[string]string{
		"serviceentry-reader-off":  "Istio ServiceEntries",
		"serviceentry-unreadable":  "could not read the Istio ServiceEntries",
		"object-host-unclassified": "under a private suffix",
		"pod-spec-unavailable":     "pod spec",
		"configmap-unreadable":     "ConfigMap",
		"made-up":                  "the operator reported made-up",
	} {
		p := externalsPacto(&CallerCoverageView{State: "partial", Reason: reason, RefreshSeconds: 300})
		unreported(t, ExternalLimitations(p, &reconciled, now), reason, words)
	}
}
