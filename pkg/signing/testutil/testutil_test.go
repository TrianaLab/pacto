package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"
)

// TestLeafCertSAN: a URL subject gets the URI SAN Fulcio issues for a GitHub
// workflow, any other subject an email SAN.
func TestLeafCertSAN(t *testing.T) {
	root, rootKey, err := ca.GenerateRootCa()
	must(t, err)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	workflow := "https://github.com/acme/payments/.github/workflows/release.yml@refs/heads/main"
	uri, err := leafCert(workflow, "https://token.actions.githubusercontent.com", "", "", "", key.Public(), root, rootKey)
	must(t, err)
	if len(uri.URIs) != 1 || uri.URIs[0].String() != workflow || len(uri.EmailAddresses) != 0 {
		t.Errorf("URI SAN: %v %v", uri.URIs, uri.EmailAddresses)
	}
	email, err := leafCert("ci@acme.example", "https://idp.example", "", "", "", key.Public(), root, rootKey)
	must(t, err)
	if len(email.EmailAddresses) != 1 || len(email.URIs) != 0 {
		t.Errorf("email SAN: %v %v", email.URIs, email.EmailAddresses)
	}
}
