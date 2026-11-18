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
	return delivery.IdentityPin{Service: svc, Issuer: pinID.Issuer, Subject: pinID.Subject, Repository: pinID.Repository,
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
	st.Predicate.Operation = delivery.ApprovedOperation{Name: delivery.TrustOperationName("prod", pinID), UID: uid, Stage: delivery.RecordStage{Name: "prod"}}
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
	got, err := (&Reader{}).ListPins(t.Context(), repo, payments, verify, approves)
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
	if _, err := (&Reader{}).ListPins(t.Context(), repo, payments, func([]byte) error { return nil }, approves); err == nil {
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
	got, err := (&Reader{}).ListPins(t.Context(), repo, payments, func([]byte) error { return nil }, approves)
	if err != nil || len(got.Pins) != 0 || len(got.Unreadable) != 1 || !strings.Contains(got.Unreadable[0], `names service "orders"`) {
		t.Fatalf("listing %+v %v", got, err)
	}
	got, _ = (&Reader{}).ListPins(t.Context(), repo, payments, func([]byte) error { return errors.New("wrong key") }, approves)
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
		"given by mallory": func(ctx context.Context, ref string) (delivery.ApprovalStatement, string, error) {
			st, _, _ := approves(ctx, ref)
			return st, "mallory", nil
		},
	} {
		got, err := (&Reader{}).ListPins(t.Context(), repo, payments, func([]byte) error { return nil }, check)
		if err != nil || len(got.Pins) != 0 || len(got.Unreadable) != 1 || !strings.Contains(got.Unreadable[0], want) {
			t.Errorf("%s: %+v %v", want, got, err)
		}
	}
}
