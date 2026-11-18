package signing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const personIssuer = "https://login.acme.example"

func personBundle(t *testing.T, ss *testutil.Sigstore, san, issuer string, timestamps bool) []byte {
	t.Helper()
	tok := testutil.Token(san, issuer)
	s, err := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return tok, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !timestamps {
		s.Timestamps = nil
	}
	b, err := s.Sign(t.Context(), "application/vnd.in-toto+json", []byte(statement))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestVerifyPersonReturnsSANAndTime: any subject the issuer vouches for under
// the trusted root verifies, and the answer names it and the time the
// timestamp authority observed.
func TestVerifyPersonReturnsSANAndTime(t *testing.T) {
	ss := testutil.New(t)
	before := time.Now().Add(-time.Second)
	san, at, err := VerifyPerson(personBundle(t, ss, "alice@acme.com", personIssuer, true), personIssuer, string(ss.RootJSON))
	if err != nil || san != "alice@acme.com" || at.Before(before) || at.After(time.Now().Add(time.Second)) || at.Location() != time.UTC {
		t.Fatalf("VerifyPerson = %q %v %v", san, at, err)
	}
}

// TestVerifyPersonRefusesOtherIssuer: a certificate from another issuer, one
// under another root, a key-signed bundle and a broken root never verify.
func TestVerifyPersonRefusesOtherIssuer(t *testing.T) {
	ss := testutil.New(t)
	b := personBundle(t, ss, "alice@acme.com", "https://other.example", true)
	if _, _, err := VerifyPerson(b, personIssuer, string(ss.RootJSON)); err == nil || !strings.Contains(err.Error(), personIssuer) {
		t.Fatalf("another issuer verified: %v", err)
	}
	other := testutil.New(t)
	if _, _, err := VerifyPerson(personBundle(t, ss, "alice@acme.com", personIssuer, true), personIssuer, string(other.RootJSON)); err == nil {
		t.Fatal("a certificate under another root verified")
	}
	if _, _, err := VerifyPerson(b, personIssuer, "{"); err == nil || !strings.Contains(err.Error(), "trustedRoot") {
		t.Fatalf("a broken root: %v", err)
	}
}

// TestVerifyPersonNeedsObserverTime: a certificate lives minutes, so a
// bundle no timestamp authority or log observed proves no signing time.
func TestVerifyPersonNeedsObserverTime(t *testing.T) {
	ss := testutil.New(t)
	if _, _, err := VerifyPerson(personBundle(t, ss, "alice@acme.com", personIssuer, false), personIssuer, string(ss.RootJSON)); err == nil {
		t.Fatal("a bundle with no observed time verified")
	}
}
