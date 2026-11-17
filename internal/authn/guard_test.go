package authn

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
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
	// Secure everywhere but plain HTTP on loopback.
	for url, secure := range map[string]bool{
		"http://127.0.0.1:3000/":         false,
		"http://localhost:3000/":         false,
		"https://127.0.0.1:3000/":        true,
		"http://dashboard.example:3000/": true,
		"https://dashboard.example/":     true,
	} {
		c := cookieNamed(t, serve(h, httptest.NewRequest(http.MethodGet, url+"?token="+d.launch, nil)), launchCookie)
		expect(t, c.Secure == secure, "launch cookie from %s: Secure=%v, want %v", url, c.Secure, secure)
	}
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
		{"read without the cookie", req(http.MethodGet, "localhost:3000", false, "", ""), http.StatusForbidden},
		{"read on loopback", req(http.MethodGet, "localhost:3000", true, "", ""), http.StatusTeapot},
		{"action without the cookie", req(http.MethodPost, "127.0.0.1:3000", false, d.launchCSRF, ""), http.StatusForbidden},
		{"action without csrf", req(http.MethodPost, "127.0.0.1:3000", true, "", ""), http.StatusForbidden},
		{"action cross-origin", req(http.MethodPost, "127.0.0.1:3000", true, d.launchCSRF, "http://evil.example"), http.StatusForbidden},
		{"action", req(http.MethodPost, "[::1]:3000", true, d.launchCSRF, "http://[::1]:3000"), http.StatusTeapot},
	}
	for _, tc := range cases {
		code := serve(h, tc.r).Code
		expect(t, code == tc.want, "%s: %d, want %d", tc.name, code, tc.want)
	}
	session := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/api/session", nil)
	expect(t, serve(h, session).Code == http.StatusTeapot, "the session check stays open signed out")
	serve(h, req(http.MethodGet, "127.0.0.1", true, "", ""))
	expect(t, got.SignedIn && got.CSRF == d.launchCSRF, "local state %+v", got)
	expect(t, got.CSRF != d.launch && got.Token == "", "local state %+v must never carry the launch token", got)
}

func TestSharedMode(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	d := NewShared(o, nil)
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
	if !got.SignedIn || got.CSRF != sess.CSRF || got.Token != sess.idToken {
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
		{"no session is turned away before any check", act("/api/delivery/plan", false, "", ""), http.StatusUnauthorized},
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
	if d.Local() || d.Login() || rec.Code != http.StatusTeapot || got.SignedIn {
		t.Fatalf("open mode identifies nobody: %d %+v", rec.Code, got)
	}
	if serve(d.Routes(), httptest.NewRequest(http.MethodGet, "/auth/login", nil)).Code != http.StatusNotFound {
		t.Fatal("open mode has no login")
	}
}

func TestSharedModeGatesTheAPI(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	h := NewShared(o, nil).Middleware(echo(&State{}))
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	get := func(path string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "https://dash.example"+path, nil)
		if cookie {
			r.AddCookie(c)
		}
		return serve(h, r)
	}
	for path, want := range map[string]int{
		"/api/fleet/status": http.StatusUnauthorized, "/api/fleet/product/services": http.StatusUnauthorized,
		"/api/capabilities": http.StatusUnauthorized, "/api/sources": http.StatusUnauthorized,
		"/api/refresh": http.StatusUnauthorized, "/api/delivery/stages": http.StatusUnauthorized, "/metrics": http.StatusUnauthorized,
		"/api/backstage/entities": http.StatusUnauthorized, "/health": http.StatusTeapot,
		"/api/session": http.StatusTeapot, "/auth/login": http.StatusTeapot,
		"/": http.StatusTeapot, "/assets/index.js": http.StatusTeapot,
	} {
		out := get(path, false)
		if out.Code != want {
			t.Errorf("signed out %s: %d, want %d", path, out.Code, want)
		}
		if want == http.StatusUnauthorized && (out.Header().Get("Content-Type") != "application/json" ||
			out.Body.String() != `{"status":401,"code":"LoginRequired","detail":"sign in to use this dashboard"}`+"\n") {
			t.Errorf("signed out %s: every 401 is the LoginRequired refusal: %s %q", path, out.Header(), out.Body)
		}
		if got := get(path, true).Code; got != http.StatusTeapot {
			t.Errorf("signed in %s: %d, want through", path, got)
		}
	}
}

// A link session holds no bearer, so a client built for it is anonymous: it
// never carries the dashboard's own credentials.
func TestLinkSessionCarriesNoBearer(t *testing.T) {
	d, _ := dashboardRoutes(Reviewed{Person: viewerSA}, nil, Reviewed{}, errors.New("Unauthorized"))
	st := stateOf(t, d, signIn(d, `{"token":"t","link":true}`))
	expect(t, st.SignedIn && st.Token == "", "a link session's state %+v", st)
	own := &rest.Config{Host: "https://k", BearerToken: "dashboard", Username: "u", Password: "p", BearerTokenFile: "/var/run/token",
		TLSClientConfig: rest.TLSClientConfig{CertData: []byte("c"), KeyData: []byte("k")}}
	c := RESTConfig(own, st.Token)
	expect(t, c.BearerToken == "" && c.BearerTokenFile == "" && c.Username == "" && c.CertData == nil && c.KeyData == nil && c.Host == own.Host,
		"a link session's client is anonymous: %+v", c)
}

// bearerFake is a TokenReview that counts its calls: good is a credential,
// link a dashboard-audience token, noaud a typed refusal, empty a review
// naming nobody, anything else a plain error.
type bearerFake struct{ calls int }

func (f *bearerFake) review(_ context.Context, token string) (Reviewed, error) {
	f.calls++
	switch token {
	case "good":
		return Reviewed{Person: Person{User: "system:serviceaccount:pacto:backstage"}, Bearer: true}, nil
	case "link":
		return Reviewed{Person: Person{User: "system:serviceaccount:pacto:pacto-viewer"}}, nil
	case "noaud":
		return Reviewed{}, &TokenRefusal{Code: "TokenAudienceRequired", Message: "mint it with --audience pacto-dashboard"}
	case "empty":
		return Reviewed{}, nil
	}
	return Reviewed{}, errors.New("Unauthorized")
}

// bearerDashboard is shared mode (token sign-in only) accepting bearer readers.
func bearerDashboard(f *bearerFake) *Dashboard {
	return NewShared(nil, NewTokenLogin(f.review, Lifetime{Idle: time.Minute, Max: time.Hour})).AcceptBearer(f.review)
}

func bearerReq(method, path, token string) *http.Request {
	r := httptest.NewRequest(method, "https://dash.example"+path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestBearerReaderReadsBackstage(t *testing.T) {
	f := &bearerFake{}
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		var got State
		rec := serve(bearerDashboard(f).Middleware(echo(&got)), bearerReq(m, "/api/backstage/entities", "good"))
		expect(t, rec.Code == http.StatusTeapot, "%s: %d", m, rec.Code)
		expect(t, got.SignedIn && got.Person.User == "system:serviceaccount:pacto:backstage" && got.Method == "bearer" &&
			got.Token == "good" && got.CSRF == "", "%s state %+v", m, got)
	}
}

func TestBearerReaderLinkReviewCarriesNoToken(t *testing.T) {
	var got State
	serve(bearerDashboard(&bearerFake{}).Middleware(echo(&got)), bearerReq(http.MethodGet, "/api/backstage/entities", "link"))
	expect(t, got.SignedIn && got.Person.User == "system:serviceaccount:pacto:pacto-viewer" && got.Token == "", "link state %+v", got)
}

func TestBearerReaderNeverSetsACookie(t *testing.T) {
	rec := serve(bearerDashboard(&bearerFake{}).Middleware(echo(&State{})), bearerReq(http.MethodGet, "/api/backstage/entities", "good"))
	expect(t, rec.Header().Get("Set-Cookie") == "", "a bearer read set %q", rec.Header().Get("Set-Cookie"))
}

func TestBearerReaderRefusedToken(t *testing.T) {
	h := bearerDashboard(&bearerFake{}).Middleware(echo(&State{}))
	for token, want := range map[string]string{
		"bad":   `"code":"BearerRefused","detail":"the cluster did not accept this token"`,
		"empty": `"code":"BearerRefused"`,
		"noaud": `"code":"TokenAudienceRequired","detail":"mint it with --audience pacto-dashboard"`,
	} {
		rec := serve(h, bearerReq(http.MethodGet, "/api/backstage/entities", token))
		expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), want) &&
			rec.Header().Get("Content-Type") == "application/json", "%s: %d %s", token, rec.Code, rec.Body)
	}
}

func TestBearerReaderRefusedTokenNotLogged(t *testing.T) {
	logs := &strings.Builder{}
	h := bearerDashboard(&bearerFake{}).Middleware(echo(&State{}))
	serve(h, logged(bearerReq(http.MethodGet, "/api/backstage/entities", "s3cr3t-token"), logs))
	expect(t, !strings.Contains(logs.String(), "s3cr3t-token"), "the log carries the token:\n%s", logs)
	has(t, logs, "dashboard refused a bearer reader", "reason=", "path=/api/backstage/entities")
}

func TestBearerReaderOnlyOnBackstagePaths(t *testing.T) {
	f := &bearerFake{}
	var got State
	h := bearerDashboard(f).Middleware(echo(&got))
	for _, p := range []string{"/api/fleet/snapshot", "/api/delivery/stages", "/api/capabilities", "/metrics"} {
		rec := serve(h, bearerReq(http.MethodGet, p, "good"))
		expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "LoginRequired"), "%s: %d %s", p, rec.Code, rec.Body)
	}
	rec := serve(h, bearerReq(http.MethodGet, "/api/session", "good"))
	expect(t, rec.Code == http.StatusTeapot && !got.SignedIn && got.Method == "", "/api/session with a bearer: %d %+v", rec.Code, got)
	expect(t, f.calls == 0, "the reviewer was called %d times", f.calls)
}

func TestBearerReaderCannotTraverse(t *testing.T) {
	f := &bearerFake{}
	h := bearerDashboard(f).Middleware(echo(&State{}))
	for _, p := range []string{"/api/backstage/../fleet/snapshot", "/api/backstage/entities/../../fleet/services", "/api/backstage/%2e%2e/fleet/snapshot"} {
		rec := serve(h, bearerReq(http.MethodGet, p, "good"))
		expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "LoginRequired"), "%s: %d %s", p, rec.Code, rec.Body)
	}
	expect(t, f.calls == 0, "the reviewer was called %d times", f.calls)
}

func TestBearerReaderReadsOnly(t *testing.T) {
	f := &bearerFake{}
	h := bearerDashboard(f).Middleware(echo(&State{}))
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := serve(h, bearerReq(m, "/api/backstage/entities", "good"))
		expect(t, rec.Code == http.StatusMethodNotAllowed, "%s: %d", m, rec.Code)
	}
	expect(t, f.calls == 0, "the reviewer was called %d times", f.calls)
}

func TestBearerReaderWithoutReviewer(t *testing.T) {
	tl := NewTokenLogin((&bearerFake{}).review, Lifetime{Idle: time.Minute, Max: time.Hour})
	for name, d := range map[string]*Dashboard{"no AcceptBearer": NewShared(nil, tl), "AcceptBearer(nil)": NewShared(nil, tl).AcceptBearer(nil)} {
		rec := serve(d.Middleware(echo(&State{})), bearerReq(http.MethodGet, "/api/backstage/entities", "good"))
		expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "a cluster to check tokens against"),
			"%s: %d %s", name, rec.Code, rec.Body)
	}
}

func TestBearerIgnoredInOpenAndLocal(t *testing.T) {
	f := &bearerFake{}
	for name, d := range map[string]*Dashboard{"open": NewOpen().AcceptBearer(f.review), "local": NewLocal().AcceptBearer(f.review)} {
		var got State
		r := bearerReq(http.MethodGet, "/api/backstage/entities", "garbage")
		r.Host = "127.0.0.1:3000"
		rec := serve(d.Middleware(echo(&got)), r)
		expect(t, rec.Code == http.StatusTeapot && got.Method == "", "%s: %d %+v", name, rec.Code, got)
	}
	expect(t, f.calls == 0, "the reviewer was called %d times", f.calls)
}
