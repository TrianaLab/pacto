package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const backstageReaderToken = "reader-token-value"

// ordersCatalog serves one by-query page naming orders, owned by team-orders,
// and records the Authorization header it got.
func ordersCatalog(t *testing.T, auth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"items":[{"kind":"Component","metadata":{"name":"orders"},"spec":{"owner":"group:team-orders"}}],"pageInfo":{}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFleetGetBackstageURLJSON(t *testing.T) {
	root, _ := writeFleetFixture(t)
	var auth string
	t.Setenv("PACTO_BACKSTAGE_TOKEN", backstageReaderToken)
	out, _, err := execFleet(t, "fleet", "get", "orders", "--local", root, "--backstage-url", ordersCatalog(t, &auth).URL, "--output-format", "json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var view struct {
		Service struct {
			InferredOwner *struct{ Value, Source string } `json:"inferredOwner"`
		} `json:"service"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if o := view.Service.InferredOwner; o == nil || o.Value != "team:team-orders" || o.Source != "backstage-catalog" {
		t.Fatalf("inferredOwner = %+v\n%s", o, out)
	}
	if auth != "Bearer "+backstageReaderToken {
		t.Errorf("Authorization = %q, want the token from PACTO_BACKSTAGE_TOKEN", auth)
	}
	if strings.Contains(out, backstageReaderToken) {
		t.Error("the answer carries the reader token")
	}
}

func TestBackstageTokenNeverLeaks(t *testing.T) {
	root, _ := writeFleetFixture(t)
	t.Setenv("PACTO_BACKSTAGE_TOKEN", backstageReaderToken)
	// Refused over plain http to a remote host: the error names neither the
	// token nor the URL's path.
	out, errOut, err := execFleet(t, "fleet", "status", "--local", root, "--backstage-url", "http://backstage.example.com/catalog", "--output-format", "json")
	if err == nil || strings.Contains(out+errOut+err.Error(), backstageReaderToken) {
		t.Fatalf("status = %v, %q, %q; want a refusal without the token", err, out, errOut)
	}
	// An unreachable catalog: the source is unavailable and the state names
	// no token.
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	out, errOut, err = execFleet(t, "fleet", "status", "--local", root, "--backstage-url", srv.URL, "--output-format", "json")
	if err != nil || !strings.Contains(out, `"backstage-catalog"`) || strings.Contains(out+errOut, backstageReaderToken) {
		t.Fatalf("status = %v\n%s\n%s", err, out, errOut)
	}
	help, _, _ := execFleet(t, "fleet", "--help")
	if !strings.Contains(help, "--backstage-url") || strings.Contains(help, backstageReaderToken) {
		t.Errorf("help:\n%s", help)
	}
}
