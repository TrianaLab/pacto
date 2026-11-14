package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestAPIHandlerServesRoutesWithoutUI: the API handler answers the API and
// nothing else: no UI files and no auth routes.
func TestAPIHandlerServesRoutesWithoutUI(t *testing.T) {
	q := demoFleetQuery(t)
	h := NewServer(Options{UI: testUI(), Fleet: func(context.Context) (*fleet.Query, error) { return q, nil },
		Auth: &Auth{Routes: http.NotFoundHandler()}}).APIHandler()
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/api/fleet/services"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"asOf"`) {
		t.Fatalf("fleet route: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/", "/index.html", "/auth/login"} {
		if rec := get(path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
}
