package evidenceingest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
	return NewAcceptor(trust, resolver, store, nil, fixedNow), priv
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
	got, _ := store.List(context.Background())
	if len(got) != 1 {
		t.Fatalf("store should hold 1 record, got %d", len(got))
	}
}

func TestAcceptor_ReplayAndSequence(t *testing.T) {
	a, priv := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 5, "e1", testEvidenceSet())); err != nil {
		t.Fatal(err)
	}
	// Same id → replay.
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
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: pub}, fakeResolver{}, NewMemoryStore(), nil, fixedNow)
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

func TestMemoryReplayGuard(t *testing.T) {
	g := NewMemoryReplayGuard()
	if !g.Admit("p", 1, "a") {
		t.Error("first admit should pass")
	}
	if g.Admit("p", 2, "a") {
		t.Error("duplicate id should be rejected")
	}
	if g.Admit("p", 1, "b") {
		t.Error("non-increasing sequence should be rejected")
	}
	if !g.Admit("p", 2, "b") {
		t.Error("higher sequence, new id should pass")
	}
	if !g.Admit("q", 1, "c") {
		t.Error("a different producer starts fresh")
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

func TestFileStore_RoundTrip(t *testing.T) {
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{Envelope: evidenceenvelope.Envelope{ID: "e1"}, Compliance: "Compliant", AcceptedAt: fixedNow()}
	if err := fs.Put(context.Background(), "prod/external/payments", rec); err != nil {
		t.Fatal(err)
	}
	// Overwrite same key → still one record.
	if err := fs.Put(context.Background(), "prod/external/payments", rec); err != nil {
		t.Fatal(err)
	}
	got, err := fs.List(context.Background())
	if err != nil || len(got) != 1 || got[0].Envelope.ID != "e1" {
		t.Fatalf("filestore list: %d recs err=%v", len(got), err)
	}
}

func TestFileStore_NewError(t *testing.T) {
	// A path under a regular file cannot be created as a dir.
	f, _ := NewFileStore(t.TempDir())
	filePath := f.dir + "/afile"
	if err := writeFileHelper(filePath); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileStore(filePath + "/sub"); err == nil {
		t.Error("NewFileStore under a file should error")
	}
}

func TestHandler_HTTP(t *testing.T) {
	store := NewMemoryStore()
	a, priv := newTestAcceptor(t, fakeResolver{}, store)
	var refreshed int
	h := NewHandler(a, []string{"env-a", "env-b"}, func() { refreshed++ })
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Health + producers.
	expectGET(t, srv.URL+"/api/evidence/v1/health", http.StatusOK)
	expectGET(t, srv.URL+"/api/evidence/v1/producers", http.StatusOK)

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

func writeFileHelper(path string) error {
	return os.WriteFile(path, []byte("x"), 0o600)
}

// TestFileStore_SeamErrors exercises the atomic-write and read error paths via
// the injectable filesystem seams (defer restores the originals).
func TestFileStore_SeamErrors(t *testing.T) {
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")

	origMarshal := fsMarshal
	fsMarshal = func(any) ([]byte, error) { return nil, boom }
	if err := fs.Put(context.Background(), "k", Record{}); err == nil {
		t.Error("marshal error should propagate")
	}
	fsMarshal = origMarshal

	origWrite := fsWriteFile
	fsWriteFile = func(string, []byte, os.FileMode) error { return boom }
	if err := fs.Put(context.Background(), "k", Record{}); err == nil {
		t.Error("write error should propagate")
	}
	fsWriteFile = origWrite

	origRename := fsRename
	fsRename = func(string, string) error { return boom }
	if err := fs.Put(context.Background(), "k", Record{}); err == nil {
		t.Error("rename error should propagate")
	}
	fsRename = origRename

	// A readable record then a forced read error on List.
	if err := fs.Put(context.Background(), "k", Record{Envelope: evidenceenvelope.Envelope{ID: "e"}}); err != nil {
		t.Fatal(err)
	}
	origRead := fsReadFile
	fsReadFile = func(string) ([]byte, error) { return nil, boom }
	if _, err := fs.List(context.Background()); err == nil {
		t.Error("read error should propagate")
	}
	fsReadFile = origRead
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failure") }

func TestHandler_BodyReadError(t *testing.T) {
	a, _ := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	h := NewHandler(a, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/evidence/v1/envelopes", brokenReader{})
	w := httptest.NewRecorder()
	h.handleEnvelope(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("body read error should be 400, got %d", w.Code)
	}
}

// failStore errors on Put and List to cover the acceptor/source error paths.
type failStore struct{ err error }

func (f failStore) Put(context.Context, string, Record) error { return f.err }
func (f failStore) List(context.Context) ([]Record, error)    { return nil, f.err }

func TestAcceptor_StoreError(t *testing.T) {
	a, priv := newTestAcceptorStore(t, fakeResolver{}, failStore{err: errors.New("disk full")})
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet())); err == nil {
		t.Error("store Put error should propagate")
	}
}

func TestSource_CollectError(t *testing.T) {
	src := NewSource("s", failStore{err: errors.New("read error")})
	if _, err := src.Collect(context.Background()); err == nil {
		t.Error("store List error should propagate")
	}
}

func newTestAcceptorStore(t *testing.T, resolver ContractResolver, store Store) (*Acceptor, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := testKeypair()
	return NewAcceptor(evidenceenvelope.MapTrustStore{keyID: pub}, resolver, store, NewMemoryReplayGuard(), fixedNow), priv
}

// TestNewAcceptor_DefaultClock covers the now==nil default-clock branch of
// NewAcceptor by accepting an envelope valid under the real wall clock.
func TestNewAcceptor_DefaultClock(t *testing.T) {
	pub, priv := testKeypair()
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: pub}, fakeResolver{}, NewMemoryStore(), NewMemoryReplayGuard(), nil)
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
	h := NewHandler(a, nil, nil) // nil onAccept must not panic
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	post(t, srv.URL+"/api/evidence/v1/envelopes", signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()), http.StatusAccepted)
}

func TestFileStore_ListErrors(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A non-json file is skipped; a subdirectory is skipped.
	if err := os.WriteFile(dir+"/note.txt", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir+"/sub", 0o755); err != nil {
		t.Fatal(err)
	}
	if recs, err := fs.List(context.Background()); err != nil || len(recs) != 0 {
		t.Fatalf("non-json/dir entries should be skipped: %d %v", len(recs), err)
	}
	// A malformed .json file is a read error.
	if err := os.WriteFile(dir+"/bad.json", []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.List(context.Background()); err == nil {
		t.Error("malformed json should error")
	}
}

func TestFileStore_ListMissingDir(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.List(context.Background()); err == nil {
		t.Error("missing dir should error on List")
	}
}

func TestFileStore_PutError(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Removing the dir makes CreateTemp inside Put fail.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := fs.Put(context.Background(), "k", Record{}); err == nil {
		t.Error("Put into a missing dir should error")
	}
}
