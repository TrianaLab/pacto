package authn

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// echo records the State the middleware passed on.
func echo(got *State) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = StateFrom(r.Context())
		w.WriteHeader(http.StatusTeapot)
	})
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// expect fails the test unless ok.
func expect(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

// launched is a local dashboard and the launch cookie its URL set.
func launched(t *testing.T) (*Dashboard, *http.Cookie) {
	t.Helper()
	d := NewLocal()
	rec := serve(d.Middleware(echo(&State{})), httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/?token="+d.launch+"&x=1", nil))
	return d, cookieNamed(t, rec, launchCookie)
}

func TestLocalLaunch(t *testing.T) {
	d := NewLocal()
	var got State
	h := d.Middleware(echo(&got))
	expect(t, d.Local(), "local")
	expect(t, strings.HasSuffix(d.LaunchURL("http://127.0.0.1:3000"), "/?token="+d.launch), "local launch URL")
	// The launch URL moves the token into an HttpOnly cookie and drops it from the address.
	rec := serve(h, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/?token="+d.launch+"&x=1", nil))
	c := cookieNamed(t, rec, launchCookie)
	expect(t, rec.Code == http.StatusSeeOther, "launch status %d", rec.Code)
	expect(t, rec.Header().Get("Location") == "/?x=1", "launch location %q", rec.Header().Get("Location"))
	expect(t, c.HttpOnly && c.SameSite == http.SameSiteStrictMode, "launch cookie %+v", c)
	expect(t, rec.Header().Get("X-Frame-Options") == "DENY", "every answer refuses framing")
	expect(t, rec.Header().Get("Content-Security-Policy") == "frame-ancestors 'none'", "every answer refuses framing")
	rec = serve(h, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/?token=wrong", nil))
	expect(t, rec.Code == http.StatusForbidden, "a wrong launch token is refused")
	// Reads outside delivery pass without the cookie.
	rec = serve(h, httptest.NewRequest(http.MethodGet, "http://evil.example/api/fleet/snapshot", nil))
	expect(t, rec.Code == http.StatusTeapot && !got.SignedIn, "fleet reads are unchanged")
	expect(t, serve(d.Routes(), httptest.NewRequest(http.MethodGet, "/auth/login", nil)).Code == http.StatusNotFound, "local mode has no login")
}

func TestLocalGuards(t *testing.T) {
	d, c := launched(t)
	var got State
	h := d.Middleware(echo(&got))
	req := func(method, host string, withCookie bool, csrf, origin string) *http.Request {
		r := httptest.NewRequest(method, "http://"+host+"/api/delivery/operations", nil)
		if withCookie {
			r.AddCookie(c)
		}
		if csrf != "" {
			r.Header.Set(CSRFHeader, csrf)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		want int
	}{
		{"rebinding host", req(http.MethodGet, "evil.example:3000", true, "", ""), http.StatusForbidden},
		{"read on loopback", req(http.MethodGet, "localhost:3000", false, "", ""), http.StatusTeapot},
		{"action without the cookie", req(http.MethodPost, "127.0.0.1:3000", false, d.launchCSRF, ""), http.StatusForbidden},
		{"action without csrf", req(http.MethodPost, "127.0.0.1:3000", true, "", ""), http.StatusForbidden},
		{"action cross-origin", req(http.MethodPost, "127.0.0.1:3000", true, d.launchCSRF, "http://evil.example"), http.StatusForbidden},
		{"action", req(http.MethodPost, "[::1]:3000", true, d.launchCSRF, "http://[::1]:3000"), http.StatusTeapot},
	}
	for _, tc := range cases {
		code := serve(h, tc.r).Code
		expect(t, code == tc.want, "%s: %d, want %d", tc.name, code, tc.want)
	}
	serve(h, req(http.MethodGet, "127.0.0.1", true, "", ""))
	expect(t, got.SignedIn && got.CSRF == d.launchCSRF, "local state %+v", got)
	expect(t, got.CSRF != d.launch && got.Token == "", "local state %+v must never carry the launch token", got)
}

func TestSharedMode(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	d := NewShared(o)
	var got State
	h := d.Middleware(echo(&got))
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	sess := o.sessions[c.Value]
	if d.Local() || serve(d.Routes(), httptest.NewRequest(http.MethodGet, "/auth/login", nil)).Code != http.StatusFound {
		t.Fatal("shared mode serves the login")
	}
	r := httptest.NewRequest(http.MethodGet, "https://dash.example/api/session", nil)
	r.AddCookie(c)
	serve(h, r)
	if !got.SignedIn || got.Fresh || got.CSRF != sess.CSRF || got.Token != sess.idToken {
		t.Fatalf("shared state %+v", got)
	}
	act := func(path string, cookie bool, csrf, origin string) int {
		r := httptest.NewRequest(http.MethodPost, "https://dash.example"+path, nil)
		if cookie {
			r.AddCookie(c)
		}
		if csrf != "" {
			r.Header.Set(CSRFHeader, csrf)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return serve(h, r).Code
	}
	cases := []struct {
		name string
		code int
		want int
	}{
		{"no origin", act("/api/delivery/plan", true, sess.CSRF, ""), http.StatusForbidden},
		{"cross origin", act("/api/delivery/plan", true, sess.CSRF, "https://evil.example"), http.StatusForbidden},
		{"no csrf", act("/api/delivery/plan", true, "", "https://dash.example"), http.StatusForbidden},
		{"wrong csrf on logout", act("/auth/logout", true, "x", "https://dash.example"), http.StatusForbidden},
		{"ok", act("/api/delivery/plan", true, sess.CSRF, "https://dash.example"), http.StatusTeapot},
		// The fleet's own POSTs carry the session cookie too, so the same check:
		// the UI sends the token on every mutating request.
		{"refresh without csrf", act("/api/refresh", true, "", "https://dash.example"), http.StatusForbidden},
		{"refresh", act("/api/refresh", true, sess.CSRF, "https://dash.example"), http.StatusTeapot},
		{"impact", act("/api/fleet/impact", true, sess.CSRF, "https://dash.example"), http.StatusTeapot},
		{"no cookie carries no rights, so no check", act("/api/delivery/plan", false, "", ""), http.StatusTeapot},
		{"non-api path", act("/somewhere", true, "", ""), http.StatusTeapot},
	}
	for _, tc := range cases {
		if tc.code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, tc.code, tc.want)
		}
	}
}

func TestOpenMode(t *testing.T) {
	d := NewOpen()
	var got State
	rec := serve(d.Middleware(echo(&got)), httptest.NewRequest(http.MethodPost, "http://dash.example/api/delivery/plan", nil))
	if d.Local() || d.Login() || rec.Code != http.StatusTeapot || got.SignedIn || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("open mode identifies nobody and still refuses framing: %d %+v", rec.Code, got)
	}
	if serve(d.Routes(), httptest.NewRequest(http.MethodGet, "/auth/login", nil)).Code != http.StatusNotFound {
		t.Fatal("open mode has no login")
	}
}

func TestSharedModeRequired(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	d := NewShared(o).RequireLogin()
	h := d.Middleware(echo(&State{}))
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	get := func(path string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "https://dash.example"+path, nil)
		if cookie {
			r.AddCookie(c)
		}
		return serve(h, r)
	}
	for path, want := range map[string]int{
		"/api/fleet/status": http.StatusUnauthorized, "/api/refresh": http.StatusUnauthorized, "/api/delivery/stages": http.StatusUnauthorized,
		"/health": http.StatusTeapot, "/api/session": http.StatusTeapot, "/auth/login": http.StatusTeapot,
	} {
		if got := get(path, false).Code; got != want {
			t.Errorf("signed out %s: %d, want %d", path, got, want)
		}
		if got := get(path, true).Code; got != http.StatusTeapot {
			t.Errorf("signed in %s: %d, want through", path, got)
		}
	}
	rec := get("/services?q=a", false)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/auth/login?return=%2Fservices%3Fq%3Da" {
		t.Fatalf("a signed-out page goes to the login and back: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := serve(NewShared(o).Middleware(echo(&State{})), httptest.NewRequest(http.MethodGet, "https://dash.example/api/fleet/status", nil)).Code; got != http.StatusTeapot {
		t.Fatalf("without RequireLogin the fleet stays open: %d", got)
	}
}
