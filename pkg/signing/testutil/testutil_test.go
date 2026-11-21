package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
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

// TestCITokenGitHubClaims: a GitHub token carries repository in owner/repo
// form and job_workflow_ref; the fake Fulcio attests the repository URL.
func TestCITokenGitHubClaims(t *testing.T) {
	sub := "https://github.com/TrianaLab/pacto-actions/.github/workflows/ci.yml@0123"
	for _, repo := range []string{"acme/payments", "https://github.com/acme/payments"} {
		var claims map[string]string
		raw, _ := base64.RawURLEncoding.DecodeString(strings.Split(CIToken(sub, githubIssuer, repo, "refs/heads/main"), ".")[1])
		must(t, json.Unmarshal(raw, &claims))
		if claims["repository"] != "acme/payments" || claims["job_workflow_ref"] != "TrianaLab/pacto-actions/.github/workflows/ci.yml@0123" {
			t.Errorf("%s: claims %v", repo, claims)
		}
		if got := sourceRepository(githubIssuer, claims["repository"]); got != "https://github.com/acme/payments" {
			t.Errorf("source repository %q", got)
		}
	}
	if got := sourceRepository("https://gitlab.example", "https://gitlab.example/acme/x"); got != "https://gitlab.example/acme/x" {
		t.Errorf("GitLab source repository %q", got)
	}
}
