package signing

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	pactoActions = "https://github.com/TrianaLab/pacto-actions/.github/workflows/ci.yml"
	shaA         = "@1111111111111111111111111111111111111111"
	shaB         = "@2222222222222222222222222222222222222222"
)

// signedBy is a bundle signed by the workflow subject, Fulcio attesting repo
// at ref.
func signedBy(t *testing.T, ss *testutil.Sigstore, subject, repo, ref string) []byte {
	t.Helper()
	b, err := signerFor(t, ss, testutil.CIToken(subject, ghIssuer, repo, ref)).Sign(t.Context(), delivery.PayloadType, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// reusablePolicy is the policy of a publish that runs pacto-actions at pin
// for acme/payments on main.
func reusablePolicy(t *testing.T, ss *testutil.Sigstore, pin string) Policy {
	t.Helper()
	p, err := PolicyForToken(testutil.CIToken(pactoActions+pin, ghIssuer, acmeRepo, "refs/heads/main"))
	if err != nil {
		t.Fatal(err)
	}
	p.Root = ss.Root
	return p
}

// TestPolicyForTokenReusableAcceptsOtherPin: a signature pacto-actions made at
// one pin verifies under the policy of a run at another pin.
func TestPolicyForTokenReusableAcceptsOtherPin(t *testing.T) {
	ss := testutil.New(t)
	p := reusablePolicy(t, ss, shaB)
	if p.Repository != acmeRepo || p.Ref != "refs/heads/main" || p.Identity != pactoActions+shaB {
		t.Fatalf("policy %+v", p)
	}
	if _, err := p.Verify(signedBy(t, ss, pactoActions+shaA, acmeRepo, "refs/heads/main"), nil); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyForTokenReusableRefusesOtherRepository(t *testing.T) {
	ss := testutil.New(t)
	if _, err := reusablePolicy(t, ss, shaB).Verify(signedBy(t, ss, pactoActions+shaA, "https://github.com/acme/other", "refs/heads/main"), nil); err == nil {
		t.Fatal("a publish of another repository verified")
	}
}

func TestPolicyForTokenReusableRefusesOtherRef(t *testing.T) {
	ss := testutil.New(t)
	if _, err := reusablePolicy(t, ss, shaB).Verify(signedBy(t, ss, pactoActions+shaA, acmeRepo, "refs/heads/feature"), nil); err == nil {
		t.Fatal("a publish from another branch verified")
	}
}

func TestPolicyForTokenReusableRefusesOtherWorkflow(t *testing.T) {
	ss := testutil.New(t)
	evil := "https://github.com/evil/actions/.github/workflows/ci.yml" + shaA
	if _, err := reusablePolicy(t, ss, shaB).Verify(signedBy(t, ss, evil, acmeRepo, "refs/heads/main"), nil); err == nil {
		t.Fatal("another reusable workflow verified")
	}
}

// TestPolicyForTokenReusableAcceptsOwnWorkflow: a baseline the caller's own
// workflow signed on the same branch still verifies after the move to the
// reusable workflow.
func TestPolicyForTokenReusableAcceptsOwnWorkflow(t *testing.T) {
	ss := testutil.New(t)
	if _, err := reusablePolicy(t, ss, shaB).Verify(signedBy(t, ss, acmeSubject, acmeRepo, "refs/heads/main"), nil); err != nil {
		t.Fatal(err)
	}
}

// TestPolicyForTokenOwnWorkflowExact: the repository's own workflow keeps the
// exact identity.
func TestPolicyForTokenOwnWorkflowExact(t *testing.T) {
	p, err := PolicyForToken(testutil.CIToken(acmeSubject, ghIssuer, acmeRepo, "refs/heads/main"))
	if err != nil || p != (Policy{Identity: acmeSubject, Issuer: ghIssuer}) {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestPolicyForTokenGitLabExact(t *testing.T) {
	tok := testutil.TokenWith(map[string]string{"iss": "https://gitlab.com", "ci_config_ref_uri": "gitlab.com/acme/x//.gitlab-ci.yml@refs/heads/main",
		"repository": "acme/x", "ref": "refs/heads/main"})
	p, err := PolicyForToken(tok)
	if err != nil || p != (Policy{Identity: "https://gitlab.com/acme/x//.gitlab-ci.yml@refs/heads/main", Issuer: "https://gitlab.com"}) {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := PolicyForToken("a.b"); err == nil {
		t.Fatal("an unreadable token gave a policy")
	}
}

// TestVerifyExtensionIdentityNeedsRoot: a repository policy without a root
// verifies nothing, and a broken SAN pattern refuses.
func TestVerifyExtensionIdentityNeedsRoot(t *testing.T) {
	ss := testutil.New(t)
	b := signedBy(t, ss, pactoActions+shaA, acmeRepo, "refs/heads/main")
	p := reusablePolicy(t, ss, shaB)
	p.Root = nil
	if _, err := p.Verify(b, nil); err == nil || !strings.Contains(err.Error(), "no trusted root") {
		t.Fatalf("verified with no root: %v", err)
	}
	p.Root, p.IdentityRegexp = ss.Root, "("
	if _, err := p.Verify(b, nil); err == nil {
		t.Fatal("a broken pattern verified")
	}
}
