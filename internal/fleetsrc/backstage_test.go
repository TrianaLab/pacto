package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const testToken = "s3cr3t-reader-token"

var backstageNow = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

// catalogPage is a by-query answer with n components named prefix-i.
func catalogPage(prefix string, n int, next string) string {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{"kind": "Component", "metadata": map[string]any{"name": fmt.Sprintf("%s-%d", prefix, i)},
			"spec": map[string]any{"owner": "group:team-ledger"}}
	}
	data, _ := json.Marshal(map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": next}})
	return string(data)
}

// newTestBackstage is a source over srv with token.
func newTestBackstage(t *testing.T, srv *httptest.Server, token string) *BackstageSource {
	t.Helper()
	src, err := NewBackstageSource("catalog", srv.URL+"/", token, func() time.Time { return backstageNow })
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// twoPageCatalog answers the first page only to the full query and the
// second only to the bare cursor.
func twoPageCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case q.Get("cursor") == "" && q.Get("filter") == "kind=component" && q.Get("limit") == "500" && strings.Contains(q.Get("fields"), "spec.dependsOn"):
		_, _ = w.Write([]byte(catalogPage("a", 2, "next")))
	case q.Get("cursor") == "next" && q.Get("filter") == "":
		_, _ = w.Write([]byte(catalogPage("b", 1, "")))
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

// endlessCatalog answers full pages with a next cursor forever.
func endlessCatalog(w http.ResponseWriter, r *http.Request) {
	n := 0
	_, _ = fmt.Sscanf(r.URL.Query().Get("cursor"), "p%d", &n)
	_, _ = w.Write([]byte(catalogPage(fmt.Sprintf("svc%d", n), 500, fmt.Sprintf("p%d", n+1))))
}

// endlessEmptyCatalog answers an empty page with the same next cursor
// forever and fails the test past the page bound.
func endlessEmptyCatalog() http.HandlerFunc {
	var calls atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > maxBackstagePages {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte(`{"items":[],"pageInfo":{"nextCursor":"same"}}`))
	}
}

// checkBackstageState asserts an advisory collection with status, facts
// service facts and a message containing msg but never the server URL.
func checkBackstageState(t *testing.T, col *fleet.Collection, err error, status fleet.SourceStatus, facts int, msg string) {
	t.Helper()
	if err != nil || col == nil || !col.Advisory || col.State == nil {
		t.Fatalf("Collect = %+v, %v", col, err)
	}
	if col.State.Status != status || len(col.ServiceFacts) != facts {
		t.Fatalf("status %s with %d facts, want %s with %d", col.State.Status, len(col.ServiceFacts), status, facts)
	}
	got := ""
	if col.State.Error != nil {
		got = col.State.Error.Message
	}
	for _, l := range col.Limitations {
		got += l.Message
	}
	if !strings.Contains(got, msg) || strings.Contains(got, "127.0.0.1") {
		t.Errorf("message %q, want %q and no URL", got, msg)
	}
}

func TestBackstageSourceStates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  fleet.SourceStatus
		facts   int
		msg     string
		cancel  bool
	}{
		{name: "one page", status: fleet.SourceAvailable, facts: 2, handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(catalogPage("a", 2, "")))
		}},
		{name: "two pages via cursor", status: fleet.SourceAvailable, facts: 3, handler: twoPageCatalog},
		{name: "exactly the cap", status: fleet.SourceAvailable, facts: backstage.MaxCatalogEntities, handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(catalogPage("a", backstage.MaxCatalogEntities, "")))
		}},
		{name: "one page over the cap", status: fleet.SourcePartial, facts: backstage.MaxCatalogEntities, msg: "the read stopped at 5000 components",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(catalogPage("a", backstage.MaxCatalogEntities+1, "")))
			}},
		{name: "cap reached", status: fleet.SourcePartial, facts: backstage.MaxCatalogEntities, msg: "the read stopped at 5000 components", handler: endlessCatalog},
		{name: "endless empty pages", status: fleet.SourcePartial, msg: "or 10 pages", handler: endlessEmptyCatalog()},
		{name: "dependencies trimmed", status: fleet.SourcePartial, facts: 1, msg: "1 components list more than 50 component dependencies",
			handler: func(w http.ResponseWriter, r *http.Request) {
				deps := make([]string, 60)
				for i := range deps {
					deps[i] = fmt.Sprintf("component:svc-%d", i)
				}
				data, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"kind": "Component",
					"metadata": map[string]any{"name": "gateway"}, "spec": map[string]any{"dependsOn": deps}}}})
				_, _ = w.Write(data)
			}},
		{name: "401", status: fleet.SourceUnavailable, msg: "refused", handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }},
		{name: "403", status: fleet.SourceUnavailable, msg: "refused", handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }},
		{name: "404", status: fleet.SourceUnavailable, msg: "entities/by-query", handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }},
		{name: "500", status: fleet.SourceUnavailable, msg: "answered 500", handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{name: "bad JSON", status: fleet.SourceUnavailable, msg: "not an entities/by-query page", handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{"))
		}},
		{name: "body over 16 MiB", status: fleet.SourceUnavailable, msg: "a catalog page is larger than 16 MiB", handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"items":[],"pad":"` + strings.Repeat("x", maxBackstageBodyBytes) + `"}`))
		}},
		{name: "body cut short", status: fleet.SourceUnavailable, msg: "could not be read", handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte(`{"items":`))
		}},
		{name: "redirect refused", status: fleet.SourceUnavailable, msg: "answered 302", handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
		}},
		{name: "context cancelled", status: fleet.SourceUnavailable, msg: "could not be reached", cancel: true, handler: func(w http.ResponseWriter, r *http.Request) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			ctx, cancel := context.WithCancel(t.Context())
			if tc.cancel {
				cancel()
			}
			defer cancel()
			col, err := newTestBackstage(t, srv, "").Collect(ctx)
			checkBackstageState(t, col, err, tc.status, tc.facts, tc.msg)
		})
	}
}

func TestBackstageSendsBearerToken(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(catalogPage("a", 1, "")))
	}))
	defer srv.Close()
	_, _ = newTestBackstage(t, srv, testToken).Collect(t.Context())
	_, _ = newTestBackstage(t, srv, "").Collect(t.Context())
	if len(auth) != 2 || auth[0] != "Bearer "+testToken || auth[1] != "" {
		t.Errorf("Authorization headers = %q", auth)
	}
	src := newTestBackstage(t, srv, "")
	if src.ID() != "catalog" || src.Kind() != backstage.SourceKind {
		t.Errorf("ID, Kind = %q, %q", src.ID(), src.Kind())
	}
}

func TestBackstageTokenNeverLeaksFromSource(t *testing.T) {
	for _, code := range []int{401, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(r.Header.Get("Authorization")))
		}))
		col, _ := newTestBackstage(t, srv, testToken).Collect(t.Context())
		srv.Close()
		data, _ := json.Marshal(col.State)
		if strings.Contains(string(data), testToken) {
			t.Errorf("%d: state %s carries the token", code, data)
		}
	}
	if err := CheckBackstageConfig("http://backstage.example.com", testToken); err == nil || strings.Contains(err.Error(), testToken) {
		t.Errorf("CheckBackstageConfig error %v", err)
	}
}

func TestBackstageTokenRefusedOverPlainHTTP(t *testing.T) {
	defer func(f func() bool) { inCluster = f }(inCluster)
	for _, tc := range []struct {
		url, token string
		cluster    bool
		refused    string
	}{
		{url: "http://backstage.example.com", token: testToken, refused: "use https or run inside the cluster"},
		{url: "http://127.0.0.1:7007", token: testToken},
		{url: "http://[::1]:7007", token: testToken},
		{url: "http://localhost:7007", token: testToken},
		{url: "http://backstage.backstage.svc:7007", token: testToken, cluster: true},
		{url: "http://backstage.backstage.svc.cluster.local:7007/", token: testToken, cluster: true},
		{url: "http://backstage.backstage.svc", token: testToken, cluster: true},
		{url: "http://backstage.backstage.svc.cluster.local/", token: testToken, cluster: true},
		{url: "http://backstage.backstage.svc", token: testToken, refused: "use https or run inside the cluster"},
		{url: "http://backstage.backstage.svc:7007", token: testToken, refused: "use https or run inside the cluster"},
		{url: "http://backstage.example.com", token: testToken, cluster: true, refused: "use https or the in-cluster Service name http://<service>.<namespace>.svc:<port>"},
		{url: "http://backstage:7007", token: testToken, cluster: true, refused: "use https or the in-cluster Service name"},
		{url: "http://backstage.backstage:7007", token: testToken, cluster: true, refused: "use https or the in-cluster Service name"},
		{url: "http://backstage.example.com"},
		{url: "https://backstage.example.com", token: testToken},
		{url: "https://u:p@backstage.example.com", refused: "credentials"},
		{url: "https://backstage.example.com?x=1", refused: "query or fragment"},
		{url: "https://backstage.example.com#x", refused: "query or fragment"},
		{url: "ftp://backstage.example.com", refused: "backstage source"},
	} {
		inCluster = func() bool { return tc.cluster }
		err := CheckBackstageConfig(tc.url, tc.token)
		if tc.refused == "" && err != nil || tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.refused)) {
			t.Errorf("CheckBackstageConfig(%q, token %v, cluster %v) = %v, want %q", tc.url, tc.token != "", tc.cluster, err, tc.refused)
		}
		if _, nerr := NewBackstageSource("catalog", tc.url, tc.token, time.Now); (nerr == nil) != (err == nil) {
			t.Errorf("NewBackstageSource(%q) = %v, CheckBackstageConfig %v", tc.url, nerr, err)
		}
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if !inClusterEnv() {
		t.Error("inCluster false with KUBERNETES_SERVICE_HOST set")
	}
}

func TestDetectedBackstageSourceRefusesABadEndpoint(t *testing.T) {
	if s, err := NewDetectedBackstageSource("backstage-catalog", "ftp://backstage.idp.svc:7007", "detected", time.Now); err == nil || s != nil {
		t.Fatalf("source = %+v, err = %v", s, err)
	}
}

// TestDetectedBackstageSourceSendsNoToken: a detected source reads without a
// token, names its origin and says how to give it one when refused.
func TestDetectedBackstageSourceSendsNoToken(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	s, err := NewDetectedBackstageSource("backstage-catalog", srv.URL, "detected", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	col, _ := s.Collect(context.Background())
	st := col.State
	if st.Status != fleet.SourceUnavailable || st.Ref != "detected" || st.Error == nil ||
		st.Error.Message != "the catalog refused the request; set dashboard.backstage.url and tokenSecret to read this catalog" {
		t.Fatalf("state = %+v", st)
	}
	if len(auth) != 1 || auth[0] != "" {
		t.Errorf("Authorization headers = %q", auth)
	}
}
