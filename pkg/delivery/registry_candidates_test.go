package delivery

import (
	"context"
	"strings"
	"testing"
)

var registryCands = Candidates{Registry: &RegistryCandidates{Repository: "ghcr.io/acme/payments"}}

// TestPublishedGateRegistry: from the registry watch the gate passes only a
// digest the publisher check accepted (status.published) whose artifact names
// its contract.
func TestPublishedGateRegistry(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	stage := stageSpec()
	stage.Candidates = registryCands
	plan := func(status StageStatus, to Revision) Plan {
		snap, _ := Snapshot(context.Background(), nil, nil, []Revision{from, to}, now)
		return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stage, Status: status, From: from, To: to, Snapshot: snap, Class: ClassAgent})
	}
	accepted := StageStatus{Pointer: dA, Published: []string{dB}, Verified: []string{dA}}
	checkPublishedCase(t, "accepted", plan(accepted, to), Pass, "is signed and names its contract")
	checkPublishedCase(t, "not accepted", plan(StageStatus{Pointer: dA}, to), Fail,
		"no publisher check accepted "+short(dB)+" on ghcr.io/acme/payments: see pacto delivery trust payments")
	checkPublishedCase(t, "no contract", plan(accepted, Revision{Digest: dB, NoContract: true}), Fail, "names no digest-pinned")
	if v := verdict(t, plan(accepted, to), GatePublished); v.Citation["source"] != "ghcr.io/acme/payments" {
		t.Fatalf("citation %+v", v.Citation)
	}
}

func TestCandidateDigestsRegistry(t *testing.T) {
	s := candidateStage(registryCands, StageStatus{Published: []string{cand1, cand2, cand3}, FailedDigests: []string{cand3}})
	if got := CandidateDigests(s, nil); strings.Join(got, ",") != cand2+","+cand1 {
		t.Fatalf("accepted, newest first, never a failed one: %v", got)
	}
}

func TestCandidateSourceRegistry(t *testing.T) {
	if got := candidateSource(registryCands); got != "registry ghcr.io/acme/payments" {
		t.Fatalf("named repository: %q", got)
	}
	if got := candidateSource(Candidates{Registry: &RegistryCandidates{}}); got != "registry the applier's source repository" {
		t.Fatalf("default repository: %q", got)
	}
}

func TestSourceNoteRegistry(t *testing.T) {
	got := sourceNote(candidateStage(registryCands, StageStatus{}), nil, "", nil)
	if got != "the registry watch accepted no version yet: see pacto delivery trust payments" {
		t.Fatalf("note %q", got)
	}
	if got := sourceNote(candidateStage(registryCands, StageStatus{}), nil, "", []CandidateVersion{{Digest: cand1}}); got != "" {
		t.Fatalf("a listed version needs no note: %q", got)
	}
}

func TestCandidatesNoteRegistryReasons(t *testing.T) {
	for _, r := range []string{ReadyReasonRegistryUnreadable, ReadyReasonPublisherUnverifiable} {
		st := candidateStage(registryCands, StageStatus{Conditions: []Condition{{Type: ConditionReady, Status: "False", Reason: r, Message: "the reason"}}})
		if got := candidatesNote(st, nil, "", nil); got != "the reason: "+ReadyFix(r) {
			t.Errorf("%s: %q", r, got)
		}
	}
}

func TestUpstreamListedRegistry(t *testing.T) {
	at := func(c Candidates, reason string) Upstream {
		st := stage("prod", "pay", "pay", StageStatus{})
		st.Spec.Candidates = c
		if reason != "" {
			st.Status.Conditions = []Condition{{Type: ConditionReady, Status: "False", Reason: reason}}
		}
		return *UpstreamListed(&st, nil, nil, "")
	}
	for _, c := range []struct {
		got, want Upstream
	}{
		{at(registryCands, ""), Upstream{Kind: "artifact", Repository: "ghcr.io/acme/payments"}},
		{at(registryCands, ReadyReasonRegistryUnreadable), Upstream{Kind: "artifact", Repository: "ghcr.io/acme/payments", Problem: "unreadable"}},
		{at(registryCands, ReadyReasonPublisherUnverifiable), Upstream{Kind: "artifact", Repository: "ghcr.io/acme/payments", Problem: "unverified"}},
		{at(Candidates{Registry: &RegistryCandidates{}}, ""), Upstream{Kind: "artifact", Repository: "the applier's source repository"}},
	} {
		if c.got != c.want {
			t.Errorf("got %+v, want %+v", c.got, c.want)
		}
	}
}

func TestReadyFixNoCandidateSourceNamesRegistry(t *testing.T) {
	if !strings.Contains(ReadyFix(ReadyReasonNoCandidateSource), "spec.candidates.registry") {
		t.Fatal(ReadyFix(ReadyReasonNoCandidateSource))
	}
	for _, r := range []string{ReadyReasonRegistryUnreadable, ReadyReasonPublisherUnverifiable} {
		if ReadyFix(r) == "" {
			t.Errorf("%s has no fix", r)
		}
	}
}

// TestSignatureEvidenceRegistryUnsigned: an entry Stage fed by the registry
// watch says unsigned only for a digest the watch judged unsigned or refused.
func TestSignatureEvidenceRegistryUnsigned(t *testing.T) {
	const d = "sha256:d"
	at := func(p *PublisherStatus) StageObject {
		st := stage("prod", "pay", "pay", StageStatus{Pointer: d, Publisher: p})
		st.Spec.Candidates = registryCands
		return st
	}
	verdicts := func(v string) *PublisherStatus {
		return &PublisherStatus{Verdicts: []PublisherVerdict{{Digest: "sha256:other", Verdict: VerdictUnsigned}, {Digest: d, Verdict: v}}}
	}
	for name, c := range map[string]struct {
		st   StageObject
		want string
	}{
		"unsigned":   {at(verdicts(VerdictUnsigned)), SignatureUnsigned},
		"refused":    {at(verdicts(VerdictRefused)), SignatureUnsigned},
		"new signer": {at(verdicts(VerdictNewSigner)), SignatureUnknown},
		"unknown":    {at(verdicts(VerdictUnknown)), SignatureUnknown},
		"no verdict": {at(nil), SignatureUnknown},
	} {
		var e SignatureEvidence
		e.Add(c.st, d)
		if got := e.Signature(); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}
