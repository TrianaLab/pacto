package delivery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

var trustID = PublisherIdentity{Issuer: issuerGitHubActions, Subject: "https://github.com/acme/payments/.github/workflows/ci.yml@refs/heads/dev",
	Repository: "https://github.com/acme/payments", Ref: "refs/heads/dev", CI: true}

func trustInput(from Revision) PlanInput {
	spec := stageSpec()
	return PlanInput{Stage: spec, Class: ClassController, From: from, To: rev(dB, c2, bundle("payments", "2.0.0")),
		Spec: OperationSpec{Type: TypeTrust, Stage: "payments-prod", Facet: FacetTrust,
			Params: Params{From: from.Digest, To: dB, Publisher: &trustID}}}
}

func TestPlanForTrustHeldForAPerson(t *testing.T) {
	in := trustInput(rev(dA, c1, bundle("payments", "1.0.0")))
	p := PlanFor(context.Background(), in)
	if p.Predicted != PredictHeld || !p.Floor.Human || p.Floor.Decision != Approve || strings.Join(p.Floor.Owners, ",") != "team-payments" {
		t.Fatalf("plan %+v", p)
	}
	for _, want := range []string{"payments: new signer " + trustID.Subject, "issuer " + issuerGitHubActions, "repository " + trustID.Repository,
		"Confirm it may publish payments?", "Asked of: team-payments"} {
		if !strings.Contains(p.Question, want) {
			t.Errorf("question %q lacks %q", p.Question, want)
		}
	}
	in.Spec.Reason = "signer changed from a to b"
	if p := PlanFor(context.Background(), in); !strings.HasPrefix(p.Question, "payments: signer changed from a to b (") {
		t.Errorf("changed: %q", p.Question)
	}
	in.Spec.Params.Publisher = nil
	if p := PlanFor(context.Background(), in); p.Predicted != PredictRejected || !strings.Contains(p.Conflicts[0], "NoPublisher") {
		t.Errorf("no publisher: %+v", p)
	}
	in = trustInput(rev(dA, c1, bundle("payments", "1.0.0")))
	in.Class = ClassAgent
	if p := PlanFor(context.Background(), in); p.Predicted != PredictRejected {
		t.Errorf("an agent raised a trust operation: %+v", p)
	}
}

// TestTrustOwnersNotFromCandidate: with nothing running, the team the
// candidate's contract declares never answers for it.
func TestTrustOwnersNotFromCandidate(t *testing.T) {
	in := trustInput(Revision{})
	p := PlanFor(context.Background(), in)
	if len(p.Floor.Owners) != 0 || !p.Floor.Human || !strings.Contains(p.Question, "any person") {
		t.Fatalf("plan %+v", p)
	}
	in.From = Revision{Digest: dA}
	if p := PlanFor(context.Background(), in); len(p.Floor.Owners) != 0 {
		t.Fatalf("an unreadable running contract names a team: %+v", p.Floor)
	}
}

func TestTrustTypeDescriptor(t *testing.T) {
	ty, ok := TypeByID(TypeTrust)
	if !ok || ty.Facet != FacetTrust || strings.Join(ty.Params, ",") != "to,publisher" || len(ty.Raisers) != 1 || ty.Raisers[0] != ClassController ||
		len(ty.PreGates) != 0 || len(ty.VerifyGates) != 0 || ty.Cancellable != "until approved" || !strings.Contains(ty.ApprovalFloor, "agent never decides") {
		t.Fatalf("descriptor %+v", ty)
	}
	if MayRaise(TypeTrust, ClassHuman, false) || MayRaise(TypeTrust, ClassAgent, false) || !MayRaise(TypeTrust, ClassController, false) {
		t.Error("only the promoter raises a trust operation")
	}
}

func policyKeyYAML(t *testing.T) string {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	return quoteYAML(pubPEM(t, pub))
}

func TestVerifierAcceptsPolicyRole(t *testing.T) {
	key := policyKeyYAML(t)
	trust, err := ParseTrust([]byte("keys:\n  - {name: rec, publicKey: " + key + "}\n  - {name: sec, publicKey: " + key + ", role: policy}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ks := trust.PolicyKeys(); len(ks) != 1 || ks[0].Name != "sec" {
		t.Fatalf("policy keys %+v", ks)
	}
	if _, err := (TrustConfig{Keys: []TrustedKey{{Name: "p", Role: RolePolicy}}}).Verifier(func(TrustedKey) (func([]byte) error, error) {
		return nil, errors.New("bad key")
	}); err == nil {
		t.Error("a policy key that does not open was accepted")
	}
	if _, err := ParseTrust([]byte("keys:\n  - {name: p, publicKey: " + key + ", role: other}\n")); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Errorf("an unknown role names policy: %v", err)
	}
}

// TestVerifierPolicyKeyNotRecordKey: a policy key never verifies a record.
func TestVerifierPolicyKeyNotRecordKey(t *testing.T) {
	v, err := (TrustConfig{Keys: []TrustedKey{{Name: "sec", Role: RolePolicy}}}).Verifier(byName)
	if err != nil {
		t.Fatal(err)
	}
	if err := v("c/ns/prod", 1, []byte("sec")); err == nil {
		t.Fatal("a policy key verified a record")
	}
	if _, ok := (TrustConfig{Keys: []TrustedKey{{Name: "sec", Role: RolePolicy}}}).For("c/ns/prod"); ok {
		t.Fatal("For named a policy key")
	}
}

func TestPolicySignersParse(t *testing.T) {
	key := policyKeyYAML(t)
	if _, err := PolicyVerifier([]byte("keys:\n  - {name: rec, publicKey: "+key+"}\n"), byName); err == nil || !strings.Contains(err.Error(), "role: policy") {
		t.Errorf("no policy key: %v", err)
	}
	if _, err := PolicyVerifier([]byte("nope: 1"), byName); err == nil {
		t.Error("an unreadable config was accepted")
	}
	if _, err := PolicyVerifier([]byte("keys:\n  - {name: sec, publicKey: "+key+", role: policy}\n"), func(TrustedKey) (func([]byte) error, error) {
		return nil, errors.New("bad")
	}); err == nil {
		t.Error("a policy key that does not open was accepted")
	}
	v, err := PolicyVerifier([]byte("keys:\n  - {name: sec, publicKey: "+key+", role: policy}\n  - {name: sec2, publicKey: "+key+", role: policy}\n"), byName)
	if err != nil {
		t.Fatal(err)
	}
	if v([]byte("sec2")) != nil || v([]byte("rec")) == nil {
		t.Error("the policy verifier")
	}
}

func TestPolicyKeysRole(t *testing.T) {
	trust := TrustConfig{Keys: []TrustedKey{{Name: "rec"}, {Name: "tpl", Role: RoleTemplate}, {Name: "sec", Role: RolePolicy}}}
	if ks := trust.PolicyKeys(); len(ks) != 1 || ks[0].Name != "sec" {
		t.Fatalf("policy keys %+v", ks)
	}
}

func TestPinKeysExplicitPinsOnly(t *testing.T) {
	trust := TrustConfig{Keys: []TrustedKey{
		{Name: "any"}, // no streams: every stream, never a pin key
		{Name: "remote", Streams: []string{"other/*/*"}},
		{Name: "pins", Streams: []string{"pins/*", "c/*/*"}},
		{Name: "revoked", Streams: []string{"pins/*"}, UntilSeq: 3, UntilRecord: "r"},
		{Name: "tpl", Role: RoleTemplate, Streams: []string{"pins/*"}},
	}}
	got := trust.PinKeys()
	if len(got.Keys) != 1 || got.Keys[0].Name != "pins" || strings.Join(got.Keys[0].Streams, ",") != "pins/*" {
		t.Fatalf("pin keys %+v", got)
	}
}

func TestPinRoundTrip(t *testing.T) {
	p := IdentityPin{Service: ServiceRef{Name: "payments"}, Issuer: trustID.Issuer, Subject: trustID.Subject, Repository: trustID.Repository,
		Approver: "alice", Approval: "reg/rec@sha256:1", Operation: "uid-1", At: "2026-11-18T10:00:00Z"}
	st := NewPinStatement(p)
	if PinStream(p.Service) != "pins/-/payments" || PinStream(ServiceRef{Domain: "ghcr.io/acme", Name: "x"}) != "pins/ghcr.io%2Facme/x" ||
		len(st.Subject) != 1 || st.Subject[0].Name != "pins" {
		t.Fatalf("statement %+v", st)
	}
	b, id, err := SignPin(t.Context(), plainSigner{}, st)
	if err != nil {
		t.Fatal(err)
	}
	got, gotID, err := OpenPin(b, func([]byte) error { return nil })
	if err != nil || gotID != id || got.Predicate != p {
		t.Fatalf("open %+v %s %v", got, gotID, err)
	}
	if _, _, err := OpenPin(b, func([]byte) error { return errors.New("untrusted") }); err == nil {
		t.Fatal("an untrusted pin opened")
	}
	if _, _, err := SignPin(t.Context(), failingSigner{}, st); err == nil {
		t.Fatal("a failing signer signed")
	}
}

func TestPinWrongPredicateRefused(t *testing.T) {
	rec, _, _ := SignRecord(t.Context(), plainSigner{}, Statement{Type: StatementType, PredicateType: PredicateOperation})
	if _, _, err := OpenPin(rec, func([]byte) error { return nil }); err == nil {
		t.Fatal("a record opened as a pin")
	}
}

func signPolicy(t *testing.T, p any) []byte {
	t.Helper()
	b, _, err := signStatement(t.Context(), plainSigner{}, map[string]any{"_type": StatementType, "subject": []Subject{},
		"predicateType": PredicatePublisherPolicy, "predicate": p})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var policyRule = PublisherRule{Name: "release", Issuer: issuerGitHubActions,
	Subject: `^https://github\.com/acme/payments/\.github/workflows/release\.yml@refs/heads/main$`, Repository: `^https://github\.com/acme/payments$`}

func TestPolicyRoundTrip(t *testing.T) {
	want := PublisherPolicy{Publishers: []PublisherRule{policyRule}, At: "2026-11-18T10:00:00Z"}
	got, err := OpenPolicy(signPolicy(t, want), func([]byte) error { return nil })
	if err != nil || len(got.Publishers) != 1 || got.Publishers[0].Name != "release" || got.At != want.At {
		t.Fatalf("open %+v %v", got, err)
	}
	if _, err := OpenPolicy(signPolicy(t, want), func([]byte) error { return errors.New("untrusted") }); err == nil {
		t.Fatal("an untrusted policy opened")
	}
	if _, err := OpenPolicy(signPolicy(t, PublisherPolicy{Publishers: want.Publishers}), func([]byte) error { return nil }); err == nil {
		t.Fatal("a policy with no signed time opened")
	}
	rec, _, _ := SignRecord(t.Context(), plainSigner{}, Statement{Type: StatementType, PredicateType: PredicateOperation})
	if _, err := OpenPolicy(rec, func([]byte) error { return nil }); err == nil {
		t.Fatal("a record opened as a policy")
	}
}

func TestPolicyRejectsOpenRule(t *testing.T) {
	for name, r := range map[string]PublisherRule{
		"open subject":    {Issuer: issuerGitHubActions, Subject: ".*", Repository: policyRule.Repository},
		"open repository": {Issuer: issuerGitHubActions, Subject: policyRule.Subject, Repository: "^.*$"},
		"no issuer":       {Subject: policyRule.Subject, Repository: policyRule.Repository},
	} {
		if _, err := OpenPolicy(signPolicy(t, PublisherPolicy{Publishers: []PublisherRule{r}, At: "2026-11-18T10:00:00Z"}),
			func([]byte) error { return nil }); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPolicyStrictDecode(t *testing.T) {
	raw := map[string]any{"publishers": []PublisherRule{policyRule}, "at": "2026-11-18T10:00:00Z", "placement": []string{"x"}}
	if _, err := OpenPolicy(signPolicy(t, raw), func([]byte) error { return nil }); err == nil || !strings.Contains(err.Error(), "placement") {
		t.Fatalf("an unknown key: %v", err)
	}
}

func TestTrustOperationName(t *testing.T) {
	a, b := TrustOperationName("prod", trustID), TrustOperationName("prod", PublisherIdentity{Issuer: trustID.Issuer, Subject: "x", Repository: trustID.Repository})
	if a == b || !strings.HasPrefix(a, "prod-trust-") || len(a) != len("prod-trust-")+10 || a != TrustOperationName("prod", trustID) {
		t.Fatalf("%s %s", a, b)
	}
}
