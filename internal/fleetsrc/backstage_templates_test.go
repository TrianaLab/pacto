package fleetsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/backstage"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// templatePage is a by-query answer with one scaffolder Template named name
// in namespace ns and n Components, with next as its cursor.
func templatePage(ns, name string, n int, next string) []byte {
	items := []map[string]any{{"apiVersion": "scaffolder.backstage.io/v1beta3", "kind": "Template",
		"metadata": map[string]any{"name": name, "namespace": ns, "title": "T " + name}}}
	for i := range n {
		items = append(items, map[string]any{"kind": "Component", "metadata": map[string]any{"name": fmt.Sprintf("%s-c%d", name, i)}})
	}
	data, _ := json.Marshal(map[string]any{"items": items, "pageInfo": map[string]any{"nextCursor": next}})
	return data
}

// TestBackstageSourceAsksForTemplates: the first page asks for Components and
// Templates with the fields a template needs; a cursor page carries no filter.
func TestBackstageSourceAsksForTemplates(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		next := ""
		if len(queries) == 1 {
			next = "p2"
		}
		_, _ = w.Write(templatePage("", fmt.Sprintf("t%d", len(queries)), 0, next))
	}))
	defer srv.Close()
	col, _ := newTestBackstage(t, srv, "").Collect(t.Context())
	if len(queries) != 2 || len(col.InferredTemplates) != 2 {
		t.Fatalf("%d requests, %d templates", len(queries), len(col.InferredTemplates))
	}
	first := queries[0]
	if f := first["filter"]; len(f) != 2 || f[0] != "kind=component" || f[1] != "kind=template" {
		t.Errorf("first filter = %q", f)
	}
	for _, field := range []string{"apiVersion", "metadata.title", "metadata.description", "spec.type"} {
		if !strings.Contains(first.Get("fields"), field) {
			t.Errorf("fields %q lack %s", first.Get("fields"), field)
		}
	}
	if queries[1].Get("cursor") != "p2" || queries[1].Has("filter") {
		t.Errorf("cursor page query = %v", queries[1])
	}
}

// TestBackstageSourceTemplatesLinkOnlyConfigured: only a configured catalog
// gives a scaffolder link and lets a person accept a template.
func TestBackstageSourceTemplatesLinkOnlyConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(templatePage("ops", "go-http", 1, ""))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	col, _ := newTestBackstage(t, srv, "").Collect(t.Context())
	if len(col.InferredTemplates) != 1 {
		t.Fatalf("configured templates = %+v", col.InferredTemplates)
	}
	got := col.InferredTemplates[0]
	if got.URL != srv.URL+"/create/templates/ops/go-http" || !got.Acceptable || got.From != "Backstage ("+host+")" ||
		got.Entity != "template:ops/go-http" || got.ObservedAt == nil || !got.ObservedAt.Equal(backstageNow) {
		t.Errorf("configured template = %+v", got)
	}

	detected, err := NewDetectedBackstageSource("backstage-catalog", srv.URL, "detected", func() time.Time { return backstageNow })
	if err != nil {
		t.Fatal(err)
	}
	col, _ = detected.Collect(t.Context())
	if len(col.InferredTemplates) != 1 {
		t.Fatalf("detected templates = %+v", col.InferredTemplates)
	}
	if got := col.InferredTemplates[0]; got.URL != "" || got.Acceptable || got.From != "Backstage (in-cluster)" {
		t.Errorf("detected template = %+v", got)
	}
}

// TestBackstageSourceListsTemplatesWhenUnavailable: a failed read still says
// the source lists templates, so it never reads as "no templates".
func TestBackstageSourceListsTemplatesWhenUnavailable(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	for name, h := range map[string]http.HandlerFunc{
		"401":       func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"404":       func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"oversized": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, maxBackstageBodyBytes+1)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			col, _ := newTestBackstage(t, srv, "").Collect(t.Context())
			if !col.ListsTemplates || col.State.Status != fleet.SourceUnavailable || len(col.InferredTemplates) != 0 {
				t.Errorf("collection = lists %v, %s, %d templates", col.ListsTemplates, col.State.Status, len(col.InferredTemplates))
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		col, _ := newTestBackstage(t, gone, "").Collect(context.Background())
		if !col.ListsTemplates || col.State.Status != fleet.SourceUnavailable || len(col.InferredTemplates) != 0 {
			t.Errorf("collection = lists %v, %s", col.ListsTemplates, col.State.Status)
		}
	})
}

// TestBackstageSourceTemplatesOnCappedRead: a read capped at the component
// bound keeps the templates of every page read and stays partial.
func TestBackstageSourceTemplatesOnCappedRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := 0
		_, _ = fmt.Sscanf(r.URL.Query().Get("cursor"), "p%d", &n)
		_, _ = w.Write(templatePage("", fmt.Sprintf("t%d", n), backstagePageSize, fmt.Sprintf("p%d", n+1)))
	}))
	defer srv.Close()
	col, _ := newTestBackstage(t, srv, "").Collect(t.Context())
	if col.State.Status != fleet.SourcePartial || len(col.ServiceFacts) != backstage.MaxCatalogEntities || len(col.InferredTemplates) != maxBackstagePages {
		t.Errorf("collection = %s, %d facts, %d templates", col.State.Status, len(col.ServiceFacts), len(col.InferredTemplates))
	}
}
