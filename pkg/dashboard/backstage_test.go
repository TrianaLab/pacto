package dashboard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const backstagePath = "/api/backstage/entities"

func encoded(t *testing.T, q *fleet.Query) string {
	t.Helper()
	var buf bytes.Buffer
	if err := backstage.Encode(&buf, backstage.Map(q)); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func backstageGet(t *testing.T, o Options, method string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewServer(o).APIHandler().ServeHTTP(rec, httptest.NewRequest(method, backstagePath, nil))
	return rec
}

func assertBackstageHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for k, want := range map[string]string{
		"Content-Type":  "application/yaml; charset=utf-8",
		"Cache-Control": "private, no-store",
		"Vary":          "Authorization, Cookie",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func assertRefused(t *testing.T, rec *httptest.ResponseRecorder, want ...string) {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "30" || strings.Contains(body, "apiVersion:") {
		t.Fatalf("got %d Retry-After=%q body %q, want a 503 with no entities", rec.Code, rec.Header().Get("Retry-After"), body)
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("body %q does not name %q", body, w)
		}
	}
}

// sourcesQuery builds a fleet over shop-svc from an available source plus extra.
func sourcesQuery(t *testing.T, extra ...fleet.Source) *fleet.Query {
	t.Helper()
	sources := append([]fleet.Source{fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Targets: []fleet.RawTarget{{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "shop-svc", Service: "shop-svc"}},
	})}, extra...)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, sources...)
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// TestBackstageEntitiesServesMapperYAML: the route serves exactly the
// mapper's YAML, and a HEAD the same headers with no body.
func TestBackstageEntitiesServesMapperYAML(t *testing.T) {
	q := viewQuery(t)
	rec := backstageGet(t, Options{Fleet: fleetOf(q)}, http.MethodGet)
	if rec.Code != http.StatusOK || rec.Body.String() != encoded(t, q) {
		t.Fatalf("got %d %q, want the mapper's bytes", rec.Code, rec.Body.String())
	}
	assertBackstageHeaders(t, rec.Header())

	base := serveOptions(t, Options{Fleet: fleetOf(q)})
	resp, err := http.Head(base + backstagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD = %d with %d body bytes", resp.StatusCode, len(body))
	}
	assertBackstageHeaders(t, resp.Header)
}

// TestBackstageEntitiesRestrictedView: the route maps only what the caller
// may see.
func TestBackstageEntitiesRestrictedView(t *testing.T) {
	q := viewQuery(t)
	shop := func(ns string) bool { return ns == "shop" }
	whole := encoded(t, q)
	rec := backstageGet(t, Options{Fleet: fleetOf(q), View: shopView}, http.MethodGet)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || body != encoded(t, q.Restrict(shop)) {
		t.Fatalf("got %d %q, want the restricted view's bytes", rec.Code, body)
	}
	if !strings.Contains(whole, "secret-svc") || strings.Contains(body, "secret-svc") {
		t.Errorf("the hidden service leaked or the fixture lacks it:\n%s", body)
	}
	for _, line := range strings.Split(whole, "\n") {
		if strings.Contains(line, backstage.AnnotationServiceKey) && strings.Contains(line, "secret") && strings.Contains(body, line) {
			t.Errorf("hidden service key leaked: %s", line)
		}
	}
}

func TestBackstageEntitiesNoFleetNoRoute(t *testing.T) {
	if rec := backstageGet(t, Options{}, http.MethodGet); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
}

func TestBackstageEntitiesSnapshotUnavailable(t *testing.T) {
	rec := backstageGet(t, Options{Fleet: func(context.Context) (*fleet.Query, error) {
		return nil, errors.New("no snapshot yet")
	}}, http.MethodGet)
	assertRefused(t, rec)
}

func TestBackstageEntitiesViewFailure(t *testing.T) {
	rec := backstageGet(t, Options{Fleet: fleetOf(viewQuery(t)), View: func(context.Context, *fleet.Query) (*fleet.Query, error) {
		return nil, errors.New("apiserver down")
	}}, http.MethodGet)
	assertRefused(t, rec)
}

// TestBackstageEntitiesSourceUnavailableIs503: a source whose records are
// missing refuses the catalog, so Backstage keeps the entities it has.
func TestBackstageEntitiesSourceUnavailableIs503(t *testing.T) {
	down := sourcesQuery(t, fleet.NewFailingSource("oci", "oci", errors.New("registry down")))
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(down)}, http.MethodGet), "oci", "unavailable")

	unreadable := sourcesQuery(t, fleet.NewMemorySource("k8s-b", "kubernetes", &fleet.Collection{
		State: &fleet.SourceState{Status: fleet.SourcePartial, Namespaces: []string{"a"}, NamespacesTotal: 2},
		Limitations: []fleet.Limitation{
			fleet.NamespaceScopeLimitation("k8s-b", []string{"a"}, 0),
			{Code: fleet.LimitationK8sNamespaceUnreadable, Source: "k8s-b", Message: "namespace b is unreadable"},
		},
	}))
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(unreadable)}, http.MethodGet), "k8s-b", "partial")

	unscoped := sourcesQuery(t, fleet.NewMemorySource("dir", "dir", &fleet.Collection{
		State: &fleet.SourceState{Status: fleet.SourcePartial},
	}))
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(unscoped)}, http.MethodGet), "dir", "partial")
}

// TestBackstageEntitiesNamespaceScopedSourceIs200: a source partial only
// because it reads a namespace list is complete for its scope, and a stale
// source keeps its records.
func TestBackstageEntitiesNamespaceScopedSourceIs200(t *testing.T) {
	q := sourcesQuery(t,
		fleet.NewMemorySource("k8s-b", "kubernetes", &fleet.Collection{
			State:       &fleet.SourceState{Status: fleet.SourcePartial, Namespaces: []string{"a"}, NamespacesTotal: 1},
			Limitations: []fleet.Limitation{fleet.NamespaceScopeLimitation("k8s-b", []string{"a"}, 0)},
		}),
		fleet.NewMemorySource("old", "oci", &fleet.Collection{State: &fleet.SourceState{Status: fleet.SourceStale}}),
	)
	if rec := backstageGet(t, Options{Fleet: fleetOf(q)}, http.MethodGet); rec.Code != http.StatusOK {
		t.Fatalf("got %d %q, want 200", rec.Code, rec.Body.String())
	}
}

// TestBackstageEntitiesRestrictedCompleteIs200: a restricted view is always
// partial, which never refuses the catalog by itself.
func TestBackstageEntitiesRestrictedCompleteIs200(t *testing.T) {
	q := viewQuery(t)
	view := shopView
	if v, _ := view(context.Background(), q); v.SnapshotForSerialization().Completeness != fleet.CompletenessPartial {
		t.Fatal("a restricted view is expected to be partial")
	}
	if rec := backstageGet(t, Options{Fleet: fleetOf(q), View: view}, http.MethodGet); rec.Code != http.StatusOK {
		t.Fatalf("got %d %q, want 200", rec.Code, rec.Body.String())
	}
	nothing := func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(string) bool { return false }), nil
	}
	if rec := backstageGet(t, Options{Fleet: fleetOf(q), View: nothing}, http.MethodGet); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("a view of nothing = %d %q, want an empty 200", rec.Code, rec.Body.String())
	}
}

func TestBackstageEntitiesDeliveryAbsent(t *testing.T) {
	if rec := backstageGet(t, Options{Fleet: fleetOf(viewQuery(t))}, http.MethodGet); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "kind: Component") {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestBackstageEntitiesRejectsWrites(t *testing.T) {
	o := Options{Fleet: fleetOf(viewQuery(t))}
	base := serveOptions(t, o)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if rec := backstageGet(t, o, m); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s on the API mux = %d, want 405", m, rec.Code)
		}
		req, _ := http.NewRequest(m, base+backstagePath, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode < 300 || strings.Contains(string(body), "apiVersion:") {
			t.Errorf("%s on a live server = %d %q", m, resp.StatusCode, body)
		}
	}
}

func TestBackstageRouteOutsideOpenAPI(t *testing.T) {
	doc, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "/api/backstage") {
		t.Error("the raw Backstage route leaked into the OpenAPI document")
	}
}

func TestServeOnListenerServesBackstage(t *testing.T) {
	base := serveOptions(t, Options{Fleet: fleetOf(viewQuery(t))})
	expectStatus(t, base+backstagePath, http.StatusOK)
}
