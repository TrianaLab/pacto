package delivery

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

func acceptanceStatement(kind string) AcceptanceStatement {
	r := AcceptanceRecord{Service: "reports", Version: "1.6.0", Kind: kind}
	if kind == AcceptKindBreak {
		r.Break = &AcceptedBreak{Provider: "ledger", Version: "3.0.0", Announcement: "sha256:aa"}
	} else {
		r.Ranges = []AcceptedRange{{Field: "dependencies[ledger].compatibility", Value: "^2.3.0", Proposed: "^2.3.0", Source: "snapshot:x"}}
	}
	return AcceptanceStatement{Type: StatementType, Subject: []Subject{{Name: "reg/reports", Digest: map[string]string{"sha256": "ab"}}},
		PredicateType: PredicateAcceptance, Predicate: r}
}

var (
	anaSigner  = PublisherIdentity{Issuer: "https://oauth2.sigstore.dev/auth", Subject: "Ana@Acme.com"}
	ownerOfAna = contract.Owner{Team: "reporting", DRI: "ana@acme.com"}
	asOf       = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
)

func TestOpenAcceptance(t *testing.T) {
	st := acceptanceStatement(AcceptKindRanges)
	b, id, err := signStatement(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got, gotID, err := OpenAcceptance(b, func([]byte) error { return nil })
	if err != nil || gotID != id || got.Predicate.Ranges[0] != st.Predicate.Ranges[0] {
		t.Fatalf("open = %+v %s %v", got, gotID, err)
	}
	if _, _, err := OpenAcceptance(b, func([]byte) error { return errors.New("untrusted") }); err == nil {
		t.Fatal("an unverified acceptance opened")
	}
}

func TestOpenAcceptanceRefusesOtherPredicate(t *testing.T) {
	b, _, _ := SignRefusal(t.Context(), plainSigner{}, NewRefusalStatement("sha256:ab", PublishRefusal{}))
	if _, _, err := OpenAcceptance(b, func([]byte) error { return nil }); err == nil {
		t.Fatal("a refusal opened as an acceptance")
	}
}

func TestOpenAcceptanceRefusesUnknownFields(t *testing.T) {
	st := acceptanceStatement(AcceptKindRanges)
	raw, _ := json.Marshal(st)
	payload := strings.Replace(string(raw), `"kind":"ranges"`, `"kind":"ranges","extra":true`, 1)
	b, err := plainSigner{}.Sign(t.Context(), PayloadType, []byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenAcceptance(b, func([]byte) error { return nil }); err == nil {
		t.Fatal("a record with an unknown field opened")
	}
}

func TestAcceptanceIntentIsStable(t *testing.T) {
	r := acceptanceStatement(AcceptKindRanges).Predicate
	a := AcceptanceIntent(r, "reg/reports@sha256:ab")
	r.Intent = a
	if AcceptanceIntent(r, "reg/reports@sha256:ab") != a || !strings.HasPrefix(a, "sha256:") {
		t.Fatalf("the intent changed with its own field: %s", a)
	}
	if AcceptanceIntent(r, "reg/reports@sha256:cd") == a {
		t.Error("another subject has the same intent")
	}
	r.Ranges[0].Value = "^2.4.0"
	if AcceptanceIntent(r, "reg/reports@sha256:ab") == a {
		t.Error("another value has the same intent")
	}
}

func TestBreakAnnouncementIsStable(t *testing.T) {
	a := BreakAnnouncement(contract.Break{Consumers: []string{"reports", "billing"}, Reason: "drop v1"})
	if BreakAnnouncement(contract.Break{Consumers: []string{"billing", "reports"}, Reason: "drop v1"}) != a {
		t.Error("the consumer order changed the announcement")
	}
	if BreakAnnouncement(contract.Break{Consumers: []string{"billing", "reports"}, Reason: "drop v2"}) == a {
		t.Error("a changed reason kept the announcement")
	}
}

func TestJudgeCountsDeclaredOwnerDRI(t *testing.T) {
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, ownerOfAna, "", asOf); s != impact.AcceptCounted {
		t.Fatalf("judged %s: %s", s, why)
	}
}

func TestJudgeCountsOwnerEmailContactCaseInsensitive(t *testing.T) {
	o := contract.Owner{Team: "reporting", Contacts: []contract.OwnerContact{{Type: "slack", Value: "ana@acme.com"}, {Type: "email", Value: "ANA@acme.com"}}}
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, o, "", asOf); s != impact.AcceptCounted {
		t.Fatalf("judged %s: %s", s, why)
	}
}

func TestNonOwnerAcceptanceIsRouted(t *testing.T) {
	for name, o := range map[string]contract.Owner{
		"team only": {Team: "reporting"},
		// An inferred owner is never declared: the caller passes none.
		"inferred owner": {},
		"another person": {DRI: "bob@acme.com", Contacts: []contract.OwnerContact{{Type: "slack", Value: "#rep"}, {Type: "email", Value: "carol@acme.com"}}},
	} {
		s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, o, "", asOf)
		if s != impact.AcceptRouted {
			t.Errorf("%s: judged %s", name, s)
		}
		if name == "another person" && why != "waiting for the owner of reports: bob@acme.com, carol@acme.com" {
			t.Errorf("route = %q", why)
		}
		if name != "another person" && !strings.Contains(why, "reports declares no owner") {
			t.Errorf("%s: route = %q", name, why)
		}
	}
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), PublisherIdentity{Issuer: anaSigner.Issuer, Subject: "x@acme.com"}, nil, contract.Owner{}, "", asOf); s != impact.AcceptRouted {
		t.Error("an empty owner matched")
	}
	if ownerMatches(contract.Owner{}, "") {
		t.Error("an empty subject matched an empty owner")
	}
}

func TestCISignedAcceptanceIsProposal(t *testing.T) {
	ci := PublisherIdentity{Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/acme/reports/.github/workflows/ci.yaml@refs/heads/main", CI: true}
	s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), ci, nil, contract.Owner{DRI: ci.Subject}, "", asOf)
	if s != impact.AcceptProposal || !strings.Contains(why, "a ci signer") {
		t.Fatalf("judged %s: %s", s, why)
	}
}

func TestWorkloadSignedAcceptanceIsProposal(t *testing.T) {
	wl := PublisherIdentity{Issuer: "https://kubernetes.default.svc", Subject: "https://kubernetes.io/namespaces/ci/serviceaccounts/bot"}
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), wl, nil, contract.Owner{DRI: wl.Subject}, "", asOf); s != impact.AcceptProposal {
		t.Fatalf("judged %s", s)
	}
}

func TestJudgeBreakExpired(t *testing.T) {
	st := acceptanceStatement(AcceptKindBreak)
	if s, why := JudgeAcceptance(st, anaSigner, nil, ownerOfAna, "2026-11-19", asOf); s != impact.AcceptExpired || why != "the break expired on 2026-11-19" {
		t.Fatalf("judged %s: %s", s, why)
	}
	if s, _ := JudgeAcceptance(st, anaSigner, nil, ownerOfAna, "2026-11-20", asOf); s != impact.AcceptCounted {
		t.Fatalf("a break on its last day judged %s", s)
	}
	if s, why := JudgeAcceptance(st, anaSigner, nil, ownerOfAna, "soon", asOf); s != impact.AcceptExpired || !strings.Contains(why, "not a YYYY-MM-DD") {
		t.Fatalf("an unreadable expiry judged %s: %s", s, why)
	}
}

func TestJudgeBreakExpiryUnknownNeverCounts(t *testing.T) {
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindBreak), anaSigner, nil, ownerOfAna, "", asOf); s != impact.AcceptExpired || !strings.HasPrefix(why, "expiry unknown") {
		t.Fatalf("judged %s: %s", s, why)
	}
}

func TestJudgedAcceptanceForImpact(t *testing.T) {
	at := asOf.Add(-time.Hour)
	j := JudgedAcceptance{Record: acceptanceStatement(AcceptKindBreak).Predicate, Ref: "reg/reports@sha256:cd", Signer: "ana@acme.com",
		Issuer: anaSigner.Issuer, State: impact.AcceptCounted, SignedAt: at}
	a := j.ForImpact()
	if a.State != impact.AcceptCounted || a.By != "ana@acme.com" || a.Record != j.Ref || !a.SignedAt.Equal(at) ||
		a.Break == nil || *a.Break != (impact.BreakRef{Provider: "ledger", Version: "3.0.0", Announcement: "sha256:aa"}) {
		t.Fatalf("ForImpact = %+v", a)
	}
	j.Record = acceptanceStatement(AcceptKindRanges).Predicate
	if j.ForImpact().Break != nil {
		t.Error("a ranges acceptance names a break")
	}
}
