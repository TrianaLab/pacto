package signing

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

func identityOf(t *testing.T, claims map[string]string) (string, string) {
	t.Helper()
	id, iss, err := TokenIdentity(testutil.TokenWith(claims))
	if err != nil {
		t.Fatal(err)
	}
	return id, iss
}

func TestTokenIdentityGitHub(t *testing.T) {
	id, iss := identityOf(t, map[string]string{"iss": githubIssuer, "job_workflow_ref": "acme/mono/.github/workflows/ci.yml@refs/heads/main"})
	if id != "https://github.com/acme/mono/.github/workflows/ci.yml@refs/heads/main" || iss != githubIssuer {
		t.Errorf("got %q %q", id, iss)
	}
}

// TestTokenIdentityReusableWorkflow: Fulcio names the reusable workflow that
// runs, not the caller, so the identity is job_workflow_ref.
func TestTokenIdentityReusableWorkflow(t *testing.T) {
	id, _ := identityOf(t, map[string]string{"iss": githubIssuer,
		"job_workflow_ref": "TrianaLab/pacto-actions/.github/workflows/ci.yml@0123abcd",
		"workflow_ref":     "acme/mono/.github/workflows/release.yml@refs/heads/main"})
	if id != "https://github.com/TrianaLab/pacto-actions/.github/workflows/ci.yml@0123abcd" {
		t.Errorf("got %q", id)
	}
}

func TestTokenIdentityGitLab(t *testing.T) {
	id, iss := identityOf(t, map[string]string{"iss": "https://gitlab.example", "ci_config_ref_uri": "gitlab.example/acme/mono//.gitlab-ci.yml@refs/heads/main"})
	if id != "https://gitlab.example/acme/mono//.gitlab-ci.yml@refs/heads/main" || iss != "https://gitlab.example" {
		t.Errorf("got %q %q", id, iss)
	}
}

func TestTokenIdentityEmail(t *testing.T) {
	id, iss := identityOf(t, map[string]string{"iss": "https://accounts.example", "email": "ci@acme.example"})
	if id != "ci@acme.example" || iss != "https://accounts.example" {
		t.Errorf("got %q %q", id, iss)
	}
}

// TestTokenIdentityUnknownIssuerRefused: a GitHub token without
// job_workflow_ref, or any token naming nothing Pacto maps, is refused with
// its issuer and the declaration that fixes it.
func TestTokenIdentityUnknownIssuerRefused(t *testing.T) {
	for _, iss := range []string{"https://idp.example", githubIssuer} {
		_, _, err := TokenIdentity(testutil.TokenWith(map[string]string{"iss": iss, "sub": "x"}))
		if err == nil || !strings.Contains(err.Error(), iss) || !strings.Contains(err.Error(), "declare publish.sign.identity and issuer") {
			t.Errorf("%s: %v", iss, err)
		}
	}
}

func TestTokenIdentityMalformed(t *testing.T) {
	for _, tok := range []string{"", "a.b", "e30.!!!.sig", "e30.bm90LWpzb24.sig"} {
		if _, _, err := TokenIdentity(tok); err == nil || !strings.Contains(err.Error(), "declare publish.sign.identity and issuer") {
			t.Errorf("%q: %v", tok, err)
		}
	}
}

func TestHasIDTokenNamesGitLab(t *testing.T) {
	t.Setenv("SIGSTORE_ID_TOKEN", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	err := HasIDToken()
	if err == nil || !strings.Contains(err.Error(), "id_tokens entry named SIGSTORE_ID_TOKEN with aud: sigstore") {
		t.Errorf("got %v", err)
	}
}
