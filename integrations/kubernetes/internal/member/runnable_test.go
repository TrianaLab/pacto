/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package member

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/trianalab/pacto/v4/pkg/kubecluster"
)

var (
	ident  = kubecluster.Identity{ID: "uid-1", Name: "eu-prod"}
	_, key = func() (ed25519.PublicKey, ed25519.PrivateKey) {
		pub, priv, _ := ed25519.GenerateKey(nil)
		return pub, priv
	}()
)

func TestHubURLRefusesHTTP(t *testing.T) {
	for _, u := range []string{"http://pacto.acme.dev", "ws://pacto.acme.dev", "pacto.acme.dev", ""} {
		if _, err := New(u, "", ident, key, &Reviewer{}); err == nil || !strings.Contains(err.Error(), "--hub-url") {
			t.Fatalf("New(%q) = %v; want the URL refused", u, err)
		}
	}
	for _, u := range []string{"https://pacto.acme.dev", "wss://pacto.acme.dev/base"} {
		if _, err := New(u, "", ident, key, &Reviewer{}); err != nil {
			t.Fatalf("New(%q) = %v", u, err)
		}
	}
}

func TestHubURLNeedsClusterID(t *testing.T) {
	if _, err := New("https://pacto.acme.dev", "", kubecluster.Identity{}, key, &Reviewer{}); err == nil ||
		!strings.Contains(err.Error(), "cluster id") {
		t.Fatalf("New without a cluster id = %v; want refused", err)
	}
}

func TestHubURLNeedsInstallationKey(t *testing.T) {
	if _, err := New("https://pacto.acme.dev", "", ident, nil, &Reviewer{}); err == nil ||
		!strings.Contains(err.Error(), "registry") {
		t.Fatalf("New without a key = %v; want refused naming the registry value", err)
	}
}

func TestHubCAFile(t *testing.T) {
	srv := httptest.NewTLSServer(nil)
	defer srv.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600)
	bad := filepath.Join(dir, "bad.crt")
	_ = os.WriteFile(bad, []byte("not pem"), 0o600)

	if _, err := New("https://pacto.acme.dev", ca, ident, key, &Reviewer{}); err != nil {
		t.Fatalf("New with a CA file = %v", err)
	}
	for _, f := range []string{bad, filepath.Join(dir, "missing")} {
		if _, err := New("https://pacto.acme.dev", f, ident, key, &Reviewer{}); err == nil || !strings.Contains(err.Error(), "--hub-ca-file") {
			t.Fatalf("New(%s) = %v; want the CA file refused", f, err)
		}
	}
	prev := systemPool
	t.Cleanup(func() { systemPool = prev })
	systemPool = func() (*x509.CertPool, error) { return nil, errors.New("no pool") }
	if _, err := New("https://pacto.acme.dev", ca, ident, key, &Reviewer{}); err == nil || !strings.Contains(err.Error(), "system CA pool") {
		t.Fatalf("New with no system pool = %v; want refused", err)
	}
}

func TestMemberLinkNeedsLeaderElection(t *testing.T) {
	l, err := New("https://127.0.0.1:1", "", ident, key, &Reviewer{})
	if err != nil {
		t.Fatal(err)
	}
	// controller-runtime starts a Runnable that does not opt out through
	// LeaderElectionRunnable on the elected leader only.
	if _, optsOut := any(l).(manager.LeaderElectionRunnable); optsOut {
		t.Fatal("the member link must run on the elected leader only")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start on a cancelled context = %v; want nil", err)
	}
}
