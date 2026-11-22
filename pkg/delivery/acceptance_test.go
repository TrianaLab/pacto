package delivery

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"

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
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, DeclaredAcceptanceOwner(ownerOfAna), "", asOf); s != impact.AcceptCounted {
		t.Fatalf("judged %s: %s", s, why)
	}
}

func TestJudgeCountsOwnerEmailContactCaseInsensitive(t *testing.T) {
	o := contract.Owner{Team: "reporting", Contacts: []contract.OwnerContact{{Type: "slack", Value: "ana@acme.com"}, {Type: "email", Value: "ANA@acme.com"}}}
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, DeclaredAcceptanceOwner(o), "", asOf); s != impact.AcceptCounted {
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
		s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, DeclaredAcceptanceOwner(o), "", asOf)
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
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), PublisherIdentity{Issuer: anaSigner.Issuer, Subject: "x@acme.com"}, nil, DeclaredAcceptanceOwner(contract.Owner{}), "", asOf); s != impact.AcceptRouted {
		t.Error("an empty owner matched")
	}
	if ownerMatches(contract.Owner{}, "") {
		t.Error("an empty subject matched an empty owner")
	}
}

func TestCISignedAcceptanceIsProposal(t *testing.T) {
	ci := PublisherIdentity{Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/acme/reports/.github/workflows/ci.yaml@refs/heads/main", CI: true}
	s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), ci, nil, DeclaredAcceptanceOwner(contract.Owner{DRI: ci.Subject}), "", asOf)
	if s != impact.AcceptProposal || !strings.Contains(why, "a ci signer") {
		t.Fatalf("judged %s: %s", s, why)
	}
}

func TestWorkloadSignedAcceptanceIsProposal(t *testing.T) {
	wl := PublisherIdentity{Issuer: "https://kubernetes.default.svc", Subject: "https://kubernetes.io/namespaces/ci/serviceaccounts/bot"}
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), wl, nil, DeclaredAcceptanceOwner(contract.Owner{DRI: wl.Subject}), "", asOf); s != impact.AcceptProposal {
		t.Fatalf("judged %s", s)
	}
}

func TestJudgeBreakExpired(t *testing.T) {
	st := acceptanceStatement(AcceptKindBreak)
	if s, why := JudgeAcceptance(st, anaSigner, nil, DeclaredAcceptanceOwner(ownerOfAna), "2026-11-19", asOf); s != impact.AcceptExpired || why != "the break expired on 2026-11-19" {
		t.Fatalf("judged %s: %s", s, why)
	}
	if s, _ := JudgeAcceptance(st, anaSigner, nil, DeclaredAcceptanceOwner(ownerOfAna), "2026-11-20", asOf); s != impact.AcceptCounted {
		t.Fatalf("a break on its last day judged %s", s)
	}
	if s, why := JudgeAcceptance(st, anaSigner, nil, DeclaredAcceptanceOwner(ownerOfAna), "soon", asOf); s != impact.AcceptExpired || !strings.Contains(why, "not a YYYY-MM-DD") {
		t.Fatalf("an unreadable expiry judged %s: %s", s, why)
	}
}

func TestJudgeBreakExpiryUnknownNeverCounts(t *testing.T) {
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindBreak), anaSigner, nil, DeclaredAcceptanceOwner(ownerOfAna), "", asOf); s != impact.AcceptExpired || !strings.HasPrefix(why, "expiry unknown") {
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
	if a := j.ForImpact(); a.Break != nil || len(a.Fields) != len(j.Record.Ranges) || len(a.Fields) == 0 || a.Fields[0] != j.Record.Ranges[0].Field {
		t.Errorf("a ranges acceptance = %+v", a)
	}
}

func TestAcceptanceStatementRoundTrip(t *testing.T) {
	r := acceptanceStatement(AcceptKindBreak).Predicate
	st := NewAcceptanceStatement("reg/reports@sha256:ab", r)
	if !reflect.DeepEqual(st, acceptanceStatement(AcceptKindBreak)) {
		t.Fatalf("statement = %+v", st)
	}
	b, id, err := SignAcceptance(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got, gotID, err := OpenAcceptance(b, func([]byte) error { return nil })
	if err != nil || gotID != id || !reflect.DeepEqual(got, st) {
		t.Fatalf("open = %+v %s %v", got, gotID, err)
	}
}

func TestAcceptanceTagShape(t *testing.T) {
	subject := digest.FromString("bundle")
	manifest := digest.FromString("record")
	tag := AcceptanceTag(subject, manifest)
	if len(tag) != 90 || tag != AcceptanceTagPrefix+subject.Encoded()+"-"+manifest.Encoded()[:12] {
		t.Fatalf("tag %q (%d)", tag, len(tag))
	}
}

func TestSignAcceptanceDraft(t *testing.T) {
	d := AcceptanceDraft{Record: acceptanceStatement(AcceptKindRanges).Predicate, Subject: "reg/reports@sha256:ab", OnBehalfOf: "ana"}
	b, _, err := SignAcceptanceDraft(t.Context(), plainSigner{}, d)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Type          string          `json:"_type"`
		PredicateType string          `json:"predicateType"`
		Subject       []Subject       `json:"subject"`
		Predicate     AcceptanceDraft `json:"predicate"`
	}
	if _, err := openStatement(b, PredicateAcceptanceDraft, &st); err != nil || st.Predicate.OnBehalfOf != "ana" || st.Subject[0].Digest["sha256"] != "ab" {
		t.Fatalf("%v %+v", err, st)
	}
}

func TestAcceptanceDraftStrictDecode(t *testing.T) {
	d := AcceptanceDraft{Record: acceptanceStatement(AcceptKindRanges).Predicate, Subject: "reg/reports@sha256:ab", OnBehalfOf: "ana",
		Asker: contract.InferredSigner{Subject: "ci@acme.com", Issuer: "https://token.actions.githubusercontent.com"}}
	raw, _ := json.Marshal(d)
	signed, _, err := SignAcceptanceDraft(t.Context(), plainSigner{}, d)
	if err != nil {
		t.Fatal(err)
	}
	for mt, payload := range map[string][]byte{AcceptanceDraftArtifactType: raw, BundleMediaType: signed} {
		if got, err := DecodeAcceptanceDraft(mt, payload); err != nil || !reflect.DeepEqual(got, d) {
			t.Fatalf("%s: %v %+v", mt, err, got)
		}
	}
	unknown := strings.Replace(string(raw), `"onBehalfOf"`, `"extra":1,"onBehalfOf"`, 1)
	noSubject, _ := json.Marshal(AcceptanceDraft{Record: d.Record})
	for name, c := range map[string]struct {
		mt      string
		payload []byte
	}{
		"unknown key":   {AcceptanceDraftArtifactType, []byte(unknown)},
		"trailing data": {AcceptanceDraftArtifactType, append(raw, '}')},
		"no subject":    {AcceptanceDraftArtifactType, noSubject},
		"other layer":   {AcceptanceArtifactType, raw},
		"not a bundle":  {BundleMediaType, raw},
	} {
		if _, err := DecodeAcceptanceDraft(c.mt, c.payload); err == nil {
			t.Errorf("%s decoded", name)
		}
	}
}

// acceptPolicy has platform-admins as default approvers, reporting's
// members by team rule, and ana's issuer for email subjects.
var acceptPolicy = &OwnerPolicy{
	DefaultApprovers: []SignerRule{{Name: "platform-admins", Issuer: "https://accounts.google.com", Subject: `^admin@acme\.com$`}},
	Teams:            map[string][]SignerRule{"reporting": {{Issuer: "https://accounts.google.com", Subject: `^bob@acme\.com$`}}},
	EmailIssuers:     []string{anaSigner.Issuer},
}

func TestAcceptanceByAttestedOwnerCounts(t *testing.T) {
	attested := AcceptanceOwner{Claim: OwnerClaim{Owner: ownerOfAna, Provenance: ownerAttested, Source: "CODEOWNERS:1"}, Policy: acceptPolicy}
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, attested, "", asOf); s != impact.AcceptCounted {
		t.Fatalf("the attested DRI judged %s: %s", s, why)
	}
	bob := PublisherIdentity{Issuer: "https://accounts.google.com", Subject: "bob@acme.com"}
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), bob, nil, attested, "", asOf); s != impact.AcceptCounted {
		t.Fatalf("a member of the attested team judged %s: %s", s, why)
	}
	carol := PublisherIdentity{Issuer: anaSigner.Issuer, Subject: "carol@acme.com"}
	if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), carol, nil, attested, "", asOf); s != impact.AcceptRouted || !strings.Contains(why, "ana@acme.com") {
		t.Fatalf("a non-member judged %s: %s", s, why)
	}
}

func TestAcceptanceByDefaultApproverCounts(t *testing.T) {
	admin := PublisherIdentity{Issuer: "https://accounts.google.com", Subject: "admin@acme.com"}
	for name, o := range map[string]AcceptanceOwner{
		"declared owner": {Claim: OwnerClaim{Owner: ownerOfAna, Provenance: ownerDeclared}, Policy: acceptPolicy},
		"no owner":       {Policy: acceptPolicy},
	} {
		if s, why := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), admin, nil, o, "", asOf); s != impact.AcceptCounted {
			t.Errorf("%s: a default approver judged %s: %s", name, s, why)
		}
	}
	// No owner and no approver: naming oneself owner is no owner right.
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, AcceptanceOwner{Policy: acceptPolicy}, "", asOf); s != impact.AcceptRouted {
		t.Errorf("a person with no owner right judged %s", s)
	}
	if HoldsOwnerRight("reports", PublisherIdentity{Issuer: admin.Issuer, Class: ClassHuman}, AcceptanceOwner{Policy: acceptPolicy}, asOf) {
		t.Error("an empty subject holds the owner right")
	}
}

func TestAcceptanceWithoutOwnerPolicyKeepsDeclaredOwnerRule(t *testing.T) {
	// No policy: an attested owner and a would-be default approver are routed,
	// and the declared DRI counts whatever issuer certified the login.
	admin := PublisherIdentity{Issuer: "https://accounts.google.com", Subject: "admin@acme.com"}
	attested := AcceptanceOwner{Claim: OwnerClaim{Owner: ownerOfAna, Provenance: ownerAttested}}
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, attested, "", asOf); s != impact.AcceptRouted {
		t.Errorf("an attested owner with no policy judged %s", s)
	}
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), admin, nil, DeclaredAcceptanceOwner(ownerOfAna), "", asOf); s != impact.AcceptRouted {
		t.Errorf("a non-owner with no policy judged %s", s)
	}
	if s, _ := JudgeAcceptance(acceptanceStatement(AcceptKindRanges), anaSigner, nil, DeclaredAcceptanceOwner(ownerOfAna), "", asOf); s != impact.AcceptCounted {
		t.Errorf("the declared DRI with no policy judged %s", s)
	}
}
