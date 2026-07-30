package fleetsrc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// targetsJSON is a full v1 DTO: identity, contract linkage, full findings,
// freshness, provenance and a healthy store.
const targetsJSON = `{"schemaVersion":"pacto.dev/evidence-source/v1","generatedAt":"2026-07-29T12:00:00Z","health":{"phase":"ready","pendingRepair":false,"corruptions":0},"truncated":false,"targets":[{"subject":"payments-prod-01","service":"payments","domain":"ghcr.io/acme","digest":"sha256:abc","producer":"prod-eu","producerKeyId":"k1","compliance":"Compliant","coverage":{"evaluated":3,"required":5},"findings":[{}],"contractRef":"oci://ghcr.io/acme/payments@sha256:abc","evidenceAt":"2026-07-29T11:00:00Z","acceptedAt":"2026-07-29T11:05:00Z"}]}`

func serveJSON(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
}

func TestEvidenceHTTPSource_IDKind(t *testing.T) {
	if s := NewEvidenceHTTPSource("", ""); s.ID() != "evidence-http" || s.Kind() != "evidence-http" {
		t.Errorf("defaults wrong: id=%q kind=%q", s.ID(), s.Kind())
	}
	if NewEvidenceHTTPSource("prod", "").ID() != "prod" {
		t.Error("custom id not honored")
	}
}

// collectOneTarget serves the full v1 DTO and returns its single mapped target,
// asserting the healthy-store shape (no forced state, exactly one target). Kept
// small so the per-aspect tests below stay low-complexity.
func collectOneTarget(t *testing.T) fleet.RawTarget {
	t.Helper()
	srv := serveJSON(t, targetsJSON)
	defer srv.Close()
	col, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if col.State != nil {
		t.Errorf("healthy store should not force a source state: %+v", col.State)
	}
	if len(col.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(col.Targets))
	}
	return col.Targets[0]
}

func TestEvidenceHTTPSource_Collect(t *testing.T) {
	tgt := collectOneTarget(t)
	// Name is the operational target (subject); Service/Domain are the resolved
	// logical identity — Service is NOT inferred from Subject.
	if tgt.Scope != "prod-eu" || tgt.Kind != "external" || tgt.Name != "payments-prod-01" {
		t.Errorf("identity wrong: %+v", tgt)
	}
	if tgt.Service != "payments" || tgt.Domain != "ghcr.io/acme" {
		t.Errorf("resolved service/domain wrong: service=%q domain=%q", tgt.Service, tgt.Domain)
	}
	if tgt.Compliance != "Compliant" {
		t.Errorf("compliance = %q, want Compliant", tgt.Compliance)
	}
	if tgt.Coverage == nil || tgt.Coverage.Evaluated != 3 || tgt.Coverage.Required != 5 {
		t.Errorf("coverage wrong: %+v", tgt.Coverage)
	}
}

// TestEvidenceHTTPSource_Collect_FaithfulProjection proves the consumer keeps the
// full record — findings, contract linkage and both timestamps — not a summary.
func TestEvidenceHTTPSource_Collect_FaithfulProjection(t *testing.T) {
	tgt := collectOneTarget(t)
	if len(tgt.Findings) != 1 {
		t.Errorf("findings not reconstructed: %+v", tgt.Findings)
	}
	if tgt.ResolvedRef != "oci://ghcr.io/acme/payments@sha256:abc" || tgt.Digest != "sha256:abc" {
		t.Errorf("contract linkage lost: ref=%q digest=%q", tgt.ResolvedRef, tgt.Digest)
	}
	if tgt.EvidenceAt == nil || tgt.EvidenceAt.Year() != 2026 {
		t.Errorf("evidenceAt wrong: %v", tgt.EvidenceAt)
	}
	if tgt.ReconciledAt == nil || tgt.ReconciledAt.Year() != 2026 {
		t.Errorf("acceptedAt→reconciledAt wrong: %v", tgt.ReconciledAt)
	}
}

func TestEvidenceHTTPSource_Collect_SchemaMismatch(t *testing.T) {
	srv := serveJSON(t, `{"schemaVersion":"pacto.dev/evidence-source/v2","targets":[]}`)
	defer srv.Close()
	if _, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background()); err == nil {
		t.Fatal("a mismatched schema version must be an error, not a silent read")
	}
}

func TestEvidenceHTTPSource_Collect_UnknownField(t *testing.T) {
	srv := serveJSON(t, `{"schemaVersion":"pacto.dev/evidence-source/v1","targets":[],"surprise":true}`)
	defer srv.Close()
	if _, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background()); err == nil {
		t.Fatal("strict decode must reject an unknown field")
	}
}

func TestEvidenceHTTPSource_Collect_InvalidCompliance(t *testing.T) {
	srv := serveJSON(t, `{"schemaVersion":"pacto.dev/evidence-source/v1","health":{"phase":"ready"},"targets":[{"subject":"payments","producer":"prod-eu","compliance":"Bogus","coverage":{},"evidenceAt":"2026-07-29T11:00:00Z","acceptedAt":"2026-07-29T11:00:00Z"}]}`)
	defer srv.Close()
	col, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Targets) != 0 {
		t.Errorf("a target with unknown compliance must be dropped, got %d", len(col.Targets))
	}
	if !hasLimitationCode(col.Limitations, fleet.LimitationSourceRecordInvalid) {
		t.Errorf("dropped record must surface a limitation: %+v", col.Limitations)
	}
}

func TestEvidenceHTTPSource_Collect_Degraded(t *testing.T) {
	cases := map[string]string{
		"phase":         `"health":{"phase":"recovering"}`,
		"pendingRepair": `"health":{"phase":"ready","pendingRepair":true}`,
		"corruptions":   `"health":{"phase":"ready","corruptions":2}`,
		"truncated":     `"health":{"phase":"ready"},"truncated":true`,
	}
	for name, health := range cases {
		t.Run(name, func(t *testing.T) {
			payload := `{"schemaVersion":"pacto.dev/evidence-source/v1",` + health + `,"targets":[{"subject":"payments","producer":"prod-eu","compliance":"Compliant","coverage":{},"evidenceAt":"2026-07-29T11:00:00Z","acceptedAt":"2026-07-29T11:00:00Z"}]}`
			srv := serveJSON(t, payload)
			defer srv.Close()
			col, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			// Usable targets are still kept — degraded means partial, not empty.
			if len(col.Targets) != 1 {
				t.Errorf("degraded source should still keep usable targets, got %d", len(col.Targets))
			}
			if col.State == nil || col.State.Status != fleet.SourcePartial {
				t.Errorf("degraded source must be SourcePartial: %+v", col.State)
			}
			if !hasLimitationCode(col.Limitations, fleet.LimitationSourcePartial) {
				t.Errorf("degraded source must surface SOURCE_PARTIAL: %+v", col.Limitations)
			}
		})
	}
}

func hasLimitationCode(ls []fleet.Limitation, code string) bool {
	for _, l := range ls {
		if l.Code == code {
			return true
		}
	}
	return false
}

func TestEvidenceHTTPSource_Collect_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background()); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestEvidenceHTTPSource_Collect_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // closed server → connection refused

	if _, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background()); err == nil {
		t.Fatal("expected a transport error against a closed server")
	}
}

func TestEvidenceHTTPSource_Collect_BadURL(t *testing.T) {
	// A control character makes request construction fail before any transport.
	if _, err := NewEvidenceHTTPSource("evidence-http", "http://\x7f").Collect(context.Background()); err == nil {
		t.Fatal("expected a request-construction error for a bad URL")
	}
}

func TestEvidenceHTTPSource_Collect_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()

	if _, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background()); err == nil {
		t.Fatal("expected a decode error for a malformed body")
	}
}

func TestEvidenceHTTPSource_Collect_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(targetsJSON))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(ctx); err == nil {
		t.Fatal("expected a context-cancellation error")
	}
}
