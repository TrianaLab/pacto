package deliveryrecord

import (
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
)

func applyRefusalOf(svc delivery.ServiceRef, stage, digest, at string) delivery.ApplyRefusal {
	return delivery.ApplyRefusal{Service: svc, Stage: stage, Namespace: "prod", Digest: digest,
		Refused: []string{"ClusterRoleBinding admin is cluster-scoped"}, Message: "m", Record: "r", Operation: "p1", At: at}
}

func signedApplyRefusal(t *testing.T, key ed25519.PrivateKey, r delivery.ApplyRefusal) []byte {
	t.Helper()
	b, _, err := delivery.SignApplyRefusal(t.Context(), edSigner(key), delivery.NewApplyRefusalStatement(r.Digest, r))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func refusalRepo(t *testing.T, mode capabilities.ReferrersMode) (*faulty, *remote.Repository) {
	t.Helper()
	f, path := setup(t, testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags})
	repo, _ := ocireferrer.Open(path, ocireferrer.Options{})
	if err := ocireferrer.Push(t.Context(), repo, Anchor(delivery.ApplyRefusalStream(payments))); err != nil {
		t.Fatal(err)
	}
	return f, repo
}

func testListApplyRefusalsNewestPerDigest(t *testing.T, mode capabilities.ReferrersMode) {
	_, repo := refusalRepo(t, mode)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	for _, r := range []delivery.ApplyRefusal{
		applyRefusalOf(payments, "prod", "sha256:a", "2026-11-20T00:00:00Z"),
		applyRefusalOf(payments, "prod", "sha256:a", "2026-11-21T00:00:00Z"),
		applyRefusalOf(payments, "prod", "sha256:b", "2026-11-19T00:00:00Z"),
		applyRefusalOf(payments, "dev", "sha256:a", "2026-11-18T00:00:00Z"),
	} {
		ref, err := PushApplyRefusal(t.Context(), repo, mode, signedApplyRefusal(t, key, r))
		if err != nil || !strings.Contains(ref, "@sha256:") {
			t.Fatalf("push %q %v", ref, err)
		}
	}
	got, err := (&Reader{}).ListApplyRefusals(t.Context(), repo, payments, func([]byte) error { return nil })
	if err != nil || len(got.Refusals) != 3 || got.Cut || len(got.Unreadable) != 0 {
		t.Fatalf("listing %+v %v", got, err)
	}
	if r := got.Refusals[0]; r.Refusal.At != "2026-11-21T00:00:00Z" || r.ID == "" || r.Manifest == "" {
		t.Fatalf("newest first, newest per digest: %+v", got.Refusals)
	}
	if got.Refusals[1].Refusal.Digest != "sha256:b" || got.Refusals[2].Refusal.Stage != "dev" {
		t.Fatalf("each Stage and digest apart: %+v", got.Refusals)
	}
	if _, err := PushApplyRefusal(t.Context(), repo, mode, []byte("nope")); err == nil {
		t.Fatal("a bundle that is no apply refusal was pushed")
	}
}

func TestListApplyRefusalsNewestPerDigest(t *testing.T) {
	testListApplyRefusalsNewestPerDigest(t, capabilities.ReferrersNative)
	testListApplyRefusalsNewestPerDigest(t, capabilities.ReferrersWriteOnceTags)
}

// TestListApplyRefusalsRejectsOtherService: a refusal naming another service
// attached to payments' stream is unreadable, never listed.
func TestListApplyRefusalsRejectsOtherService(t *testing.T) {
	_, repo := refusalRepo(t, capabilities.ReferrersNative)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	other := applyRefusalOf(delivery.ServiceRef{Name: "orders"}, "prod", "sha256:a", "2026-11-20T00:00:00Z")
	if _, err := Push(t.Context(), repo, delivery.ApplyRefusalStream(payments), capabilities.ReferrersNative, delivery.PredicateApplyRefusal, signedApplyRefusal(t, key, other), nil); err != nil {
		t.Fatal(err)
	}
	got, err := (&Reader{}).ListApplyRefusals(t.Context(), repo, payments, func([]byte) error { return nil })
	if err != nil || len(got.Refusals) != 0 || len(got.Unreadable) != 1 || !strings.Contains(got.Unreadable[0], `names service "orders"`) {
		t.Fatalf("listing %+v %v", got, err)
	}
}

// TestListApplyRefusalsUnreadableIsNamed: a refusal the verifier refuses is
// named unreadable, and a registry that cannot be read is an error.
func TestListApplyRefusalsUnreadableIsNamed(t *testing.T) {
	f, repo := refusalRepo(t, capabilities.ReferrersNative)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := PushApplyRefusal(t.Context(), repo, capabilities.ReferrersNative, signedApplyRefusal(t, key, applyRefusalOf(payments, "prod", "sha256:a", "2026-11-20T00:00:00Z"))); err != nil {
		t.Fatal(err)
	}
	r := &Reader{}
	got, err := r.ListApplyRefusals(t.Context(), repo, payments, func([]byte) error { return errors.New("wrong key") })
	if err != nil || len(got.Refusals) != 0 || len(got.Unreadable) != 1 || !strings.Contains(got.Unreadable[0], "wrong key") {
		t.Fatalf("listing %+v %v", got, err)
	}
	f.manifest.Store(true)
	if _, err := (&Reader{}).ListApplyRefusals(t.Context(), repo, payments, func([]byte) error { return nil }); err == nil {
		t.Fatal("an unreadable registry listed no refusals")
	}
}
