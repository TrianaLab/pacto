package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/logging"
)

// logged returns a request whose context logs into logs.
func logged(r *http.Request, logs *strings.Builder) *http.Request {
	return r.WithContext(logging.WithLogger(r.Context(), logging.New(logs, false)))
}

func has(t *testing.T, logs *strings.Builder, want ...string) {
	t.Helper()
	for _, w := range want {
		expect(t, strings.Contains(logs.String(), w), "the log lacks %q:\n%s", w, logs)
	}
	logs.Reset()
}

// Every refusal is logged with where it came from and why, never a token.
func TestRefusalsAreLogged(t *testing.T) {
	logs := &strings.Builder{}
	f := newIdP(t)

	m := newTestMCP(t, f, "pacto-mcp")
	bad := f.accessToken("pacto-cli", time.Now().Add(time.Hour))
	r := httptest.NewRequest(http.MethodPost, "http://mcp.example/mcp", nil)
	r.Header.Set("X-Request-Id", "req-7")
	_, _ = m.Verify(logged(r, logs).Context(), bad, r)
	expect(t, !strings.Contains(logs.String(), bad), "the log carries the token:\n%s", logs)
	has(t, logs, "remote MCP refused a bearer token", "issuer="+f.srv.URL, "audience=pacto-mcp", "reason=", "request=req-7", "path=/mcp")
	_, _ = m.Verify(logging.WithLogger(context.Background(), logging.New(logs, false)), "x.y.z", nil)
	has(t, logs, "remote MCP refused a bearer token")

	o := newTestOIDC(t, f)
	serve(http.HandlerFunc(o.Callback), logged(httptest.NewRequest(http.MethodGet, "https://dash.example/auth/callback?state=x", nil), logs))
	has(t, logs, "dashboard refused a sign-in", "client=pacto-dashboard", "reason=\"this sign-in was not started here")

	d, _ := launched(t)
	h := d.Middleware(echo(&State{}))
	serve(h, logged(httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/?token=guess", nil), logs))
	expect(t, !strings.Contains(logs.String(), "guess"), "the log carries the token:\n%s", logs)
	has(t, logs, "local dashboard refused a launch token")
	serve(h, logged(httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/api/delivery/operations", nil), logs))
	has(t, logs, "local dashboard refused a delivery request", "launch token")

	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	s := NewShared(o, nil).Middleware(echo(&State{}))
	req := httptest.NewRequest(http.MethodPost, "https://dash.example/api/delivery/operations", nil)
	req.AddCookie(c)
	req.Header.Set("Origin", "https://evil.example")
	serve(s, logged(req, logs))
	has(t, logs, "dashboard refused a signed-in request", "origin=https://evil.example")
	req = httptest.NewRequest(http.MethodPost, "https://dash.example/api/delivery/operations", nil)
	req.AddCookie(c)
	req.Header.Set("Origin", "https://dash.example")
	serve(s, logged(req, logs))
	has(t, logs, "dashboard refused a signed-in request", "X-Pacto-CSRF")
}
