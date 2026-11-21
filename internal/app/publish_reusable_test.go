package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	acmeRepoURL  = "https://github.com/acme/payments"
	acmeRelease  = acmeRepoURL + "/.github/workflows/release.yml@refs/heads/main"
	pactoActions = "https://github.com/TrianaLab/pacto-actions/.github/workflows/ci.yml"
)

// asCI sets the CI token of a run of subject for acme/payments on ref.
func asCI(t *testing.T, subject, ref string) {
	t.Setenv("SIGSTORE_ID_TOKEN", sigtest.CIToken(subject, ghIssuer, acmeRepoURL, ref))
}

// keylessPublish publishes the folder keyless as the current token, at
// version ("" derives it).
func keylessPublish(t *testing.T, svc *Service, dir, host, version, rev string) (*publish.Result, error) {
	t.Helper()
	writeDerivedBundle(t, dir, host, version, "")
	withoutSign(t, dir)
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# payments "+rev+"\n"), 0o644)
	return svc.Publish(context.Background(), PublishOptions{Path: dir, Revision: rev})
}

// TestDeriveVersionAfterPinBump: a baseline pacto-actions published at one
// pin is the baseline of a publish at the next pin.
func TestDeriveVersionAfterPinBump(t *testing.T) {
	privateSigstore(t, ciWorkflow)
	host, reg := publishRegistry(t)
	svc, dir := publishService(&fakeRun{}, reg), t.TempDir()
	asCI(t, pactoActions+"@1111111111111111111111111111111111111111", "refs/heads/main")
	if _, err := keylessPublish(t, svc, dir, host, "1.4.2", "r1"); err != nil {
		t.Fatal(err)
	}
	asCI(t, pactoActions+"@2222222222222222222222222222222222222222", "refs/heads/main")
	res, err := keylessPublish(t, svc, dir, host, "", "r2")
	if err != nil || res.Plan.Baseline != "1.4.2" || res.Plan.Version != "1.4.3" {
		t.Fatalf("%v %+v", err, res)
	}
}

// TestDeriveVersionAfterMovingFromOwnWorkflow: the first publish through
// pacto-actions derives from what the repository's own workflow signed on
// the same branch, and skips what it signed on another.
func TestDeriveVersionAfterMovingFromOwnWorkflow(t *testing.T) {
	privateSigstore(t, ciWorkflow)
	host, reg := publishRegistry(t)
	svc, dir := publishService(&fakeRun{}, reg), t.TempDir()
	asCI(t, acmeRelease, "refs/heads/main")
	if _, err := keylessPublish(t, svc, dir, host, "1.4.2", "r1"); err != nil {
		t.Fatal(err)
	}
	asCI(t, strings.Replace(acmeRelease, "main", "feature", 1), "refs/heads/feature")
	if _, err := keylessPublish(t, svc, dir, host, "1.5.0", "r2"); err != nil {
		t.Fatal(err)
	}
	asCI(t, pactoActions+"@1111111111111111111111111111111111111111", "refs/heads/main")
	res, err := keylessPublish(t, svc, dir, host, "", "r3")
	if err != nil || res.Plan.Baseline != "1.4.2" || res.Plan.Version != "1.5.1" {
		t.Fatalf("%v %+v", err, res)
	}
}

// TestDeclaredIdentityStillExact: a declared keyless identity verifies only
// that identity, never the pacto-actions subject of the same repository.
func TestDeclaredIdentityStillExact(t *testing.T) {
	privateSigstore(t, ciWorkflow)
	host, reg := publishRegistry(t)
	declared := contract.PublishSign{Identity: acmeRelease, Issuer: ghIssuer}
	tools := newExecTools(t.TempDir(), nil, reg)
	asCI(t, acmeRelease, "refs/heads/main")
	own := pushImage(t, reg, host+"/own")
	if _, err := tools.Sign(t.Context(), declared, own); err != nil {
		t.Fatal(err)
	}
	if err := newExecTools(t.TempDir(), nil, reg).Verify(t.Context(), declared, own); err != nil {
		t.Fatalf("the declared identity: %v", err)
	}
	asCI(t, pactoActions+"@1111111111111111111111111111111111111111", "refs/heads/main")
	v2 := pushImage(t, reg, host+"/v2")
	if _, err := newExecTools(t.TempDir(), nil, reg).Sign(t.Context(), contract.PublishSign{}, v2); err != nil {
		t.Fatal(err)
	}
	if err := newExecTools(t.TempDir(), nil, reg).Verify(t.Context(), declared, v2); err == nil {
		t.Fatal("the pacto-actions subject verified under a declared identity")
	}
}
