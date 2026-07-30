package fleet

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Two sources contributing the SAME target key but disagreeing on an
// identity-bearing field must quarantine the target and surface a structured
// conflict — deterministically, regardless of source order (review section D).
func TestTargetMerge_IdentityConflict_QuarantineAndOrderIndependent(t *testing.T) {
	rev := func(domain string) RawRevision {
		return RawRevision{
			Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.0",
				Service: contract.Service{Name: "payments", Version: "1.0.0"}}, FS: fstest.MapFS{}},
			Domain: domain, ResolvedRef: "oci://" + domain + "/payments:1.0.0", Digest: "sha256:" + domain,
		}
	}
	// Same operational target key (prod/k8s/pay), but two sources resolve it to
	// DIFFERENT domains — a genuine identity conflict.
	srcA := NewMemorySource("a", "k8s", &Collection{
		Revisions: []RawRevision{rev("reg-a")},
		Targets:   []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pay", Service: "payments", Domain: "reg-a", Compliance: StatusCompliant}},
	})
	srcB := NewMemorySource("b", "k8s", &Collection{
		Revisions: []RawRevision{rev("reg-b")},
		Targets:   []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pay", Service: "payments", Domain: "reg-b", Compliance: StatusNonCompliant}},
	})

	check := func(t *testing.T, snap *FleetSnapshot) {
		t.Helper()
		var target *TargetRecord
		for _, tr := range snap.Targets {
			target = tr
		}
		if target == nil {
			t.Fatal("expected one merged target")
		}
		if !target.Quarantined {
			t.Errorf("conflicting-identity target must be quarantined: %+v", target)
		}
		if !hasLim(snap.Limitations, LimitationTargetFieldConflict) {
			t.Errorf("expected a TARGET_FIELD_CONFLICT limitation, got %+v", snap.Limitations)
		}
	}

	snapAB, err := Build(context.Background(), BuildOptions{Now: fixedNow}, srcA, srcB)
	if err != nil {
		t.Fatal(err)
	}
	snapBA, err := Build(context.Background(), BuildOptions{Now: fixedNow}, srcB, srcA)
	if err != nil {
		t.Fatal(err)
	}
	check(t, snapAB)
	check(t, snapBA)

	// Order independence: the quarantine outcome is identical either way.
	if len(snapAB.Targets) != len(snapBA.Targets) {
		t.Errorf("target count differs by order: %d vs %d", len(snapAB.Targets), len(snapBA.Targets))
	}
}

// An empty identity field on one side is filled from the other — that is a merge,
// not a conflict.
func TestTargetMerge_FillsEmptyIdentity(t *testing.T) {
	withDomain := NewMemorySource("a", "k8s", &Collection{
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pay", Service: "payments", Domain: "reg-a", Compliance: StatusCompliant}},
	})
	noDomain := NewMemorySource("b", "evidence-ingest", &Collection{
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pay", Service: "payments", Compliance: StatusCompliant}},
	})
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, noDomain, withDomain)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range snap.Targets {
		if tr.Quarantined {
			t.Errorf("filling an empty domain must not quarantine: %+v", tr)
		}
		if tr.Domain != "reg-a" {
			t.Errorf("empty domain should be filled to reg-a, got %q", tr.Domain)
		}
	}
	if hasLim(snap.Limitations, LimitationTargetFieldConflict) {
		t.Errorf("no conflict expected when filling empty: %+v", snap.Limitations)
	}
}

// The evaluation-freshness merge must never carry the identity-bearing Digest:
// a fresher evaluation that happens to lack an image digest must not wipe a
// digest another source already established, or the serialized snapshot and its
// SnapshotID would depend on source order (review section S14).
func TestTargetMerge_EvaluationDoesNotOverrideDigest_OrderIndependent(t *testing.T) {
	older := fixedNow().Add(-time.Hour)
	newer := fixedNow()
	// k8s knows the running image digest but its evidence is older.
	withDigest := NewMemorySource("k8s", "k8s", &Collection{
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pay", Service: "payments",
			Digest: "sha256:aaa", Compliance: StatusCompliant, EvidenceAt: &older}},
	})
	// evidence-ingest has fresher evidence but no image digest.
	noDigest := NewMemorySource("ev", "evidence-ingest", &Collection{
		Targets: []RawTarget{{Scope: "prod", Kind: "k8s", Name: "pay", Service: "payments",
			Compliance: StatusCompliant, EvidenceAt: &newer}},
	})

	digestOf := func(t *testing.T, snap *FleetSnapshot) string {
		t.Helper()
		var d string
		var n int
		for _, tr := range snap.Targets {
			d = tr.Digest
			n++
		}
		if n != 1 {
			t.Fatalf("expected one merged target, got %d", n)
		}
		return d
	}

	snapDN, err := Build(context.Background(), BuildOptions{Now: fixedNow}, withDigest, noDigest)
	if err != nil {
		t.Fatal(err)
	}
	snapND, err := Build(context.Background(), BuildOptions{Now: fixedNow}, noDigest, withDigest)
	if err != nil {
		t.Fatal(err)
	}

	if d := digestOf(t, snapDN); d != "sha256:aaa" {
		t.Errorf("[withDigest, noDigest] digest = %q, want sha256:aaa (a fresher empty-digest evaluation must not wipe it)", d)
	}
	if d := digestOf(t, snapND); d != "sha256:aaa" {
		t.Errorf("[noDigest, withDigest] digest = %q, want sha256:aaa", d)
	}
	if snapDN.SnapshotID != snapND.SnapshotID {
		t.Errorf("SnapshotID depends on source order: %q vs %q", snapDN.SnapshotID, snapND.SnapshotID)
	}
}

func hasLim(ls []Limitation, code string) bool {
	for _, l := range ls {
		if l.Code == code {
			return true
		}
	}
	return false
}
