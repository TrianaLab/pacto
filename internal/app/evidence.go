package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ed25519"
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
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/internal/evidenceoci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/graph"
	"github.com/trianalab/pacto/v4/pkg/logging"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/strictjson"
)

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
	pub, priv, err := ed25519.GenerateKey(s.env().rand)
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
	if err := s.env().writeFile(privPath, []byte(seed+"\n"), 0o600); err != nil {
		return KeyPair{}, err
	}
	if err := s.env().writeFile(pubPath, []byte(pubB64+"\n"), 0o644); err != nil {
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
	// Stdin is read when EvidencePath is "-".
	Stdin io.Reader
}

// SignEvidence reads an EvidenceSet JSON file, wraps it in a signed Envelope and
// returns it. The envelope ID defaults to a content hash so signing is
// deterministic given the same evidence, key and IssuedAt.
func (s *Service) SignEvidence(opts SignOptions) (evidenceenvelope.Envelope, error) {
	return s.sign(opts, func() (evidence.EvidenceSet, error) { return readEvidenceSet(opts.EvidencePath, opts.Stdin) })
}

// SignEvidenceSet is SignEvidence for a set a producer holds in memory (opts.EvidencePath is unused): `pacto
// agent --sink evidence` signs one per target and interval. The set is validated like a file's.
func (s *Service) SignEvidenceSet(set evidence.EvidenceSet, opts SignOptions) (evidenceenvelope.Envelope, error) {
	if errs := evidence.ValidateEvidenceSet(set); len(errs) > 0 {
		return evidenceenvelope.Envelope{}, fmt.Errorf("invalid evidence set: %w", errors.Join(errs...))
	}
	return s.sign(opts, func() (evidence.EvidenceSet, error) { return set, nil })
}

// sign wraps the set load returns in a signed Envelope.
func (s *Service) sign(opts SignOptions, load func() (evidence.EvidenceSet, error)) (evidenceenvelope.Envelope, error) {
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
	set, err := load()
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

// readEvidenceSet strictly decodes and structurally validates an EvidenceSet,
// read from stdin when path is "-".
func readEvidenceSet(path string, stdin io.Reader) (evidence.EvidenceSet, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
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

// ProvisionedOptions configures ProvisionedEvidence. Exactly one of
// Configuration or Dependency names the consumer entry that asked for the
// resource.
type ProvisionedOptions struct {
	ContractRef   string // the consumer revision: oci://<repo>@sha256:<digest>
	Subject       string // optional; defaults to <repository>/<environment or default>/<kind>/<name>
	Configuration string
	Dependency    string
	Environment   string
	Provider      string
	ExternalRef   string
	Phase         string
	Outcome       string    // optional; defaults to Observed
	ObservedAt    time.Time // optional; defaults to now
	Collector     string    // optional; defaults to "pacto"
}

// ProvisionedEvidence builds the EvidenceSet a platform adapter reports: the
// provisioning state of the resource a consumer's configuration reference or
// dependency asked for, about the consumer's immutable contract revision. The
// subject sits under the consumer's repository path, one resource per subject
// (the kind keeps a dependency and a configuration of one name apart), so two
// resources never replace each other in the latest-per-target store.
func (s *Service) ProvisionedEvidence(opts ProvisionedOptions) (evidence.EvidenceSet, error) {
	use, set, err := s.resourceReport(opts.ContractRef, opts.Configuration, opts.Dependency, opts.Subject, "", opts.Environment, opts.Collector, opts.ObservedAt)
	if err != nil {
		return evidence.EvidenceSet{}, err
	}
	return finishResourceReport(set, evidence.ResourceProvisioned, use, opts.Environment, opts.Outcome, func(prov evidence.Provenance) (evidence.Observation, error) {
		return evidence.NewResourceProvisioned(use, evidence.ProvisionedObservation{Provider: opts.Provider,
			ExternalRef: opts.ExternalRef, Phase: evidence.ProvisionPhase(opts.Phase), Environment: opts.Environment}, prov), nil
	})
}

// UsageOptions configures UsageEvidence. Exactly one of Configuration or
// Dependency names the consumer entry that asked for the resource; an Observed
// report carries at least one metric or a cost.
type UsageOptions struct {
	ContractRef   string // the consumer revision: oci://<repo>@sha256:<digest>
	Subject       string // optional; defaults to <repository>/<environment or default>/usage/<kind>/<name>
	Configuration string
	Dependency    string
	Environment   string
	Provider      string
	ExternalRef   string
	Metrics       []evidence.UsageMetric
	Cost          *evidence.UsageCost
	Outcome       string    // optional; defaults to Observed
	ObservedAt    time.Time // optional; defaults to now
	Collector     string    // optional; defaults to "pacto"
}

// UsageEvidence builds the EvidenceSet a platform adapter reports for the usage
// and cost of the resource a consumer's configuration reference or dependency
// asked for. Its subject holds a usage segment, so a usage report never
// replaces the same resource's provisioning report in the latest-per-target
// store.
func (s *Service) UsageEvidence(opts UsageOptions) (evidence.EvidenceSet, error) {
	use, set, err := s.resourceReport(opts.ContractRef, opts.Configuration, opts.Dependency, opts.Subject, "usage/", opts.Environment, opts.Collector, opts.ObservedAt)
	if err != nil {
		return evidence.EvidenceSet{}, err
	}
	return finishResourceReport(set, evidence.ResourceUsage, use, opts.Environment, opts.Outcome, func(prov evidence.Provenance) (evidence.Observation, error) {
		p := evidence.UsageObservation{Provider: opts.Provider, ExternalRef: opts.ExternalRef, Environment: opts.Environment, Metrics: opts.Metrics, Cost: opts.Cost}
		// Validated before it is marshalled: a non-finite value cannot be.
		if err := evidence.ValidateUsage(p); err != nil {
			return evidence.Observation{}, fmt.Errorf("invalid usage report: %w", err)
		}
		return evidence.NewResourceUsage(use, p, prov), nil
	})
}

// resourceReport checks the contract checks a provisioning and a usage report
// share and returns the consumer entry they are about plus the set without its
// observation. The default subject is <repository>/<environment or
// default>/<segment><kind>/<name>.
func (s *Service) resourceReport(ref, configuration, dependency, subject, segment, environment, collector string, observedAt time.Time) (evidence.SubjectRef, evidence.EvidenceSet, error) {
	if (configuration == "") == (dependency == "") {
		return evidence.SubjectRef{}, evidence.EvidenceSet{}, errors.New("set exactly one of configuration or dependency")
	}
	if err := evidenceingest.CheckContractRef(ref); err != nil {
		return evidence.SubjectRef{}, evidence.EvidenceSet{}, fmt.Errorf("contract %q must be an immutable oci://<repo>@sha256:<digest> reference: %w", ref, err)
	}
	use := evidence.SubjectRef{Kind: "configuration", Name: configuration}
	if dependency != "" {
		use = evidence.SubjectRef{Kind: "dependency", Name: dependency}
	}
	repo, _, _ := strings.Cut(contract.TrimOCIScheme(ref), "@")
	if observedAt.IsZero() {
		observedAt = s.env().now().UTC()
	}
	collector = cmp.Or(collector, "pacto")
	return use, evidence.EvidenceSet{
		Subject:     evidence.SubjectRef{Kind: "resource", Name: cmp.Or(subject, repo+"/"+cmp.Or(environment, "default")+"/"+segment+use.Kind+"/"+use.Name)},
		ContractRef: ref,
		Source:      collector,
		ObservedAt:  observedAt,
	}, nil
}

// finishResourceReport adds the one observation of kind to set: the observed
// one for an Observed outcome, a payload-less one otherwise (anything short of
// Observed carries no phase or figures: Unknown is never reported as a state),
// then checks the subject and the set.
func finishResourceReport(set evidence.EvidenceSet, kind evidence.ObservationKind, use evidence.SubjectRef, environment, outcome string,
	observed func(evidence.Provenance) (evidence.Observation, error)) (evidence.EvidenceSet, error) {
	prov := evidence.Provenance{Collector: set.Source, DetectedAt: set.ObservedAt}
	var obs evidence.Observation
	var err error
	if o := evidence.Outcome(cmp.Or(outcome, string(evidence.Observed))); o == evidence.Observed {
		obs, err = observed(prov)
	} else {
		// The environment rides on the subject, so the report keeps its environment's row.
		use.Environment = environment
		obs, err = evidence.NewUnobserved(kind, use, o, prov)
	}
	if err != nil {
		return evidence.EvidenceSet{}, err
	}
	set.Observations = []evidence.Observation{obs}
	if err := evidenceingest.CheckReportSubject(set); err != nil {
		return evidence.EvidenceSet{}, err
	}
	if errs := evidence.ValidateEvidenceSet(set); len(errs) > 0 {
		return evidence.EvidenceSet{}, fmt.Errorf("invalid %s report: %w", kind, errors.Join(errs...))
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

// ServeOptions configures the evidence ingestion host. It carries no address:
// where to bind is the CLI's decision, made once in internal/cli/evidence.go
// from --listen-address and --port, and handed over as a ready listener.
type ServeOptions struct {
	TrustPath string   // a public-key file or a directory of <keyId>.pub files
	Subjects  []string // exact oci://…@sha256:… contract revisions evidence is stored on
	Producers []string // trusted producer ids advertised on GET /producers
	// Cluster is the cluster this server runs in (the operator sets it), stamped
	// on every record it accepts; nil when unknown.
	Cluster *evidenceingest.ClusterDTO
}

// checkServeCluster refuses a cluster identity the fleet would drop, so a
// misconfigured server fails at start instead of reporting clusterless records.
func checkServeCluster(c *evidenceingest.ClusterDTO) error {
	switch {
	case c == nil:
		return nil
	case !fleet.ValidClusterID(c.ID):
		return fmt.Errorf("invalid cluster id %q: want letters, digits, '.', '_' or '-', at most 253 characters, starting with a letter or digit", c.ID)
	case (c.Name != "" || c.NameSource != "") && !fleet.ValidClusterName(c.Name, c.NameSource):
		return fmt.Errorf("invalid cluster name %q from %q: want at most 63 printable bytes and a name source of node-label, context, file or uid", c.Name, c.NameSource)
	}
	return nil
}

// defaultReadinessTimeout bounds ONE readiness preflight end to end: every configured
// subject's resolve and Referrers walk, together. It sits inside the readiness
// probe's five-second period so probes cannot pile up, and it is the backstop for
// any caller — a healthcheck, a curl — that sets no deadline of its own. A var so
// Service can shorten it.
const defaultReadinessTimeout = 3 * time.Second

// evidenceStoreOptions is how the Evidence Server reaches the contract registry:
// the same keychain and plain-HTTP policy `pacto pull` uses, since evidence is
// written to the contract registry and authenticates like a contract does. The
// cloud workload-identity sources are skipped: the server writes referrers onto
// subjects anywhere, so it carries no host-wide cloud identity. Its push scope
// is referrer-only (decision 11): it never tags, deletes or writes a
// subject-less manifest.
func evidenceStoreOptions() evidenceoci.RepositoryOptions {
	creds := oci.EnvCredentialOptions()
	creds.SkipCloud = true
	return evidenceoci.RepositoryOptions{
		Keychain: oci.NewKeychain(creds),
		Insecure: oci.EnvInsecureRegistries(),
		Scope:    oci.NewPushScope(""),
	}
}

// buildEvidenceHost assembles the ingestion HTTP mux over the contract registry:
// it loads the trust store, parses the configured contract subjects and opens one
// repository per subject. Nothing is stored locally, so there is no recovery to
// wait for — the host is ready as soon as every subject resolves and answers
// native Referrers discovery, which /ready checks live. Assembly errors (an
// unreadable trust store, an empty or malformed subject list) surface here so
// serve validates configuration before it listens; a registry that is merely
// unreachable surfaces via /ready, not as a startup failure. It takes no context:
// every registry call it wires up belongs to the request that triggers it, so
// it returns the ids of keys that may report provisioning for every provider
// for the caller to warn about.
func (s *Service) buildEvidenceHost(opts ServeOptions) (*http.ServeMux, []string, error) {
	if err := checkServeCluster(opts.Cluster); err != nil {
		return nil, nil, err
	}
	trust, err := loadTrustStore(opts.TrustPath)
	if err != nil {
		return nil, nil, err
	}
	subjects, err := evidenceoci.ParseSubjects(opts.Subjects)
	if err != nil {
		return nil, nil, err
	}
	store, err := evidenceoci.NewStore(subjects, evidenceStoreOptions())
	if err != nil {
		return nil, nil, err
	}
	if opts.Cluster != nil {
		store.WithCluster(opts.Cluster.ID)
	}
	acceptor := evidenceingest.NewAcceptor(trust, s.EvidenceResolver(), store, nil)
	timeout := s.env().readinessTimeout
	handler := evidenceingest.NewHandler(acceptor, opts.Producers, func(ctx context.Context) bool {
		// Parented on the asking REQUEST, never on the server's lifetime: the
		// preflight must die when the caller hangs up, and die on its own budget
		// when the registry accepts the connection and then answers nothing. One
		// deadline covers every subject's resolve and Referrers walk together.
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return store.Ready(ctx) == nil
	}).WithCluster(opts.Cluster).WithAdapters(evidenceingest.ReportingAdapters(trust)).
		WithContributors(evidenceingest.ContributorsOf(trust))
	mux := http.NewServeMux()
	handler.Routes(mux)
	return mux, unscopedProvisioningKeys(trust), nil
}

// unscopedProvisioningKeys returns, sorted, the ids of keys granted a
// provider-scoped kind (ResourceProvisioned or ResourceUsage) with no provider
// scope: each may report provisioning or usage for every provider.
func unscopedProvisioningKeys(ts evidenceenvelope.MapTrustStore) []string {
	var ids []string
	for id, e := range ts {
		if slices.ContainsFunc(evidenceingest.ProviderScopedKinds(), e.KindAllowed) && len(e.ProviderRepos) == 0 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// ServeEvidenceOnListener serves the ingestion host on an existing listener until
// ctx is cancelled, then shuts down gracefully. It takes ownership of ln, closing
// it if assembly fails. This is the seam tests drive on a random port.
func (s *Service) ServeEvidenceOnListener(ctx context.Context, ln net.Listener, opts ServeOptions) error {
	mux, unscoped, err := s.buildEvidenceHost(opts)
	if err != nil {
		_ = ln.Close()
		return err
	}
	log := logging.LoggerFromContext(ctx)
	for _, id := range unscoped {
		log.Warn("trust key grants ResourceProvisioned or ResourceUsage for every provider", "keyId", id, "hint", "set allowedProviderRepos when more than one adapter reports")
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
	return s.postEnvelope(ctx, url, data)
}

// PostEnvelope is SendEvidence for an envelope held in memory.
func (s *Service) PostEnvelope(ctx context.Context, url string, env evidenceenvelope.Envelope) (SendResult, error) {
	data, err := json.Marshal(env)
	if err != nil {
		return SendResult{}, err
	}
	return s.postEnvelope(ctx, url, data)
}

// evidenceClient posts envelopes. A timeout, because http.DefaultClient has
// none and an ingestion host that accepts the connection and never answers
// would otherwise hang the sender (and the agent's emit loop) forever.
var evidenceClient = &http.Client{Timeout: 30 * time.Second}

// maxSendResponse bounds how much of the host's answer is read back: it is
// shown to a human or logged, never parsed, so a megabyte is plenty and a
// hostile host cannot exhaust the sender's memory.
const maxSendResponse = 1 << 20

// postEnvelope posts an encoded envelope to an ingestion host.
func (s *Service) postEnvelope(ctx context.Context, url string, data []byte) (SendResult, error) {
	url = normalizeIngestURL(url)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.env().httpClient.Do(req)
	if err != nil {
		return SendResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSendResponse))
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
