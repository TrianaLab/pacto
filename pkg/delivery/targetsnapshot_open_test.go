package delivery_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const snapCluster = "6f1e-uid"

// signedSnapshot signs a snapshot of snapCluster with a fresh Ed25519 key and
// returns the bundle and that key's PEM public key.
func signedSnapshot(t *testing.T) ([]byte, string) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, err := signing.FromPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	st := delivery.NewTargetSnapshotStatement(delivery.TargetSnapshot{Cluster: snapCluster, AsOf: "2026-11-17T10:00:00Z",
		Completeness: fleet.CompletenessComplete, Publisher: "pacto-system/pacto-operator-delivery-recorder"})
	b, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), st)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := delivery.PublicKeyPEM(kp.GetPublicKey())
	return b, pub
}

func TestTargetSnapshotStream(t *testing.T) {
	if got := delivery.TargetSnapshotStream("c1"); got != "c1/pacto-targets/snapshot" {
		t.Fatal(got)
	}
}

// TestOpenTargetSnapshotVerifies: a key scoped to the cluster verifies and is
// returned; a scoped key that did not sign is skipped for one that did.
func TestOpenTargetSnapshotVerifies(t *testing.T) {
	b, pub := signedSnapshot(t)
	_, other := signedSnapshot(t)
	trust := &delivery.TrustConfig{Keys: []delivery.TrustedKey{
		{Name: "stale", PublicKey: other, Streams: []string{snapCluster + "/*/*"}},
		{Name: "recorder", PublicKey: pub, Streams: []string{"*/prod/*", snapCluster + "/*/*"}},
	}}
	st, v, err := delivery.OpenTargetSnapshot(b, trust, signing.OpenKey)
	if err != nil || !v.Verified || !v.Scoped || v.Key.Name != "recorder" || st.Predicate.Cluster != snapCluster {
		t.Fatalf("verdict %+v, %v", v, err)
	}
}

// TestOpenTargetSnapshotBarePEMIsUnscoped: a bare PEM trusts every stream, so
// it verifies the snapshot without being scoped to its cluster.
func TestOpenTargetSnapshotBarePEMIsUnscoped(t *testing.T) {
	b, pub := signedSnapshot(t)
	trust, err := delivery.ParseTrust([]byte(pub))
	if err != nil {
		t.Fatal(err)
	}
	_, v, err := delivery.OpenTargetSnapshot(b, &trust, signing.OpenKey)
	if err != nil || !v.Verified || v.Scoped {
		t.Fatalf("verdict %+v, %v", v, err)
	}
}

func TestOpenTargetSnapshotUnverifiedWithoutTrust(t *testing.T) {
	b, _ := signedSnapshot(t)
	st, v, err := delivery.OpenTargetSnapshot(b, nil, signing.OpenKey)
	if err != nil || v.Verified || st.Predicate.Publisher == "" {
		t.Fatalf("verdict %+v, %v", v, err)
	}
}

// TestOpenTargetSnapshotRejectsOtherPredicate: a signed delivery record, or a
// snapshot Check refuses, is no target snapshot.
func TestOpenTargetSnapshotRejectsOtherPredicate(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	kp, _ := signing.FromPrivateKey(priv)
	rec, _, err := delivery.SignRecord(t.Context(), signing.Keyed(kp, ""), delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenTargetSnapshot(rec, nil, signing.OpenKey); err == nil {
		t.Fatal("an operation record opened as a target snapshot")
	}
	bad, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(delivery.TargetSnapshot{Cluster: "a/b"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.OpenTargetSnapshot(bad, nil, signing.OpenKey); err == nil {
		t.Fatal("a snapshot Check refuses opened")
	}
}

// TestTargetSnapshotKeyScopedToOtherClusterRefused: a key scoped to another
// cluster's streams never verifies this cluster's snapshot, nor does a key of
// another role or a key that does not open.
func TestTargetSnapshotKeyScopedToOtherClusterRefused(t *testing.T) {
	b, pub := signedSnapshot(t)
	trust := &delivery.TrustConfig{Keys: []delivery.TrustedKey{
		{Name: "other", PublicKey: pub, Streams: []string{"other-uid/*/*"}},
		{Name: "policy", PublicKey: pub, Role: delivery.RolePolicy},
	}}
	if _, v, err := delivery.OpenTargetSnapshot(b, trust, signing.OpenKey); err == nil || v.Verified ||
		!strings.Contains(err.Error(), "no trusted key may sign the target snapshot") {
		t.Fatalf("verdict %+v, %v", v, err)
	}
	broken := &delivery.TrustConfig{Keys: []delivery.TrustedKey{{Name: "broken", PublicKey: pub, Streams: []string{snapCluster + "/*/*"}}}}
	failing := func(delivery.TrustedKey) (func([]byte) error, error) { return nil, errors.New("cannot open") }
	if _, _, err := delivery.OpenTargetSnapshot(b, broken, failing); err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("err = %v", err)
	}
	// A pattern that starts with the UID but does not cover the stream scopes nothing.
	partial := &delivery.TrustConfig{Keys: []delivery.TrustedKey{{Name: "wide", PublicKey: pub, Streams: []string{snapCluster + "/prod/*", "*/*/*"}}}}
	if _, v, err := delivery.OpenTargetSnapshot(b, partial, signing.OpenKey); err != nil || !v.Verified || v.Scoped {
		t.Fatalf("verdict %+v, %v", v, err)
	}
}
