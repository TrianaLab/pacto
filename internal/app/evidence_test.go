package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
)

// sampleEvidenceSet returns a minimal, structurally valid EvidenceSet.
func sampleEvidenceSet() evidence.EvidenceSet {
	prov := evidence.Provenance{Collector: "collector", DetectedAt: time.Unix(1000, 0).UTC()}
	obs := evidence.NewCapabilityObserved(evidence.SubjectRef{Kind: "capability", Name: "cap1"}, true, prov)
	return evidence.EvidenceSet{
		Subject:      evidence.SubjectRef{Kind: "service", Name: "svc"},
		ContractRef:  "oci://ghcr.io/acme/svc:1.0.0",
		Source:       "test",
		ObservedAt:   time.Unix(1000, 0).UTC(),
		Observations: []evidence.Observation{obs},
	}
}

// writeEvidenceFile marshals a set to disk and returns its path.
func writeEvidenceFile(t *testing.T, dir string, set evidence.EvidenceSet) string {
	t.Helper()
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

func TestGenerateKey_DefaultKeyID(t *testing.T) {
	dir := t.TempDir()
	kp, err := (&Service{}).GenerateKey(dir, "")
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if kp.KeyID == "" || kp.PublicKey == "" {
		t.Fatalf("expected key id and public key, got %+v", kp)
	}
	if kp.PrivateKeyPath != filepath.Join(dir, kp.KeyID+".key") {
		t.Errorf("unexpected private path %q", kp.PrivateKeyPath)
	}
	if kp.PublicKeyPath != filepath.Join(dir, kp.KeyID+".pub") {
		t.Errorf("unexpected public path %q", kp.PublicKeyPath)
	}
	info, err := os.Stat(kp.PrivateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("private key perms = %v, want 0600", info.Mode().Perm())
	}
}

func TestGenerateKey_ExplicitKeyID(t *testing.T) {
	dir := t.TempDir()
	kp, err := (&Service{}).GenerateKey(dir, "mykey")
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if kp.KeyID != "mykey" {
		t.Errorf("key id = %q, want mykey", kp.KeyID)
	}
	if _, err := os.Stat(filepath.Join(dir, "mykey.pub")); err != nil {
		t.Errorf("mykey.pub not written: %v", err)
	}
}

func TestGenerateKey_RandError(t *testing.T) {
	old := randReader
	randReader = errReader{}
	defer func() { randReader = old }()
	if _, err := (&Service{}).GenerateKey(t.TempDir(), ""); err == nil {
		t.Fatal("expected error from failing entropy source")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestGenerateKey_MkdirError(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory cannot be created beneath a regular file.
	if _, err := (&Service{}).GenerateKey(filepath.Join(file, "sub"), ""); err == nil {
		t.Fatal("expected mkdir error")
	}
}

func TestGenerateKey_WriteErrors(t *testing.T) {
	old := writeFileFn
	defer func() { writeFileFn = old }()

	// Private-key write fails.
	writeFileFn = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if _, err := (&Service{}).GenerateKey(t.TempDir(), ""); err == nil {
		t.Fatal("expected private-key write error")
	}

	// Public-key write fails (private succeeds).
	calls := 0
	writeFileFn = func(string, []byte, os.FileMode) error {
		calls++
		if calls == 1 {
			return nil
		}
		return errors.New("disk full")
	}
	if _, err := (&Service{}).GenerateKey(t.TempDir(), ""); err == nil {
		t.Fatal("expected public-key write error")
	}
}

// signAndWrite generates a key, signs the sample evidence and returns the
// keypair, the envelope-file path and the trust directory.
func signAndWrite(t *testing.T, opts SignOptions) (KeyPair, string) {
	t.Helper()
	dir := t.TempDir()
	svc := &Service{}
	kp, err := svc.GenerateKey(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	opts.KeyPath = kp.PrivateKeyPath
	if opts.KeyID == "" {
		opts.KeyID = kp.KeyID
	}
	if opts.ProducerID == "" {
		opts.ProducerID = "producer-a"
	}
	if opts.EvidencePath == "" {
		opts.EvidencePath = writeEvidenceFile(t, dir, sampleEvidenceSet())
	}
	env, err := svc.SignEvidence(opts)
	if err != nil {
		t.Fatalf("SignEvidence: %v", err)
	}
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "envelope.json")
	if err := os.WriteFile(envPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return kp, envPath
}

func TestSignVerify_Roundtrip(t *testing.T) {
	kp, envPath := signAndWrite(t, SignOptions{})
	res, err := (&Service{}).VerifyEnvelope(VerifyOptions{EnvelopePath: envPath, TrustPath: filepath.Dir(kp.PublicKeyPath)})
	if err != nil {
		t.Fatalf("VerifyEnvelope: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected valid, got %+v", res)
	}
	if res.KeyID != kp.KeyID {
		t.Errorf("key id = %q, want %q", res.KeyID, kp.KeyID)
	}
}

func TestSignVerify_SingleFileTrust(t *testing.T) {
	kp, envPath := signAndWrite(t, SignOptions{})
	res, err := (&Service{}).VerifyEnvelope(VerifyOptions{EnvelopePath: envPath, TrustPath: kp.PublicKeyPath})
	if err != nil {
		t.Fatalf("VerifyEnvelope: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected valid, got %+v", res)
	}
}

func TestSignEvidence_DeterministicID(t *testing.T) {
	// Explicit id + issued-at + ttl 0 => a fully deterministic, always-fresh envelope.
	fixed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	_, envPath := signAndWrite(t, SignOptions{ID: "env-1", IssuedAt: fixed, TTL: 0})
	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), `"id":"env-1"`) && !strings.Contains(string(data), `"id": "env-1"`) {
		t.Errorf("expected explicit id in envelope, got %s", data)
	}
}

func TestSignEvidence_Sequence(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	kp, _ := svc.GenerateKey(dir, "")
	ev := writeEvidenceFile(t, dir, sampleEvidenceSet())
	env, err := svc.SignEvidence(SignOptions{
		EvidencePath: ev, KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: "p", Sequence: 7, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.Sequence != 7 {
		t.Errorf("Sequence = %d, want 7", env.Sequence)
	}
}

func TestSignEvidence_ContentHashID(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	kp, _ := svc.GenerateKey(dir, "")
	ev := writeEvidenceFile(t, dir, sampleEvidenceSet())
	env, err := svc.SignEvidence(SignOptions{EvidencePath: ev, KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: "p", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(env.ID, "sha256:") {
		t.Errorf("expected content-hash id, got %q", env.ID)
	}
	if env.IssuedAt.IsZero() {
		t.Error("expected IssuedAt to default to now")
	}
	if env.ExpiresAt.IsZero() {
		t.Error("expected ExpiresAt from default TTL")
	}
}

func TestSignEvidence_Errors(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	kp, _ := svc.GenerateKey(dir, "")
	good := writeEvidenceFile(t, dir, sampleEvidenceSet())

	cases := []struct {
		name string
		opts SignOptions
	}{
		{"missing key id", SignOptions{EvidencePath: good, KeyPath: kp.PrivateKeyPath, ProducerID: "p"}},
		{"missing producer", SignOptions{EvidencePath: good, KeyPath: kp.PrivateKeyPath, KeyID: "k"}},
		{"bad key path", SignOptions{EvidencePath: good, KeyPath: "/nope", KeyID: "k", ProducerID: "p"}},
		{"bad evidence path", SignOptions{EvidencePath: "/nope", KeyPath: kp.PrivateKeyPath, KeyID: "k", ProducerID: "p"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.SignEvidence(tc.opts); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSignEvidence_BadEvidenceJSON(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	kp, _ := svc.GenerateKey(dir, "")

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SignEvidence(SignOptions{EvidencePath: bad, KeyPath: kp.PrivateKeyPath, KeyID: "k", ProducerID: "p"}); err == nil {
		t.Fatal("expected decode error")
	}

	// Decodes but fails structural validation (empty subject/source/etc).
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SignEvidence(SignOptions{EvidencePath: empty, KeyPath: kp.PrivateKeyPath, KeyID: "k", ProducerID: "p"}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestVerifyEnvelope_UnknownKey(t *testing.T) {
	// Sign with a Producer.KeyID that is absent from the trust store.
	kp, envPath := signAndWrite(t, SignOptions{KeyID: "ghost"})
	res, err := (&Service{}).VerifyEnvelope(VerifyOptions{EnvelopePath: envPath, TrustPath: filepath.Dir(kp.PublicKeyPath)})
	if err != nil {
		t.Fatalf("unexpected operational error: %v", err)
	}
	if res.OK {
		t.Fatal("expected verification to fail for unknown key")
	}
	if !strings.Contains(res.Reason, "not trusted") {
		t.Errorf("reason = %q, want unknown-key message", res.Reason)
	}
}

func TestVerifyEnvelope_OperationalErrors(t *testing.T) {
	kp, envPath := signAndWrite(t, SignOptions{})
	trustDir := filepath.Dir(kp.PublicKeyPath)
	svc := &Service{}

	if _, err := svc.VerifyEnvelope(VerifyOptions{EnvelopePath: "/nope", TrustPath: trustDir}); err == nil {
		t.Error("expected envelope-read error")
	}
	if _, err := svc.VerifyEnvelope(VerifyOptions{EnvelopePath: envPath, TrustPath: "/nope/missing"}); err == nil {
		t.Error("expected trust-store error")
	}

	// Undecodable envelope.
	garbage := filepath.Join(t.TempDir(), "garbage.json")
	if err := os.WriteFile(garbage, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyEnvelope(VerifyOptions{EnvelopePath: garbage, TrustPath: trustDir}); err == nil {
		t.Error("expected decode error")
	}
}

func TestLoadTrustStore_DirVariants(t *testing.T) {
	kp, _ := signAndWrite(t, SignOptions{})
	trustDir := filepath.Dir(kp.PublicKeyPath)
	// Add a subdirectory and a non-.pub file — both must be skipped.
	if err := os.Mkdir(filepath.Join(trustDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trustDir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, err := loadTrustStore(trustDir)
	if err != nil {
		t.Fatalf("loadTrustStore: %v", err)
	}
	if _, ok := ts[kp.KeyID]; !ok {
		t.Errorf("expected key %q in trust store", kp.KeyID)
	}
}

func TestLoadTrustStore_EmptyDir(t *testing.T) {
	if _, err := loadTrustStore(t.TempDir()); err == nil {
		t.Fatal("expected error for empty trust store")
	}
}

func TestLoadTrustStore_BadPubInDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.pub"), []byte("!!!notbase64"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTrustStore(dir); err == nil {
		t.Fatal("expected error for undecodable public key")
	}
}

func TestLoadKey_LengthAndDecodeErrors(t *testing.T) {
	dir := t.TempDir()

	shortSeed := filepath.Join(dir, "short.key")
	if err := os.WriteFile(shortSeed, []byte("YWJj"), 0o644); err != nil { // "abc" -> 3 bytes
		t.Fatal(err)
	}
	if _, err := loadPrivateKey(shortSeed); err == nil {
		t.Error("expected private-key length error")
	}

	shortPub := filepath.Join(dir, "short.pub")
	if err := os.WriteFile(shortPub, []byte("YWJj"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPublicKey(shortPub); err == nil {
		t.Error("expected public-key length error")
	}

	badB64 := filepath.Join(dir, "bad.key")
	if err := os.WriteFile(badB64, []byte("!!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPrivateKey(badB64); err == nil {
		t.Error("expected base64 decode error")
	}

	if _, err := loadPublicKey("/nope"); err == nil {
		t.Error("expected read error")
	}
}

func TestEvidenceResolver(t *testing.T) {
	bundleDir := t.TempDir()
	body := "pactoVersion: \"2.0\"\nservice:\n  name: svc-a\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(bundleDir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := (&Service{}).EvidenceResolver()
	c, err := r.Resolve(context.Background(), bundleDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Service.Name != "svc-a" {
		t.Errorf("service = %q, want svc-a", c.Service.Name)
	}
	if _, err := r.Resolve(context.Background(), "/nope/missing"); err == nil {
		t.Error("expected error resolving a missing bundle")
	}
}

// serveTestFixtures mints a key, a resolvable local bundle and a signed envelope
// file, returning the trust dir, the envelope path and the producer id.
func serveTestFixtures(t *testing.T) (trustDir, envPath, producer string) {
	t.Helper()
	dir := t.TempDir()
	svc := &Service{}
	kp, err := svc.GenerateKey(dir, "k1")
	if err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(dir, "bundle")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "pactoVersion: \"2.0\"\nservice:\n  name: svc-a\n  version: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(bundleDir, "pacto.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set := sampleEvidenceSet()
	set.ContractRef = bundleDir
	set.Subject = evidence.SubjectRef{Kind: "service", Name: "svc-a"}
	evFile := writeEvidenceFile(t, dir, set)
	env, err := svc.SignEvidence(SignOptions{EvidencePath: evFile, KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: "prod-eu", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(env)
	envPath = filepath.Join(dir, "envelope.json")
	if err := os.WriteFile(envPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, envPath, "prod-eu"
}

func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server not ready at %s", url)
}

func TestServeEvidence_EndToEnd(t *testing.T) {
	trustDir, envPath, producer := serveTestFixtures(t)
	storeDir := filepath.Join(t.TempDir(), "store")
	svc := &Service{}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- svc.ServeEvidenceOnListener(ctx, ln, ServeOptions{
			TrustPath: trustDir, BucketURL: "file://" + storeDir, Prefix: DefaultEvidencePrefix,
			Producers: []string{producer},
		})
	}()

	waitForHTTP(t, base+"/api/evidence/v1/health")

	// Readiness is 200 once the store has recovered.
	if code := getStatus(t, base+"/api/evidence/v1/ready"); code != http.StatusOK {
		t.Errorf("ready = %d, want 200", code)
	}

	// POST the signed envelope -> 202 Accepted; re-POSTing is a replay -> 409
	// (durable dup-id → ErrReplay).
	if code := postFile(t, svc, base, envPath); code != http.StatusAccepted {
		t.Fatalf("accept status = %d", code)
	}
	if code := postFile(t, svc, base, envPath); code != http.StatusConflict {
		t.Errorf("replay status = %d, want 409", code)
	}

	// Producers advertises the trusted producer; targets projects the record.
	if body := getBody(t, base+"/api/evidence/v1/producers"); !strings.Contains(body, producer) {
		t.Errorf("producers body = %q, want %q", body, producer)
	}
	if body := getBody(t, base+"/api/evidence/v1/targets"); !strings.Contains(body, "svc-a") {
		t.Errorf("targets body = %q, want svc-a", body)
	}

	// The accepted record was persisted durably under the prefix.
	entries, err := os.ReadDir(filepath.Join(storeDir, DefaultEvidencePrefix, "envelopes"))
	if err != nil || len(entries) == 0 {
		t.Errorf("expected a persisted record under %s, err=%v entries=%d", storeDir, err, len(entries))
	}

	// Graceful shutdown on context cancel returns nil.
	cancel()
	if err := <-done; err != nil {
		t.Errorf("ServeEvidenceOnListener = %v, want nil on cancel", err)
	}
}

// postFile sends an envelope file and returns the response status, failing on a
// transport error.
func postFile(t *testing.T, svc *Service, base, envPath string) int {
	t.Helper()
	res, err := svc.SendEvidence(context.Background(), base+"/api/evidence/v1/envelopes", envPath)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	return res.StatusCode
}

// getStatus GETs url and returns its status code, failing on a transport error.
func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// getBody GETs url and returns its body, failing on a transport error.
func getBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return readAllClose(resp)
}

func readAllClose(resp *http.Response) string {
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestServeEvidenceOnListener_AssemblyError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// An empty trust dir makes assembly fail; the listener must be closed.
	err = (&Service{}).ServeEvidenceOnListener(context.Background(), ln, ServeOptions{
		TrustPath: t.TempDir(), BucketURL: "file://" + t.TempDir(), Prefix: DefaultEvidencePrefix,
	})
	if err == nil {
		t.Fatal("expected assembly error for empty trust store")
	}
	if _, e := ln.Accept(); e == nil {
		t.Error("expected the listener to be closed after assembly failure")
	}
}

func TestServeEvidenceOnListener_ServeError(t *testing.T) {
	trustDir, _, _ := serveTestFixtures(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // Serve on a closed listener fails via errCh.
	err = (&Service{}).ServeEvidenceOnListener(context.Background(), ln, ServeOptions{
		TrustPath: trustDir, BucketURL: "file://" + t.TempDir(), Prefix: DefaultEvidencePrefix,
	})
	if err == nil {
		t.Error("expected serve error on a closed listener")
	}
}

func TestServeEvidence_ListenAndCancel(t *testing.T) {
	trustDir, _, _ := serveTestFixtures(t)
	svc := &Service{}

	// Invalid port -> listen error.
	if err := svc.ServeEvidence(context.Background(), ServeOptions{Port: -1, TrustPath: trustDir, BucketURL: "file://" + t.TempDir(), Prefix: DefaultEvidencePrefix}); err == nil {
		t.Error("expected listen error for invalid port")
	}

	// OS-assigned port, cancelled context -> graceful nil.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- svc.ServeEvidence(ctx, ServeOptions{Port: 0, TrustPath: trustDir, BucketURL: "file://" + t.TempDir(), Prefix: DefaultEvidencePrefix})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Errorf("ServeEvidence = %v, want nil on cancel", err)
	}
}

func TestBuildEvidenceHost_Errors(t *testing.T) {
	svc := &Service{}
	ctx := context.Background()
	// Trust-store error.
	if _, _, err := svc.buildEvidenceHost(ctx, ServeOptions{TrustPath: t.TempDir(), BucketURL: "file://" + t.TempDir(), Prefix: DefaultEvidencePrefix}); err == nil {
		t.Error("expected trust-store error")
	}
	trustDir, _, _ := serveTestFixtures(t)
	// Bucket mkdir error: cannot create a directory beneath a regular file.
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.buildEvidenceHost(ctx, ServeOptions{TrustPath: trustDir, BucketURL: "file://" + filepath.Join(file, "store"), Prefix: DefaultEvidencePrefix}); err == nil {
		t.Error("expected bucket mkdir error")
	}
	// Bucket open error: an unregistered scheme cannot be opened.
	if _, _, err := svc.buildEvidenceHost(ctx, ServeOptions{TrustPath: trustDir, BucketURL: "bogus://x", Prefix: DefaultEvidencePrefix}); err == nil {
		t.Error("expected bucket open error")
	}
	// Invalid prefix: rejected by the store before it opens.
	if _, _, err := svc.buildEvidenceHost(ctx, ServeOptions{TrustPath: trustDir, BucketURL: "file://" + t.TempDir(), Prefix: "../bad"}); err == nil {
		t.Error("expected prefix error")
	}
}

func TestSendEvidence(t *testing.T) {
	_, envPath, _ := serveTestFixtures(t)
	svc := &Service{}

	// 2xx.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ok.Close()
	res, err := svc.SendEvidence(context.Background(), ok.URL, envPath)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.StatusCode != http.StatusAccepted || !strings.Contains(res.Body, "ok") {
		t.Errorf("res = %+v", res)
	}

	// Non-2xx is reported without error.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	res, err = svc.SendEvidence(context.Background(), bad.URL, envPath)
	if err != nil || res.StatusCode != http.StatusInternalServerError {
		t.Errorf("res = %+v, err = %v", res, err)
	}

	// Missing envelope file.
	if _, err := svc.SendEvidence(context.Background(), ok.URL, "/nope/envelope.json"); err == nil {
		t.Error("expected read error")
	}

	// Malformed URL -> request-build error.
	if _, err := svc.SendEvidence(context.Background(), "://bad", envPath); err == nil {
		t.Error("expected request-build error")
	}

	// Body-read error: a response promising more bytes than it delivers makes
	// io.ReadAll fail with an unexpected EOF.
	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort"))
		_ = conn.Close()
	}))
	defer truncated.Close()
	if _, err := svc.SendEvidence(context.Background(), truncated.URL, envPath); err == nil {
		t.Error("expected body-read error")
	}

	// Transport error: point at a closed server.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	if _, err := svc.SendEvidence(context.Background(), closedURL, envPath); err == nil {
		t.Error("expected transport error")
	}
}

// TestVerify_UsesEnvelopePackage guards the wiring: an envelope signed here must
// decode + verify through the frozen package with the same trust store.
func TestVerify_UsesEnvelopePackage(t *testing.T) {
	kp, envPath := signAndWrite(t, SignOptions{})
	data, _ := os.ReadFile(envPath)
	env, err := evidenceenvelope.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := loadTrustStore(kp.PublicKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := evidenceenvelope.Verify(env, ts, time.Now()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
