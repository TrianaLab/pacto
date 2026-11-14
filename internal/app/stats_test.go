package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
	"github.com/trianalab/pacto/v4/pkg/runrecord/registry"
	"github.com/trianalab/pacto/v4/pkg/runrecord/stats"
)

var statsClock = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// statsService is a Service whose stats repository is an in-process registry
// wrapped by wrap, with records published to it, and the path it reads.
func statsService(t *testing.T, wrap func(http.Handler) http.Handler, records ...runrecord.Record) (*Service, string) {
	t.Helper()
	h := testutil.NewReferrersRegistry(testutil.ReferrersOptions{})
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	path := host + "/acme/pacto-stats"
	open := func(p string) (*remote.Repository, error) { return registry.Open(p, nil, []string{host}) }
	repo, err := open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if _, err := registry.Publish(t.Context(), repo, r); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(nil, nil)
	s.sys.statsRepo = open
	return s, path
}

func diffRecord(service string) runrecord.Record {
	return runrecord.Record{
		Version: runrecord.Version, ID: runrecord.ID(runrecord.KindDiff, service, "a", "b"),
		Kind: runrecord.KindDiff, Service: service, OldRevision: "a", NewRevision: "b",
		Classification: "BREAKING", Outcome: runrecord.OutcomeFailed, PactoVersion: "dev",
		RecordedAt: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
	}
}

func operationRecord(t *testing.T, id, namespace string) runrecord.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "pkg", "runrecord", "testdata", "operation.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := runrecord.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	r.ID, r.Stage.Namespace = id, namespace
	return r
}

func statsWindow() stats.Query {
	return stats.Query{Window: stats.Window{Since: statsClock.Add(-stats.DefaultWindow), Until: statsClock}}
}

func TestServiceStatsReadsAndTotals(t *testing.T) {
	s, path := statsService(t, nil, diffRecord("payments"), diffRecord("orders"))
	st, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Now: statsClock})
	if err != nil {
		t.Fatal(err)
	}
	if st.Registry != path || st.Runs != 2 || st.Provenance != stats.Provenance || !st.AsOf.Equal(statsClock) {
		t.Fatalf("stats = %+v", st)
	}
}

func TestServiceStatsTimeout(t *testing.T) {
	var block atomic.Bool
	s, path := statsService(t, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if block.Load() {
				<-r.Context().Done()
				return
			}
			inner.ServeHTTP(w, r)
		})
	}, diffRecord("payments"))
	block.Store(true)
	old := statsReadTimeout
	statsReadTimeout = 100 * time.Millisecond
	t.Cleanup(func() { statsReadTimeout = old })
	_, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Now: statsClock})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "reading run records from "+path) {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceStatsOneServiceReadsOneAnchor(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	s, path := statsService(t, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.Method+" "+r.URL.Path)
			mu.Unlock()
			inner.ServeHTTP(w, r)
		})
	}, diffRecord("payments"), diffRecord("orders"))
	mu.Lock()
	paths = nil
	mu.Unlock()
	q := statsWindow()
	q.Service = "payments"
	st, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: q, Now: statsClock})
	if err != nil || st.Runs != 1 {
		t.Fatalf("stats = %+v err = %v", st, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range paths {
		if strings.HasSuffix(p, "/tags/list") || strings.Contains(p, runrecord.AnchorTag("orders")) {
			t.Errorf("read beyond the one anchor: %s", p)
		}
	}
}

func TestServiceStatsZeroNowUsesServiceClock(t *testing.T) {
	s, path := statsService(t, nil, diffRecord("payments"))
	s.sys.now = func() time.Time { return statsClock }
	st, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow()})
	if err != nil || !st.AsOf.Equal(statsClock) {
		t.Fatalf("stats = %+v err = %v", st, err)
	}
}

// failReferrers answers every referrers listing with a 403.
func failReferrers(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/referrers/") {
			http.Error(w, "boom", http.StatusForbidden)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func TestServiceStatsRestrictedAnchorErrorIsGeneric(t *testing.T) {
	s, path := statsService(t, failReferrers, diffRecord("payments"))
	_, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Scope: stats.Scope{Restricted: true}, Now: statsClock})
	if err == nil || err.Error() != "reading run records from "+path+" failed for one service" || strings.Contains(err.Error(), runrecord.AnchorTagPrefix) {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceStatsUnrestrictedAnchorErrorNamesTheAnchor(t *testing.T) {
	s, path := statsService(t, failReferrers, diffRecord("payments"))
	_, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Now: statsClock})
	var anchor *registry.AnchorError
	if !errors.As(err, &anchor) || !strings.Contains(err.Error(), runrecord.AnchorTag("payments")) {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceStatsOpenError(t *testing.T) {
	s := NewService(nil, nil)
	s.sys.statsRepo = func(string) (*remote.Repository, error) { return nil, errors.New("bad reference") }
	_, err := s.Stats(t.Context(), StatsRequest{Registry: "::", Query: statsWindow(), Now: statsClock})
	if err == nil || err.Error() != "reading run records from ::: bad reference" {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceStatsDefaultOpener(t *testing.T) {
	if _, err := (&Service{}).env().statsRepo("reg.example/acme/pacto-stats"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStatsOperationAccess(t *testing.T) {
	s, path := statsService(t, nil, operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000a", "prod-a"), operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000b", "prod-b"), operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000c", "prod-a"), diffRecord("payments"))
	var asked []string
	access := func(_ context.Context, namespaces []string) (func(string) bool, error) {
		asked = namespaces
		return func(ns string) bool { return ns == "prod-a" }, nil
	}
	st, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Scope: stats.Scope{Restricted: true, Place: func(string) (stats.Placement, bool) { return stats.Placement{}, true }}, Now: statsClock, OperationAccess: access})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []string{"prod-a", "prod-b"}) || st.Runs != 3 || !st.Hidden.Present {
		t.Fatalf("asked %v, stats = %+v", asked, st)
	}
}

func TestServiceStatsOperationAccessErrorIsAnError(t *testing.T) {
	s, path := statsService(t, nil, operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000a", "prod-a"))
	access := func(context.Context, []string) (func(string) bool, error) { return nil, errors.New("forbidden") }
	_, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Now: statsClock, OperationAccess: access})
	if err == nil || err.Error() != "checking your Kubernetes permissions failed: forbidden" {
		t.Fatalf("err = %v", err)
	}
}
