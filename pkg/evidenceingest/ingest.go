// Package evidenceingest accepts signed external [evidenceenvelope.Envelope]s,
// verifies them, evaluates the carried EvidenceSet against the resolved contract
// revision, commits the result to a durable store, and exposes it as a
// [fleet.Source]. It is the platform side of outbound-only evidence reporting: a
// remote or disconnected environment produces and signs an EvidenceSet and
// reports it here; the operational graph then shows that environment as a target
// with real findings, freshness and provenance — and shows it going stale when
// reporting stops, never deleting it.
//
// The package is transport-light: the accept pipeline is pure over interfaces (a
// trust store, a contract resolver and a commit-based [Store]) and unit-testable
// with fakes; a thin net/http handler wraps it for the ingestion host. Replay
// protection lives inside [Store.Commit] — atomic with the durable write — so a
// failed commit never leaves a phantom reservation. The package stays
// infrastructure-free (no gocloud); the durable store is wired in by the host.
package evidenceingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/logging"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

// ErrReplay is returned when an envelope was already accepted (by id) or is out
// of sequence for its producer.
var ErrReplay = errors.New("evidence ingest: duplicate or out-of-sequence envelope")

// ErrInvalidEvidence is returned when the carried EvidenceSet is invalid.
var ErrInvalidEvidence = errors.New("evidence ingest: evidence set is invalid")

// ErrContractRefPolicy is returned when an externally-reported contract reference
// violates the ingestion policy: a remote producer must reference an IMMUTABLE
// oci:// digest, and (when its trust entry lists allowed repos) one within that
// allowlist. This stops a remote producer from making the ingestion host read an
// arbitrary local path, pin evaluation to a mutable tag, or reference an
// unapproved registry.
var ErrContractRefPolicy = errors.New("evidence ingest: contract reference violates ingestion policy")

// Category sentinels wrap the raw, potentially-sensitive errors from decoding,
// contract resolution and the durable store, so the HTTP layer can return a
// STABLE code + a generic sanitized message while the detailed error is logged
// server-side only. errors.Is still sees the underlying sentinel, so specific
// classifications (auth, replay) are matched before these broad wrappers.
var (
	ErrMalformedEnvelope  = errors.New("evidence ingest: malformed envelope")
	ErrContractResolution = errors.New("evidence ingest: contract resolution failed")
	ErrStoreWrite         = errors.New("evidence ingest: durable store write failed")
)

// validateContractRef enforces the externally-reported contract-ref policy: an
// immutable oci:// digest reference (a full, well-formed sha256 digest — not
// merely a string that contains "@sha256:"), optionally within an allowlisted
// repository matched on path-segment boundaries so "ghcr.io/acme" never
// authorizes "ghcr.io/acme-attacker".
func validateContractRef(ref string, allowedRepos []string) error {
	if !strings.HasPrefix(ref, "oci://") {
		return ErrContractRefPolicy // no local paths, no bare refs
	}
	body := strings.TrimPrefix(ref, "oci://")
	repo, digest, ok := strings.Cut(body, "@")
	if !ok || !validSHA256Digest(digest) {
		return ErrContractRefPolicy // require a complete immutable digest, not a mutable tag
	}
	if repo == "" {
		return ErrContractRefPolicy
	}
	if len(allowedRepos) == 0 {
		return nil
	}
	for _, prefix := range allowedRepos {
		if repo == prefix || strings.HasPrefix(repo, prefix+"/") {
			return nil
		}
	}
	return ErrContractRefPolicy
}

// ociDomain returns the domain (registry + org — everything before the final
// path segment, the service name) of an oci digest ref, matching the domain a
// contract's OCI source is keyed under. Empty for a ref with no org segment.
func ociDomain(ref string) string {
	s := strings.TrimPrefix(ref, "oci://")
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[:i]
	}
	return ""
}

// ociDigest returns the "sha256:…" digest of an oci ref (empty when absent).
func ociDigest(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}

// validSHA256Digest reports whether d is a complete "sha256:<64 lowercase hex>"
// digest — the full immutable shape, so a truncated or malformed digest is
// rejected rather than accepted because it merely contains the "@sha256:" marker.
func validSHA256Digest(d string) bool {
	const prefix = "sha256:"
	hex, ok := strings.CutPrefix(d, prefix)
	if !ok || len(hex) != 64 {
		return false
	}
	for i := 0; i < len(hex); i++ {
		c := hex[i]
		isDigit := c >= '0' && c <= '9'
		isHexLetter := c >= 'a' && c <= 'f'
		if !isDigit && !isHexLetter {
			return false
		}
	}
	return true
}

// Record is one accepted envelope and the evaluation it produced. The RESOLVED
// identity (Service/Domain/Digest) is captured at accept time from the resolved
// contract and its immutable ContractRef — NOT inferred from the envelope's
// Subject. Subject is the operational-target identity; the logical service, its
// domain and the revision digest come from what the ContractRef actually resolved
// to, so an externally-ingested target links to the correct domain-qualified
// service and revision even when two services share a name across OCI domains.
type Record struct {
	Envelope   evidenceenvelope.Envelope `json:"envelope"`
	Compliance string                    `json:"compliance"`
	Findings   []finding.Finding         `json:"findings,omitempty"`
	Coverage   validation.Coverage       `json:"coverage"`
	AcceptedAt time.Time                 `json:"acceptedAt"`
	// Resolved logical identity (from the contract the ContractRef resolved to).
	Service string `json:"service"`
	Domain  string `json:"domain,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// Store durably persists accepted records. Commit is the acceptance authority:
// it performs the immutable write and replay protection atomically, so a failed
// commit never reserves an id or sequence (no phantom acceptance). List returns
// the latest record per target for the fleet projection.
type Store interface {
	// Commit atomically persists rec and enforces replay protection. It returns
	// [ErrReplay] when the envelope id was already committed or the producer
	// sequence is not strictly newer than its highest committed sequence.
	Commit(ctx context.Context, rec Record) error
	// List returns the latest record per target, in deterministic order.
	List(ctx context.Context) ([]Record, error)
}

// ContractResolver resolves an immutable contract ref to its contract, so the
// carried evidence can be evaluated against declared intent.
type ContractResolver interface {
	Resolve(ctx context.Context, ref string) (contract.Contract, error)
}

// Acceptor is the pure accept pipeline.
type Acceptor struct {
	trust    evidenceenvelope.TrustStore
	resolver ContractResolver
	store    Store
	now      func() time.Time
}

// NewAcceptor wires the accept pipeline. now defaults to time.Now.
func NewAcceptor(trust evidenceenvelope.TrustStore, resolver ContractResolver, store Store, now func() time.Time) *Acceptor {
	if now == nil {
		now = time.Now
	}
	return &Acceptor{trust: trust, resolver: resolver, store: store, now: now}
}

// Accept decodes, verifies, evaluates and commits an envelope, returning the
// resulting record. Replay protection is enforced inside store.Commit (atomic
// with the durable write), so it is the last step and never leaves a phantom
// reservation on failure. Every error is safe to surface (no secrets).
func (a *Acceptor) Accept(ctx context.Context, data []byte) (Record, error) {
	env, err := evidenceenvelope.Decode(data)
	if err != nil {
		return Record{}, fmt.Errorf("%w: %w", ErrMalformedEnvelope, err)
	}
	if err := evidenceenvelope.Verify(env, a.trust, a.now()); err != nil {
		return Record{}, err
	}
	if errs := evidence.ValidateEvidenceSet(env.EvidenceSet); len(errs) > 0 {
		return Record{}, ErrInvalidEvidence
	}
	// Enforce the contract-ref policy BEFORE resolving: a remote producer must not
	// be able to drive the host to read a local path or a mutable/unapproved ref.
	// The producer was authenticated by Verify, so its trust entry (with any repo
	// allowlist) is present.
	entry, _ := a.trust.Entry(env.Producer.KeyID)
	if err := validateContractRef(env.EvidenceSet.ContractRef, entry.ContractRepos); err != nil {
		return Record{}, err
	}
	c, err := a.resolver.Resolve(ctx, env.EvidenceSet.ContractRef)
	if err != nil {
		return Record{}, fmt.Errorf("%w: %w", ErrContractResolution, err)
	}
	findings, coverage := validation.Evaluate(c, env.EvidenceSet)
	rec := Record{
		Envelope:   env,
		Compliance: deriveCompliance(findings, coverage),
		Findings:   findings,
		Coverage:   coverage,
		AcceptedAt: a.now(),
		// The logical service is what the ContractRef resolved to, never the
		// envelope Subject; the domain and digest come from the immutable ref.
		Service: c.Service.Name,
		Domain:  ociDomain(env.EvidenceSet.ContractRef),
		Digest:  ociDigest(env.EvidenceSet.ContractRef),
	}
	if err := a.store.Commit(ctx, rec); err != nil {
		// Replay is a defined, safe-to-surface outcome; any other commit failure is
		// an internal store error whose detail must not reach the client.
		if errors.Is(err, ErrReplay) {
			return Record{}, err
		}
		return Record{}, fmt.Errorf("%w: %w", ErrStoreWrite, err)
	}
	return rec, nil
}

// List returns the latest committed record per target, for the read-only source
// projection exposed over HTTP.
func (a *Acceptor) List(ctx context.Context) ([]Record, error) {
	return a.store.List(ctx)
}

// targetKey identifies the operational target an envelope reports on: the
// producer environment plus the subject service.
func targetKey(env evidenceenvelope.Envelope) string {
	return string(fleet.NewTargetKey(env.Producer.ID, "external", env.EvidenceSet.Subject.Name))
}

// deriveCompliance maps findings and coverage onto the canonical status: a
// confirmed error is NonCompliant; incomplete coverage is Unknown (uncertainty,
// not a violation); otherwise Compliant.
func deriveCompliance(findings []finding.Finding, cov validation.Coverage) string {
	for _, f := range findings {
		if f.Severity == finding.SeverityError {
			return fleet.StatusNonCompliant
		}
	}
	if cov.Required > 0 && cov.Evaluated < cov.Required {
		return fleet.StatusUnknown
	}
	for _, f := range findings {
		if f.Severity == finding.SeverityWarning {
			return fleet.StatusWarning
		}
	}
	return fleet.StatusCompliant
}

// Source exposes accepted evidence as fleet targets.
type Source struct {
	id    string
	store Store
}

// NewSource returns a fleet source over an accepted-evidence store.
func NewSource(id string, store Store) *Source {
	if id == "" {
		id = "evidence-ingest"
	}
	return &Source{id: id, store: store}
}

// ID implements [fleet.Source].
func (s *Source) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *Source) Kind() string { return "evidence-ingest" }

// Collect implements [fleet.Source], projecting each stored record into a target.
func (s *Source) Collect(ctx context.Context) (*fleet.Collection, error) {
	recs, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	col := &fleet.Collection{}
	for _, rec := range recs {
		env := rec.Envelope
		at := env.EvidenceSet.ObservedAt
		col.Targets = append(col.Targets, fleet.RawTarget{
			Scope: env.Producer.ID,
			Kind:  "external",
			// Name is the operational target (the envelope Subject); Service/Domain/
			// Digest are the RESOLVED logical identity, so the target links to the
			// correct domain-qualified service and revision.
			Name:         env.EvidenceSet.Subject.Name,
			Service:      rec.Service,
			Domain:       rec.Domain,
			Digest:       rec.Digest,
			ResolvedRef:  env.EvidenceSet.ContractRef,
			Compliance:   rec.Compliance,
			Findings:     rec.Findings,
			Coverage:     &fleet.Coverage{Evaluated: rec.Coverage.Evaluated, Required: rec.Coverage.Required},
			EvidenceAt:   &at,
			ReconciledAt: &rec.AcceptedAt,
		})
	}
	return col, nil
}

// MemoryStore is an in-memory [Store]: it enforces replay protection (duplicate
// id and per-producer non-increasing sequence) atomically inside Commit, then
// keeps the latest record per target key. It is the substrate for tests and any
// caller that does not need durability across restarts.
type MemoryStore struct {
	mu      sync.Mutex
	seenID  map[string]bool
	maxSeq  map[string]uint64
	haveSeq map[string]bool
	recs    map[string]Record
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{seenID: map[string]bool{}, maxSeq: map[string]uint64{}, haveSeq: map[string]bool{}, recs: map[string]Record{}}
}

// Commit implements [Store]: a repeated id, or a producer sequence not strictly
// greater than its last committed one, is rejected with [ErrReplay]; otherwise
// the record becomes the latest for its target.
func (m *MemoryStore) Commit(_ context.Context, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	env := rec.Envelope
	if m.seenID[env.ID] {
		return ErrReplay
	}
	if m.haveSeq[env.Producer.ID] && env.Sequence <= m.maxSeq[env.Producer.ID] {
		return ErrReplay
	}
	m.seenID[env.ID] = true
	m.maxSeq[env.Producer.ID] = env.Sequence
	m.haveSeq[env.Producer.ID] = true
	m.recs[targetKey(env)] = rec
	return nil
}

// List implements [Store] in deterministic target-key order.
func (m *MemoryStore) List(_ context.Context) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.recs))
	for k := range m.recs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Record, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.recs[k])
	}
	return out, nil
}

// maxSourceTargets bounds the number of targets GET /targets returns so a large
// store cannot produce an unbounded response. It is a var so tests can lower it.
var maxSourceTargets = 1000

// Handler mounts the ingestion HTTP API on a mux: POST accepts an envelope, and
// GET endpoints report health, readiness, the trusted producer ids and the
// latest accepted targets. TLS termination is the host's responsibility;
// signature verification here is mandatory.
type Handler struct {
	acceptor  *Acceptor
	producers []string
	onAccept  func()
	// ready reports whether the backing store has completed recovery. When nil
	// the host is treated as always ready (e.g. an in-memory store).
	ready func() bool
	// health reports the store's diagnostic health for the /targets DTO, so a
	// degraded store is surfaced instead of masquerading as healthy. When nil the
	// DTO reports a ready, uncorrupted store (the in-memory case).
	health func() SourceHealth
}

// NewHandler returns an HTTP handler for the accept pipeline. producers is the
// list of trusted producer ids to advertise; onAccept (optional) is invoked after
// a successful accept; ready (optional) gates the /ready probe; health (optional)
// feeds the /targets DTO's store-health block.
func NewHandler(acceptor *Acceptor, producers []string, onAccept func(), ready func() bool, health func() SourceHealth) *Handler {
	sorted := append([]string(nil), producers...)
	sort.Strings(sorted)
	return &Handler{acceptor: acceptor, producers: sorted, onAccept: onAccept, ready: ready, health: health}
}

// EnvelopesPath is the ingestion endpoint a producer POSTs a signed envelope to.
// Exported so a client can target it against a base host URL.
const EnvelopesPath = "/api/evidence/v1/envelopes"

// Routes registers the ingestion endpoints on mux under /api/evidence/v1.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+EnvelopesPath, h.handleEnvelope)
	mux.HandleFunc("GET /api/evidence/v1/health", h.handleHealth)
	mux.HandleFunc("GET /api/evidence/v1/ready", h.handleReady)
	mux.HandleFunc("GET /api/evidence/v1/producers", h.handleProducers)
	mux.HandleFunc("GET /api/evidence/v1/targets", h.handleTargets)
}

func (h *Handler) handleEnvelope(w http.ResponseWriter, r *http.Request) {
	// Refuse ingestion until the store has recovered: accepting a write against a
	// not-yet-recovered replay index could re-accept a replayed envelope.
	if h.ready != nil && !h.ready() {
		writeErr(w, http.StatusServiceUnavailable, "store_not_ready", "the evidence store is not ready")
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, evidenceenvelope.MaxEnvelopeBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_envelope", "the request body could not be read")
		return
	}
	rec, err := h.acceptor.Accept(r.Context(), data)
	if err != nil {
		status, code, msg := classifyAcceptError(err)
		// The DETAILED error is logged server-side only; the client gets a stable
		// code and a generic, secret-free message.
		logging.LoggerFromContext(r.Context()).Warn("evidence accept rejected",
			"code", code, "status", status, "error", err.Error())
		writeErr(w, status, code, msg)
		return
	}
	if h.onAccept != nil {
		h.onAccept()
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id": rec.Envelope.ID, "compliance": rec.Compliance,
		"findings": len(rec.Findings), "acceptedAt": rec.AcceptedAt,
	})
}

// handleHealth is the always-200 liveness probe (the process is up).
func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady is the readiness probe: 503 until the backing store has recovered
// (ready or degraded), so callers do not route writes to a not-yet-recovered
// host. A degraded store still serves, so it counts as ready here.
func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	if h.ready != nil && !h.ready() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) handleProducers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]string{"producers": h.producers})
}

// TargetsSchemaVersion identifies the read-only evidence-source DTO wire model.
const TargetsSchemaVersion = "pacto.dev/evidence-source/v1"

// maxFindingsPerTarget bounds the findings projected per target so one pathological
// target cannot blow the response body even under the target-count bound.
var maxFindingsPerTarget = 500

// SourceHealth is the store-health block carried by the /targets DTO so a consumer
// can tell a degraded or corrupt store from a healthy one.
type SourceHealth struct {
	Phase         string `json:"phase"`
	PendingRepair bool   `json:"pendingRepair"`
	Corruptions   int    `json:"corruptions"`
}

// TargetDTO is a faithful, bounded projection of one accepted target — enough to
// reconstruct a fleet target with findings, contract linkage, freshness and
// provenance, not a lossy summary.
type TargetDTO struct {
	// Subject is the operational-target identity (the envelope subject). Service/
	// Domain/Digest are the RESOLVED logical identity — from the contract the
	// ContractRef resolved to — so a consumer links the target to the correct
	// domain-qualified service and revision, never inferring Service from Subject.
	Subject       string              `json:"subject"`
	Service       string              `json:"service"`
	Domain        string              `json:"domain,omitempty"`
	Digest        string              `json:"digest,omitempty"`
	Producer      string              `json:"producer"`
	ProducerKeyID string              `json:"producerKeyId,omitempty"`
	Compliance    string              `json:"compliance"`
	Coverage      validation.Coverage `json:"coverage"`
	Findings      []finding.Finding   `json:"findings,omitempty"`
	ContractRef   string              `json:"contractRef,omitempty"`
	EvidenceAt    time.Time           `json:"evidenceAt"`
	AcceptedAt    time.Time           `json:"acceptedAt"`
}

// TargetsResponse is the versioned read-only evidence-source contribution.
type TargetsResponse struct {
	SchemaVersion string       `json:"schemaVersion"`
	GeneratedAt   time.Time    `json:"generatedAt"`
	Health        SourceHealth `json:"health"`
	Truncated     bool         `json:"truncated"`
	Targets       []TargetDTO  `json:"targets"`
}

// handleTargets serves the versioned, bounded, read-only evidence-source DTO. It
// carries full (bounded) findings, contract linkage and store health, so a
// consumer builds a faithful target and can tell a degraded store from a healthy
// one; truncation is signaled explicitly rather than silently dropping targets.
func (h *Handler) handleTargets(w http.ResponseWriter, r *http.Request) {
	recs, err := h.acceptor.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "could not list targets")
		return
	}
	truncated := false
	if len(recs) > maxSourceTargets {
		recs, truncated = recs[:maxSourceTargets], true
	}
	out := make([]TargetDTO, 0, len(recs))
	for _, rec := range recs {
		es := rec.Envelope.EvidenceSet
		findings := rec.Findings
		if len(findings) > maxFindingsPerTarget {
			findings, truncated = findings[:maxFindingsPerTarget], true
		}
		out = append(out, TargetDTO{
			Subject:       es.Subject.Name,
			Service:       rec.Service,
			Domain:        rec.Domain,
			Digest:        rec.Digest,
			Producer:      rec.Envelope.Producer.ID,
			ProducerKeyID: rec.Envelope.Producer.KeyID,
			Compliance:    rec.Compliance,
			Coverage:      rec.Coverage,
			Findings:      findings,
			ContractRef:   es.ContractRef,
			EvidenceAt:    es.ObservedAt,
			AcceptedAt:    rec.AcceptedAt,
		})
	}
	health := SourceHealth{Phase: "ready"}
	if h.health != nil {
		health = h.health()
	}
	writeJSON(w, http.StatusOK, TargetsResponse{
		SchemaVersion: TargetsSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Health:        health,
		Truncated:     truncated,
		Targets:       out,
	})
}

// classifyAcceptError maps an accept error to (HTTP status, stable code, generic
// sanitized message). Specific sentinels are checked before the broad category
// wrappers (ErrMalformedEnvelope/ErrContractResolution/ErrStoreWrite) so an
// auth or replay error inside a wrapped decode/commit is still classified
// precisely. The message NEVER contains the underlying error text.
func classifyAcceptError(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrReplay):
		return http.StatusConflict, "replay", "the report was already accepted or is out of sequence"
	case errors.Is(err, evidenceenvelope.ErrExpired), errors.Is(err, evidenceenvelope.ErrNotYetValid),
		errors.Is(err, evidenceenvelope.ErrBadSignature), errors.Is(err, evidenceenvelope.ErrUnknownKey),
		errors.Is(err, evidenceenvelope.ErrUnsignedEnvelope), errors.Is(err, evidenceenvelope.ErrBadAlgorithm),
		errors.Is(err, evidenceenvelope.ErrProducerMismatch), errors.Is(err, evidenceenvelope.ErrSubjectNotAllowed):
		return http.StatusUnauthorized, "unauthorized_producer", "the envelope could not be authenticated or is not authorized for this producer or subject"
	case errors.Is(err, ErrContractRefPolicy):
		return http.StatusUnprocessableEntity, "contract_ref_rejected", "the contract reference is not an approved immutable digest reference"
	case errors.Is(err, ErrInvalidEvidence):
		return http.StatusUnprocessableEntity, "invalid_evidence", "the evidence set is invalid"
	case errors.Is(err, ErrMalformedEnvelope):
		return http.StatusBadRequest, "invalid_envelope", "the envelope could not be decoded"
	case errors.Is(err, ErrContractResolution):
		return http.StatusBadGateway, "contract_resolution_failed", "the referenced contract could not be resolved"
	case errors.Is(err, ErrStoreWrite):
		return http.StatusServiceUnavailable, "store_degraded", "the evidence store could not durably commit the record"
	default:
		return http.StatusInternalServerError, "internal_error", "the request could not be processed"
	}
}

// writeErr writes a stable, sanitized error response: {code, message}.
func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
