package ocireferrer

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/testutil"
)

// catalogServer serves names as a sorted, paginated _catalog, or status
// when set, and records every query it answered.
type catalogServer struct {
	mu      sync.Mutex
	queries []string
}

func newCatalogServer(t *testing.T, names []string, status int) (*catalogServer, *remote.Repository) {
	t.Helper()
	cs := &catalogServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.queries = append(cs.queries, r.URL.RawQuery)
		cs.mu.Unlock()
		if status != 0 {
			http.Error(w, `{"errors":[{"code":"DENIED"}]}`, status)
			return
		}
		testutil.ServeCatalog(w, r, names)
	}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return cs, &remote.Repository{Reference: registry.Reference{Registry: host, Repository: "acme/pacto-index"}, PlainHTTP: true}
}

// names is n repositories under dir, zero-padded so they sort by number.
func names(dir string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s/svc-%04d", dir, i)
	}
	return out
}

func TestCatalogPrefixAndPaging(t *testing.T) {
	before, under, after := names("aaa", 250), names("acme", 230), names("zzz", 250)
	all := slices.Concat(before, under, after, []string{"acme-other/x", "acme"})
	cs, repo := newCatalogServer(t, all, 0)
	got, err := Catalog(t.Context(), repo, "acme", 5000)
	if err != nil || !slices.Equal(got, under) {
		t.Fatalf("%d names, %v", len(got), err)
	}
	// 230 names under the prefix: three pages, and the page past them stops
	// the walk; nothing before the prefix is paged through.
	if len(cs.queries) != 3 {
		t.Fatalf("%d pages: %v", len(cs.queries), cs.queries)
	}
	got, err = Catalog(t.Context(), repo, "", 5000)
	if err != nil || len(got) != len(all) {
		t.Fatalf("whole registry: %d names, %v", len(got), err)
	}
}

func TestCatalogStartsAtPrefix(t *testing.T) {
	cs, repo := newCatalogServer(t, names("acme", 2), 0)
	if _, err := Catalog(t.Context(), repo, "acme", 10); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cs.queries[0], "last=acme") {
		t.Fatalf("first request %q", cs.queries[0])
	}
}

func TestCatalogUnsupportedStatuses(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusNotFound:            ErrCatalogUnsupported,
		http.StatusMethodNotAllowed:    ErrCatalogUnsupported,
		http.StatusUnauthorized:        ErrCatalogRefused,
		http.StatusForbidden:           ErrCatalogRefused,
		http.StatusInternalServerError: nil,
	} {
		_, repo := newCatalogServer(t, nil, status)
		got, err := Catalog(t.Context(), repo, "acme", 10)
		switch {
		case got != nil || err == nil:
			t.Errorf("%d: %v %v", status, got, err)
		case want == nil && (errors.Is(err, ErrCatalogUnsupported) || errors.Is(err, ErrCatalogRefused)):
			t.Errorf("%d: %v", status, err)
		case want != nil && (!errors.Is(err, want) || !strings.HasPrefix(err.Error(), repo.Reference.Registry+" ")):
			t.Errorf("%d: %v", status, err)
		}
	}
}

func TestCatalogTruncated(t *testing.T) {
	_, repo := newCatalogServer(t, names("acme", 30), 0)
	got, err := Catalog(t.Context(), repo, "acme", 12)
	if !errors.Is(err, ErrCatalogTruncated) || len(got) != 12 {
		t.Fatalf("names bound: %d %v", len(got), err)
	}
	old := maxTagPages
	maxTagPages = 2
	t.Cleanup(func() { maxTagPages = old })
	_, repo = newCatalogServer(t, names("acme", 250), 0)
	got, err = Catalog(t.Context(), repo, "acme", 5000)
	if !errors.Is(err, ErrCatalogTruncated) || len(got) != 2*catalogPageSize {
		t.Fatalf("pages bound: %d %v", len(got), err)
	}
}

// TestCatalogSeekPastUnknownLast pins zot's catalog: a last that names no
// repository answers an empty list, so the walk starts again from the top
// and still finds the names under the prefix.
func TestCatalogSeekPastUnknownLast(t *testing.T) {
	all := []string{"aaa/x", "acme/paths/http-service", "acme/streams-1", "zzz/y"}
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if last := r.URL.Query().Get("last"); last != "" && !slices.Contains(all, last) {
			testutil.ServeCatalog(w, r, nil)
			return
		}
		testutil.ServeCatalog(w, r, all)
	}))
	t.Cleanup(srv.Close)
	repo := &remote.Repository{Reference: registry.Reference{Registry: strings.TrimPrefix(srv.URL, "http://"), Repository: "acme/pacto-index"}, PlainHTTP: true}
	got, err := Catalog(t.Context(), repo, "acme", 10)
	if err != nil || !slices.Equal(got, all[1:3]) || len(queries) != 2 {
		t.Fatalf("%v %v after %v", got, err, queries)
	}
	// Nothing at all under the prefix stays an empty, complete listing.
	queries = nil
	if got, err := Catalog(t.Context(), repo, "nothing", 10); err != nil || len(got) != 0 || len(queries) != 2 {
		t.Fatalf("%v %v after %v", got, err, queries)
	}
}
