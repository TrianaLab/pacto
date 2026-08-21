package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/trianalab/pacto/v3/internal/evidenceoci"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/evidenceingest"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"github.com/trianalab/pacto/v3/pkg/strictjson"
)

// randReader is the entropy source for key generation, overridable in tests to
// exercise the failure path (crypto/rand.Reader effectively never fails).
var randReader io.Reader = rand.Reader

// KeyPair describes an Ed25519 signing keypair written to disk. The private key
// is the base64 32-byte seed (0600); the public key is the base64 32-byte key.
// The public-key file is named <keyId>.pub so its base name IS the trust-store
// key id consumed by VerifyEnvelope.
type KeyPair struct {
	KeyID          string `json:"keyId"`
	ProducerID     string `json:"producerId"`
	PrivateKeyPath string `json:"privateKeyPath"`
	PublicKeyPath  string `json:"publicKeyPath"`
	PublicKey      string `json:"publicKey"`
}

// GenerateKey creates an Ed25519 keypair in dir. When keyID is empty it defaults
// to a short fingerprint of the public key. Files are <keyId>.key (private seed,
// 0600) and <keyId>.pub (public key, 0644). Existing files are NOT overwritten
// unless force is set, so a re-run can never silently destroy a private seed.
func (s *Service) GenerateKey(dir, producer, keyID string, force bool) (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(randReader)
	if err != nil {
		return KeyPair{}, err
	}
	if keyID == "" {
		keyID = keyFingerprint(pub)
	}
	// The public-key filename encodes the trust binding the ingestion host reads:
	// "<producer>__<keyId>.pub" binds the key to exactly that producer. With no
	// --producer the producer defaults to the key id (a bare "<keyId>.pub"), so a
	// single-producer setup needs no filename convention knowledge.
	if producer == "" {
		producer = keyID
	}
	// Identifiers become filenames AND a trust binding, so they must satisfy a
	// closed ASCII grammar: no path separators, "..", control characters,
	// whitespace or the reserved "__" producer/key delimiter. Because the grammar
	// forbids every path-escaping character, filepath.Join(dir, <name>) is
	// guaranteed to stay inside dir — no separate post-join traversal check is
	// reachable, so none is written (it would be dead code under the 100% gate).
	if err := validateKeyIdent("key id", keyID); err != nil {
		return KeyPair{}, err
	}
	if err := validateKeyIdent("producer", producer); err != nil {
		return KeyPair{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return KeyPair{}, err
	}
	pubBase := keyID + ".pub"
	if producer != keyID {
		pubBase = producer + "__" + keyID + ".pub"
	}
	privPath := filepath.Join(dir, keyID+".key")
	pubPath := filepath.Join(dir, pubBase)
	if !force {
		for _, p := range []string{privPath, pubPath} {
			if _, err := os.Stat(p); err == nil {
				return KeyPair{}, fmt.Errorf("%q already exists; pass --force to overwrite", p)
			}
		}
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	seed := base64.StdEncoding.EncodeToString(priv.Seed())
	if err := writeFileFn(privPath, []byte(seed+"\n"), 0o600); err != nil {
		return KeyPair{}, err
	}
	if err := writeFileFn(pubPath, []byte(pubB64+"\n"), 0o644); err != nil {
		return KeyPair{}, err
	}
	return KeyPair{KeyID: keyID, ProducerID: producer, PrivateKeyPath: privPath, PublicKeyPath: pubPath, PublicKey: pubB64}, nil
}

// validateKeyIdent enforces a closed grammar for a producer id or key id: a
// non-empty, <=64-char string of letters, digits, '.' and '-' that starts with a
// letter or digit, with no ".." and (by disallowing '_' entirely) no "__"
// delimiter collision. This rejects path separators, absolute paths, traversal,
// control characters and whitespace before the value is ever used as a filename.
func validateKeyIdent(kind, s string) error {
	switch {
	case s == "":
		return fmt.Errorf("%s must not be empty", kind)
	case len(s) > 64:
		return fmt.Errorf("%s %q exceeds 64 characters", kind, s)
	case strings.Contains(s, ".."):
		return fmt.Errorf("%s %q must not contain %q", kind, s, "..")
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '.' || r == '-') && i > 0:
		default:
			return fmt.Errorf("%s %q contains an invalid character %q (allowed: letters, digits, '.', '-'; must start alphanumeric)", kind, s, string(r))
		}
	}
	return nil
}

// keyFingerprint derives a stable short key id from a public key.
func keyFingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// SignOptions configures SignEvidence.
type SignOptions struct {
	EvidencePath    string
	KeyPath         string
	KeyID           string
	ProducerID      string
	ProducerVersion string
	ID              string        // optional; defaults to a content hash of the EvidenceSet
	IssuedAt        time.Time     // optional; defaults to time.Now (pass a fixed value for determinism)
	TTL             time.Duration // 0 disables expiry
	// Sequence is the producer-scoped monotonic sequence. A producer increments
	// it per report so the ingestion host can reject replays and out-of-order
	// reports; each accepted envelope must have a sequence strictly greater than
	// the producer's last.
	Sequence uint64
}

// SignEvidence reads an EvidenceSet JSON file, wraps it in a signed Envelope and
// returns it. The envelope ID defaults to a content hash so signing is
// deterministic given the same evidence, key and IssuedAt.
func (s *Service) SignEvidence(opts SignOptions) (evidenceenvelope.Envelope, error) {
	if opts.KeyID == "" {
		return evidenceenvelope.Envelope{}, fmt.Errorf("key-id is required")
	}
	if opts.ProducerID == "" {
		return evidenceenvelope.Envelope{}, fmt.Errorf("producer is required")
	}
	key, err := loadPrivateKey(opts.KeyPath)
	if err != nil {
		return evidenceenvelope.Envelope{}, err
	}
	set, err := readEvidenceSet(opts.EvidencePath)
	if err != nil {
		return evidenceenvelope.Envelope{}, err
	}
	id := opts.ID
	if id == "" {
		id = defaultEnvelopeID(evidenceenvelope.APIVersionV1, opts.ProducerID, opts.Sequence, set)
	}
	issued := opts.IssuedAt
	if issued.IsZero() {
		issued = time.Now().UTC()
	}
	env := evidenceenvelope.Envelope{
		APIVersion:  evidenceenvelope.APIVersionV1,
		Kind:        evidenceenvelope.KindEnvelope,
		ID:          id,
		Producer:    evidenceenvelope.Producer{ID: opts.ProducerID, Version: opts.ProducerVersion, KeyID: opts.KeyID},
		Sequence:    opts.Sequence,
		IssuedAt:    issued,
		EvidenceSet: set,
	}
	if opts.TTL > 0 {
		env.ExpiresAt = issued.Add(opts.TTL)
	}
	return evidenceenvelope.Sign(env, key)
}

// readEvidenceSet strictly decodes and structurally validates an EvidenceSet.
func readEvidenceSet(path string) (evidence.EvidenceSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return evidence.EvidenceSet{}, err
	}
	var set evidence.EvidenceSet
	if err := strictjson.Unmarshal(data, &set); err != nil {
		return evidence.EvidenceSet{}, fmt.Errorf("decode evidence set: %w", err)
	}
	if errs := evidence.ValidateEvidenceSet(set); len(errs) > 0 {
		return evidence.EvidenceSet{}, fmt.Errorf("invalid evidence set: %w", errors.Join(errs...))
	}
	return set, nil
}

// contentID hashes the canonical EvidenceSet — the evidence-digest component of
// an envelope id.
func contentID(set evidence.EvidenceSet) string {
	// ponytail: set is already validated and its payloads are closed structs, so
	// json.Marshal cannot fail here — dropping the error keeps this branch-free.
	raw, _ := json.Marshal(set)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// defaultEnvelopeID derives a producer-safe default envelope id when the caller
// supplies none. It folds the API version, producer id and sequence in ALONGSIDE
// the evidence digest, so two DIFFERENT producers reporting an identical
// EvidenceSet never collide in the store's global envelope-id namespace (the
// second would otherwise be dropped as a replay). A producer re-signing the same
// (producer, sequence, evidence) still gets a stable, idempotent id. Fields are
// length-prefixed so no two field boundaries can be confused.
func defaultEnvelopeID(apiVersion, producerID string, sequence uint64, set evidence.EvidenceSet) string {
	h := sha256.New()
	writeLenPrefixed(h, []byte(apiVersion))
	writeLenPrefixed(h, []byte(producerID))
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], sequence)
	h.Write(seq[:])
	writeLenPrefixed(h, []byte(contentID(set)))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func writeLenPrefixed(h hash.Hash, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	h.Write(n[:])
	h.Write(b)
}

// VerifyOptions configures VerifyEnvelope.
type VerifyOptions struct {
	EnvelopePath string
	TrustPath    string // a public-key file or a directory of <keyId>.pub files
}

// VerifyResult reports the verification outcome. OK is false with a Reason when
// the envelope decoded but failed signature, freshness or trust checks.
type VerifyResult struct {
	OK       bool   `json:"ok"`
	ID       string `json:"id"`
	Producer string `json:"producer"`
	KeyID    string `json:"keyId"`
	Reason   string `json:"reason,omitempty"`
}

// VerifyEnvelope loads a trust store, decodes an envelope and verifies it. A
// non-nil error signals an operational failure (unreadable input, bad trust
// store, undecodable envelope). A decoded-but-invalid envelope returns OK=false
// with a Reason and a nil error.
func (s *Service) VerifyEnvelope(opts VerifyOptions) (VerifyResult, error) {
	ts, err := loadTrustStore(opts.TrustPath)
	if err != nil {
		return VerifyResult{}, err
	}
	data, err := os.ReadFile(opts.EnvelopePath)
	if err != nil {
		return VerifyResult{}, err
	}
	env, err := evidenceenvelope.Decode(data)
	if err != nil {
		return VerifyResult{}, err
	}
	res := VerifyResult{ID: env.ID, Producer: env.Producer.ID, KeyID: env.Producer.KeyID}
	if err := evidenceenvelope.Verify(env, ts, time.Now()); err != nil {
		res.Reason = err.Error()
		return res, nil
	}
	res.OK = true
	return res, nil
}

// loadTrustStore builds a key-id -> trust-entry map from a directory of *.pub
// files or a single public-key file. Each key is bound to exactly one producer by
// the filename: "<producerID>__<keyId>.pub" binds explicitly, and a bare
// "<keyId>.pub" binds the producer to the key id — so a trusted key can never
// sign as another producer.
func loadTrustStore(path string) (evidenceenvelope.MapTrustStore, error) {
	// A YAML file is the structured, versioned trust config (can bind per-key
	// subject/contract-repo allowlists). A directory or a single .pub file is the
	// legacy bare-key mode, which binds only the producer and CANNOT express
	// scopes — see loadStructuredTrustStore.
	if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".yaml" || ext == ".yml" {
			return loadStructuredTrustStore(path)
		}
	}
	ts := evidenceenvelope.MapTrustStore{}
	entries, err := os.ReadDir(path)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
				continue
			}
			if err := addTrustKey(ts, filepath.Join(path, e.Name())); err != nil {
				return nil, err
			}
		}
	} else if err := addTrustKey(ts, path); err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, fmt.Errorf("no public keys found in trust store %q", path)
	}
	return ts, nil
}

// addTrustKey loads one public key and registers it under its key id, bound to
// the producer encoded in the filename ("<producerID>__<keyId>.pub", or a bare
// "<keyId>.pub" binding the producer to the key id).
func addTrustKey(ts evidenceenvelope.MapTrustStore, path string) error {
	pub, err := loadPublicKey(path)
	if err != nil {
		return err
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	producerID, keyID := base, base
	if p, k, ok := strings.Cut(base, "__"); ok {
		producerID, keyID = p, k
	}
	// A duplicate key id would silently let one trust entry overwrite another
	// (e.g. two files binding the same key id to different producers) — a
	// trust-store integrity hole. Fail loudly instead.
	if _, exists := ts[keyID]; exists {
		return fmt.Errorf("duplicate key id %q in trust store (each key id must appear once)", keyID)
	}
	ts[keyID] = evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: producerID}
	return nil
}

// loadPrivateKey reads a base64 32-byte Ed25519 seed and expands it.
func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := readBase64(path)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.SeedSize {
		return nil, fmt.Errorf("invalid private key %q: expected %d-byte seed, got %d", path, ed25519.SeedSize, len(raw))
	}
	return ed25519.NewKeyFromSeed(raw), nil
}

// loadPublicKey reads a base64 32-byte Ed25519 public key.
func loadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := readBase64(path)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key %q: expected %d bytes, got %d", path, ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// readBase64 reads a file and base64-decodes its trimmed contents.
func readBase64(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("decode key %q: %w", path, err)
	}
	return raw, nil
}

// contractResolver adapts the Service's OCI bundle resolution to the ingest
// layer's ContractResolver seam, so accepted evidence is evaluated against the
// same published contract revisions the rest of the CLI resolves.
//
// OCI ONLY, and structurally so. The reference is named by a REMOTE producer,
// and the ingest policy already refuses anything but an immutable oci:// digest
// before resolution is reached. But a policy is a guard in front of a branch
// that still exists: resolving through [Service.resolveBundle] would leave a
// local-filesystem path one policy bug away from a remote party. Going straight
// to the OCI path means an ingest host has no local branch to reach at all — it
// never stats, reads or walks a directory somebody else chose.
type contractResolver struct{ svc *Service }

// Resolve implements evidenceingest.ContractResolver.
func (r contractResolver) Resolve(ctx context.Context, ref string) (contract.Contract, error) {
	parsed := graph.ParseDependencyRef(ref)
	if !parsed.IsOCI() {
		return contract.Contract{}, fmt.Errorf("evidence contract ref %q must be an oci:// reference", ref)
	}
	b, err := r.svc.resolveOCIBundle(ctx, parsed.Location)
	if err != nil {
		return contract.Contract{}, err
	}
	return *b.Contract, nil
}

// EvidenceResolver returns a ContractResolver backed by this Service, resolving
// an evidence set's ContractRef to its parsed contract.
func (s *Service) EvidenceResolver() evidenceingest.ContractResolver {
	return contractResolver{svc: s}
}

// ServeOptions configures the evidence ingestion host.
type ServeOptions struct {
	Port      int      // listen port for ServeEvidence (0 = OS-assigned)
	TrustPath string   // a public-key file or a directory of <keyId>.pub files
	Subjects  []string // exact oci://…@sha256:… contract revisions evidence is stored on
	Producers []string // trusted producer ids advertised on GET /producers
}

// readinessTimeout bounds ONE readiness preflight end to end: every configured
// subject's resolve and Referrers walk, together. It sits inside the readiness
// probe's five-second period so probes cannot pile up, and it is the backstop for
// any caller — a healthcheck, a curl — that sets no deadline of its own. A var so
// a test can shorten it.
var readinessTimeout = 3 * time.Second

// buildEvidenceHost assembles the ingestion HTTP mux over the contract registry:
// it loads the trust store, parses the configured contract subjects and opens one
// repository per subject. Nothing is stored locally, so there is no recovery to
// wait for — the host is ready as soon as every subject resolves and answers
// native Referrers discovery, which /ready checks live. Assembly errors (an
// unreadable trust store, an empty or malformed subject list) surface here so
// serve validates configuration before it listens; a registry that is merely
// unreachable surfaces via /ready, not as a startup failure. It takes no context:
// every registry call it wires up belongs to the request that triggers it.
func (s *Service) buildEvidenceHost(opts ServeOptions) (*http.ServeMux, error) {
	trust, err := loadTrustStore(opts.TrustPath)
	if err != nil {
		return nil, err
	}
	subjects, err := evidenceoci.ParseSubjects(opts.Subjects)
	if err != nil {
		return nil, err
	}
	// The same keychain and plain-HTTP policy `pacto pull` uses: evidence is
	// written to the contract registry, so it authenticates like a contract does.
	store, err := evidenceoci.NewStore(subjects, evidenceoci.RepositoryOptions{
		Keychain: oci.NewKeychain(oci.EnvCredentialOptions()),
		Insecure: oci.EnvInsecureRegistries(),
	})
	if err != nil {
		return nil, err
	}
	acceptor := evidenceingest.NewAcceptor(trust, s.EvidenceResolver(), store, nil)
	handler := evidenceingest.NewHandler(acceptor, opts.Producers, nil, func(ctx context.Context) bool {
		// Parented on the asking REQUEST, never on the server's lifetime: the
		// preflight must die when the caller hangs up, and die on its own budget
		// when the registry accepts the connection and then answers nothing. One
		// deadline covers every subject's resolve and Referrers walk together.
		ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
		defer cancel()
		return store.Ready(ctx) == nil
	})
	mux := http.NewServeMux()
	handler.Routes(mux)
	return mux, nil
}

// ServeEvidence assembles the ingestion host, listens on opts.Port and serves
// until ctx is cancelled. It is the port-based convenience over
// ServeEvidenceOnListener.
func (s *Service) ServeEvidence(ctx context.Context, opts ServeOptions) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
	if err != nil {
		return err
	}
	return s.ServeEvidenceOnListener(ctx, ln, opts)
}

// ServeEvidenceOnListener serves the ingestion host on an existing listener until
// ctx is cancelled, then shuts down gracefully. It takes ownership of ln, closing
// it if assembly fails. This is the seam tests drive on a random port.
func (s *Service) ServeEvidenceOnListener(ctx context.Context, ln net.Listener, opts ServeOptions) error {
	mux, err := s.buildEvidenceHost(opts)
	if err != nil {
		_ = ln.Close()
		return err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// SendResult is the response from posting an envelope to an ingestion host.
type SendResult struct {
	StatusCode int
	Body       string
}

// SendEvidence posts an envelope file to an ingestion host URL and returns the
// response. A non-2xx status is reported in the result (the caller decides the
// exit code); only IO and transport failures return an error.
func (s *Service) SendEvidence(ctx context.Context, url, envelopePath string) (SendResult, error) {
	data, err := os.ReadFile(envelopePath)
	if err != nil {
		return SendResult{}, err
	}
	url = normalizeIngestURL(url)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return SendResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{StatusCode: resp.StatusCode, Body: string(body)}, nil
}

// normalizeIngestURL lets a caller pass either a base host URL (the documented
// form, e.g. https://ingest.example.com) or a full endpoint URL. A bare host or
// trailing "/" gets the standard envelope endpoint path appended; a URL that
// already carries a path is used verbatim so existing full-path callers still work.
func normalizeIngestURL(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil || u.Path == "" || u.Path == "/" {
		return strings.TrimRight(raw, "/") + evidenceingest.EnvelopesPath
	}
	return raw
}
