package cli_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/cli"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
)

// runEvidence executes the CLI with args and returns stdout plus any error.
func runEvidence(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "dev"})
	root.SetArgs(args)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	return out.String(), err
}

// writeEvidence marshals a valid EvidenceSet to disk and returns its path.
func writeEvidence(t *testing.T, dir string) string {
	t.Helper()
	prov := evidence.Provenance{Collector: "collector", DetectedAt: time.Unix(1000, 0).UTC()}
	set := evidence.EvidenceSet{
		Subject:      evidence.SubjectRef{Kind: "service", Name: "svc"},
		ContractRef:  "oci://ghcr.io/acme/svc:1.0.0",
		Source:       "test",
		ObservedAt:   time.Unix(1000, 0).UTC(),
		Observations: []evidence.Observation{evidence.NewCapabilityObserved(evidence.SubjectRef{Kind: "capability", Name: "cap1"}, true, prov)},
	}
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "evidence.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// setup mints a keypair (key id "k1") and an evidence file in a temp dir.
func setup(t *testing.T) (dir, keyPath, evidencePath string) {
	t.Helper()
	dir = t.TempDir()
	if _, err := runEvidence(t, "evidence", "keygen", "--out", dir, "--key-id", "k1"); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	// Bind key k1 to producer "p" in the trust store ("<producer>__<keyId>.pub"),
	// matching the --producer p the sign helpers use.
	if err := os.Rename(filepath.Join(dir, "k1.pub"), filepath.Join(dir, "p__k1.pub")); err != nil {
		t.Fatal(err)
	}
	return dir, filepath.Join(dir, "k1.key"), writeEvidence(t, dir)
}

// signTo runs `evidence sign` and writes the envelope JSON to a file.
func signTo(t *testing.T, dir, keyPath, evidencePath string, extra ...string) string {
	t.Helper()
	args := append([]string{"evidence", "sign", "--key", keyPath, "--key-id", "k1", "--producer", "p", "--ttl", "0"}, extra...)
	args = append(args, evidencePath)
	out, err := runEvidence(t, args...)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	envPath := filepath.Join(dir, "envelope.json")
	if err := os.WriteFile(envPath, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	return envPath
}

func TestEvidenceKeygen_TextAndJSON(t *testing.T) {
	dir := t.TempDir()
	out, err := runEvidence(t, "evidence", "keygen", "--out", dir, "--key-id", "k1")
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	if !strings.Contains(out, "key id:") || !strings.Contains(out, "k1") {
		t.Errorf("text output missing fields: %q", out)
	}
	for _, name := range []string{"k1.key", "k1.pub"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	info, _ := os.Stat(filepath.Join(dir, "k1.key"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("private key perms = %v, want 0600", info.Mode().Perm())
	}

	out, err = runEvidence(t, "evidence", "keygen", "--out", t.TempDir(), "--output-format", "json")
	if err != nil {
		t.Fatalf("keygen json: %v", err)
	}
	if !strings.Contains(out, `"keyId"`) || !strings.Contains(out, `"publicKey"`) {
		t.Errorf("json output missing fields: %q", out)
	}
}

func TestEvidenceKeygen_Error(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// --out beneath a regular file cannot be created.
	if _, err := runEvidence(t, "evidence", "keygen", "--out", filepath.Join(file, "sub")); err == nil {
		t.Fatal("expected keygen error")
	}
}

func TestEvidenceSignVerify_Roundtrip(t *testing.T) {
	dir, keyPath, ev := setup(t)
	envPath := signTo(t, dir, keyPath, ev, "--id", "env-1", "--issued-at", "2020-01-01T00:00:00Z")

	out, err := runEvidence(t, "evidence", "verify", "--trust", dir, envPath)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(out, "ok:") || !strings.Contains(out, "env-1") {
		t.Errorf("expected ok text, got %q", out)
	}

	// JSON output.
	out, err = runEvidence(t, "evidence", "verify", "--trust", dir, "--output-format", "json", envPath)
	if err != nil {
		t.Fatalf("verify json: %v", err)
	}
	if !strings.Contains(out, `"ok": true`) {
		t.Errorf("expected ok json, got %q", out)
	}
}

func TestEvidenceSign_DefaultIDAndNow(t *testing.T) {
	_, keyPath, ev := setup(t)
	// No --id, no --issued-at, default ttl: exercises content-hash id and now().
	out, err := runEvidence(t, "evidence", "sign", "--key", keyPath, "--key-id", "k1", "--producer", "p", ev)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(out, `"sha256:`) {
		t.Errorf("expected content-hash id, got %q", out)
	}
}

func TestEvidenceSign_Errors(t *testing.T) {
	_, keyPath, ev := setup(t)

	// Missing --producer -> app-layer error.
	if _, err := runEvidence(t, "evidence", "sign", "--key", keyPath, "--key-id", "k1", ev); err == nil {
		t.Error("expected error for missing producer")
	}
	// Bad --issued-at -> parse error.
	if _, err := runEvidence(t, "evidence", "sign", "--key", keyPath, "--key-id", "k1", "--producer", "p", "--issued-at", "nope", ev); err == nil {
		t.Error("expected error for bad issued-at")
	}
}

func TestEvidenceVerify_TamperedAndUnknownKey(t *testing.T) {
	dir, keyPath, ev := setup(t)
	envPath := signTo(t, dir, keyPath, ev, "--id", "env-1", "--issued-at", "2020-01-01T00:00:00Z")

	// Verify against an empty trust store -> unknown key -> non-zero exit.
	emptyTrust := t.TempDir()
	if _, err := os.Create(filepath.Join(emptyTrust, "unrelated.pub")); err != nil {
		t.Fatal(err)
	}
	// unrelated.pub is empty/undecodable -> operational trust-store error.
	if _, err := runEvidence(t, "evidence", "verify", "--trust", emptyTrust, envPath); err == nil {
		t.Error("expected trust-store error")
	}

	// Tamper the signature so the envelope still decodes but fails verification.
	data, _ := os.ReadFile(envPath)
	env, err := evidenceenvelope.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature.Value = base64.StdEncoding.EncodeToString(make([]byte, 64))
	tamperedBytes, _ := json.Marshal(env)
	tamperedPath := filepath.Join(dir, "tampered.json")
	if err := os.WriteFile(tamperedPath, tamperedBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runEvidence(t, "evidence", "verify", "--trust", dir, tamperedPath)
	if err == nil {
		t.Fatal("expected verification failure exit")
	}
	if !strings.Contains(out, "FAILED") {
		t.Errorf("expected FAILED text, got %q", out)
	}
}

func TestEvidenceVerify_OperationalError(t *testing.T) {
	// Missing envelope file -> operational error.
	dir, _, _ := setup(t)
	if _, err := runEvidence(t, "evidence", "verify", "--trust", dir, "/nope/envelope.json"); err == nil {
		t.Error("expected error for missing envelope")
	}
}

func TestEvidenceVerify_PrintError(t *testing.T) {
	dir, keyPath, ev := setup(t)
	envPath := signTo(t, dir, keyPath, ev, "--id", "env-1", "--issued-at", "2020-01-01T00:00:00Z")

	// A failing writer makes JSON encoding of the result error out, covering the
	// print-error branch of the verify command.
	root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "dev"})
	root.SetArgs([]string{"evidence", "verify", "--trust", dir, "--output-format", "json", envPath})
	root.SetOut(failWriter{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Fatal("expected print error")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write fail") }

func TestEvidenceInspect_TextJSONAndRedaction(t *testing.T) {
	dir := t.TempDir()
	// Empty store recovers to ready. --store-dir aliases --bucket-url file://.
	out, err := runEvidence(t, "evidence", "inspect", "--store-dir", dir)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !strings.Contains(out, "Evidence store (file,") || !strings.Contains(out, "phase:") || !strings.Contains(out, "records:") {
		t.Errorf("text output missing fields: %q", out)
	}

	out, err = runEvidence(t, "evidence", "inspect", "--bucket-url", "file://"+dir, "--output-format", "json")
	if err != nil {
		t.Fatalf("inspect json: %v", err)
	}
	if !strings.Contains(out, `"backend"`) || !strings.Contains(out, `"phase"`) {
		t.Errorf("json output missing fields: %q", out)
	}
	// Redaction: the raw bucket path / scheme URL must never appear in any output.
	if strings.Contains(out, dir) || strings.Contains(out, "file://") {
		t.Errorf("inspect leaked the raw bucket URL: %q", out)
	}

	// An unopenable bucket surfaces as an error.
	if _, err := runEvidence(t, "evidence", "inspect", "--bucket-url", "bogus://x"); err == nil {
		t.Error("expected an error for an unopenable bucket")
	}
}

func TestEvidenceServe_RequiresFlags(t *testing.T) {
	// --trust is required; --bucket-url and --prefix have defaults.
	if _, err := runEvidence(t, "evidence", "serve"); err == nil {
		t.Error("expected required-flag error (--trust)")
	}
}

func TestEvidenceServe_ListenError(t *testing.T) {
	dir, _, _ := setup(t)
	if _, err := runEvidence(t, "evidence", "serve", "--port", "-1", "--trust", dir, "--bucket-url", "file://"+t.TempDir()); err == nil {
		t.Error("expected listen error for invalid port")
	}
	// --listen-address supersedes --port and is exercised on the same error path.
	// A value with no port is rejected by net.Listen without any DNS lookup.
	if _, err := runEvidence(t, "evidence", "serve", "--listen-address", "missing-port", "--trust", dir, "--bucket-url", "file://"+t.TempDir()); err == nil {
		t.Error("expected listen error for bad listen-address")
	}
}

func TestEvidenceServe_GracefulShutdown(t *testing.T) {
	dir, _, _ := setup(t)
	// --store-dir is the deprecated alias; it maps to a file:// bucket and warns.
	storeDir := filepath.Join(t.TempDir(), "store")

	root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "dev"})
	root.SetArgs([]string{"evidence", "serve", "--port", "0", "--trust", dir, "--store-dir", storeDir})
	root.SetOut(&bytes.Buffer{})
	var stderr bytes.Buffer
	root.SetErr(&stderr)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if err := root.ExecuteContext(ctx); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if !strings.Contains(stderr.String(), "listening on http://") {
		t.Errorf("expected listening message, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--store-dir is deprecated") {
		t.Errorf("expected deprecation warning, got %q", stderr.String())
	}
}

func TestEvidenceSend(t *testing.T) {
	dir, keyPath, ev := setup(t)
	envPath := signTo(t, dir, keyPath, ev, "--id", "env-1", "--issued-at", "2020-01-01T00:00:00Z")

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ok.Close()

	out, err := runEvidence(t, "evidence", "send", "--url", ok.URL, envPath)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected response body, got %q", out)
	}

	// Non-2xx -> non-zero exit, body still printed.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := runEvidence(t, "evidence", "send", "--url", bad.URL, envPath); err == nil {
		t.Error("expected non-2xx exit")
	}

	// Missing --url.
	if _, err := runEvidence(t, "evidence", "send", envPath); err == nil {
		t.Error("expected required --url error")
	}

	// Read error from a missing envelope file.
	if _, err := runEvidence(t, "evidence", "send", "--url", ok.URL, "/nope/envelope.json"); err == nil {
		t.Error("expected read error")
	}
}
