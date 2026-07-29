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
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

// ErrReplay is returned when an envelope was already accepted (by id) or is out
// of sequence for its producer.
var ErrReplay = errors.New("evidence ingest: duplicate or out-of-sequence envelope")

// ErrInvalidEvidence is returned when the carried EvidenceSet is invalid.
var ErrInvalidEvidence = errors.New("evidence ingest: evidence set is invalid")

// Record is one accepted envelope and the evaluation it produced.
type Record struct {
	Envelope   evidenceenvelope.Envelope `json:"envelope"`
	Compliance string                    `json:"compliance"`
	Findings   []finding.Finding         `json:"findings,omitempty"`
	Coverage   validation.Coverage       `json:"coverage"`
	AcceptedAt time.Time                 `json:"acceptedAt"`
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
		return Record{}, err
	}
	if err := evidenceenvelope.Verify(env, a.trust, a.now()); err != nil {
		return Record{}, err
	}
	if errs := evidence.ValidateEvidenceSet(env.EvidenceSet); len(errs) > 0 {
		return Record{}, ErrInvalidEvidence
	}
	c, err := a.resolver.Resolve(ctx, env.EvidenceSet.ContractRef)
	if err != nil {
		return Record{}, err
	}
	findings, coverage := validation.Evaluate(c, env.EvidenceSet)
	rec := Record{
		Envelope:   env,
		Compliance: deriveCompliance(findings, coverage),
		Findings:   findings,
		Coverage:   coverage,
		AcceptedAt: a.now(),
	}
	if err := a.store.Commit(ctx, rec); err != nil {
		return Record{}, err
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
			Scope:        env.Producer.ID,
			Kind:         "external",
			Name:         env.EvidenceSet.Subject.Name,
			Service:      env.EvidenceSet.Subject.Name,
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
}

// NewHandler returns an HTTP handler for the accept pipeline. producers is the
// list of trusted producer ids to advertise; onAccept (optional) is invoked
// after a successful accept (e.g. to trigger a snapshot refresh); ready
// (optional) gates the /ready probe.
func NewHandler(acceptor *Acceptor, producers []string, onAccept func(), ready func() bool) *Handler {
	sorted := append([]string(nil), producers...)
	sort.Strings(sorted)
	return &Handler{acceptor: acceptor, producers: sorted, onAccept: onAccept, ready: ready}
}

// Routes registers the ingestion endpoints on mux under /api/evidence/v1.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/evidence/v1/envelopes", h.handleEnvelope)
	mux.HandleFunc("GET /api/evidence/v1/health", h.handleHealth)
	mux.HandleFunc("GET /api/evidence/v1/ready", h.handleReady)
	mux.HandleFunc("GET /api/evidence/v1/producers", h.handleProducers)
	mux.HandleFunc("GET /api/evidence/v1/targets", h.handleTargets)
}

func (h *Handler) handleEnvelope(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, evidenceenvelope.MaxEnvelopeBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read request body"})
		return
	}
	rec, err := h.acceptor.Accept(r.Context(), data)
	if err != nil {
		writeJSON(w, statusForError(err), map[string]string{"error": err.Error()})
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

// targetView is the small, stable projection of an accepted target for the
// read-only HTTP evidence source.
type targetView struct {
	Subject       string              `json:"subject"`
	Producer      string              `json:"producer"`
	Compliance    string              `json:"compliance"`
	FindingsCount int                 `json:"findingsCount"`
	Coverage      validation.Coverage `json:"coverage"`
	ObservedAt    time.Time           `json:"observedAt"`
}

// handleTargets projects the latest accepted records into a bounded, read-only
// list an HTTP evidence source can consume.
func (h *Handler) handleTargets(w http.ResponseWriter, r *http.Request) {
	recs, err := h.acceptor.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list targets"})
		return
	}
	if len(recs) > maxSourceTargets {
		recs = recs[:maxSourceTargets]
	}
	out := make([]targetView, 0, len(recs))
	for _, rec := range recs {
		es := rec.Envelope.EvidenceSet
		out = append(out, targetView{
			Subject:       es.Subject.Name,
			Producer:      rec.Envelope.Producer.ID,
			Compliance:    rec.Compliance,
			FindingsCount: len(rec.Findings),
			Coverage:      rec.Coverage,
			ObservedAt:    es.ObservedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": out})
}

// statusForError maps an accept error to an HTTP status without leaking secrets.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ErrReplay):
		return http.StatusConflict
	case errors.Is(err, evidenceenvelope.ErrExpired), errors.Is(err, evidenceenvelope.ErrNotYetValid),
		errors.Is(err, evidenceenvelope.ErrBadSignature), errors.Is(err, evidenceenvelope.ErrUnknownKey),
		errors.Is(err, evidenceenvelope.ErrUnsignedEnvelope), errors.Is(err, evidenceenvelope.ErrBadAlgorithm):
		return http.StatusUnauthorized
	default:
		return http.StatusUnprocessableEntity
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
