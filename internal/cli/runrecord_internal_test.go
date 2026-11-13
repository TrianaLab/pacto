package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

func validRecord() runrecord.Record {
	return runrecord.Record{
		Version: runrecord.Version, ID: runrecord.ID(runrecord.KindDiff, "payments", "a", "b"),
		Kind: runrecord.KindDiff, Service: "payments", OldRevision: "a", NewRevision: "b",
		Outcome: runrecord.OutcomePassed, PactoVersion: "dev", RecordedAt: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
	}
}

func TestDefaultPublishRunRecord(t *testing.T) {
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	for _, v := range []string{"PACTO_REGISTRY_USERNAME", "PACTO_REGISTRY_PASSWORD", "PACTO_REGISTRY_TOKEN"} {
		t.Setenv(v, "")
	}
	repo := host + "/acme/pacto-stats"
	got, err := defaultPublishRunRecord(t.Context(), repo, validRecord())
	if err != nil || !strings.HasPrefix(got, repo+"@sha256:") {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := defaultPublishRunRecord(t.Context(), "not a reference", validRecord()); err == nil {
		t.Fatal("published to a bad reference")
	}
}

func TestDefaultPublishRunRecordTimeout(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hang.Close()
	host := strings.TrimPrefix(hang.URL, "http://")
	t.Setenv("PACTO_INSECURE_REGISTRIES", host)
	old := statsTimeout
	statsTimeout = 50 * time.Millisecond
	t.Cleanup(func() { statsTimeout = old })
	_, err := defaultPublishRunRecord(t.Context(), host+"/acme/pacto-stats", validRecord())
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("err = %v", err)
	}
}
