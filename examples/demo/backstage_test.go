package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoServesBackstageEntities: the demo's in-memory API serves the
// Backstage route offline, from its bundled fleet.
func TestDemoServesBackstageEntities(t *testing.T) {
	q := fleet.NewQuery(storyFleet(t))
	h := demoHandler(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/backstage/entities", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/yaml") ||
		!strings.Contains(body, "apiVersion: backstage.io/v1alpha1") || !strings.Contains(body, "kind: Component") {
		t.Fatalf("got %d %q %q", rec.Code, rec.Header().Get("Content-Type"), body)
	}
}
