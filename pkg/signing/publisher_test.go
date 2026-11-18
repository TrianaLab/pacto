package signing

import (
	"context"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	ghIssuer    = "https://token.actions.githubusercontent.com"
	acmeRepo    = "https://github.com/acme/payments"
	acmeSubject = acmeRepo + "/.github/workflows/release.yml@refs/heads/main"
)

// signerFor is a keyless signer of ss for token.
func signerFor(t *testing.T, ss *testutil.Sigstore, token string) Signer {
	t.Helper()
	s, err := Keyless(ss.Fulcio, ss.TSA, "", func(context.Context) (string, error) { return token, nil }, ss.Root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ciSigner(t *testing.T, ss *testutil.Sigstore, subject, repo string) Signer {
	t.Helper()
	return signerFor(t, ss, testutil.CIToken(subject, ghIssuer, repo, "refs/heads/main"))
}

func TestIdentityOfCIWorkflow(t *testing.T) {
	ss := testutil.New(t)
	b, err := ciSigner(t, ss, acmeSubject, acmeRepo).Sign(t.Context(), delivery.PayloadType, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Policy{AnyIdentity: true, Root: ss.Root}.Verify(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := IdentityOf(res)
	want := delivery.PublisherIdentity{Issuer: ghIssuer, Subject: acmeSubject, Repository: acmeRepo, Ref: "refs/heads/main", CI: true}
	if id != want {
		t.Fatalf("identity %+v", id)
	}
}

func TestIdentityOfPerson(t *testing.T) {
	ss := testutil.New(t)
	b, _ := signerFor(t, ss, testutil.Token("ana@acme.example", "https://accounts.example")).Sign(t.Context(), delivery.PayloadType, []byte(`{}`))
	res, err := Policy{AnyIdentity: true, Root: ss.Root}.Verify(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id := IdentityOf(res); id != (delivery.PublisherIdentity{Issuer: "https://accounts.example", Subject: "ana@acme.example"}) {
		t.Fatalf("identity %+v", id)
	}
	if IdentityOf(nil) != (delivery.PublisherIdentity{}) {
		t.Fatal("nothing verified names nobody")
	}
}

func TestAnyIdentityStillNeedsRoot(t *testing.T) {
	ss := testutil.New(t)
	b, _ := signerFor(t, ss, testutil.Token("ana@acme.example", "https://accounts.example")).Sign(t.Context(), delivery.PayloadType, []byte(`{}`))
	if _, err := (Policy{AnyIdentity: true}).Verify(b, nil); err == nil || !strings.Contains(err.Error(), "no trusted root") {
		t.Fatalf("verified with no root: %v", err)
	}
}
