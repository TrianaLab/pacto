package app

import (
	"context"
	"errors"
	"net/http"
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
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
	srv := probedServer(t, h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	path := host + "/acme/pacto-stats"
	open := func(p string) (*remote.Repository, error) { return registry.Open(p, nil, []string{host}, nil) }
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	s, path := statsService(t, failReferrers, diffRecord("payments"))
	_, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Scope: stats.Scope{Restricted: true, Names: []string{"payments"}}, Now: statsClock})
	if err == nil || err.Error() != "reading run records from "+path+" failed" || strings.Contains(err.Error(), runrecord.AnchorTagPrefix) {
		t.Fatalf("err = %v", err)
	}
}

// Any registry error can carry a request URL whose tag hashes a hidden
// service, such as a tag list page that starts after one: a restricted
// caller gets none of it, and only a deadline or cancel stays apparent.
func TestServiceStatsRestrictedReadErrorIsGeneric(t *testing.T) {
	t.Parallel()
	tag := runrecord.AnchorTag("payments-api")
	req := StatsRequest{Registry: "reg/stats", Scope: stats.Scope{Restricted: true}}
	raw := errors.New(`GET "https://reg/v2/stats/tags/list?last=` + tag + `&n=100": response status code 500`)
	if err := statsReadError(req, raw); err.Error() != "reading run records from reg/stats failed" {
		t.Fatalf("err = %v", err)
	}
	for _, ctxErr := range []error{context.DeadlineExceeded, context.Canceled} {
		if err := statsReadError(req, ctxErr); !errors.Is(err, ctxErr) {
			t.Fatalf("err = %v", err)
		}
	}
	if err := statsReadError(StatsRequest{Registry: "reg/stats"}, raw); !errors.Is(err, raw) {
		t.Fatalf("unrestricted err = %v", err)
	}
}

// A restricted caller reads only the anchors of the services in its view: a
// hidden service's records are never listed, so they cannot use the bounds.
func TestServiceStatsRestrictedReadsOnlyViewAnchors(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var paths []string
	s, path := statsService(t, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.String())
			mu.Unlock()
			inner.ServeHTTP(w, r)
		})
	}, diffRecord("payments"), diffRecord("orders"))
	mu.Lock()
	paths = nil
	mu.Unlock()
	place := func(string) (stats.Placement, bool) { return stats.Placement{}, true }
	st, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Scope: stats.Scope{Restricted: true, Place: place, Names: []string{"orders"}}, Now: statsClock})
	if err != nil || st.Runs != 1 || st.Hidden.Present {
		t.Fatalf("stats = %+v err = %v", st, err)
	}
	mu.Lock()
	for _, p := range paths {
		if strings.Contains(p, "/tags/list") || strings.Contains(p, runrecord.AnchorTag("payments")) {
			t.Errorf("read a hidden anchor: %s", p)
		}
	}
	paths = nil
	mu.Unlock()
	st, err = s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Scope: stats.Scope{Restricted: true, Place: place}, Now: statsClock})
	if err != nil || st.Runs != 0 || len(paths) != 0 {
		t.Fatalf("no names: stats = %+v err = %v paths %v", st, err, paths)
	}
}

func TestServiceStatsUnrestrictedAnchorErrorNamesTheAnchor(t *testing.T) {
	t.Parallel()
	s, path := statsService(t, failReferrers, diffRecord("payments"))
	_, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Now: statsClock})
	var anchor *registry.AnchorError
	if !errors.As(err, &anchor) || !strings.Contains(err.Error(), runrecord.AnchorTag("payments")) {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceStatsOpenError(t *testing.T) {
	t.Parallel()
	s := NewService(nil, nil)
	s.sys.statsRepo = func(string) (*remote.Repository, error) { return nil, errors.New("bad reference") }
	_, err := s.Stats(t.Context(), StatsRequest{Registry: "::", Query: statsWindow(), Now: statsClock})
	if err == nil || err.Error() != "reading run records from ::: bad reference" {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceStatsDefaultOpener(t *testing.T) {
	t.Parallel()
	if _, err := (&Service{}).env().statsRepo("reg.example/acme/pacto-stats"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStatsOperationAccess(t *testing.T) {
	t.Parallel()
	s, path := statsService(t, nil, operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000a", "prod-a"), operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000b", "prod-b"), operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000c", "prod-a"), diffRecord("payments"))
	var asked []string
	access := func(_ context.Context, namespaces []string) (stats.OperationAccess, error) {
		asked = namespaces
		return stats.OperationAccess{Visible: func(ns string) bool { return ns == "prod-a" }, Clusters: []string{operationRecord(t, "x", "x").Stage.Cluster}}, nil
	}
	names := []string{operationRecord(t, "x", "x").Service, "payments"}
	st, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Scope: stats.Scope{Restricted: true, Names: names, Place: func(string) (stats.Placement, bool) { return stats.Placement{}, true }}, Now: statsClock, OperationAccess: access})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []string{"prod-a", "prod-b"}) || st.Runs != 3 || !st.Hidden.Present || st.Hidden.OtherClusters != 0 {
		t.Fatalf("asked %v, stats = %+v", asked, st)
	}
}

func TestServiceStatsOperationAccessErrorIsAnError(t *testing.T) {
	t.Parallel()
	s, path := statsService(t, nil, operationRecord(t, "0f6c2a1e-5b7d-4c3a-9e2f-00000000000a", "prod-a"))
	access := func(context.Context, []string) (stats.OperationAccess, error) {
		return stats.OperationAccess{}, errors.New("forbidden")
	}
	_, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Now: statsClock, OperationAccess: access})
	if err == nil || err.Error() != "checking your Kubernetes permissions failed: forbidden" {
		t.Fatalf("err = %v", err)
	}
}

// The dashboard's read and pacto stats open the repository through one
// opener, and read every anchor when no service is named.
func TestReadRunRecordsSharesOpener(t *testing.T) {
	t.Parallel()
	s, path := statsService(t, nil, diffRecord("payments"), diffRecord("orders"))
	var opened []string
	open := s.sys.statsRepo
	s.sys.statsRepo = func(p string) (*remote.Repository, error) { opened = append(opened, p); return open(p) }
	set, err := s.ReadRunRecords(t.Context(), path, nil)
	if err != nil || len(set.Entries) != 2 {
		t.Fatalf("set = %+v err = %v", set, err)
	}
	if _, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: statsWindow(), Now: statsClock}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opened, []string{path, path}) {
		t.Fatalf("opened %v", opened)
	}
	s.sys.statsRepo = func(string) (*remote.Repository, error) { return nil, errors.New("bad reference") }
	if _, err := s.ReadRunRecords(t.Context(), "::", nil); err == nil || err.Error() != "bad reference" {
		t.Fatalf("the registry's error, unwrapped: %v", err)
	}
}

// A restricted caller never reads the coverage anchor, yet is told coverage
// is withheld from it, never that there is none.
func TestServiceStatsRestrictedSaysCoverageWithheld(t *testing.T) {
	t.Parallel()
	asOf := statsClock.Add(-time.Hour)
	cov := runrecord.CoverageRecord(runrecord.Coverage{SnapshotAsOf: asOf, Completeness: "complete"}, "dev", asOf)
	s, path := statsService(t, nil, cov, diffRecord("orders"))
	place := func(string) (stats.Placement, bool) { return stats.Placement{}, true }
	st, err := s.Stats(t.Context(), StatsRequest{Registry: path, Query: stats.Query{Window: statsWindow().Window, Kinds: []string{runrecord.KindCoverage}},
		Scope: stats.Scope{Restricted: true, Place: place, Names: []string{"orders"}}, Now: statsClock})
	if err != nil || st.Coverage != nil || st.Completeness != fleet.CompletenessPartial ||
		!slices.ContainsFunc(st.Limitations, func(l fleet.Limitation) bool { return l.Code == stats.LimitationCoverageOutsideView }) {
		t.Fatalf("stats = %+v err = %v", st, err)
	}
}
