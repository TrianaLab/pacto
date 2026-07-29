// Package evidenceingest accepts signed external [evidenceenvelope.Envelope]s,
// verifies and de-duplicates them, evaluates the carried EvidenceSet against the
// resolved contract revision, stores the result, and exposes it as a
// [fleet.Source]. It is the platform side of outbound-only evidence reporting: a
// remote or disconnected environment produces and signs an EvidenceSet and
// reports it here; the operational graph then shows that environment as a target
// with real findings, freshness and provenance — and shows it going stale when
// reporting stops, never deleting it.
//
// The package is transport-light: the accept pipeline is pure over interfaces (a
// trust store, a contract resolver, a store, a replay guard) and unit-testable
// with fakes; a thin net/http handler wraps it for the ingestion host.
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

// Store persists accepted records keyed by target identity (last write per
// target wins, so a target's latest evidence is what the graph shows).
type Store interface {
	Put(ctx context.Context, key string, rec Record) error
	List(ctx context.Context) ([]Record, error)
}

// ContractResolver resolves an immutable contract ref to its contract, so the
// carried evidence can be evaluated against declared intent.
type ContractResolver interface {
	Resolve(ctx context.Context, ref string) (contract.Contract, error)
}

// ReplayGuard rejects duplicate or out-of-sequence envelopes.
type ReplayGuard interface {
	Admit(producerID string, sequence uint64, id string) bool
}

// Acceptor is the pure accept pipeline.
type Acceptor struct {
	trust    evidenceenvelope.TrustStore
	resolver ContractResolver
	store    Store
	replay   ReplayGuard
	now      func() time.Time
}

// NewAcceptor wires the accept pipeline. now defaults to time.Now.
func NewAcceptor(trust evidenceenvelope.TrustStore, resolver ContractResolver, store Store, replay ReplayGuard, now func() time.Time) *Acceptor {
	if now == nil {
		now = time.Now
	}
	if replay == nil {
		replay = NewMemoryReplayGuard()
	}
	return &Acceptor{trust: trust, resolver: resolver, store: store, replay: replay, now: now}
}

// Accept decodes, verifies, de-duplicates, evaluates and stores an envelope,
// returning the resulting record. Every error is safe to surface (no secrets).
func (a *Acceptor) Accept(ctx context.Context, data []byte) (Record, error) {
	env, err := evidenceenvelope.Decode(data)
	if err != nil {
		return Record{}, err
	}
	if err := evidenceenvelope.Verify(env, a.trust, a.now()); err != nil {
		return Record{}, err
	}
	if !a.replay.Admit(env.Producer.ID, env.Sequence, env.ID) {
		return Record{}, ErrReplay
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
	if err := a.store.Put(ctx, targetKey(env), rec); err != nil {
		return Record{}, err
	}
	return rec, nil
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

// MemoryStore is an in-memory [Store] (last record per target key).
type MemoryStore struct {
	mu   sync.RWMutex
	recs map[string]Record
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{recs: map[string]Record{}} }

// Put implements [Store].
func (m *MemoryStore) Put(_ context.Context, key string, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[key] = rec
	return nil
}

// List implements [Store] in deterministic key order.
func (m *MemoryStore) List(_ context.Context) ([]Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
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

// MemoryReplayGuard rejects duplicate ids and non-increasing sequences per
// producer.
type MemoryReplayGuard struct {
	mu      sync.Mutex
	seenID  map[string]bool
	maxSeq  map[string]uint64
	haveSeq map[string]bool
}

// NewMemoryReplayGuard returns an empty replay guard.
func NewMemoryReplayGuard() *MemoryReplayGuard {
	return &MemoryReplayGuard{seenID: map[string]bool{}, maxSeq: map[string]uint64{}, haveSeq: map[string]bool{}}
}

// Admit returns true if the envelope is new and in order, recording it. A
// repeated id, or a sequence not greater than the producer's last, is rejected.
func (g *MemoryReplayGuard) Admit(producerID string, sequence uint64, id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seenID[id] {
		return false
	}
	if g.haveSeq[producerID] && sequence <= g.maxSeq[producerID] {
		return false
	}
	g.seenID[id] = true
	g.maxSeq[producerID] = sequence
	g.haveSeq[producerID] = true
	return true
}

// Handler mounts the ingestion HTTP API on a mux: POST accepts an envelope, and
// GET endpoints report health and the trusted producer ids. TLS termination is
// the host's responsibility; signature verification here is mandatory.
type Handler struct {
	acceptor  *Acceptor
	producers []string
	onAccept  func()
}

// NewHandler returns an HTTP handler for the accept pipeline. producers is the
// list of trusted producer ids to advertise; onAccept (optional) is invoked
// after a successful accept (e.g. to trigger a snapshot refresh).
func NewHandler(acceptor *Acceptor, producers []string, onAccept func()) *Handler {
	sorted := append([]string(nil), producers...)
	sort.Strings(sorted)
	return &Handler{acceptor: acceptor, producers: sorted, onAccept: onAccept}
}

// Routes registers the ingestion endpoints on mux under /api/evidence/v1.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/evidence/v1/envelopes", h.handleEnvelope)
	mux.HandleFunc("GET /api/evidence/v1/health", h.handleHealth)
	mux.HandleFunc("GET /api/evidence/v1/producers", h.handleProducers)
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

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleProducers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]string{"producers": h.producers})
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
