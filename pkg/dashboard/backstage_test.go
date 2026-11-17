package dashboard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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
	assertRefused(t, rec, "the fleet snapshot is not available yet: no snapshot yet")
}

// A failed view names its own cause, never a snapshot still being built.
func TestBackstageEntitiesViewFailure(t *testing.T) {
	rec := backstageGet(t, Options{Fleet: fleetOf(viewQuery(t)), View: func(context.Context, *fleet.Query) (*fleet.Query, error) {
		return nil, errors.New("apiserver down")
	}}, http.MethodGet)
	assertRefused(t, rec, "could not narrow the fleet to this reader: apiserver down")
	if strings.Contains(rec.Body.String(), "not available yet") {
		t.Errorf("a failed view reads as a missing snapshot: %q", rec.Body.String())
	}
}

// restrictTo is a view that may read only the namespaces named.
func restrictTo(nss ...string) func(context.Context, *fleet.Query) (*fleet.Query, error) {
	return func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
		return q.Restrict(func(ns string) bool { return slices.Contains(nss, ns) }), nil
	}
}

// TestBackstageEntitiesRestrictedViewKeepsRefusals: whether records are
// missing is judged before the reader's view narrows the fleet, which drops
// limitations about namespaces the reader cannot see and any limitation whose
// prose names a hidden name.
func TestBackstageEntitiesRestrictedViewKeepsRefusals(t *testing.T) {
	scoped := func(read []string, unreadable string) fleet.Source {
		return fleet.NewMemorySource("k8s-b", "kubernetes", &fleet.Collection{
			State: &fleet.SourceState{Status: fleet.SourcePartial, Namespaces: read, NamespacesTotal: len(read) + 1},
			Limitations: []fleet.Limitation{
				fleet.NamespaceScopeLimitation("k8s-b", read, 0),
				fleet.NamespaceUnreadableLimitation("k8s-b", unreadable, "forbidden"),
			},
		})
	}
	// pay was never listed, so no view asks about it and Restrict drops its limitation.
	never := sourcesQuery(t, scoped([]string{"shop"}, "pay"))
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(never), View: restrictTo("shop", "pay")}, http.MethodGet), "k8s-b", "partial")

	// The hidden namespace payments is a prefix of the visible, unreadable payments-prod.
	prefix := sourcesQuery(t, scoped([]string{"payments"}, "payments-prod"),
		fleet.NewMemorySource("k8s-c", "kubernetes", &fleet.Collection{
			Targets: []fleet.RawTarget{{Scope: "payments", Kind: fleet.SourceKindKubernetes, Name: "pay-svc", Service: "pay-svc"}},
		}))
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(prefix), View: restrictTo("shop", "payments-prod")}, http.MethodGet), "k8s-b", "partial")

	// The organization catalog's message says "default catalog"; hiding the default namespace must not hide it.
	lim := fleet.Limitation{Code: fleet.LimitationExternalCatalogUnavailable, Source: "external-catalog",
		Message: "The organization catalog oci://reg/catalog could not be read, so only the default catalog classified external systems: denied"}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{ExternalCatalogLimitations: []fleet.Limitation{lim}}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "shop-svc", Service: "shop-svc"},
			{Scope: "default", Kind: fleet.SourceKindKubernetes, Name: "web", Service: "web"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(fleet.NewQuery(snap)), View: restrictTo("shop")}, http.MethodGet), "external-catalog", "unavailable")
}

// TestBackstageEntitiesEmptyFleetIs503: a fleet whose sources answered and
// hold nothing refuses, as pacto export backstage does, while a reader whose
// view of a fleet that holds services is empty gets an empty 200.
func TestBackstageEntitiesEmptyFleetIs503(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{}))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Completeness != fleet.CompletenessEmpty {
		t.Fatalf("completeness = %s, want empty", snap.Completeness)
	}
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(fleet.NewQuery(snap))}, http.MethodGet), "the fleet is empty")
}

// TestBackstageEntitiesUnresolvedObservedIs200: a scoped source that observed
// a call to a name the fleet does not hold has lost no record.
func TestBackstageEntitiesUnresolvedObservedIs200(t *testing.T) {
	q := sourcesQuery(t, fleet.NewMemorySource("k8s-b", "kubernetes", &fleet.Collection{
		State: &fleet.SourceState{Status: fleet.SourcePartial, Namespaces: []string{"a"}, NamespacesTotal: 1},
		Limitations: []fleet.Limitation{
			fleet.NamespaceScopeLimitation("k8s-b", []string{"a"}, 0),
			{Code: fleet.LimitationObservedIdentityUnresolved, Source: "k8s-b", Message: "observed service some-db could not be mapped"},
		},
	}))
	if rec := backstageGet(t, Options{Fleet: fleetOf(q)}, http.MethodGet); rec.Code != http.StatusOK {
		t.Fatalf("got %d %q, want 200", rec.Code, rec.Body.String())
	}
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

	// The organization catalog classifies external systems; unread, their
	// Resources would vanish, as pacto export backstage also refuses.
	lim := fleet.Limitation{Code: fleet.LimitationExternalCatalogUnavailable, Source: "external-catalog", Message: "the organization catalog could not be read"}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{ExternalCatalogLimitations: []fleet.Limitation{lim}}, fleet.NewMemorySource("k8s", "kubernetes", &fleet.Collection{}))
	if err != nil {
		t.Fatal(err)
	}
	assertRefused(t, backstageGet(t, Options{Fleet: fleetOf(fleet.NewQuery(snap))}, http.MethodGet), "external-catalog", "unavailable")
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
