package fleetsrc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const targetsJSON = `{"targets":[{"subject":"payments","producer":"prod-eu","compliance":"Compliant","findingsCount":0,"coverage":{"evaluated":3,"required":5},"observedAt":"2026-07-29T11:00:00Z"}]}`

func TestEvidenceHTTPSource_IDKind(t *testing.T) {
	if s := NewEvidenceHTTPSource("", ""); s.ID() != "evidence-http" || s.Kind() != "evidence-http" {
		t.Errorf("defaults wrong: id=%q kind=%q", s.ID(), s.Kind())
	}
	if NewEvidenceHTTPSource("prod", "").ID() != "prod" {
		t.Error("custom id not honored")
	}
}

func TestEvidenceHTTPSource_Collect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != evidenceTargetsPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(targetsJSON))
	}))
	defer srv.Close()

	col, err := NewEvidenceHTTPSource("evidence-http", srv.URL).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(col.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(col.Targets))
	}
	tgt := col.Targets[0]
	if tgt.Scope != "prod-eu" || tgt.Kind != "external" || tgt.Name != "payments" || tgt.Service != "payments" {
		t.Errorf("identity wrong: %+v", tgt)
	}
	if tgt.Compliance != "Compliant" {
		t.Errorf("compliance = %q, want Compliant", tgt.Compliance)
	}
	if tgt.Coverage == nil || tgt.Coverage.Evaluated != 3 || tgt.Coverage.Required != 5 {
		t.Errorf("coverage wrong: %+v", tgt.Coverage)
	}
	if tgt.EvidenceAt == nil || tgt.EvidenceAt.Year() != 2026 {
		t.Errorf("evidenceAt wrong: %v", tgt.EvidenceAt)
	}
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
