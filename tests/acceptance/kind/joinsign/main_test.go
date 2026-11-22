package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
	"github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	member = "member-uid"
	hub    = "hub-uid"
	issuer = "https://idp.acme.test"
)

// publish signs the member's snapshot with key and tags it cluster-<member>.
func publish(t *testing.T, repo string, key ed25519.PrivateKey) {
	t.Helper()
	der, _ := x509.MarshalPKIXPublicKey(key.Public())
	s := delivery.TargetSnapshot{Cluster: member, AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete,
		Publisher: "pacto/pacto-operator", Join: &delivery.SnapshotJoin{Name: "eu-prod", NameSource: fleet.ClusterNameFromNodeLabel,
			KeyID: signing.Hint(key.Public()), PublicKey: base64.StdEncoding.EncodeToString(der)}}
	kp, _ := signing.FromPrivateKey(key)
	bundle, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := ocireferrer.Open(repo, ocireferrer.Options{})
	if _, err := deliveryrecord.PushTargetSnapshot(t.Context(), r, member, bundle); err != nil {
		t.Fatal(err)
	}
}

// state is the member as a hub that trusts the root at rootFile reads it,
// where every user may approve.
func state(t *testing.T, repo, rootFile string) delivery.ClusterJoinState {
	t.Helper()
	raw, _ := os.ReadFile(rootFile)
	policy := delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: issuer, UsernamePrefix: "oidc:", Subjects: `[^@]+@acme\.test`, TrustedRoot: string(raw)}}}
	allow := func(context.Context, authn.Person, string, authn.ResourceAttributes) (bool, error) { return true, nil }
	l := app.NewService(nil, nil).ClusterJoins(t.Context(), app.JoinOptions{Repository: repo, Hub: hub, Authorize: allow, Delivery: &app.Delivery{Policy: policy}})
	for _, c := range l.Clusters {
		if c.UID == member {
			return c
		}
	}
	t.Fatalf("no member in %+v", l)
	return delivery.ClusterJoinState{}
}

// The record the kind scenario writes is one the hub counts: approved under
// the root the command wrote, then removed; a key the snapshot no longer
// carries gets nothing signed.
func TestRunWritesWhatTheHubCounts(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	reg := strings.TrimPrefix(srv.URL, "http://") + "/acme"
	repo := reg + "/" + delivery.TargetSnapshotRepository
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	publish(t, repo, key)
	der, _ := x509.MarshalPKIXPublicKey(key.Public())
	rootFile := filepath.Join(t.TempDir(), "trusted_root.json")
	req := request{root: rootFile, registry: reg, cluster: member, hub: hub, approver: "oidc:alice@acme.test", email: "alice@acme.test",
		key: "sha256:" + signing.Hint(key.Public()), publicKey: base64.StdEncoding.EncodeToString(der), issuer: issuer, name: "eu-west"}

	var out bytes.Buffer
	if err := run(t.Context(), testutil.New(t), req, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "approve "+member+" as oidc:alice@acme.test; recorded ") {
		t.Errorf("output %q", out.String())
	}
	if st := state(t, repo, rootFile); st.State != delivery.JoinApproved || st.Approver != "oidc:alice@acme.test" || st.Name != "eu-west" {
		t.Fatalf("after approve: %+v", st)
	}

	req.remove = true
	if err := run(t.Context(), testutil.New(t), req, io.Discard); err != nil {
		t.Fatal(err)
	}
	if st := state(t, repo, rootFile); st.State != delivery.JoinRemoved {
		t.Fatalf("after remove: %+v", st)
	}

	publish(t, repo, other)
	req.remove = false
	if err := run(t.Context(), testutil.New(t), req, io.Discard); err == nil || !strings.Contains(err.Error(), "ClusterKeyMismatch") {
		t.Fatalf("a swapped key signed: %v", err)
	}
	if err := run(t.Context(), testutil.New(t), request{}, io.Discard); err == nil {
		t.Error("no flags ran")
	}
}
