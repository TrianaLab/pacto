package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadContributionTrust loads a trust config whose one key, of producer
// my-idp, lists repo in allowedContractRepos.
func loadContributionTrust(t *testing.T, repo string) error {
	t.Helper()
	dir := t.TempDir()
	if _, err := (&Service{}).GenerateKey(dir, "", "idp", false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "trust.yaml")
	body := "apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n  - {keyId: idp-2026, producerId: my-idp, publicKeyFile: idp.pub, allowedContractRepos: [ghcr.io/acme/contracts, " + repo + "]}\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadTrustStore(p)
	return err
}

func TestTrustConfigGrantsOwnContributionPath(t *testing.T) {
	t.Parallel()
	if err := loadContributionTrust(t, "ghcr.io/acme/pacto-inferred/my-idp"); err != nil {
		t.Fatalf("own path: %v", err)
	}
}

func TestTrustConfigRefusesForeignContributionPath(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{"ghcr.io/acme/pacto-inferred/other-idp", "ghcr.io/acme/pacto-inferred", "ghcr.io/acme/pacto-inferred/my-idp/services", "ghcr.io/acme/pacto-inferred/my-idp/x"} {
		err := loadContributionTrust(t, repo)
		if err == nil || !strings.Contains(err.Error(), `"`+repo+`"`) || !strings.Contains(err.Error(), `key "idp-2026"`) {
			t.Errorf("%s: err = %v, want a refusal naming the entry and the key", repo, err)
		}
	}
}

// TestTrustConfigBarePubGrantsNothing: the directory-of-*.pub mode has no
// allowlists, so no key of it contributes records.
func TestTrustConfigBarePubGrantsNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := (&Service{}).GenerateKey(dir, "", "my-idp", false); err != nil {
		t.Fatal(err)
	}
	ts, err := loadTrustStore(dir)
	if err != nil || len(ts) == 0 {
		t.Fatalf("loadTrustStore = %v %v", ts, err)
	}
	for id, e := range ts {
		if len(e.ContractRepos) != 0 {
			t.Errorf("%s lists %v", id, e.ContractRepos)
		}
	}
}
