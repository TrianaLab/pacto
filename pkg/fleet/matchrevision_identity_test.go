package fleet

import (
	"strings"
	"testing"
)

// These tests enforce ONE identity truth (requirement, item 6): target-to-revision
// linking uses the same ClassifyExactIdentity invariant as RevisionDetail,
// TargetDetail and Product Impact, so the exact tier and the identity class can
// never contradict one another.

func digestFill(fill string) string { return "sha256:" + strings.Repeat(fill, 64) }

// identitySnap builds a fleet with one service whose revisions carry the given
// digests, each with a canonical oci:// digest ResolvedRef.
func identitySnap() (*FleetSnapshot, ServiceKey, string, string) {
	svc := NewServiceKey("svc")
	dA := digestFill("a")
	dB := digestFill("b")
	return &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@a": {Key: "svc@a", Service: "svc", ServiceKey: svc, Digest: dA, ResolvedRef: "oci://reg/svc@" + dA},
		"svc@b": {Key: "svc@b", Service: "svc", ServiceKey: svc, Digest: dB, ResolvedRef: "oci://reg/svc@" + dB},
	}}, svc, dA, dB
}

// Counterexample A: a target whose ResolvedRef is a canonical digest ref but which
// records no separate Digest is EXACT (IdentityMissingDigest), matched by the ref's
// digest -- not merely an inferred resolved-ref correlation.
func TestMatchRevision_CanonicalRefMissingDigest_Exact(t *testing.T) {
	snap, svc, dA, _ := identitySnap()
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, ResolvedRef: "oci://reg/svc@" + dA})
	if key != "svc@a" || kind != revisionMatchExact {
		t.Errorf("got %q/%q, want svc@a/exact (a digest-pinned ref with no recorded digest is exact)", key, kind)
	}
}

// digest field + matching canonical resolvedRef => exact.
func TestMatchRevision_DigestAndMatchingRef_Exact(t *testing.T) {
	snap, svc, dA, _ := identitySnap()
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, Digest: dA, ResolvedRef: "oci://reg/svc@" + dA})
	if key != "svc@a" || kind != revisionMatchExact {
		t.Errorf("got %q/%q, want svc@a/exact", key, kind)
	}
}

// Counterexample B: a target whose recorded Digest contradicts its digest-pinned
// ResolvedRef is internally inconsistent -- NEVER an exact link off the contradicting
// recorded digest.
func TestMatchRevision_DigestRefMismatch_NeverExact(t *testing.T) {
	snap, svc, dA, dB := identitySnap()
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, Digest: dB, ResolvedRef: "oci://reg/svc@" + dA})
	if kind == revisionMatchExact {
		t.Errorf("digest/ref mismatch produced an exact link to %q; identity is internally inconsistent", key)
	}
	if kind != revisionMatchInconsistent {
		t.Errorf("got kind %q, want %q for an internally inconsistent identity", kind, revisionMatchInconsistent)
	}
}

// A recorded Digest identifies known content: with no contradicting ref it links
// exact (the k8s operator observes the running digest). Exactness is by CONTENT, so
// a recorded digest is authoritative when nothing contradicts it.
func TestMatchRevision_RecordedDigestNoRef_Exact(t *testing.T) {
	snap, svc, dA, _ := identitySnap()
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, Digest: dA})
	if key != "svc@a" || kind != revisionMatchExact {
		t.Errorf("got %q/%q, want svc@a/exact (a recorded digest identifies known content)", key, kind)
	}
}

// A scheme-less ref embedding a digest that MATCHES the recorded digest links exact.
// This is the real k8s operator shape ("registry/repo:tag@sha256:...", the oci://
// scheme stripped by the operator): the embedded and recorded digests agree, so the
// content is known exactly and the target links exact.
func TestMatchRevision_SchemelessMatchingDigest_Exact(t *testing.T) {
	snap, svc, dA, _ := identitySnap()
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, Digest: dA, ResolvedRef: "reg/svc:1.0@" + dA})
	if key != "svc@a" || kind != revisionMatchExact {
		t.Errorf("got %q/%q, want svc@a/exact (scheme-less ref embedding a matching digest)", key, kind)
	}
}

// A ref (scheme-less or oci://) embedding a digest that CONTRADICTS the recorded
// digest is internally inconsistent and must NEVER link exact -- this is the residual
// the second review found: a scheme-less "reg/svc@<other>" ref, classed local, whose
// embedded digest the oci:// classifier never parses, must still be subject to the
// same digest-mismatch guard.
func TestMatchRevision_ContradictingEmbeddedDigest_Inconsistent(t *testing.T) {
	dA := digestFill("a")
	dB := digestFill("b")
	for _, ref := range []string{"reg/svc@" + dB, "reg/svc:1.0@" + dB, "oci://reg/svc@" + dB} {
		snap, svc, _, _ := identitySnap()
		key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, Digest: dA, ResolvedRef: ref})
		if kind == revisionMatchExact {
			t.Errorf("ref %q embeds a digest contradicting the recorded one but got exact link %q", ref, key)
		}
		if kind != revisionMatchInconsistent {
			t.Errorf("ref %q: got %q, want inconsistent (contradicting embedded digest)", ref, kind)
		}
	}
}

// linkTargets surfaces a limitation and makes no exact link for a target whose ref
// embeds a digest contradicting its recorded digest, so the target self-describes the
// internal inconsistency instead of presenting a false exact link.
func TestLinkTargets_ContradictingEmbeddedDigest_Limitation(t *testing.T) {
	dA := digestFill("a")
	dB := digestFill("b")
	snap := &FleetSnapshot{
		Targets: map[TargetKey]*TargetRecord{
			"t": {Key: "t", ServiceKey: NewServiceKey("svc"), Digest: dA, ResolvedRef: "reg/svc@" + dB},
		},
		Revisions: map[RevisionKey]*ContractRevision{
			"svc@a": {Key: "svc@a", Service: "svc", ServiceKey: NewServiceKey("svc"), Digest: dA, ResolvedRef: "oci://reg/svc@" + dA},
		},
	}
	linkTargets(snap)
	tr := snap.Targets["t"]
	if targetLinkState(tr) == "exact" {
		t.Error("a contradicting embedded digest must not yield an exact linkState")
	}
	if !hasLimitation(tr.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("target should carry a limitation for its inconsistent identity: %+v", tr.Limitations)
	}
	if !hasLimitation(snap.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("snapshot should surface the inconsistent-identity limitation: %+v", snap.Limitations)
	}
}

// A malformed oci:// identity is never exact and is not silently correlated.
func TestMatchRevision_MalformedIdentity_NotExact(t *testing.T) {
	snap, svc, _, _ := identitySnap()
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, ResolvedRef: "oci://reg/svc@notadigest"})
	if kind == revisionMatchExact {
		t.Errorf("malformed identity produced an exact link to %q", key)
	}
	if kind != revisionMatchInconsistent {
		t.Errorf("got kind %q, want %q for a malformed identity", kind, revisionMatchInconsistent)
	}
}

// An exact (digest-pinned) identity whose content is in no fleet revision yields no
// link and no dishonest mutable fallback.
func TestMatchRevision_ExactIdentityNoMatchingRevision_Unresolved(t *testing.T) {
	snap, svc, _, _ := identitySnap()
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, ResolvedRef: "oci://reg/svc@" + digestFill("c")})
	if key != "" || kind != "" {
		t.Errorf("got %q/%q, want no link (exact content not present in the fleet)", key, kind)
	}
}

// A mutable tag that uniquely correlates is inferred (unchanged honest semantics).
func TestMatchRevision_MutableTagUnique_Inferred(t *testing.T) {
	svc := NewServiceKey("svc")
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@x": {Key: "svc@x", Service: "svc", ServiceKey: svc, ResolvedRef: "reg/svc:2.0"},
	}}
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, ResolvedRef: "reg/svc:2.0"})
	if key != "svc@x" || kind != revisionMatchInferred {
		t.Errorf("got %q/%q, want svc@x/inferred", key, kind)
	}
}

// A mutable tag that matches multiple revisions is ambiguous (no link).
func TestMatchRevision_MutableTagMultiple_Ambiguous(t *testing.T) {
	svc := NewServiceKey("svc")
	snap := &FleetSnapshot{Revisions: map[RevisionKey]*ContractRevision{
		"svc@x": {Key: "svc@x", Service: "svc", ServiceKey: svc, ResolvedRef: "reg/svc:2.0"},
		"svc@y": {Key: "svc@y", Service: "svc", ServiceKey: svc, ResolvedRef: "reg/svc:2.0"},
	}}
	key, kind := matchRevision(snap, &TargetRecord{ServiceKey: svc, ResolvedRef: "reg/svc:2.0"})
	if kind != revisionMatchAmbiguous || key != "" {
		t.Errorf("got %q/%q, want /ambiguous", key, kind)
	}
}

// linkTargets surfaces a limitation for an internally inconsistent identity, and
// the target's linkState is never exact.
func TestLinkTargets_InconsistentIdentity_Limitation(t *testing.T) {
	svc := NewServiceKey("svc")
	dA := digestFill("a")
	dB := digestFill("b")
	snap := &FleetSnapshot{Targets: map[TargetKey]*TargetRecord{
		"t": {Key: "t", ServiceKey: svc, Digest: dB, ResolvedRef: "oci://reg/svc@" + dA},
	}, Revisions: map[RevisionKey]*ContractRevision{
		"svc@b": {Key: "svc@b", Service: "svc", ServiceKey: svc, Digest: dB, ResolvedRef: "oci://reg/svc@" + dB},
	}}
	linkTargets(snap)
	tr := snap.Targets["t"]
	if tr.RevisionMatch == revisionMatchExact {
		t.Error("inconsistent identity must not yield an exact revision match")
	}
	if targetLinkState(tr) == "exact" {
		t.Error("inconsistent identity must not yield an exact linkState")
	}
	if !hasLimitation(tr.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("target should carry a limitation for its inconsistent identity: %+v", tr.Limitations)
	}
	if !hasLimitation(snap.Limitations, LimitationSourceRecordInvalid) {
		t.Errorf("snapshot should surface the inconsistent-identity limitation: %+v", snap.Limitations)
	}
}
