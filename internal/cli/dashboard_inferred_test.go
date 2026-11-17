package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestInferredEnvNeverReachesAPI serves the Decision 9 snapshot through the
// dashboard API (it lives here because pkg/dashboard may not import the
// internal workload source that reads it).
func TestInferredEnvNeverReachesAPI(t *testing.T) {
	q := inferredQuery(t, true)
	mux := http.NewServeMux()
	dashboard.NewServer(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }}).
		RegisterOperations(humago.New(mux, dashboard.APIConfig()))
	for _, path := range []string{
		"/api/fleet/entities",
		"/api/fleet/entities/service?key=" + url.QueryEscape(string(fleet.NewServiceKey(billingName))),
		"/api/fleet/service?key=" + url.QueryEscape(billingName),
		"/api/fleet/services",
		"/api/fleet/snapshot",
		"/api/fleet/attention",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		body := rec.Body.String()
		expect(t, rec.Code == http.StatusOK && strings.Contains(body, "billing"), "%s: %d\n%s", path, rec.Code, body)
		for _, leak := range append(inferredLeakStrings, `\u001b`) {
			expect(t, !strings.Contains(body, leak), "%s holds %q:\n%s", path, leak, body)
		}
	}
}
