package delivery

import (
	"context"
	"strings"
	"testing"
	"time"
)

const (
	cand1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	cand2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	cand3 = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

func candidateStage(c Candidates, st StageStatus) StageObject {
	return StageObject{Metadata: ObjectMeta{Name: "payments", Namespace: "prod"},
		Spec: StageSpec{Service: ServiceRef{Name: "payments"}, Candidates: c}, Status: st}
}

// describeVersions reads cand1 as 2.4.0 and cand2 as 3.0.0; cand3 names no
// contract, and anything else cannot be read.
func describeVersions(_ context.Context, d string) Revision {
	switch d {
	case cand1:
		return Revision{Digest: d, ContractRef: "oci://reg/c@" + d, Bundle: bundle("payments", "2.4.0")}
	case cand2:
		return Revision{Digest: d, ContractRef: "oci://reg/c@" + d, Bundle: bundle("payments", "3.0.0")}
	case cand3:
		return Revision{Digest: d, NoContract: true}
	}
	return Revision{Digest: d, Err: "manifest unknown"}
}

func TestCandidateDigests(t *testing.T) {
	oci := Candidates{OCIRepositoryRef: &LocalRef{Name: "payments-candidates"}}
	s := candidateStage(oci, StageStatus{Published: []string{cand1, cand2, cand3}, FailedDigests: []string{cand3}})
	if got := CandidateDigests(s, nil); strings.Join(got, ",") != cand2+","+cand1 {
		t.Fatalf("published, newest first, never a failed one: %v", got)
	}
	up := candidateStage(Candidates{UpstreamStage: &StageRef{Name: "payments", Namespace: "test"}}, StageStatus{})
	if got := CandidateDigests(up, nil); len(got) != 0 {
		t.Fatalf("an unread upstream offers nothing: %v", got)
	}
	if got := CandidateDigests(up, &StageStatus{Offered: []string{cand1, cand2}, Verified: []string{cand1}}); strings.Join(got, ",") != cand1 {
		t.Fatalf("offered and verified upstream: %v", got)
	}
	if got := CandidateDigests(candidateStage(Candidates{}, StageStatus{Published: []string{cand1}}), nil); len(got) != 0 {
		t.Fatalf("no candidate source, no candidate: %v", got)
	}
}

func TestListCandidates(t *testing.T) {
	oci := candidateStage(Candidates{OCIRepositoryRef: &LocalRef{Name: "payments-candidates"}},
		StageStatus{Pointer: cand1, Published: []string{cand1, cand2, cand3}})
	l := ListCandidates(context.Background(), oci, nil, "", describeVersions)
	if l.Source != "candidate OCIRepository payments-candidates" || l.Running != cand1 || l.Note != "" || len(l.Versions) != 2 ||
		l.Versions[0].Version != "3.0.0" || l.Versions[1].Version != "2.4.0" || l.Versions[0].Contract != "oci://reg/c@"+cand2 {
		t.Fatalf("a digest with no contract is left out: %+v", l)
	}
	oci.Status.Published = []string{"sha256:9"}
	if l := ListCandidates(context.Background(), oci, nil, "", describeVersions); l.Versions[0].Unreadable != "manifest unknown" ||
		!strings.Contains(l.Note, "some candidates could not be read") {
		t.Fatalf("an unreadable candidate: %+v", l)
	}
	oci.Status.Published = nil
	if l := ListCandidates(context.Background(), oci, nil, "", describeVersions); !strings.Contains(l.Note, "publish one with pacto publish") {
		t.Fatalf("nothing published yet: %+v", l)
	}
	up := candidateStage(Candidates{UpstreamStage: &StageRef{Name: "payments", Namespace: "test"}}, StageStatus{})
	if l := ListCandidates(context.Background(), up, nil, "", describeVersions); l.Source != "upstream Stage test/payments" ||
		!strings.Contains(l.Note, "does not exist or cannot be read") {
		t.Fatalf("an unread upstream: %+v", l)
	}
	if l := ListCandidates(context.Background(), up, nil, "no trust config was given", describeVersions); l.Note != "no trust config was given, so it offers no candidate" {
		t.Fatalf("why the upstream gave nothing: %+v", l)
	}
	if l := ListCandidates(context.Background(), up, &StageStatus{}, "", describeVersions); !strings.Contains(l.Note, "promote one there first") {
		t.Fatalf("an upstream that offered nothing yet: %+v", l)
	}
	if l := ListCandidates(context.Background(), candidateStage(Candidates{}, StageStatus{}), nil, "", describeVersions); l.Source != "" ||
		!strings.Contains(l.Note, "names no candidate source") {
		t.Fatalf("no candidate source: %+v", l)
	}
}

// Every read shares one bound: once it is spent, the rest say so unread.
func TestListCandidatesOneBound(t *testing.T) {
	defer func(d time.Duration) { registryTimeout = d }(registryTimeout)
	registryTimeout = 20 * time.Millisecond
	s := candidateStage(Candidates{OCIRepositoryRef: &LocalRef{Name: "c"}}, StageStatus{Published: []string{cand1, cand2}})
	reads := 0
	l := ListCandidates(context.Background(), s, nil, "", func(ctx context.Context, d string) Revision {
		reads++
		<-ctx.Done()
		return Revision{Digest: d, Err: ctx.Err().Error()}
	})
	if reads != 1 || !strings.Contains(l.Versions[1].Unreadable, "not read, the registry did not answer within 20ms") {
		t.Fatalf("one bound: %d reads %+v", reads, l)
	}
}

func TestCandidatesResolve(t *testing.T) {
	l := CandidateVersions{Namespace: "prod", Stage: "payments", Source: "upstream Stage test/payments",
		Versions: []CandidateVersion{{Digest: cand2, Version: "3.0.0"}, {Digest: cand1, Version: "2.4.0"}}}
	for _, tc := range []struct{ to, tag, want, err string }{
		{to: "3.0.0", want: cand2},
		{to: "latest", tag: cand1, want: cand1},
		{to: "3.0.0", tag: cand2, want: cand2},
		{to: "3.0.0", tag: cand1, err: `"3.0.0" names 2 versions this Stage may take (` + cand2 + ", " + cand1 + "), so Pacto cannot tell which you mean: pass the digest"},
		{to: "latest", tag: cand3, err: `the tag "latest" points at ` + cand3 + ", which is not a version this Stage may take: only a version pacto publish " +
			"published that the upstream Stage test/payments offers is. It may take 3.0.0 (" + cand2 + "), 2.4.0 (" + cand1 + ")"},
		{to: "9.0.0", err: `no version "9.0.0" was published for Stage prod/payments. It may take 3.0.0`},
	} {
		got, err := l.Resolve(tc.to, tc.tag)
		if got != tc.want || (tc.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.err) {
			t.Fatalf("%s %s: %q %v", tc.to, tc.tag, got, err)
		}
	}
	l.Versions = append(l.Versions, CandidateVersion{Digest: cand3, Unreadable: "manifest unknown"})
	if _, err := l.Resolve("3.0.0", ""); err == nil || !strings.Contains(err.Error(), "could not read every candidate (sha256:333333333333: manifest unknown)") {
		t.Fatalf("an unread candidate may be the same version: %v", err)
	}
	empty := CandidateVersions{Namespace: "prod", Stage: "payments", Versions: []CandidateVersion{}, Note: "nothing yet"}
	if _, err := empty.Resolve("3.0.0", ""); err == nil || !strings.HasSuffix(err.Error(), "It has no candidate: nothing yet") {
		t.Fatalf("no candidate at all: %v", err)
	}
}
