package deliveryrecord

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

var (
	payments = delivery.ServiceRef{Name: "payments"}
	pinID    = delivery.PublisherIdentity{Issuer: "https://token.actions.githubusercontent.com",
		Subject: "https://github.com/acme/payments/.github/workflows/ci.yml@refs/heads/dev", Repository: "https://github.com/acme/payments"}
)

func pinOf(svc delivery.ServiceRef, uid, at string) delivery.IdentityPin {
	return delivery.IdentityPin{Service: svc, Namespace: "prod", Issuer: pinID.Issuer, Subject: pinID.Subject, Repository: pinID.Repository,
		Approver: "alice", Approval: "reg/approvals@" + uid, Operation: uid, At: at}
}

func signedPin(t *testing.T, key ed25519.PrivateKey, p delivery.IdentityPin) []byte {
	t.Helper()
	b, _, err := delivery.SignPin(context.Background(), edSigner(key), delivery.NewPinStatement(p))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// approves answers every approval ref as alice's approval of the trust
// operation of pinID on Stage prod whose uid the ref names after "@".
func approves(_ context.Context, ref string) (delivery.ApprovalStatement, string, error) {
	_, uid, _ := strings.Cut(ref, "@")
	var st delivery.ApprovalStatement
	st.Predicate.Decision = delivery.Approve
	st.Predicate.Operation = delivery.ApprovedOperation{Name: delivery.TrustOperationName("prod", pinID), UID: uid, Stage: delivery.RecordStage{Namespace: "prod", Name: "prod"}}
	return st, "alice", nil
}

func pinRepo(t *testing.T, mode capabilities.ReferrersMode) (*faulty, *remote.Repository) {
	t.Helper()
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	anchor := Anchor(delivery.PinStream(payments))
	if err := ocireferrer.Push(t.Context(), repo, anchor); err != nil {
		t.Fatal(err)
	}
	return f, repo
}

func testPushListPin(t *testing.T, mode capabilities.ReferrersMode) {
	_, repo := pinRepo(t, mode)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	for _, p := range []delivery.IdentityPin{pinOf(payments, "u1", "2026-11-17T00:00:00Z"), pinOf(payments, "u2", "2026-11-18T00:00:00Z")} {
		ref, err := PushPin(t.Context(), repo, mode, signedPin(t, key, p))
		if err != nil || !strings.Contains(ref, "@sha256:") {
			t.Fatalf("push %q %v", ref, err)
		}
	}
	verify := func(b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
	got, err := (&Reader{}).ListPins(t.Context(), repo, "prod", payments, verify, approves)
	if err != nil || len(got.Pins) != 2 || got.Pins[0].Pin.Operation != "u2" || len(got.Unreadable) != 0 || got.Cut || got.Pins[0].ID == "" {
		t.Fatalf("listing %+v %v", got, err)
	}
	if _, err := PushPin(t.Context(), repo, mode, []byte("nope")); err == nil {
		t.Fatal("a bundle that is no pin was pushed")
	}
}

func TestPushListPinNative(t *testing.T) { testPushListPin(t, capabilities.ReferrersNative) }

func TestPushListPinWriteOnce(t *testing.T) { testPushListPin(t, capabilities.ReferrersWriteOnceTags) }

func TestListPinsReadFailureIsError(t *testing.T) {
	f, repo := pinRepo(t, capabilities.ReferrersNative)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := PushPin(t.Context(), repo, capabilities.ReferrersNative, signedPin(t, key, pinOf(payments, "u1", "2026-11-17T00:00:00Z"))); err != nil {
		t.Fatal(err)
	}
	f.manifest.Store(true)
	if _, err := (&Reader{}).ListPins(t.Context(), repo, "prod", payments, func([]byte) error { return nil }, approves); err == nil {
		t.Fatal("an unreadable registry listed no pins")
	}
}

// TestPinOtherServiceUnreadable: a pin naming another service, or signed by
// a key the verifier refuses, is listed unreadable and never used.
func TestPinOtherServiceUnreadable(t *testing.T) {
	_, repo := pinRepo(t, capabilities.ReferrersNative)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	other := pinOf(delivery.ServiceRef{Name: "orders"}, "u1", "2026-11-17T00:00:00Z")
	// Attach a pin of orders to the pins of payments.
	if _, err := Push(t.Context(), repo, delivery.PinStream(payments), capabilities.ReferrersNative, delivery.PredicateIdentityPin, signedPin(t, key, other), nil); err != nil {
		t.Fatal(err)
	}
	got, err := (&Reader{}).ListPins(t.Context(), repo, "prod", payments, func([]byte) error { return nil }, approves)
	if err != nil || len(got.Pins) != 0 || len(got.Unreadable) != 1 || !strings.Contains(got.Unreadable[0], `names service "orders"`) {
		t.Fatalf("listing %+v %v", got, err)
	}
	got, _ = (&Reader{}).ListPins(t.Context(), repo, "prod", payments, func([]byte) error { return errors.New("wrong key") }, approves)
	if len(got.Pins) != 0 || !strings.Contains(got.Unreadable[0], "wrong key") {
		t.Fatalf("wrong signer %+v", got)
	}
}

// TestPinApprovalRecordMismatchIgnored: a pin whose cited approval does not
// verify, rejects, answers another operation or another identity's trust
// operation, or was given by someone else is never used.
func TestPinApprovalRecordMismatchIgnored(t *testing.T) {
	_, repo := pinRepo(t, capabilities.ReferrersNative)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := PushPin(t.Context(), repo, capabilities.ReferrersNative, signedPin(t, key, pinOf(payments, "u1", "2026-11-17T00:00:00Z"))); err != nil {
		t.Fatal(err)
	}
	for want, check := range map[string]ApprovalCheck{
		"does not verify": func(context.Context, string) (delivery.ApprovalStatement, string, error) {
			return delivery.ApprovalStatement{}, "", errors.New("bad signature")
		},
		"not approve": func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
			st, who, _ := approves(ctx, ref)
			st.Predicate.Decision = delivery.Reject
			return st, who, nil
		},
		"answers operation u9": func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
			st, who, _ := approves(ctx, ref)
			st.Predicate.Operation.UID = "u9"
			return st, who, nil
		},
		"not the trust operation": func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
			st, who, _ := approves(ctx, ref)
			st.Predicate.Operation.Name = delivery.TrustOperationName("prod", delivery.PublisherIdentity{Subject: "someone else"})
			return st, who, nil
		},
		`in namespace "team-x", not "prod"`: func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
			st, who, _ := approves(ctx, ref)
			st.Predicate.Operation.Stage.Namespace = "team-x"
			return st, who, nil
		},
		"given by mallory": func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
			st, _, _ := approves(ctx, ref)
			return st, "mallory", nil
		},
	} {
		got, err := (&Reader{}).ListPins(t.Context(), repo, "prod", payments, func([]byte) error { return nil }, check)
		if err != nil || len(got.Pins) != 0 || len(got.Unreadable) != 1 || !strings.Contains(got.Unreadable[0], want) {
			t.Errorf("%s: %+v %v", want, got, err)
		}
	}
}

// TestPinApproval: the approval a pin cites is fetched and verified: a
// person's record by the person check, a recorder-signed one under the pin
// keys and naming whom it was signed for; anything else does not verify.
func TestPinApproval(t *testing.T) {
	_, repo := pinRepo(t, capabilities.ReferrersNative)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	stream := delivery.PinStream(payments)
	push := func(signer delivery.ApprovalSigner) string {
		t.Helper()
		st := delivery.NewApprovalRecord(delivery.RecordStage{Name: "prod"}, "", delivery.OperationObject{}, delivery.ApprovalSpec{Decision: delivery.Approve}, signer, "2026-11-17T00:00:00Z")
		b, _, err := delivery.SignApproval(t.Context(), edSigner(key), st)
		ref, perr := Push(t.Context(), repo, stream, capabilities.ReferrersNative, delivery.PredicateApproval, b, nil)
		if err != nil || perr != nil {
			t.Fatal(err, perr)
		}
		return ref
	}
	open := func(string) (*remote.Repository, error) { return repo, nil }
	person := func([]byte) (delivery.ApprovalStatement, string, error) {
		return delivery.ApprovalStatement{}, "ana", nil
	}
	verify := func(b []byte) error { _, err := signing.Policy{Key: pub}.Verify(b, nil); return err }
	check := PinApproval(open, person, verify)

	recorder := push(delivery.ApprovalSigner{Kind: delivery.SignerRecorder, OnBehalfOf: &delivery.ApprovalBehalf{User: "alice"}})
	if _, who, err := check(t.Context(), recorder); err != nil || who != "alice" {
		t.Fatalf("recorder-signed: %q %v", who, err)
	}
	if _, who, err := check(t.Context(), push(delivery.ApprovalSigner{Kind: delivery.SignerPerson})); err != nil || who != "ana" {
		t.Fatalf("person-signed: %q %v", who, err)
	}
	if _, _, err := check(t.Context(), push(delivery.ApprovalSigner{Kind: delivery.SignerRecorder})); err == nil || !strings.Contains(err.Error(), "for nobody") {
		t.Fatalf("signed for nobody: %v", err)
	}
	if _, _, err := PinApproval(open, person, func([]byte) error { return errors.New("wrong key") })(t.Context(), recorder); err == nil {
		t.Fatal("an approval another key signed verified")
	}
	if _, _, err := check(t.Context(), "reg@sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("a missing record verified")
	}
	if _, _, err := PinApproval(func(string) (*remote.Repository, error) { return nil, errors.New("no repo") }, person, verify)(t.Context(), recorder); err == nil {
		t.Fatal("an unopenable repository verified")
	}
}

// TestPinFromOtherNamespaceIgnored: a pin confirmed on team-x's Stage of
// payments is never a pin of prod's Stages, and a pin that claims prod but
// cites an approval of an operation in team-x is never used.
func TestPinFromOtherNamespaceIgnored(t *testing.T) {
	_, repo := pinRepo(t, capabilities.ReferrersNative)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	teamX := pinOf(payments, "u1", "2026-11-17T00:00:00Z")
	teamX.Namespace = "team-x"
	if _, err := PushPin(t.Context(), repo, capabilities.ReferrersNative, signedPin(t, key, teamX)); err != nil {
		t.Fatal(err)
	}
	inTeamX := func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
		st, who, err := approves(ctx, ref)
		st.Predicate.Operation.Stage.Namespace = "team-x"
		return st, who, err
	}
	r := &Reader{}
	if got, err := r.ListPins(t.Context(), repo, "prod", payments, func([]byte) error { return nil }, inTeamX); err != nil || len(got.Pins) != 0 || len(got.Unreadable) != 0 {
		t.Fatalf("prod read team-x's pin: %+v %v", got, err)
	}
	if got, _ := r.ListPins(t.Context(), repo, "team-x", payments, func([]byte) error { return nil }, inTeamX); len(got.Pins) != 1 {
		t.Fatalf("team-x lost its own pin: %+v", got)
	}
	if got, _ := r.ListPins(t.Context(), repo, "", payments, func([]byte) error { return nil }, inTeamX); len(got.Pins) != 1 || got.Pins[0].Pin.Namespace != "team-x" {
		t.Fatalf("every namespace: %+v", got)
	}
	// A pin claiming prod, citing team-x's approval.
	if _, err := PushPin(t.Context(), repo, capabilities.ReferrersNative, signedPin(t, key, pinOf(payments, "u2", "2026-11-18T00:00:00Z"))); err != nil {
		t.Fatal(err)
	}
	if got, _ := (&Reader{}).ListPins(t.Context(), repo, "prod", payments, func([]byte) error { return nil }, inTeamX); len(got.Pins) != 0 || len(got.Unreadable) != 1 {
		t.Fatalf("a prod pin with team-x's approval: %+v", got)
	}
}

// TestUnusablePinTakesNoBoundAgain: a pin known not to verify is rejected,
// so the next listing neither reads it nor counts it; one whose approval
// record could not be read is read again.
func TestUnusablePinTakesNoBoundAgain(t *testing.T) {
	_, repo := pinRepo(t, capabilities.ReferrersNative)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := PushPin(t.Context(), repo, capabilities.ReferrersNative, signedPin(t, key, pinOf(payments, "u1", "2026-11-17T00:00:00Z"))); err != nil {
		t.Fatal(err)
	}
	r := &Reader{}
	unread := func(context.Context, string) (delivery.ApprovalStatement, string, error) {
		return delivery.ApprovalStatement{}, "", errors.New("503")
	}
	for range 2 {
		if got, _ := r.ListPins(t.Context(), repo, "prod", payments, func([]byte) error { return nil }, unread); len(got.Unreadable) != 1 || !got.Incomplete {
			t.Fatalf("an unreadable approval was not read again: %+v", got)
		}
	}
	wrong := func([]byte) error { return errors.New("wrong key") }
	if got, _ := r.ListPins(t.Context(), repo, "prod", payments, wrong, approves); len(got.Unreadable) != 1 || got.Incomplete {
		t.Fatalf("first read: %+v", got)
	}
	if got, _ := r.ListPins(t.Context(), repo, "prod", payments, wrong, approves); len(got.Unreadable) != 0 || len(got.Pins) != 0 {
		t.Fatalf("a rejected pin was read again: %+v", got)
	}
}
