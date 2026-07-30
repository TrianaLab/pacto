package evidenceingest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

const keyID = "env-a-2026"

func testKeypair() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}

func fixedNow() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }

func testEvidenceSet() evidence.EvidenceSet {
	return evidence.EvidenceSet{
		Subject:     evidence.SubjectRef{Kind: "service", Name: "payments"},
		ContractRef: "oci://ghcr.io/acme/payments@sha256:abc",
		Source:      "remote",
		ObservedAt:  fixedNow().Add(-time.Minute),
		Observations: []evidence.Observation{
			evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "payments"}, "service",
				evidence.Provenance{Collector: "remote", DetectedAt: fixedNow().Add(-time.Minute)}),
		},
	}
}

func signedEnvelopeBytes(t *testing.T, priv ed25519.PrivateKey, seq uint64, id string, es evidence.EvidenceSet) []byte {
	t.Helper()
	env := evidenceenvelope.Envelope{
		ID:          id,
		Producer:    evidenceenvelope.Producer{ID: "env-a", KeyID: keyID},
		Sequence:    seq,
		IssuedAt:    fixedNow().Add(-time.Hour),
		ExpiresAt:   fixedNow().Add(time.Hour),
		EvidenceSet: es,
	}
	signed, err := evidenceenvelope.Sign(env, priv)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type fakeResolver struct {
	c   contract.Contract
	err error
}

func (f fakeResolver) Resolve(context.Context, string) (contract.Contract, error) {
	return f.c, f.err
}

func newTestAcceptor(t *testing.T, resolver ContractResolver, store Store) (*Acceptor, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := testKeypair()
	trust := evidenceenvelope.MapTrustStore{keyID: pub}
	return NewAcceptor(trust, resolver, store, fixedNow), priv
}

func TestAcceptor_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	a, priv := newTestAcceptor(t, fakeResolver{c: contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments"}}}, store)
	rec, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()))
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if rec.Envelope.ID != "e1" || rec.Compliance == "" {
		t.Errorf("unexpected record: %+v", rec)
	}
	got, _ := a.List(context.Background())
	if len(got) != 1 {
		t.Fatalf("store should hold 1 record, got %d", len(got))
	}
}

func TestAcceptor_ReplayAndSequence(t *testing.T) {
	a, priv := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 5, "e1", testEvidenceSet())); err != nil {
		t.Fatal(err)
	}
	// Same id → replay (caught atomically inside Commit).
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 6, "e1", testEvidenceSet())); !errors.Is(err, ErrReplay) {
		t.Errorf("duplicate id should be ErrReplay, got %v", err)
	}
	// Lower sequence, new id → out of order.
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 4, "e2", testEvidenceSet())); !errors.Is(err, ErrReplay) {
		t.Errorf("out-of-sequence should be ErrReplay, got %v", err)
	}
}

func TestAcceptor_VerifyFailure(t *testing.T) {
	// Sign with a key not in the trust store.
	_, otherPriv := testKeypair2()
	pub, _ := testKeypair()
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: pub}, fakeResolver{}, NewMemoryStore(), fixedNow)
	data := signedEnvelopeBytes(t, otherPriv, 1, "e1", testEvidenceSet())
	if _, err := a.Accept(context.Background(), data); !errors.Is(err, evidenceenvelope.ErrBadSignature) {
		t.Errorf("wrong key should fail verification, got %v", err)
	}
}

func TestAcceptor_InvalidEvidence(t *testing.T) {
	a, priv := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	bad := testEvidenceSet()
	bad.Subject = evidence.SubjectRef{} // invalid: empty subject
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", bad)); !errors.Is(err, ErrInvalidEvidence) {
		t.Errorf("invalid evidence should be ErrInvalidEvidence, got %v", err)
	}
}

func TestAcceptor_ResolveError(t *testing.T) {
	a, priv := newTestAcceptor(t, fakeResolver{err: errors.New("not found")}, NewMemoryStore())
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet())); err == nil {
		t.Error("resolver error should propagate")
	}
}

func TestAcceptor_DecodeError(t *testing.T) {
	a, _ := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	if _, err := a.Accept(context.Background(), []byte("{not json")); err == nil {
		t.Error("bad payload should error")
	}
}

func TestDeriveCompliance(t *testing.T) {
	errF := finding.Finding{Severity: finding.SeverityError}
	warnF := finding.Finding{Severity: finding.SeverityWarning}
	if got := deriveCompliance([]finding.Finding{errF}, validation.Coverage{}); got != "NonCompliant" {
		t.Errorf("error → NonCompliant, got %q", got)
	}
	if got := deriveCompliance(nil, validation.Coverage{Evaluated: 1, Required: 3}); got != "Unknown" {
		t.Errorf("incomplete coverage → Unknown, got %q", got)
	}
	if got := deriveCompliance([]finding.Finding{warnF}, validation.Coverage{Evaluated: 3, Required: 3}); got != "Warning" {
		t.Errorf("warning → Warning, got %q", got)
	}
	if got := deriveCompliance(nil, validation.Coverage{Evaluated: 2, Required: 2}); got != "Compliant" {
		t.Errorf("clean → Compliant, got %q", got)
	}
}

func TestMemoryStore_Commit(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	rec := func(id string, seq uint64, producer, subject string) Record {
		return Record{Envelope: evidenceenvelope.Envelope{
			ID: id, Sequence: seq, Producer: evidenceenvelope.Producer{ID: producer},
			EvidenceSet: evidence.EvidenceSet{Subject: evidence.SubjectRef{Kind: "service", Name: subject}},
		}}
	}
	if err := m.Commit(ctx, rec("a", 1, "p", "s1")); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := m.Commit(ctx, rec("a", 2, "p", "s1")); !errors.Is(err, ErrReplay) {
		t.Errorf("duplicate id: got %v", err)
	}
	if err := m.Commit(ctx, rec("b", 1, "p", "s1")); !errors.Is(err, ErrReplay) {
		t.Errorf("non-increasing sequence: got %v", err)
	}
	if err := m.Commit(ctx, rec("c", 2, "p", "s2")); err != nil {
		t.Errorf("higher sequence, new id: %v", err)
	}
	if err := m.Commit(ctx, rec("d", 1, "q", "s3")); err != nil {
		t.Errorf("a different producer starts fresh: %v", err)
	}
	got, err := m.List(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("list: %d recs err=%v", len(got), err)
	}
}

func TestSource_Collect(t *testing.T) {
	store := NewMemoryStore()
	a, priv := newTestAcceptor(t, fakeResolver{}, store)
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet())); err != nil {
		t.Fatal(err)
	}
	src := NewSource("", store)
	if src.ID() != "evidence-ingest" || src.Kind() != "evidence-ingest" {
		t.Errorf("source id/kind: %s/%s", src.ID(), src.Kind())
	}
	col, err := src.Collect(context.Background())
	if err != nil || len(col.Targets) != 1 {
		t.Fatalf("collect: %d targets err=%v", len(col.Targets), err)
	}
	tg := col.Targets[0]
	if tg.Service != "payments" || tg.Scope != "env-a" || tg.Kind != "external" || tg.ResolvedRef == "" || tg.EvidenceAt == nil {
		t.Errorf("unexpected target: %+v", tg)
	}
}

func TestHandler_HTTP(t *testing.T) {
	store := NewMemoryStore()
	a, priv := newTestAcceptor(t, fakeResolver{}, store)
	var refreshed int
	h := NewHandler(a, []string{"env-a", "env-b"}, func() { refreshed++ }, nil)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Health + producers + readiness (nil ready → always 200).
	expectGET(t, srv.URL+"/api/evidence/v1/health", http.StatusOK)
	expectGET(t, srv.URL+"/api/evidence/v1/producers", http.StatusOK)
	expectGET(t, srv.URL+"/api/evidence/v1/ready", http.StatusOK)

	// Valid envelope → 202 + refresh hook fired.
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()), http.StatusAccepted)
	if refreshed != 1 {
		t.Errorf("onAccept should fire once, got %d", refreshed)
	}
	// Replay → 409.
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()), http.StatusConflict)
	// Bad signature (unknown key) → 401.
	_, other := testKeypair2()
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, other, 2, "e2", testEvidenceSet()), http.StatusUnauthorized)
	// Invalid evidence → 422.
	bad := testEvidenceSet()
	bad.Subject = evidence.SubjectRef{}
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, priv, 3, "e3", bad), http.StatusUnprocessableEntity)
}

func TestHandler_Ready(t *testing.T) {
	a, priv := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	ready := false
	h := NewHandler(a, nil, nil, func() bool { return ready })
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// While not ready: readiness AND ingestion are refused, but liveness (health)
	// is unaffected — the whole point of the independent probes.
	expectGET(t, srv.URL+"/api/evidence/v1/health", http.StatusOK)
	expectGET(t, srv.URL+"/api/evidence/v1/ready", http.StatusServiceUnavailable)
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()), http.StatusServiceUnavailable)

	// Once ready: readiness and ingestion both succeed.
	ready = true
	expectGET(t, srv.URL+"/api/evidence/v1/ready", http.StatusOK)
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()), http.StatusAccepted)
}

func TestHandler_Targets(t *testing.T) {
	store := NewMemoryStore()
	a, priv := newTestAcceptor(t, fakeResolver{}, store)
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet())); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(a, nil, nil, nil)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var body struct {
		Targets []targetView `json:"targets"`
	}
	getJSON(t, srv.URL+"/api/evidence/v1/targets", &body)
	if len(body.Targets) != 1 || body.Targets[0].Subject != "payments" || body.Targets[0].Producer != "env-a" {
		t.Fatalf("unexpected targets: %+v", body.Targets)
	}

	// The bound truncates the returned list.
	orig := maxSourceTargets
	maxSourceTargets = 0
	defer func() { maxSourceTargets = orig }()
	getJSON(t, srv.URL+"/api/evidence/v1/targets", &body)
	if len(body.Targets) != 0 {
		t.Errorf("bound should truncate, got %d", len(body.Targets))
	}
}

func TestHandler_TargetsError(t *testing.T) {
	a := NewAcceptor(nil, fakeResolver{}, failStore{err: errors.New("read error")}, fixedNow)
	h := NewHandler(a, nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/evidence/v1/targets", nil)
	w := httptest.NewRecorder()
	h.handleTargets(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("list error should be 500, got %d", w.Code)
	}
}

// --- small helpers ---

func testKeypair2() (ed25519.PublicKey, ed25519.PrivateKey) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 100)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return priv.Public().(ed25519.PublicKey), priv
}

func expectGET(t *testing.T, url string, want int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != want {
		t.Fatalf("GET %s: got %d want %d", url, resp.StatusCode, want)
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

func post(t *testing.T, url string, body []byte, want int) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != want {
		t.Fatalf("POST %s: got %d want %d", url, resp.StatusCode, want)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

func TestHandler_BodyReadError(t *testing.T) {
	a, _ := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	h := NewHandler(a, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/evidence/v1/envelopes", brokenReader{})
	w := httptest.NewRecorder()
	h.handleEnvelope(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("body read error should be 400, got %d", w.Code)
	}
}

// failStore errors on Commit and List to cover the acceptor/source error paths.
type failStore struct{ err error }

func (f failStore) Commit(context.Context, Record) error   { return f.err }
func (f failStore) List(context.Context) ([]Record, error) { return nil, f.err }

func TestAcceptor_StoreError(t *testing.T) {
	pub, priv := testKeypair()
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: pub}, fakeResolver{}, failStore{err: errors.New("disk full")}, fixedNow)
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet())); err == nil {
		t.Error("store Commit error should propagate")
	}
}

func TestSource_CollectError(t *testing.T) {
	src := NewSource("s", failStore{err: errors.New("read error")})
	if _, err := src.Collect(context.Background()); err == nil {
		t.Error("store List error should propagate")
	}
}

// TestNewAcceptor_DefaultClock covers the now==nil default-clock branch of
// NewAcceptor by accepting an envelope valid under the real wall clock.
func TestNewAcceptor_DefaultClock(t *testing.T) {
	pub, priv := testKeypair()
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: pub}, fakeResolver{}, NewMemoryStore(), nil)
	env := evidenceenvelope.Envelope{
		ID: "now1", Producer: evidenceenvelope.Producer{ID: "env-a", KeyID: keyID}, Sequence: 1,
		IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
		EvidenceSet: testEvidenceSet(),
	}
	signed, err := evidenceenvelope.Sign(env, priv)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Accept(context.Background(), data); err != nil {
		t.Fatalf("default-clock accept should succeed: %v", err)
	}
}

func TestHandler_NilOnAccept(t *testing.T) {
	store := NewMemoryStore()
	a, priv := newTestAcceptor(t, fakeResolver{}, store)
	h := NewHandler(a, nil, nil, nil) // nil onAccept must not panic
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()), http.StatusAccepted)
}
