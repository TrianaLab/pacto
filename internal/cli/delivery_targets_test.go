package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// targetsEnv is an in-process registry the CLI reaches over plain HTTP, a
// fresh registry prober and a PEM signing key.
func targetsEnv(t *testing.T) (host, key string, pub ed25519.PublicKey) {
	t.Helper()
	old := ocireferrer.Probes
	ocireferrer.Probes = &ocireferrer.Prober{}
	t.Cleanup(func() { ocireferrer.Probes = old })
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	t.Cleanup(srv.Close)
	host = strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	key = filepath.Join(t.TempDir(), "recorder.key")
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return host, key, pub
}

// snapshotDoc writes a target snapshot document of cluster with the extra
// JSON fields given and returns its path.
func snapshotDoc(t *testing.T, fields map[string]any) string {
	t.Helper()
	doc := map[string]any{"cluster": "c-6f1e", "asOf": "", "completeness": "complete", "publisher": "", "pactos": []any{}, "workloads": []any{}}
	for k, v := range fields {
		doc[k] = v
	}
	raw, _ := json.Marshal(doc)
	p := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// publishedSnapshot reads back the snapshot statement tagged for cluster.
func publishedSnapshot(t *testing.T, host, cluster string) delivery.TargetSnapshot {
	t.Helper()
	repo, _ := ocireferrer.Open(host+"/acme/pacto-targets", ocireferrer.Options{Insecure: []string{host}})
	desc, err := repo.Resolve(t.Context(), "cluster-"+cluster)
	if err != nil {
		t.Fatal(err)
	}
	_, rc, err := repo.FetchReference(t.Context(), desc.Digest.String())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	var m struct{ Layers []struct{ Digest string } }
	_ = json.Unmarshal(raw, &m)
	desc, _ = repo.Blobs().Resolve(t.Context(), m.Layers[0].Digest)
	rc, _ = repo.Blobs().Fetch(t.Context(), desc)
	raw, _ = io.ReadAll(rc)
	_ = rc.Close()
	var b struct{ DSSEEnvelope struct{ Payload []byte } }
	_ = json.Unmarshal(raw, &b)
	var st delivery.TargetSnapshotStatement
	if err := json.Unmarshal(b.DSSEEnvelope.Payload, &st); err != nil {
		t.Fatal(err)
	}
	return st.Predicate
}

func TestDeliveryTargetsPublish(t *testing.T) {
	host, key, pub := targetsEnv(t)
	out, err := runDelivery(t, defaultDeps(), "", "targets", "publish", snapshotDoc(t, nil), "--registry", host+"/acme", "--key", key)
	if err != nil {
		t.Fatal(err)
	}
	fp := signing.Hint(pub)
	if !strings.HasPrefix(out, "Published "+host+"/acme/pacto-targets@sha256:") || !strings.Contains(out, "(cluster c-6f1e, asOf ") ||
		!strings.Contains(out, "key "+fp+")") {
		t.Fatalf("out = %q", out)
	}
	if s := publishedSnapshot(t, host, "c-6f1e"); s.Cluster != "c-6f1e" || s.AsOf == "" {
		t.Fatalf("snapshot = %+v", s)
	}
	// JSON output, and a .pub.pem of the same key already there is left alone.
	same, _ := delivery.PublicKeyPEM(pub)
	if err := os.WriteFile(key+".pub.pem", []byte("\n"+same), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runDelivery(t, defaultDeps(), "json", "targets", "publish", snapshotDoc(t, nil), "--registry", "oci://"+host+"/acme/", "--key", key)
	if err != nil || !strings.Contains(out, `"cluster": "c-6f1e"`) {
		t.Fatalf("json = %q, %v", out, err)
	}
	if raw, _ := os.ReadFile(key + ".pub.pem"); string(raw) != "\n"+same {
		t.Fatalf(".pub.pem overwritten: %q", raw)
	}
}

// TestDeliveryTargetsPublishRefusesStalePublicKey: a .pub.pem that holds
// another key (a seed regenerated in place) or no key is refused before the
// push, so the file never names a key other than the one that signs.
func TestDeliveryTargetsPublishRefusesStalePublicKey(t *testing.T) {
	host, key, pub := targetsEnv(t)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	stale, _ := delivery.PublicKeyPEM(other)
	for content, want := range map[string]string{stale: "key " + signing.Hint(other), "mine": "no PEM public key"} {
		if err := os.WriteFile(key+".pub.pem", []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := runDelivery(t, defaultDeps(), "", "targets", "publish", snapshotDoc(t, nil), "--registry", host+"/acme", "--key", key)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), signing.Hint(pub)) {
			t.Fatalf("%q: %v", want, err)
		}
	}
	// A .pub.pem that cannot be read at all is refused too.
	if err := os.Remove(key + ".pub.pem"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(key+".pub.pem", 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", snapshotDoc(t, nil), "--registry", host+"/acme", "--key", key); err == nil ||
		!strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("an unreadable .pub.pem: %v", err)
	}
	repo, _ := ocireferrer.Open(host+"/acme/pacto-targets", ocireferrer.Options{Insecure: []string{host}})
	if _, err := repo.Resolve(t.Context(), "cluster-c-6f1e"); err == nil {
		t.Fatal("a refused publish pushed")
	}
}

func TestDeliveryTargetsPublishRefusesBadDocument(t *testing.T) {
	host, key, _ := targetsEnv(t)
	dir := t.TempDir()
	big := filepath.Join(dir, "big.json")
	_ = os.WriteFile(big, make([]byte, delivery.MaxTargetSnapshotBytes+1), 0o644)
	for name, c := range map[string]struct{ file, want string }{
		"unknown field": {snapshotDoc(t, map[string]any{"extra": 1}), "extra"},
		"bad cluster":   {snapshotDoc(t, map[string]any{"cluster": "a/b"}), "cluster"},
		"completeness":  {snapshotDoc(t, map[string]any{"completeness": "empty"}), "completeness"},
		"missing":       {filepath.Join(dir, "none.json"), "none.json"},
		"directory":     {dir, "directory"},
		"oversize":      {big, "bound"},
	} {
		if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", c.file, "--registry", host+"/acme", "--key", key); err == nil ||
			!strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestDeliveryTargetsPublishRecordsRegistryMode: the mode comes from the probe
// of the cluster's existing tag, so the first publish records none, whatever
// the document claims.
func TestDeliveryTargetsPublishRecordsRegistryMode(t *testing.T) {
	host, key, _ := targetsEnv(t)
	for i := range 2 {
		if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", snapshotDoc(t, map[string]any{"registryMode": "claimed"}), "--registry", host+"/acme", "--key", key); err != nil {
			t.Fatal(err)
		}
		mode := publishedSnapshot(t, host, "c-6f1e").RegistryMode
		if (i == 0) != (mode == "") || (i == 1 && !strings.Contains(mode, "referrers=")) {
			t.Fatalf("publish %d recorded mode %q", i, mode)
		}
	}
}

// TestDeliveryTargetsPublishRecordsKeyAsPublisher: the publisher is the key
// that signed, whatever the document claims.
func TestDeliveryTargetsPublishRecordsKeyAsPublisher(t *testing.T) {
	host, key, pub := targetsEnv(t)
	doc := snapshotDoc(t, map[string]any{"publisher": "pacto-system/pacto-operator-delivery-recorder"})
	if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", doc, "--registry", host+"/acme", "--key", key); err != nil {
		t.Fatal(err)
	}
	if got := publishedSnapshot(t, host, "c-6f1e").Publisher; got != "key:"+signing.Hint(pub) {
		t.Fatalf("publisher = %q", got)
	}
}

// TestDeliveryTargetsPublishAcceptsEvidenceKeygenKey: a `pacto evidence
// keygen` seed signs, and the .pub.pem written next to it is a trust config.
func TestDeliveryTargetsPublishAcceptsEvidenceKeygenKey(t *testing.T) {
	host, _, _ := targetsEnv(t)
	dir := t.TempDir()
	keygen := newEvidenceKeygenCommand(app.NewService(nil, nil), viper.New())
	keygen.SetOut(io.Discard)
	keygen.SetArgs([]string{"--out", dir, "--key-id", "k1"})
	if err := keygen.Execute(); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "k1.key")
	if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", snapshotDoc(t, nil), "--registry", host+"/acme", "--key", key); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(key + ".pub.pem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := delivery.ParseTrust(raw); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
}

// TestDeliveryTargetsPublishKeyAndRegistryErrors: a key that is neither PEM
// nor seed, a missing key, a registry that cannot be opened or written and a
// .pub.pem that cannot be written are errors.
func TestDeliveryTargetsPublishKeyAndRegistryErrors(t *testing.T) {
	host, key, _ := targetsEnv(t)
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.key")
	_ = os.WriteFile(junk, []byte("not a key"), 0o600)
	doc := snapshotDoc(t, nil)
	for name, args := range map[string][]string{
		"junk key":     {"--registry", host + "/acme", "--key", junk},
		"missing key":  {"--registry", host + "/acme", "--key", filepath.Join(dir, "none.key")},
		"bad registry": {"--registry", "UPPER CASE", "--key", key},
		"no registry":  {"--registry", "127.0.0.1:1/acme", "--key", key},
	} {
		if _, err := runDelivery(t, defaultDeps(), "", append([]string{"targets", "publish", doc}, args...)...); err == nil {
			t.Errorf("%s: published", name)
		}
	}
	// The key's directory is read-only, so the .pub.pem cannot be written.
	ro := t.TempDir()
	roKey := filepath.Join(ro, "k.key")
	raw, _ := os.ReadFile(key)
	_ = os.WriteFile(roKey, raw, 0o600)
	_ = os.Chmod(ro, 0o500)
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	if _, err := runDelivery(t, defaultDeps(), "", "targets", "publish", doc, "--registry", host+"/acme", "--key", roKey); err == nil {
		t.Error("a .pub.pem that cannot be written passed")
	}
}

type failingTargetsSigner struct{}

func (failingTargetsSigner) Sign(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("kms down")
}

// TestPushTargetsSignerFails: a snapshot the signer cannot sign is never pushed.
func TestPushTargetsSignerFails(t *testing.T) {
	host, _, _ := targetsEnv(t)
	s := delivery.TargetSnapshot{Cluster: "c-6f1e"}
	if _, err := pushTargets(t.Context(), defaultDeps(), host+"/acme/pacto-targets", &s, failingTargetsSigner{}); err == nil || err.Error() != "kms down" {
		t.Fatalf("err = %v", err)
	}
}
