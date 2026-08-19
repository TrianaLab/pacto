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
	"sync"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/internal/testutil"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
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
	kp, err := (&Service{}).GenerateKey(dir, "", "", false)
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
	kp, err := (&Service{}).GenerateKey(dir, "", "mykey", false)
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

func TestGenerateKey_WithProducer(t *testing.T) {
	dir := t.TempDir()
	kp, err := (&Service{}).GenerateKey(dir, "prod-eu", "k1", false)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if kp.ProducerID != "prod-eu" || kp.KeyID != "k1" {
		t.Errorf("keypair identity = %+v", kp)
	}
	want := filepath.Join(dir, "prod-eu__k1.pub")
	if kp.PublicKeyPath != want {
		t.Errorf("public key path = %q, want %q", kp.PublicKeyPath, want)
	}
	// The workflow round-trips: the trust loader binds the key to the producer the
	// filename encodes — sign with the SAME --producer and it verifies.
	ts, err := loadTrustStore(want)
	if err != nil {
		t.Fatalf("loadTrustStore: %v", err)
	}
	if ts["k1"].ProducerID != "prod-eu" {
		t.Errorf("trust binding = %+v, want producer prod-eu", ts["k1"])
	}
}

func TestLoadTrustStore_DuplicateKeyID(t *testing.T) {
	dir := t.TempDir()
	kp, err := (&Service{}).GenerateKey(dir, "", "k1", false) // k1.pub (bare → producer k1)
	if err != nil {
		t.Fatal(err)
	}
	// A second file binding the SAME key id (to a different producer) must be
	// rejected, not silently overwrite the first.
	data, err := os.ReadFile(kp.PublicKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other__k1.pub"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTrustStore(dir); err == nil {
		t.Fatal("expected a duplicate-key-id error")
	}
}

func TestGenerateKey_RandError(t *testing.T) {
	old := randReader
	randReader = errReader{}
	defer func() { randReader = old }()
	if _, err := (&Service{}).GenerateKey(t.TempDir(), "", "", false); err == nil {
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
	if _, err := (&Service{}).GenerateKey(filepath.Join(file, "sub"), "", "", false); err == nil {
		t.Fatal("expected mkdir error")
	}
}

func TestGenerateKey_WriteErrors(t *testing.T) {
	old := writeFileFn
	defer func() { writeFileFn = old }()

	// Private-key write fails.
	writeFileFn = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if _, err := (&Service{}).GenerateKey(t.TempDir(), "", "", false); err == nil {
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
	if _, err := (&Service{}).GenerateKey(t.TempDir(), "", "", false); err == nil {
		t.Fatal("expected public-key write error")
	}
}

// signAndWrite generates a key, signs the sample evidence and returns the
// keypair, the envelope-file path and the trust directory.
func signAndWrite(t *testing.T, opts SignOptions) (KeyPair, string) {
	t.Helper()
	dir := t.TempDir()
	svc := &Service{}
	kp, err := svc.GenerateKey(dir, "", "", false)
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
	// Bind the trust file to the producer so verification authorizes it:
	// "<producer>__<keyId>.pub".
	boundPub := filepath.Join(dir, opts.ProducerID+"__"+kp.KeyID+".pub")
	if err := os.Rename(kp.PublicKeyPath, boundPub); err != nil {
		t.Fatal(err)
	}
	kp.PublicKeyPath = boundPub
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
	kp, _ := svc.GenerateKey(dir, "", "", false)
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

// A structured trust config populates the per-key subject and contract-repo
// allowlists the filename-only loader can never express (review section S11).
func TestLoadStructuredTrustStore_Valid(t *testing.T) {
	dir := t.TempDir()
	if _, err := (&Service{}).GenerateKey(dir, "", "edgekey", false); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "trust.yaml")
	if err := os.WriteFile(cfgPath, []byte(`apiVersion: pacto.dev/evidence-trust/v1
keys:
  - keyId: edge-eu-2026
    producerId: edge-eu
    publicKeyFile: edgekey.pub
    allowedSubjects:
      - "payments-*"
    allowedContractRepos:
      - ghcr.io/acme/contracts
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ts, err := loadTrustStore(cfgPath) // dispatches on the .yaml extension
	if err != nil {
		t.Fatalf("loadTrustStore: %v", err)
	}
	e, ok := ts["edge-eu-2026"]
	if !ok {
		t.Fatalf("key not loaded: %v", ts)
	}
	if e.ProducerID != "edge-eu" {
		t.Errorf("producer = %q", e.ProducerID)
	}
	if len(e.Subjects) != 1 || e.Subjects[0] != "payments-*" {
		t.Errorf("subjects = %v (the allowlist the bare-.pub loader could not set)", e.Subjects)
	}
	if len(e.ContractRepos) != 1 || e.ContractRepos[0] != "ghcr.io/acme/contracts" {
		t.Errorf("contractRepos = %v", e.ContractRepos)
	}
}

func TestLoadStructuredTrustStore_Rejects(t *testing.T) {
	dir := t.TempDir()
	if _, err := (&Service{}).GenerateKey(dir, "", "edgekey", false); err != nil {
		t.Fatal(err)
	}
	head := "apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n"
	cases := map[string]string{
		"unsupported apiVersion": "apiVersion: pacto.dev/evidence-trust/v2\nkeys:\n  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub}\n",
		"no keys":                "apiVersion: pacto.dev/evidence-trust/v1\nkeys: []\n",
		"unknown field":          head + "  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub, bogus: 1}\n",
		"duplicate key id":       head + "  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub}\n  - {keyId: k, producerId: q, publicKeyFile: edgekey.pub}\n",
		"contradictory producer": head + "  - {keyId: a, producerId: p, publicKeyFile: edgekey.pub}\n  - {keyId: b, producerId: q, publicKeyFile: edgekey.pub}\n",
		"missing key file":       head + "  - {keyId: k, producerId: p, publicKeyFile: nope.pub}\n",
		"traversal key file":     head + "  - {keyId: k, producerId: p, publicKeyFile: ../edgekey.pub}\n",
		"bad key id grammar":     head + "  - {keyId: a/b, producerId: p, publicKeyFile: edgekey.pub}\n",
		"bad producer grammar":   head + "  - {keyId: k, producerId: p/q, publicKeyFile: edgekey.pub}\n",
		"empty public key file":  head + "  - {keyId: k, producerId: p}\n",
		"malformed subject glob": head + "  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub, allowedSubjects: [\"[\"]}\n",
		"malformed repo prefix":  head + "  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub, allowedContractRepos: [\"oci://x@y\"]}\n",
		"empty repo prefix":      head + "  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub, allowedContractRepos: [\"\"]}\n",
		"whitespace repo prefix": head + "  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub, allowedContractRepos: [\"a b\"]}\n",
	}
	// A directly-unreadable config file surfaces the read error.
	if _, err := loadStructuredTrustStore(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("unreadable trust config must error")
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "trust.yaml")
			// The key file lives in `dir`; reference it via a copy alongside the config.
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			// Provide edgekey.pub next to this config for cases that need a real file.
			pub, _ := os.ReadFile(filepath.Join(dir, "edgekey.pub"))
			_ = os.WriteFile(filepath.Join(filepath.Dir(p), "edgekey.pub"), pub, 0o644)
			if _, err := loadTrustStore(p); err == nil {
				t.Errorf("%s: expected rejection, got nil", name)
			}
		})
	}
}

func TestValidateKeyIdent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid-dotted", "edge-eu.2026", true},
		{"valid-alnum", "k1", true},
		{"empty", "", false},
		{"too-long", strings.Repeat("a", 65), false},
		{"dotdot", "a..b", false},
		{"slash", "a/b", false},
		{"backslash", `a\b`, false},
		{"underscore-reserved", "a_b", false},
		{"leading-dot", ".hidden", false},
		{"leading-dash", "-x", false},
		{"space", "a b", false},
		{"tab", "a\tb", false},
		{"null", "a\x00b", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := validateKeyIdent("id", c.in); (err == nil) != c.ok {
				t.Errorf("validateKeyIdent(%q) err=%v, want ok=%v", c.in, err, c.ok)
			}
		})
	}
}

// A key id or producer that could escape the output directory must be rejected
// before any file is written, on both Unix and Windows path syntaxes (S12).
func TestGenerateKey_RejectsUnsafeIdentifiers(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	for _, tc := range []struct{ producer, keyID string }{
		{"", "../../evil"},
		{"../evil", "k1"},
		{"", "a/b"},
		{"", `a\b`},
	} {
		if _, err := svc.GenerateKey(dir, tc.producer, tc.keyID, false); err == nil {
			t.Errorf("GenerateKey(producer=%q, keyID=%q) must be rejected", tc.producer, tc.keyID)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("no files should be written for rejected identifiers, got %d", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil.key")); err == nil {
		t.Error("a traversal write escaped the output directory")
	}
}

// Regenerating over an existing key must fail (protecting the private seed) unless
// --force is given (S12).
func TestGenerateKey_NoSilentOverwrite(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	kp, err := svc.GenerateKey(dir, "", "k1", false)
	if err != nil {
		t.Fatal(err)
	}
	seed1, _ := os.ReadFile(kp.PrivateKeyPath)
	if _, err := svc.GenerateKey(dir, "", "k1", false); err == nil {
		t.Error("re-generating over an existing key must fail without --force")
	}
	if seed2, _ := os.ReadFile(kp.PrivateKeyPath); string(seed1) != string(seed2) {
		t.Error("the existing private seed must be untouched when overwrite is refused")
	}
	if _, err := svc.GenerateKey(dir, "", "k1", true); err != nil {
		t.Fatalf("force overwrite should succeed: %v", err)
	}
	if seed3, _ := os.ReadFile(kp.PrivateKeyPath); string(seed3) == string(seed1) {
		t.Error("force should have written a new seed")
	}
}

// The default envelope id must be producer-safe: two producers reporting an
// identical EvidenceSet must not collide in the store's global id namespace, and
// a producer's id must change with its sequence, while re-signing the same
// (producer, sequence, evidence) stays stable (review section S9).
func TestSignEvidence_DefaultIDProducerSafe(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	kp, _ := svc.GenerateKey(dir, "", "", false)
	ev := writeEvidenceFile(t, dir, sampleEvidenceSet())
	sign := func(producer string, seq uint64) string {
		env, err := svc.SignEvidence(SignOptions{EvidencePath: ev, KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: producer, Sequence: seq})
		if err != nil {
			t.Fatal(err)
		}
		return env.ID
	}
	base := sign("prod-eu", 1)
	if sign("prod-us", 1) == base {
		t.Error("two producers with an identical EvidenceSet must not share a default envelope id")
	}
	if sign("prod-eu", 2) == base {
		t.Error("same producer, different sequence must yield a different id")
	}
	if sign("prod-eu", 1) != base {
		t.Error("same producer+sequence+evidence must yield a stable, idempotent id")
	}
	// A maximum sequence must not panic and stays distinct.
	if hi := sign("prod-eu", ^uint64(0)); hi == base {
		t.Error("max-sequence id must differ from sequence 1")
	}
}

func TestSignEvidence_ContentHashID(t *testing.T) {
	dir := t.TempDir()
	svc := &Service{}
	kp, _ := svc.GenerateKey(dir, "", "", false)
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
	kp, _ := svc.GenerateKey(dir, "", "", false)
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
	kp, _ := svc.GenerateKey(dir, "", "", false)

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

// evidenceFixture is one fully wired ingestion scenario: the contract revision
// evidence is stored on, the trust store that authorizes its producer and a
// signed envelope reporting on exactly that revision.
type evidenceFixture struct {
	svc      *Service
	trustDir string
	envPath  string
	producer string
	subject  string // the exact oci://…@sha256:… revision, and the only configured one
}

// serveOptions is the fixture's server configuration: trust plus the one
// configured subject. There is nothing else to configure — the contract registry
// is the store.
func (f evidenceFixture) serveOptions() ServeOptions {
	return ServeOptions{TrustPath: f.trustDir, Subjects: []string{f.subject}, Producers: []string{f.producer}}
}

// referrersRegistry runs an in-process OCI 1.1 registry. Evidence is published as
// a Referrer of the contract manifest, so the plain matrix registry — which has
// no Referrers API — cannot host it. stop closes it, to simulate an outage.
func referrersRegistry(t *testing.T) (host string, stop func()) {
	t.Helper()
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	var once sync.Once
	stop = func() { once.Do(srv.Close) }
	t.Cleanup(stop)
	return srv.Listener.Addr().String(), stop
}

// stalledRegistry serves an inner registry until it is stalled, after which it
// accepts the request, announces its arrival and then answers nothing at all.
// That is the outage a connection error never reports: the registry is
// reachable, the handshake succeeds, and the response simply never comes.
type stalledRegistry struct {
	inner     http.Handler
	mu        sync.Mutex
	stalled   bool
	arrived   chan struct{} // a request reached the stalled registry
	cancelled chan struct{} // that request's caller gave up on it
}

func (s *stalledRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	stalled := s.stalled
	s.mu.Unlock()
	if !stalled {
		s.inner.ServeHTTP(w, r)
		return
	}
	signal(s.arrived)
	<-r.Context().Done() // the caller hung up or its budget expired
	signal(s.cancelled)
}

func (s *stalledRegistry) stall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stalled = true
}

// signal reports on ch without ever blocking the registry goroutine, so a test
// that only cares about the first request cannot wedge a later one.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// serveTestFixtures mints a key, pushes a resolvable bundle and signs an envelope
// against the digest that push produced.
func serveTestFixtures(t *testing.T) evidenceFixture {
	t.Helper()
	host, _ := referrersRegistry(t)
	return evidenceFixtureOn(t, host)
}

// evidenceFixtureOn builds the fixture against an already-running registry, so a
// test that needs to take the registry down owns its lifetime.
func evidenceFixtureOn(t *testing.T, host string) evidenceFixture {
	t.Helper()
	dir := t.TempDir()
	// Push the svc-a bundle so the evidence references it by an IMMUTABLE oci://
	// digest — the ingestion contract-ref policy accepts nothing else (no local
	// paths, no mutable tags), and that same digest is the OCI subject the accepted
	// record is attached to. The returned Service resolves the ref against the same
	// registry.
	svc, client := mxService(mxHostKeychain{})
	digest, err := client.Push(context.Background(), host+"/svc-a:1.0.0", mxBundle(t, "svc-a", "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	contractRef := "oci://" + host + "/svc-a@" + digest

	kp, err := (&Service{}).GenerateKey(dir, "", "k1", false)
	if err != nil {
		t.Fatal(err)
	}
	// Bind the trust file to the producer: "<producer>__<keyId>.pub".
	if err := os.Rename(kp.PublicKeyPath, filepath.Join(dir, "prod-eu__"+kp.KeyID+".pub")); err != nil {
		t.Fatal(err)
	}
	set := sampleEvidenceSet()
	set.ContractRef = contractRef
	set.Subject = evidence.SubjectRef{Kind: "service", Name: "svc-a"}
	evFile := writeEvidenceFile(t, dir, set)
	env, err := (&Service{}).SignEvidence(SignOptions{EvidencePath: evFile, KeyPath: kp.PrivateKeyPath, KeyID: kp.KeyID, ProducerID: "prod-eu", TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(env)
	envPath := filepath.Join(dir, "envelope.json")
	if err := os.WriteFile(envPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return evidenceFixture{svc: svc, trustDir: dir, envPath: envPath, producer: "prod-eu", subject: contractRef}
}

// serveEvidence starts the ingestion host on a random port and returns its base
// URL. The server is stopped when the test ends.
func serveEvidence(t *testing.T, svc *Service, opts ServeOptions) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.ServeEvidenceOnListener(ctx, ln, opts) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeEvidenceOnListener = %v, want nil on cancel", err)
		}
	})
	waitForHTTP(t, base+"/api/evidence/v1/health")
	return base
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
	f := serveTestFixtures(t)
	base := serveEvidence(t, f.svc, f.serveOptions())

	// The server holds nothing locally, so readiness is a live registry preflight
	// and is answered immediately — there is no recovery to wait for.
	if code := getStatus(t, base+"/api/evidence/v1/ready"); code != http.StatusOK {
		t.Errorf("ready = %d, want 200", code)
	}

	// POST the signed envelope -> 202 Accepted; re-POSTing is a replay -> 409.
	if code := postFile(t, f.svc, base, f.envPath); code != http.StatusAccepted {
		t.Fatalf("accept status = %d", code)
	}
	if code := postFile(t, f.svc, base, f.envPath); code != http.StatusConflict {
		t.Errorf("replay status = %d, want 409", code)
	}

	// Producers advertises the trusted producer; targets projects the record.
	if body := getBody(t, base+"/api/evidence/v1/producers"); !strings.Contains(body, f.producer) {
		t.Errorf("producers body = %q, want %q", body, f.producer)
	}
	if body := getBody(t, base+"/api/evidence/v1/targets"); !strings.Contains(body, "svc-a") {
		t.Errorf("targets body = %q, want svc-a", body)
	}

	// A second server, with no shared state whatsoever, reads the same evidence and
	// still refuses the replay: the durable store is the registry, so restarting the
	// Evidence Server — or replacing its container outright — loses nothing.
	restarted := serveEvidence(t, f.svc, f.serveOptions())
	if body := getBody(t, restarted+"/api/evidence/v1/targets"); !strings.Contains(body, "svc-a") {
		t.Errorf("targets after restart = %q, want svc-a", body)
	}
	if code := postFile(t, f.svc, restarted, f.envPath); code != http.StatusConflict {
		t.Errorf("replay after restart = %d, want 409", code)
	}
}

// The registry IS the store, so losing it must be visible as uncertainty: not
// ready, ingestion refused, and the read reported unavailable. Serving 200 with an
// empty target list would claim every environment is gone.
func TestServeEvidence_RegistryOutage(t *testing.T) {
	host, stopRegistry := referrersRegistry(t)
	f := evidenceFixtureOn(t, host)
	base := serveEvidence(t, f.svc, f.serveOptions())

	if code := postFile(t, f.svc, base, f.envPath); code != http.StatusAccepted {
		t.Fatalf("accept status = %d", code)
	}
	if code := getStatus(t, base+"/api/evidence/v1/targets"); code != http.StatusOK {
		t.Fatalf("targets before the outage = %d, want 200", code)
	}

	stopRegistry()

	// Liveness is about the process, which is fine; readiness is about the store,
	// which is not. An outage must never restart the container.
	if code := getStatus(t, base+"/api/evidence/v1/health"); code != http.StatusOK {
		t.Errorf("health during the outage = %d, want 200", code)
	}
	if code := getStatus(t, base+"/api/evidence/v1/ready"); code != http.StatusServiceUnavailable {
		t.Errorf("ready during the outage = %d, want 503", code)
	}
	if code := postFile(t, f.svc, base, f.envPath); code < 500 {
		t.Errorf("ingest during the outage = %d, want a 5xx: it must fail closed", code)
	}
	resp, err := http.Get(base + "/api/evidence/v1/targets")
	if err != nil {
		t.Fatal(err)
	}
	body := readAllClose(resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("targets during the outage = %d %s, want 503: unreadable is not empty", resp.StatusCode, body)
	}
	if !strings.Contains(body, "registry_unavailable") {
		t.Errorf("targets body = %q, want it to name the unavailable registry", body)
	}
}

// A registry that accepts the request and then never answers it is the outage a
// connection error never reports, and an unbounded client waits on it forever.
// Readiness has to be bounded end to end: it answers 503 on its own budget, the
// registry request it abandons is cancelled rather than leaked, and liveness
// keeps answering throughout — a wedged registry takes the host out of rotation,
// it never restarts it.
func TestServeEvidence_ReadinessIsBounded(t *testing.T) {
	budget := 500 * time.Millisecond
	restore := readinessTimeout
	readinessTimeout = budget
	t.Cleanup(func() { readinessTimeout = restore })

	// Two concurrent probes — a kubelet's and a producer's, say. Each must be
	// bounded on its own, which is only true if nothing serializes them behind a
	// lock no deadline can interrupt.
	const probes = 2
	reg := &stalledRegistry{
		inner:     testutil.NewReferrersRegistry(testutil.ReferrersOptions{}),
		arrived:   make(chan struct{}, probes),
		cancelled: make(chan struct{}, probes),
	}
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	f := evidenceFixtureOn(t, srv.Listener.Addr().String())
	base := serveEvidence(t, f.svc, f.serveOptions())

	reg.stall()
	type probe struct {
		code    int
		elapsed time.Duration
		err     error
	}
	ready := make(chan probe, probes)
	for range probes {
		go func() {
			start := time.Now()
			// Far more patience than the budget, so an unbounded readiness reports a
			// transport timeout here instead of hanging the whole test.
			resp, err := (&http.Client{Timeout: 20 * budget}).Get(base + "/api/evidence/v1/ready")
			p := probe{elapsed: time.Since(start), err: err}
			if err == nil {
				p.code = resp.StatusCode
				_ = resp.Body.Close()
			}
			ready <- p
		}()
	}

	// Both probes reached the registry and it is HOLDING them, so everything below
	// happens while the store is genuinely unanswerable rather than merely slow.
	await(t, reg.arrived, probes, 20*budget, "reach the registry")
	if code := getStatus(t, base+"/api/evidence/v1/health"); code != http.StatusOK {
		t.Errorf("health while the registry is stuck = %d, want 200: liveness is about the process", code)
	}

	for range probes {
		p := <-ready
		if p.err != nil {
			t.Fatalf("ready never came back: %v", p.err)
		}
		if p.code != http.StatusServiceUnavailable {
			t.Errorf("ready against a stuck registry = %d, want 503", p.code)
		}
		if p.elapsed > 10*budget {
			t.Errorf("ready took %v, want it bounded by the %v budget", p.elapsed, budget)
		}
	}
	await(t, reg.cancelled, probes, 20*budget, "be cancelled once abandoned")
}

// await waits for n signals on ch, failing with what they were waiting for.
func await(t *testing.T, ch <-chan struct{}, n int, patience time.Duration, what string) {
	t.Helper()
	for i := range n {
		select {
		case <-ch:
		case <-time.After(patience):
			t.Fatalf("only %d of %d registry requests did %s within %v", i, n, what, patience)
		}
	}
}

// The other half of bounded: the caller can be the one who gives up. A probe
// whose client disconnects must take its registry request down with it, which it
// can only do if the preflight is parented on the request rather than on the
// server's lifetime.
func TestServeEvidence_ReadinessFollowsTheCaller(t *testing.T) {
	// A budget far longer than anything this test waits for, so only the caller's
	// disconnect can be what ends the registry request.
	restore := readinessTimeout
	readinessTimeout = 10 * time.Second
	t.Cleanup(func() { readinessTimeout = restore })

	reg := &stalledRegistry{
		inner:     testutil.NewReferrersRegistry(testutil.ReferrersOptions{}),
		arrived:   make(chan struct{}, 1),
		cancelled: make(chan struct{}, 1),
	}
	srv := httptest.NewServer(reg)
	t.Cleanup(srv.Close)
	f := evidenceFixtureOn(t, srv.Listener.Addr().String())
	base := serveEvidence(t, f.svc, f.serveOptions())

	reg.stall()
	reqCtx, hangUp := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/api/evidence/v1/ready", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-reg.arrived // the registry is holding the probe's request
	hangUp()
	select {
	case <-reg.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the registry request outlived the caller: readiness is not parented on the request")
	}
	<-done
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
	err = (&Service{}).ServeEvidenceOnListener(context.Background(), ln, ServeOptions{TrustPath: t.TempDir()})
	if err == nil {
		t.Fatal("expected assembly error for empty trust store")
	}
	if _, e := ln.Accept(); e == nil {
		t.Error("expected the listener to be closed after assembly failure")
	}
}

func TestServeEvidenceOnListener_ServeError(t *testing.T) {
	f := serveTestFixtures(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close() // Serve on a closed listener fails via errCh.
	if err := (&Service{}).ServeEvidenceOnListener(context.Background(), ln, f.serveOptions()); err == nil {
		t.Error("expected serve error on a closed listener")
	}
}

func TestServeEvidence_ListenAndCancel(t *testing.T) {
	f := serveTestFixtures(t)
	svc := &Service{}

	// Invalid port -> listen error.
	opts := f.serveOptions()
	opts.Port = -1
	if err := svc.ServeEvidence(context.Background(), opts); err == nil {
		t.Error("expected listen error for invalid port")
	}

	// OS-assigned port, cancelled context -> graceful nil.
	opts.Port = 0
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.ServeEvidence(ctx, opts) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Errorf("ServeEvidence = %v, want nil on cancel", err)
	}
}

// Configuration is validated before the server listens, so a misconfigured
// Evidence Server fails to start rather than accepting evidence it cannot store.
func TestBuildEvidenceHost_Errors(t *testing.T) {
	svc := &Service{}
	f := serveTestFixtures(t)
	digest := "sha256:" + strings.Repeat("a", 64)

	cases := map[string]ServeOptions{
		"unreadable trust store": {TrustPath: t.TempDir(), Subjects: []string{f.subject}},
		// No subject at all: there is nowhere to write and nothing to read, and
		// there is deliberately no catalog-wide discovery to fall back on.
		"no configured subject": {TrustPath: f.trustDir},
		// A mutable tag is not a revision: evidence attached to it would silently
		// change meaning the next time somebody pushed.
		"mutable tag subject": {TrustPath: f.trustDir, Subjects: []string{"oci://reg.example/team/orders:latest"}},
		"local path subject":  {TrustPath: f.trustDir, Subjects: []string{"./bundle"}},
		// Parses as a subject, but no registry client can address it.
		"unaddressable registry": {TrustPath: f.trustDir, Subjects: []string{"oci://bad host/orders@" + digest}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.buildEvidenceHost(opts); err == nil {
				t.Errorf("%s: buildEvidenceHost succeeded, want a configuration error", name)
			}
		})
	}
}

func TestSendEvidence(t *testing.T) {
	envPath := serveTestFixtures(t).envPath
	svc := &Service{}

	// 2xx. A BASE host URL (the documented form) must be POSTed to the standard
	// envelope endpoint path, not verbatim to "/".
	var gotPath string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
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
	if gotPath != evidenceingest.EnvelopesPath {
		t.Errorf("base URL should POST to %q, got %q", evidenceingest.EnvelopesPath, gotPath)
	}
	// A full endpoint URL is used verbatim (no double-append).
	if _, err := svc.SendEvidence(context.Background(), ok.URL+evidenceingest.EnvelopesPath, envPath); err != nil {
		t.Fatalf("send full url: %v", err)
	}
	if gotPath != evidenceingest.EnvelopesPath {
		t.Errorf("full URL path mangled: %q", gotPath)
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
