package delivery

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	lcD   = "sha256:dddd"
	lcOld = "sha256:0000"
	lcNew = "sha256:eeee"
)

func lcDigests(n int, prefix string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("sha256:%s%060d", prefix, i)
	}
	return out
}

func TestKnownDigests(t *testing.T) {
	st := stage("prod", "pay", "pay", StageStatus{Published: []string{"a", "b"}, Offered: []string{"b", "c"}, Verified: []string{"d"},
		FailedDigests: []string{"e", "a"}, Pointer: "f"})
	if got := KnownDigests(st); !slices.Equal(got, []string{"a", "b", "c", "d", "e", "f"}) {
		t.Fatalf("known = %v", got)
	}
	if got := KnownDigests(stage("prod", "pay", "pay", StageStatus{})); len(got) != 0 {
		t.Fatalf("an empty Stage knows %v", got)
	}
}

func lcRev(d, version, ref string) Revision {
	return Revision{Digest: d, ContractRef: ref, Bundle: &contract.Bundle{Contract: &contract.Contract{Service: contract.Service{Version: version}}}}
}

func TestResolveLifecycleDigest(t *testing.T) {
	const c1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	revs := []Revision{lcRev(lcOld, "1.0.0", "oci://r/pay@"+c1), lcRev(lcD, "2.0.0", "oci://r/pay@sha256:2222222222222222222222222222222222222222222222222222222222222222"),
		lcRev(lcNew, "2.0.0", ""), {Digest: "sha256:broken", Err: "registry down"}, {Digest: "sha256:nc", NoContract: true}}
	cases := []struct {
		name    string
		q       LifecycleQuery
		want    string
		wantErr string
	}{
		{"nothing", LifecycleQuery{Service: "pay"}, "", "pass exactly one of digest, version or contract"},
		{"two", LifecycleQuery{Service: "pay", Digest: lcD, Version: "1.0.0"}, "", "pass exactly one"},
		{"known digest", LifecycleQuery{Service: "pay", Digest: lcOld}, lcOld, ""},
		{"unknown digest", LifecycleQuery{Service: "pay", Digest: "sha256:else"}, "sha256:else", ""},
		{"version", LifecycleQuery{Service: "pay", Version: "1.0.0"}, lcOld, ""},
		{"contract ref", LifecycleQuery{Service: "pay", Contract: "oci://r/pay@" + c1}, lcOld, ""},
		{"contract digest", LifecycleQuery{Service: "pay", Contract: c1}, lcOld, ""},
		{"no such version", LifecycleQuery{Service: "pay", Version: "9.9.9"}, "", "no version 9.9.9 of pay is known to its Stages; 1 of the artifacts its Stages know could not be read"},
		{"no such contract", LifecycleQuery{Service: "pay", Contract: "sha256:nope"}, "", "no artifact of pay that names contract sha256:nope is known to its Stages"},
		{"ambiguous", LifecycleQuery{Service: "pay", Version: "2.0.0"}, "", "version 2.0.0 names 2 artifacts (" + lcD + ", " + lcNew + "): pass the digest"},
	}
	for _, c := range cases {
		r, err := ResolveLifecycleDigest(c.q, revs)
		switch {
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: err %v, want %q", c.name, err, c.wantErr)
		case c.wantErr == "" && (err != nil || r.Digest != c.want):
			t.Errorf("%s: %+v %v, want %s", c.name, r, err, c.want)
		}
	}
	if r, _ := ResolveLifecycleDigest(LifecycleQuery{Digest: lcOld}, revs); r.ContractRef == "" {
		t.Fatal("a known digest keeps what its artifact carries")
	}
}

func TestLifecycleDigests(t *testing.T) {
	a := stage("prod", "a", "pay", StageStatus{Pointer: lcD, Verified: []string{lcOld}})
	b := stage("test", "b", "pay", StageStatus{Pointer: lcNew, Published: []string{lcD}})
	ds, from, note := LifecycleDigests(LifecycleQuery{Version: "1"}, []StageObject{a, b})
	if !slices.Equal(ds, []string{lcOld, lcD, lcNew}) || from[2].Metadata.Name != "b" || from[1].Metadata.Name != "a" || note != "" {
		t.Fatalf("digests %v %v %q", ds, from, note)
	}
	ds, from, _ = LifecycleDigests(LifecycleQuery{Digest: lcNew}, []StageObject{a, b})
	if !slices.Equal(ds, []string{lcNew}) || from[0].Metadata.Name != "b" {
		t.Fatalf("one digest %v %v", ds, from)
	}
	ds, from, _ = LifecycleDigests(LifecycleQuery{Digest: "sha256:else"}, []StageObject{a, b})
	if !slices.Equal(ds, []string{"sha256:else"}) || from[0].Metadata.Name != "a" {
		t.Fatalf("a digest no Stage knows is read from the first Stage: %v %v", ds, from)
	}
	if ds, _, _ := LifecycleDigests(LifecycleQuery{Digest: lcD}, nil); len(ds) != 0 {
		t.Fatalf("no Stage, nothing to read: %v", ds)
	}
	big := stage("prod", "big", "pay", StageStatus{Published: lcDigests(MaxPublished, "a"), Offered: lcDigests(MaxOffered, "b"),
		Verified: lcDigests(MaxVerified, "c"), FailedDigests: lcDigests(MaxFailedDigests, "d")})
	more := stage("prod", "more", "pay", StageStatus{Published: lcDigests(MaxPublished, "e"), Offered: lcDigests(MaxOffered, "f")})
	ds, _, note = LifecycleDigests(LifecycleQuery{Version: "1"}, []StageObject{big, more})
	if len(ds) != LifecycleDigestLimit || note != "; the Stages know 260 artifacts and only the first 200 were read to find the one asked for" {
		t.Fatalf("cap: %d %q", len(ds), note)
	}
}

func lcEntry(seq int64, op, typ, from, to string, outcome Outcome, v Verification, move *PointerChange) HistoryEntry {
	return HistoryEntry{Seq: seq, Operation: op, Type: typ, From: from, To: to, Outcome: outcome, Verification: v, Pointer: move,
		Record: fmt.Sprintf("sha256:r%d", seq)}
}

func fullHistory(ns, name string, es ...HistoryEntry) StageHistory {
	return StageHistory{Namespace: ns, History: History{Stage: StageNow{Name: name}, Completeness: "complete", Source: SourceRecords, Verified: true, Entries: es}}
}

func lcRoles(s LifecycleStage) []string {
	var out []string
	for _, e := range s.Events {
		out = append(out, e.Role+" "+e.Operation)
	}
	return out
}

// lifecycleSample is lcD on five Stages: prod rolled it back, test runs it,
// staging's history is partial, dev never had it and qa's history was not
// read.
func lifecycleSample() Lifecycle {
	r := lcRev(lcD, "2.0.0", "oci://r/pay@sha256:c")
	r.SourceRevision = "4f2c1e0"
	ship := lcEntry(2, "ship", TypePromote, lcOld, lcD, Succeeded, Verified, &PointerChange{Before: lcOld, After: lcD})
	held := HistoryEntry{Operation: "held", Type: TypePromote, From: lcOld, To: lcD, Phase: PhaseHeld}
	back := lcEntry(3, "back", TypeRollback, lcD, lcOld, Succeeded, Verified, &PointerChange{Before: lcD, After: lcOld})
	back.Cause = &Cause{Verdict: &Verdict{Gate: "health", Result: Fail}}
	prod := stage("prod", "pay", "pay", StageStatus{Pointer: lcOld, Verified: []string{lcOld}, FailedDigests: []string{lcD}})
	test := stage("test", "pay", "pay", StageStatus{Pointer: lcD, Verified: []string{lcD}, Offered: []string{lcD},
		LastCheck: &Recheck{Digest: lcD, At: "2026-11-08T09:59:02Z", Verdicts: []Verdict{{Gate: "health", Result: Pass}}}})
	stg := stage("staging", "pay", "pay", StageStatus{LastCheck: &Recheck{Digest: lcOld}})
	none := stage("dev", "pay", "pay", StageStatus{})
	gone := stage("qa", "pay", "pay", StageStatus{Pointer: lcD})
	return LifecycleOf(r, []StageObject{prod, test, stg, none, gone}, []StageHistory{
		fullHistory("prod", "pay", lcEntry(1, "first", TypePromote, "", lcOld, Succeeded, Verified, &PointerChange{After: lcOld}), ship, back),
		fullHistory("test", "pay", held, ship),
		{Namespace: "staging", History: History{Stage: StageNow{Name: "pay"}, Completeness: "partial", Source: SourceStatus}},
		fullHistory("dev", "pay"),
		// qa's history was not read: dev's must not land on it.
	})
}

// lcLine sums up one Stage of a lifecycle for comparison.
func lcLine(s LifecycleStage) string {
	check := "-"
	if s.LastCheck != nil {
		check = s.LastCheck.At
	}
	return fmt.Sprintf("%s/%s running=%v verified=%s failed=%s offered=%v %s/%v %s check=%s events=%v nil=%v", s.Namespace, s.Stage, s.Running,
		s.Verified, s.Failed, s.Offered, s.Source, s.RecordsVerified, s.Completeness, check, lcRoles(s), s.Events == nil)
}

func TestLifecycleOf(t *testing.T) {
	l := lifecycleSample()
	head := fmt.Sprint(l.Digest, l.Version, l.Contract, l.SourceRevision, l.Completeness, l.Signature, l.SignedOn)
	if head != fmt.Sprint(lcD, "2.0.0", "oci://r/pay@sha256:c", "4f2c1e0", "partial", SignatureSigned, []string{"test/pay"}) {
		t.Fatalf("lifecycle %+v", l)
	}
	want := []string{
		"prod/pay running=false verified=no failed=yes offered=false records/true complete check=- events=[arrived ship left back] nil=false",
		"test/pay running=true verified=yes failed=no offered=true records/true complete check=2026-11-08T09:59:02Z events=[attempted held arrived ship] nil=false",
		// a re-check of another digest is not this one's; a partial history decides nothing
		"staging/pay running=false verified=unknown failed=unknown offered=false status/false partial check=- events=[] nil=false",
		// a complete history with lists below their caps says no
		"dev/pay running=false verified=no failed=no offered=false records/true complete check=- events=[] nil=false",
		// no history read: dev's must not land on it
		"qa/pay running=true verified=unknown failed=unknown offered=false status/false partial check=- events=[] nil=false",
	}
	for i, s := range l.Stages {
		if got := lcLine(s); got != want[i] {
			t.Errorf("stage %d:\n got %s\nwant %s", i, got, want[i])
		}
	}
	if !strings.Contains(l.Note, "the history of staging/pay, qa/pay is partial or was not read") {
		t.Fatalf("note %q", l.Note)
	}
}

func TestLifecycleOfLists(t *testing.T) {
	r := Revision{Digest: lcD}
	oci := Candidates{OCIRepositoryRef: &LocalRef{Name: "oci"}}
	at := func(c Candidates, s StageStatus) StageObject {
		st := stage("prod", "pay", "pay", s)
		st.Spec.Candidates = c
		return st
	}
	cases := map[string]struct {
		st            StageObject
		hs            []StageHistory
		sig, ver, fai string
	}{
		"aged out of published, still verified": {at(oci, StageStatus{Published: lcDigests(MaxPublished, "a"), Verified: []string{lcD}}), nil, SignatureUnknown, "yes", "unknown"},
		"entry below the cap":                   {at(oci, StageStatus{Pointer: lcD}), nil, SignatureUnsigned, "unknown", "unknown"},
		"aged out of verified":                  {at(Candidates{}, StageStatus{Published: []string{lcD}, Verified: lcDigests(MaxVerified, "c")}), []StageHistory{fullHistory("prod", "pay")}, SignatureSigned, "unknown", "no"},
		"failed list at its cap":                {at(Candidates{}, StageStatus{Published: []string{lcD}, FailedDigests: lcDigests(MaxFailedDigests, "d")}), []StageHistory{fullHistory("prod", "pay")}, SignatureSigned, "no", "unknown"},
		"in no list, complete history":          {at(Candidates{}, StageStatus{Published: []string{lcD}}), []StageHistory{fullHistory("prod", "pay")}, SignatureSigned, "no", "no"},
		"failed in the history":                 {at(Candidates{}, StageStatus{}), []StageHistory{fullHistory("prod", "pay", lcEntry(1, "x", TypePromote, "", lcD, Failed, "", nil))}, SignatureUnknown, "no", "yes"},
		"verified, aged out, history says yes":  {at(Candidates{}, StageStatus{Verified: lcDigests(MaxVerified, "c")}), []StageHistory{fullHistory("prod", "pay", lcEntry(1, "x", TypePromote, "", lcD, Succeeded, Verified, &PointerChange{After: lcD}))}, SignatureUnknown, "yes", "no"},
	}
	for name, c := range cases {
		l := LifecycleOf(r, []StageObject{c.st}, c.hs)
		s := l.Stages[0]
		if l.Signature != c.sig || s.Verified != c.ver || s.Failed != c.fai {
			t.Errorf("%s: signature %s verified %s failed %s, want %s %s %s", name, l.Signature, s.Verified, s.Failed, c.sig, c.ver, c.fai)
		}
	}
	if l := LifecycleOf(r, []StageObject{at(oci, StageStatus{Pointer: lcD})}, nil); !slices.Equal(l.UnsignedOn, []string{"prod/pay"}) || len(l.SignedOn) != 0 {
		t.Fatalf("unsigned evidence %+v", l)
	}
	if l := LifecycleOf(r, []StageObject{at(Candidates{}, StageStatus{})}, []StageHistory{fullHistory("prod", "pay")}); l.Signature != SignatureUnknown ||
		!strings.Contains(l.Note, "no Stage you can see knows "+lcD) || l.Completeness != "complete" {
		t.Fatalf("a digest no Stage knows %+v", l)
	}
	if l := LifecycleOf(Revision{Digest: lcD, Err: "registry down"}, nil, nil); !strings.Contains(l.Note, "its artifact could not be read: registry down") ||
		l.Stages == nil || l.SignedOn == nil {
		t.Fatalf("unreadable artifact %+v", l)
	}
}

// TestLifecycleOfUnrecordedArrival: a Stage that runs the digest on a
// complete history with no arrival says so, and never where its history is
// partial or capped (the arrival may be what is missing).
func TestLifecycleOfUnrecordedArrival(t *testing.T) {
	runs := stage("prod", "pay", "pay", StageStatus{Pointer: lcD})
	l := LifecycleOf(Revision{Digest: lcD}, []StageObject{runs}, []StageHistory{fullHistory("prod", "pay")})
	if !strings.Contains(l.Note, "no recorded change brought it to prod/pay: it was placed there before the records began or moved outside Pacto") {
		t.Fatalf("complete, no arrival: %q", l.Note)
	}
	arrived := lcEntry(1, "ship", TypePromote, "", lcD, Succeeded, Verified, &PointerChange{After: lcD})
	for name, hs := range map[string][]StageHistory{
		"arrival recorded": {fullHistory("prod", "pay", arrived)},
		"history not read": nil,
	} {
		if l := LifecycleOf(Revision{Digest: lcD}, []StageObject{runs}, hs); strings.Contains(l.Note, "no recorded change") {
			t.Errorf("%s: %q", name, l.Note)
		}
	}
	var es []HistoryEntry
	for i := range lifecycleEventLimit + 1 {
		es = append(es, HistoryEntry{Seq: int64(i + 1), Operation: fmt.Sprintf("op%d", i), Type: TypePromote, To: lcD})
	}
	if l := LifecycleOf(Revision{Digest: lcD}, []StageObject{runs}, []StageHistory{fullHistory("prod", "pay", es...)}); strings.Contains(l.Note, "no recorded change") {
		t.Errorf("capped: %q", l.Note)
	}
}

func TestLifecycleOfEventCap(t *testing.T) {
	var es []HistoryEntry
	for i := range lifecycleEventLimit + 5 {
		es = append(es, HistoryEntry{Seq: int64(i + 1), Operation: fmt.Sprintf("op%d", i), Type: TypePromote, To: lcD})
	}
	l := LifecycleOf(Revision{Digest: lcD}, []StageObject{stage("prod", "pay", "pay", StageStatus{})}, []StageHistory{fullHistory("prod", "pay", es...)})
	s := l.Stages[0]
	if len(s.Events) != lifecycleEventLimit || s.Events[0].Operation != "op5" || !strings.Contains(l.Note, "only the newest 50 of 55 events are listed on prod/pay") {
		t.Fatalf("cap: %d %s %q", len(s.Events), s.Events[0].Operation, l.Note)
	}
}

func TestLifecycleRole(t *testing.T) {
	cases := []struct {
		e    HistoryEntry
		want string
	}{
		{HistoryEntry{To: lcD, Pointer: &PointerChange{Before: lcOld, After: lcD}}, "arrived"},
		{HistoryEntry{To: lcOld, Pointer: &PointerChange{Before: lcD, After: lcOld}}, "left"},
		{HistoryEntry{To: lcD}, "attempted"},
		{HistoryEntry{To: lcD, Pointer: &PointerChange{Before: lcOld, After: lcOld}}, "attempted"},
		{HistoryEntry{To: lcNew, Pointer: &PointerChange{Before: lcOld, After: lcNew}}, ""},
		{HistoryEntry{Type: TypeFreeze}, ""},
	}
	for _, c := range cases {
		if got := lifecycleRole(c.e, lcD); got != c.want {
			t.Errorf("%+v: %q, want %q", c.e, got, c.want)
		}
	}
}
