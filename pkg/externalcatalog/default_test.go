package externalcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The SAN and issuer were read on 2026-11-16 with `cosign verify
// --certificate-identity-regexp '^https://github\.com/TrianaLab/pacto/\.github/workflows/release\.yml@'
// --certificate-oidc-issuer https://token.actions.githubusercontent.com
// ghcr.io/trianalab/pacto/dashboard:4.5.0`, the signed image the same release
// workflow publishes, and then confirmed with that exact --certificate-identity.
func TestDefaultSignerMatchesReleaseWorkflow(t *testing.T) {
	const workflow = "/.github/workflows/release.yml@refs/heads/main"
	if !strings.HasSuffix(DefaultSigner, workflow) {
		t.Fatalf("DefaultSigner %q does not name %s", DefaultSigner, workflow)
	}
	gomod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	// module github.com/trianalab/pacto/v4: GitHub prints the owner's case.
	module := strings.Fields(strings.SplitN(string(gomod), "\n", 2)[0])[1]
	repo := "https://" + strings.TrimSuffix(module, "/v4")
	if got := strings.TrimSuffix(DefaultSigner, workflow); !strings.EqualFold(got, repo) {
		t.Fatalf("DefaultSigner repository %q, the module is %q", got, repo)
	}
	if DefaultIssuer != "https://token.actions.githubusercontent.com" {
		t.Fatalf("DefaultIssuer %q is not the GitHub Actions OIDC issuer", DefaultIssuer)
	}
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wf), `cosign sign --yes "${REF}@${EXPECTED}"`) {
		t.Fatal("release.yml never signs the external catalog, so DefaultSigner names nobody")
	}
}
