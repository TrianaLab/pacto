package evidenceingest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/evidence"
	"github.com/trianalab/pacto/v3/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v3/pkg/finding"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/validation"
)

const keyID = "env-a-2026"

// testContractRef is a full, well-formed immutable digest ref (the shape the
// ingestion policy now requires — a truncated "@sha256:abc" is rejected).
var testContractRef = "oci://ghcr.io/acme/payments@sha256:" + strings.Repeat("a", 64)

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
		ContractRef: testContractRef,
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
	trust := evidenceenvelope.MapTrustStore{keyID: evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "env-a"}}
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
	got := a.List(context.Background())
	if len(got.Records) != 1 || got.Health.Status != HealthReady {
		t.Fatalf("store should hold 1 ready record, got %+v", got)
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

func TestValidateContractRef(t *testing.T) {
	dig := "@sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name    string
		ref     string
		allowed []string
		wantErr bool
	}{
		{"local path", "/tmp/bundle", nil, true},
		{"bare ref no scheme", "ghcr.io/x" + dig, nil, true},
		{"mutable tag", "oci://ghcr.io/x:1.0", nil, true},
		{"truncated digest (substring only) rejected", "oci://ghcr.io/x@sha256:abc", nil, true},
		{"digest wrong length rejected", "oci://ghcr.io/x@sha256:" + strings.Repeat("a", 63), nil, true},
		{"digest non-hex rejected", "oci://ghcr.io/x@sha256:" + strings.Repeat("g", 64), nil, true},
		{"empty repo rejected", "oci://" + dig, nil, true},
		{"full digest, no allowlist", "oci://ghcr.io/x" + dig, nil, false},
		{"full digest in allowlist", "oci://ghcr.io/acme/x" + dig, []string{"ghcr.io/acme"}, false},
		{"exact repo == allowlist entry", "oci://ghcr.io/acme" + dig, []string{"ghcr.io/acme"}, false},
		{"digest not in allowlist", "oci://other.io/x" + dig, []string{"ghcr.io/acme"}, true},
		// The boundary check: a sibling org sharing a prefix must NOT be authorized.
		{"prefix-sibling is not authorized (boundary)", "oci://ghcr.io/acme-attacker/x" + dig, []string{"ghcr.io/acme"}, true},
	}
	for _, tc := range cases {
		if err := validateContractRef(tc.ref, tc.allowed); (err != nil) != tc.wantErr {
			t.Errorf("%s: validateContractRef(%q) err=%v, wantErr=%v", tc.name, tc.ref, err, tc.wantErr)
		}
	}
}

func TestClassifyAcceptError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"replay", ErrReplay, http.StatusConflict, "replay"},
		{"auth", evidenceenvelope.ErrBadSignature, http.StatusUnauthorized, "unauthorized_producer"},
		{"producer mismatch", evidenceenvelope.ErrProducerMismatch, http.StatusUnauthorized, "unauthorized_producer"},
		{"contract ref", ErrContractRefPolicy, http.StatusUnprocessableEntity, "contract_ref_rejected"},
		{"invalid evidence", ErrInvalidEvidence, http.StatusUnprocessableEntity, "invalid_evidence"},
		{"malformed", fmt.Errorf("%w: %w", ErrMalformedEnvelope, errors.New("bad json")), http.StatusBadRequest, "invalid_envelope"},
		{"resolution", fmt.Errorf("%w: %w", ErrContractResolution, errors.New("registry.internal:5000 down")), http.StatusBadGateway, "contract_resolution_failed"},
		{"store write", fmt.Errorf("%w: %w", ErrStoreWrite, errors.New("registry.internal:5000/secret?token=abc")), http.StatusServiceUnavailable, "store_degraded"},
		// Both are wrapped by ErrStoreWrite at the call site, and both must still
		// classify as the retryable environment problem they are.
		{"registry unavailable", fmt.Errorf("%w: %w", ErrStoreWrite, ErrRegistryUnavailable), http.StatusServiceUnavailable, "registry_unavailable"},
		{"registry incomplete", fmt.Errorf("%w: %w", ErrStoreWrite, ErrRegistryIncomplete), http.StatusServiceUnavailable, "registry_incomplete"},
		{"unknown", errors.New("surprise"), http.StatusInternalServerError, "internal_error"},
		// An auth error wrapped inside a decode wrapper still classifies as auth.
		{"auth wins over wrapper", fmt.Errorf("%w: %w", ErrMalformedEnvelope, evidenceenvelope.ErrUnsignedEnvelope), http.StatusUnauthorized, "unauthorized_producer"},
	}
	for _, tc := range cases {
		status, code, msg := classifyAcceptError(tc.err)
		if status != tc.wantStatus || code != tc.wantCode {
			t.Errorf("%s: got (%d,%q), want (%d,%q)", tc.name, status, code, tc.wantStatus, tc.wantCode)
		}
		// The generic message must NEVER echo the underlying (possibly sensitive) error text.
		if strings.Contains(msg, "token=") || strings.Contains(msg, "registry.internal") || strings.Contains(msg, "bad json") {
			t.Errorf("%s: message leaks underlying error: %q", tc.name, msg)
		}
	}
}

func TestAcceptor_ContractRefPolicy(t *testing.T) {
	a, priv := newTestAcceptor(t, fakeResolver{}, NewMemoryStore())
	es := testEvidenceSet()
	es.ContractRef = "/local/path" // not an immutable oci:// digest -> rejected before resolve
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", es)); !errors.Is(err, ErrContractRefPolicy) {
		t.Errorf("local-path contract ref must be rejected, got %v", err)
	}
}

func TestAcceptor_VerifyFailure(t *testing.T) {
	// Sign with a key not in the trust store.
	_, otherPriv := testKeypair2()
	pub, _ := testKeypair()
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "env-a"}}, fakeResolver{}, NewMemoryStore(), fixedNow)
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
	got := m.List(ctx)
	if len(got.Records) != 3 || got.Health.Status != HealthReady {
		t.Fatalf("list: %+v", got)
	}
	// A store with no subject set narrows nothing; the ref policy is the only gate.
	if err := m.AuthorizeSubject("anything"); err != nil {
		t.Errorf("memory store should authorize any ref, got %v", err)
	}
}

func TestSource_Collect(t *testing.T) {
	store := NewMemoryStore()
	// The RESOLVED contract (service "payments" under domain ghcr.io/acme) — not the
	// envelope Subject — is the source of the logical identity.
	a, priv := newTestAcceptor(t, fakeResolver{c: contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments"}}}, store)
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
	if tg.Service != "payments" || tg.Domain != "ghcr.io/acme" || tg.Scope != "env-a" || tg.Kind != "external" || tg.ResolvedRef == "" || tg.EvidenceAt == nil {
		t.Errorf("unexpected target: %+v", tg)
	}
	if tg.Digest != "sha256:"+strings.Repeat("a", 64) {
		t.Errorf("digest = %q, want the resolved revision digest", tg.Digest)
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
	h := NewHandler(a, nil, nil, func(ctx context.Context) bool {
		// Both gated paths must hand the probe the LIVE request context: it is the
		// only thing that can cancel a store call when the caller gives up, and a
		// Background() shortcut here would silently unbound every probe.
		if ctx.Done() == nil || ctx.Err() != nil {
			t.Errorf("readiness context = %v (done %v), want a live request context", ctx.Err(), ctx.Done())
		}
		return ready
	})
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

	var body TargetsResponse
	getJSON(t, srv.URL+"/api/evidence/v1/targets", &body)
	if body.SchemaVersion != TargetsSchemaVersion {
		t.Errorf("schemaVersion = %q, want %q", body.SchemaVersion, TargetsSchemaVersion)
	}
	if body.Health.Status != HealthReady || body.Truncated {
		t.Errorf("default health/truncated: %+v truncated=%v", body.Health, body.Truncated)
	}
	if len(body.Targets) != 1 {
		t.Fatalf("unexpected targets: %+v", body.Targets)
	}
	got := body.Targets[0]
	// Faithful projection: identity, contract linkage, freshness and provenance
	// all cross the wire, not just a summary.
	if got.Subject != "payments" || got.Producer != "env-a" || got.ProducerKeyID != keyID {
		t.Errorf("identity/provenance lost: %+v", got)
	}
	if got.ContractRef != testContractRef {
		t.Errorf("contract linkage lost: %q", got.ContractRef)
	}
	if got.AcceptedAt.IsZero() || got.EvidenceAt.IsZero() {
		t.Errorf("freshness lost: acceptedAt=%v evidenceAt=%v", got.AcceptedAt, got.EvidenceAt)
	}

	// The count bound truncates the list AND says so.
	orig := maxSourceTargets
	maxSourceTargets = 0
	defer func() { maxSourceTargets = orig }()
	getJSON(t, srv.URL+"/api/evidence/v1/targets", &body)
	if len(body.Targets) != 0 || !body.Truncated {
		t.Errorf("count bound should truncate+signal, got %d truncated=%v", len(body.Targets), body.Truncated)
	}
}

func TestHandler_TargetsFindingsCapAndHealth(t *testing.T) {
	store := NewMemoryStore()
	rec := Record{
		Envelope: evidenceenvelope.Envelope{
			ID:       "e1",
			Producer: evidenceenvelope.Producer{ID: "env-a", KeyID: keyID},
			Sequence: 1,
			EvidenceSet: evidence.EvidenceSet{
				Subject:     evidence.SubjectRef{Kind: "service", Name: "payments"},
				ContractRef: testContractRef,
			},
		},
		Compliance: "compliant",
		Findings:   []finding.Finding{{}, {}, {}},
		AcceptedAt: fixedNow(),
	}
	if err := store.Commit(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	// The health block comes back from the SAME read that produced the targets, so
	// the DTO can never describe a read it did not perform.
	partial := healthStore{
		recs:   store.List(context.Background()).Records,
		health: SourceHealth{Status: HealthPartial, Subjects: 3, FailedSubjects: 1, InvalidArtifacts: 2},
	}
	h := NewHandler(NewAcceptor(nil, fakeResolver{}, partial, fixedNow), nil, nil, nil)

	orig := maxFindingsPerTarget
	maxFindingsPerTarget = 1
	defer func() { maxFindingsPerTarget = orig }()

	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var body TargetsResponse
	getJSON(t, srv.URL+"/api/evidence/v1/targets", &body)
	if body.Health != (SourceHealth{Status: HealthPartial, Subjects: 3, FailedSubjects: 1, InvalidArtifacts: 2}) {
		t.Errorf("store health not surfaced: %+v", body.Health)
	}
	if !body.Truncated {
		t.Errorf("findings cap should mark truncated")
	}
	if n := len(body.Targets[0].Findings); n != 1 {
		t.Errorf("findings cap = %d, want 1", n)
	}
}

// TestHandler_TargetsUnavailable is the DTO's reason to exist: a store nobody
// could read must NOT be served as 200 with an empty target list, or every
// external environment silently reads as gone rather than as unobserved.
func TestHandler_TargetsUnavailable(t *testing.T) {
	store := healthStore{health: SourceHealth{Status: HealthUnavailable, Subjects: 2, FailedSubjects: 2}}
	h := NewHandler(NewAcceptor(nil, fakeResolver{}, store, fixedNow), nil, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/evidence/v1/targets", nil)
	w := httptest.NewRecorder()
	h.handleTargets(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unreadable store should be 503, got %d", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "registry_unavailable" {
		t.Errorf("code = %q, want registry_unavailable", body["code"])
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

// healthStore serves a fixed read health (and optional records) and fails Commit
// with a fixed error, covering the fail-closed read and write paths.
type healthStore struct {
	err    error
	health SourceHealth
	recs   []Record
}

func (f healthStore) AuthorizeSubject(string) error        { return nil }
func (f healthStore) Commit(context.Context, Record) error { return f.err }
func (f healthStore) List(context.Context) ListResult {
	return ListResult{Records: f.recs, Health: f.health}
}

func TestAcceptor_StoreError(t *testing.T) {
	pub, priv := testKeypair()
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "env-a"}}, fakeResolver{}, healthStore{err: errors.New("push rejected")}, fixedNow)
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet())); !errors.Is(err, ErrStoreWrite) {
		t.Errorf("store Commit error should wrap ErrStoreWrite, got %v", err)
	}
}

func TestAccept_CrossDomainIsolation(t *testing.T) {
	store := NewMemoryStore()
	// The resolver returns a "payments" contract for any ref; the DOMAIN comes from
	// the ContractRef, so two same-named services published to different OCI domains
	// must stay distinct — no cross-contamination.
	a, priv := newTestAcceptor(t, fakeResolver{c: contract.Contract{PactoVersion: "2.0", Service: contract.Service{Name: "payments"}}}, store)
	digA := "sha256:" + strings.Repeat("a", 64)
	digB := "sha256:" + strings.Repeat("b", 64)
	mk := func(subject, ref, id string, seq uint64) []byte {
		es := testEvidenceSet()
		es.Subject = evidence.SubjectRef{Kind: "service", Name: subject}
		es.ContractRef = ref
		return signedEnvelopeBytes(t, priv, seq, id, es)
	}
	if _, err := a.Accept(context.Background(), mk("payments-a", "oci://reg-a.io/team/payments@"+digA, "a", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Accept(context.Background(), mk("payments-b", "oci://reg-b.io/team/payments@"+digB, "b", 2)); err != nil {
		t.Fatal(err)
	}
	col, err := NewSource("", store).Collect(context.Background())
	if err != nil || len(col.Targets) != 2 {
		t.Fatalf("collect: %d targets err=%v", len(col.Targets), err)
	}
	byDomain := map[string]fleet.RawTarget{}
	for _, tg := range col.Targets {
		byDomain[tg.Domain] = tg
	}
	// Same logical service NAME, distinct domains, distinct revision digests.
	want := map[string]string{"reg-a.io/team": digA, "reg-b.io/team": digB}
	for dom, dig := range want {
		tg, ok := byDomain[dom]
		if !ok {
			t.Fatalf("missing target for domain %s (got %v)", dom, byDomain)
		}
		if tg.Service != "payments" {
			t.Errorf("domain %s: service = %q, want payments", dom, tg.Service)
		}
		if tg.Digest != dig {
			t.Errorf("domain %s: digest = %q, want %q (crossed?)", dom, tg.Digest, dig)
		}
	}
	// Built into a snapshot, they are two distinct domain-qualified services.
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("ev", "evidence-ingest", col))
	if err != nil {
		t.Fatal(err)
	}
	for dom := range want {
		if snap.Services[fleet.NewServiceKeyDomain(dom, "payments")] == nil {
			t.Errorf("missing domain-qualified service %s/payments", dom)
		}
	}
}

func TestOCIRefHelpers(t *testing.T) {
	dig := "sha256:" + strings.Repeat("a", 64)
	if got := ociDomain("oci://ghcr.io/acme/payments@" + dig); got != "ghcr.io/acme" {
		t.Errorf("ociDomain = %q, want ghcr.io/acme", got)
	}
	if got := ociDomain("noslash"); got != "" { // no org segment → empty domain
		t.Errorf("ociDomain(noslash) = %q, want empty", got)
	}
	if got := ociDigest("oci://ghcr.io/x@" + dig); got != dig {
		t.Errorf("ociDigest = %q, want %q", got, dig)
	}
	if got := ociDigest("oci://ghcr.io/x:tag"); got != "" { // no digest → empty
		t.Errorf("ociDigest(no @) = %q, want empty", got)
	}
}

// TestSource_CollectUnavailable: an unreadable store is a source ERROR, so the
// snapshot records the source as unavailable — never as an empty environment.
func TestSource_CollectUnavailable(t *testing.T) {
	src := NewSource("s", healthStore{health: SourceHealth{Status: HealthUnavailable, Subjects: 2, FailedSubjects: 2}})
	_, err := src.Collect(context.Background())
	if !errors.Is(err, ErrRegistryUnavailable) {
		t.Fatalf("unreadable store should error, got %v", err)
	}
	if !strings.Contains(err.Error(), "2 of 2 contract subjects unreadable") {
		t.Errorf("error should carry the sanitized reason, got %q", err)
	}
}

// TestSource_CollectPartial: a partly-read store keeps what it read and says so,
// so absence within it is explicitly non-authoritative.
func TestSource_CollectPartial(t *testing.T) {
	rec := Record{Envelope: evidenceenvelope.Envelope{
		ID: "e1", Producer: evidenceenvelope.Producer{ID: "env-a"},
		EvidenceSet: evidence.EvidenceSet{Subject: evidence.SubjectRef{Kind: "service", Name: "payments"}},
	}}
	src := NewSource("s", healthStore{
		recs:   []Record{rec},
		health: SourceHealth{Status: HealthPartial, Subjects: 2, FailedSubjects: 1, InvalidArtifacts: 1},
	})
	col, err := src.Collect(context.Background())
	if err != nil {
		t.Fatalf("a partial read must still yield what it read: %v", err)
	}
	if len(col.Targets) != 1 {
		t.Fatalf("targets = %d, want the one record that WAS read", len(col.Targets))
	}
	if col.State == nil || col.State.Status != fleet.SourcePartial {
		t.Errorf("state = %+v, want partial", col.State)
	}
	if len(col.Limitations) != 1 || col.Limitations[0].Code != fleet.LimitationSourcePartial {
		t.Errorf("limitations = %+v, want one SOURCE_PARTIAL", col.Limitations)
	}
}

// TestSourceHealth_Reason: the reason names COUNTS only. A registry host,
// repository or credential in this string would reach every unauthenticated
// consumer of the DTO.
func TestSourceHealth_Reason(t *testing.T) {
	cases := []struct {
		h    SourceHealth
		want string
	}{
		{SourceHealth{Status: HealthPartial, Subjects: 3, FailedSubjects: 1, InvalidArtifacts: 2},
			"1 of 3 contract subjects unreadable, 2 invalid evidence artifact(s)"},
		{SourceHealth{Status: HealthUnavailable, Subjects: 2, FailedSubjects: 2},
			"2 of 2 contract subjects unreadable"},
		{SourceHealth{Status: HealthPartial, Subjects: 2, InvalidArtifacts: 1},
			"1 invalid evidence artifact(s)"},
		{SourceHealth{Status: HealthReady, Subjects: 1}, `evidence store health "ready"`},
	}
	for _, tc := range cases {
		if got := tc.h.Reason(); got != tc.want {
			t.Errorf("Reason() = %q, want %q", got, tc.want)
		}
	}
}

// TestAcceptor_StoreRejectsSubject: trust says which refs a producer MAY report
// on; the store says which the operator actually configured. An authenticated
// producer reporting on an unconfigured revision is refused BEFORE any fetch.
func TestAcceptor_StoreRejectsSubject(t *testing.T) {
	pub, priv := testKeypair()
	resolver := &countingResolver{}
	a := NewAcceptor(
		evidenceenvelope.MapTrustStore{keyID: evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "env-a"}},
		resolver, unconfiguredStore{MemoryStore: NewMemoryStore()}, fixedNow)
	_, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", testEvidenceSet()))
	if !errors.Is(err, ErrContractRefPolicy) {
		t.Fatalf("unconfigured subject should be ErrContractRefPolicy, got %v", err)
	}
	if resolver.calls != 0 {
		t.Errorf("the ref was resolved %d times; an unconfigured subject must never be fetched", resolver.calls)
	}
}

type countingResolver struct{ calls int }

func (r *countingResolver) Resolve(context.Context, string) (contract.Contract, error) {
	r.calls++
	return contract.Contract{}, nil
}

// unconfiguredStore rejects every subject, as a store whose configured subject
// set does not contain the reported ref does.
type unconfiguredStore struct{ *MemoryStore }

func (unconfiguredStore) AuthorizeSubject(ref string) error {
	return fmt.Errorf("%w: %q is not a configured evidence subject", ErrContractRefPolicy, ref)
}

// TestNewAcceptor_DefaultClock covers the now==nil default-clock branch of
// NewAcceptor by accepting an envelope valid under the real wall clock.
func TestNewAcceptor_DefaultClock(t *testing.T) {
	pub, priv := testKeypair()
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "env-a"}}, fakeResolver{}, NewMemoryStore(), nil)
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
