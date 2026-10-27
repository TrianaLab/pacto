package authn

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func newTestOIDC(t *testing.T, f *fakeIdP) *OIDC {
	t.Helper()
	o, err := NewOIDC(Config{IssuerURL: f.srv.URL, ClientID: "pacto-dashboard", ClientSecret: "s",
		RedirectURL: "https://dash.example/auth/callback", ExtraScopes: []string{"audience:server:client_id:pacto-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func cookieNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", name, rec.Result().Cookies())
	return nil
}

// login runs /auth/login and /auth/callback and returns the callback's answer.
func login(t *testing.T, o *OIDC, f *fakeIdP, query string, extra ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login?"+query, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	code, q := f.authorize(t, rec.Header().Get("Location"), "u-1")
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code="+code+"&state="+q.Get("state"), nil)
	req.AddCookie(cookieNamed(t, rec, loginCookie))
	for _, c := range extra {
		req.AddCookie(c)
	}
	cb := httptest.NewRecorder()
	o.Callback(cb, req)
	return cb
}

func withCookie(c *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	r.AddCookie(c)
	return r
}

func TestNewOIDCNeedsTheBasics(t *testing.T) {
	if _, err := NewOIDC(Config{IssuerURL: "https://x"}); err == nil {
		t.Fatal("a client id and redirect URL are required")
	}
}

func TestLoginAsksForWhatTheDesignNeeds(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	_, q := f.authorize(t, rec.Header().Get("Location"), "u")
	if q.Get("code_challenge_method") != "S256" || q.Get("max_age") != "43200" || q.Get("prompt") != "" ||
		strings.Contains(q.Get("scope"), "offline_access") || q.Get("scope") != "openid profile groups audience:server:client_id:pacto-cli" {
		t.Fatalf("authorize query %v", q)
	}
	c := cookieNamed(t, rec, loginCookie)
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("login cookie %+v", c)
	}
	rec = httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login?fresh=1&return=/%23/delivery", nil))
	if _, q = f.authorize(t, rec.Header().Get("Location"), "u"); q.Get("max_age") != "0" || q.Get("prompt") != "login" {
		t.Fatalf("a fresh login must force the person to log in again: %v", q)
	}
	for _, bad := range []string{"https://evil.example", "//evil.example", "/\\evil", "relative", "/x%0d%0a"} {
		rec = httptest.NewRecorder()
		o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login?return="+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("return %q must be refused, got %d", bad, rec.Code)
		}
	}
	down, _ := NewOIDC(Config{IssuerURL: "http://127.0.0.1:1", ClientID: "c", RedirectURL: "https://d/cb"})
	rec = httptest.NewRecorder()
	down.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("an unreachable provider is a 502, got %d", rec.Code)
	}
}

func TestCallbackCreatesASession(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	cb := login(t, o, f, "return=/x")
	expect(t, cb.Code == http.StatusFound && cb.Header().Get("Location") == "/x", "callback %d %s", cb.Code, cb.Body)
	c := cookieNamed(t, cb, SessionCookie)
	expect(t, c.Secure && c.HttpOnly && c.SameSite == http.SameSiteLaxMode, "session cookie %+v", c)
	expect(t, c.Path == "/" && len(c.Value) == 64, "session cookie %+v", c)
	s := o.Session(withCookie(c))
	expect(t, s != nil && !s.Fresh, "session %+v", s)
	expect(t, s.IDToken() != "" && s.CSRF != "", "session %+v", s)
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "session", s)
	for _, secret := range []string{s.IDToken(), s.CSRF, c.Value} {
		expect(t, !strings.Contains(buf.String(), secret), "a session leaked a secret into a log: %s", buf.String())
	}
}

func TestFreshLoginRotatesTheSession(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	nc := cookieNamed(t, login(t, o, f, "fresh=1", c), SessionCookie)
	expect(t, nc.Value != c.Value, "a fresh login must rotate the session id")
	expect(t, o.Session(withCookie(c)) == nil, "the old session must be invalidated")
	s := o.Session(withCookie(nc))
	expect(t, s != nil && s.Fresh, "the fresh session %+v", s)
	expect(t, len(f.revoked) == 1 && f.revoked[0] != "", "the old session's refresh token must be revoked: %v", f.revoked)
}

func TestCallbackRefusals(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	rec := httptest.NewRecorder()
	o.Callback(rec, httptest.NewRequest(http.MethodGet, "/auth/callback?state=x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no login cookie: %d", rec.Code)
	}
	// A tampered, expired or mismatched login cookie.
	start := func() (*http.Cookie, string) {
		r := httptest.NewRecorder()
		o.Login(r, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
		_, q := f.authorize(t, r.Header().Get("Location"), "u")
		return cookieNamed(t, r, loginCookie), q.Get("state")
	}
	call := func(c *http.Cookie, query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/auth/callback?"+query, nil)
		r.AddCookie(c)
		out := httptest.NewRecorder()
		o.Callback(out, r)
		return out
	}
	c, st := start()
	bad := *c
	bad.Value = strings.Replace(bad.Value, ".", ".x", 1)
	if call(&bad, "state="+st).Code != http.StatusBadRequest {
		t.Fatal("a tampered login cookie is refused")
	}
	if call(c, "state=other").Code != http.StatusBadRequest {
		t.Fatal("a state mismatch is refused")
	}
	if r := call(c, "state="+st+"&error=access_denied"); r.Code != http.StatusUnauthorized || !strings.Contains(r.Body.String(), "access_denied") {
		t.Fatalf("the IdP's refusal is passed on: %d %s", r.Code, r.Body)
	}
	if call(c, "state="+st+"&code=unknown").Code != http.StatusUnauthorized {
		t.Fatal("a code that does not exchange is refused")
	}
	o.now = func() time.Time { return time.Now().Add(time.Hour) }
	if call(c, "state="+st).Code != http.StatusBadRequest {
		t.Fatal("a login that took longer than ten minutes is refused")
	}
	o.now = time.Now
	// An ID token for someone else's sign-in (wrong nonce).
	c, st = start()
	f.mu.Lock()
	g := f.codes["c"+st]
	g.nonce = "other"
	f.codes["c"+st] = g
	f.mu.Unlock()
	if r := call(c, "code=c"+st+"&state="+st); r.Code != http.StatusUnauthorized || !strings.Contains(r.Body.String(), "not for this sign-in") {
		t.Fatalf("nonce: %d %s", r.Code, r.Body)
	}
	// No auth_time.
	f.noAuthTime = true
	c, st = start()
	if r := call(c, "code=c"+st+"&state="+st); !strings.Contains(r.Body.String(), "auth_time") {
		t.Fatalf("auth_time: %d %s", r.Code, r.Body)
	}
	f.noAuthTime = false
	// A fresh login the IdP did not actually repeat.
	f.authTime = time.Now().Add(-time.Hour)
	if r := login(t, o, f, "fresh=1"); r.Code != http.StatusUnauthorized || !strings.Contains(r.Body.String(), "did not ask you to log in again") {
		t.Fatalf("stale fresh login: %d %s", r.Code, r.Body)
	}
	f.authTime = time.Time{}
	// An ID token signed by someone else.
	c, st = start()
	f.mu.Lock()
	f.refreshID = "x"
	f.mu.Unlock()
	other := newIdP(t)
	f.signer = other.signer
	if r := call(c, "code=c"+st+"&state="+st); !strings.Contains(r.Body.String(), "does not verify") {
		t.Fatalf("signature: %d %s", r.Code, r.Body)
	}
	// The provider went away between login and callback (a fresh client).
	o2, _ := NewOIDC(Config{IssuerURL: "http://127.0.0.1:1", ClientID: "c", RedirectURL: "https://d/cb"})
	o2.key = o.key
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?state="+st, nil)
	r.AddCookie(c)
	out := httptest.NewRecorder()
	o2.Callback(out, r)
	if out.Code != http.StatusBadGateway {
		t.Fatalf("provider down at callback: %d", out.Code)
	}
}

func TestSessionLifetimes(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	if o.Session(httptest.NewRequest(http.MethodGet, "/", nil)) != nil || o.Session(withCookie(sessionCookie("nope", 1))) != nil {
		t.Fatal("no cookie or an unknown id is not signed in")
	}
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	base := time.Now()
	o.now = func() time.Time { return base.Add(59 * time.Minute) }
	if o.Session(withCookie(c)) == nil {
		t.Fatal("still inside the idle timeout")
	}
	// The ID token is about to expire: it is refreshed, auth_time unchanged.
	before := o.sessions[c.Value].idToken
	o.now = func() time.Time { return base.Add(59*time.Minute + 30*time.Second) }
	if s := o.Session(withCookie(c)); s == nil || s.IDToken() == before || o.sessions[c.Value].refresh != "rt-next" {
		t.Fatal("a token close to expiry is refreshed")
	}
	// Idle for over an hour: gone, and its refresh token revoked.
	o.now = func() time.Time { return base.Add(3 * time.Hour) }
	if o.Session(withCookie(c)) != nil || len(f.revoked) == 0 || f.revoked[len(f.revoked)-1] != "rt-next" {
		t.Fatalf("idle timeout: revoked %v", f.revoked)
	}
	// Absolute timeout, swept on the next login even if never seen again.
	o.now = time.Now
	c = cookieNamed(t, login(t, o, f, ""), SessionCookie)
	o.sessions[c.Value].created = time.Now().Add(-13 * time.Hour)
	n := len(f.revoked)
	_ = login(t, o, f, "")
	if _, ok := o.sessions[c.Value]; ok || len(f.revoked) != n+1 {
		t.Fatal("an expired session is swept and revoked")
	}
}

func TestSessionRefreshFailures(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	s := o.sessions[c.Value]
	// Within the margin but not expired: a failed refresh keeps the session.
	s.expiry = time.Now().Add(30 * time.Second)
	f.tokenStatus = http.StatusBadRequest
	if o.Session(withCookie(c)) == nil {
		t.Fatal("a failed refresh before expiry keeps the session")
	}
	// Expired and the refresh fails: signed out.
	s.expiry = time.Now().Add(-time.Second)
	if o.Session(withCookie(c)) != nil {
		t.Fatal("an expired token that cannot be refreshed signs out")
	}
	f.tokenStatus = 0
	// A refreshed ID token that does not verify, and no refresh token at all.
	c = cookieNamed(t, login(t, o, f, ""), SessionCookie)
	s = o.sessions[c.Value]
	s.expiry = time.Now().Add(-time.Second)
	f.refreshID = "garbage"
	if o.Session(withCookie(c)) != nil {
		t.Fatal("an unverifiable refresh signs out")
	}
	f.refreshID = ""
	c = cookieNamed(t, login(t, o, f, ""), SessionCookie)
	o.sessions[c.Value].refresh = ""
	o.sessions[c.Value].expiry = time.Now().Add(-time.Second)
	if o.Session(withCookie(c)) != nil {
		t.Fatal("no refresh token and expired signs out")
	}
	// The provider cannot be read for the refresh.
	o2, _ := NewOIDC(Config{IssuerURL: "http://127.0.0.1:1", ClientID: "c", RedirectURL: "https://d/cb"})
	if err := o2.refreshSession(httptest.NewRequest(http.MethodGet, "/", nil), &Session{refresh: "x"}); err == nil {
		t.Fatal("a refresh with no provider fails")
	}
}

func TestConcurrentRequestsRefreshOnce(t *testing.T) {
	f := newIdP(t)
	f.rotate = true
	o := newTestOIDC(t, f)
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	o.mu.Lock()
	o.sessions[c.Value].expiry = time.Now().Add(30 * time.Second)
	o.mu.Unlock()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if o.Session(withCookie(c)) == nil {
				t.Error("a request lost the session")
			}
		}()
	}
	wg.Wait()
	o.mu.Lock()
	defer o.mu.Unlock()
	if f.refreshCalls != 1 || o.sessions[c.Value].refresh != "rt-1" {
		t.Fatalf("one refresh for all requests, keeping its token: %d calls, token %q", f.refreshCalls, o.sessions[c.Value].refresh)
	}
}

func TestRenewSeesOtherRequests(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	s := o.sessions[c.Value]
	r := withCookie(c)
	// Another request refreshed it while this one waited: no second refresh.
	if _, ok := o.renew(r, s, time.Now()); !ok || f.refreshCalls != 0 {
		t.Fatal("an already refreshed session is not refreshed again")
	}
	// Another request ended it: gone, and nothing is refreshed or revoked.
	o.end(r.Context(), c.Value)
	n := len(f.revoked)
	s.expiry = time.Now()
	if _, ok := o.renew(r, s, time.Now()); ok || f.refreshCalls != 0 || len(f.revoked) != n {
		t.Fatal("an ended session stays ended")
	}
}

func TestSweeperEndsSessionsNobodyComesBackFor(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	o.sweepEvery = 10 * time.Millisecond
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	o.mu.Lock()
	o.sessions[c.Value].seen = time.Now().Add(-2 * time.Hour)
	o.mu.Unlock()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		o.mu.Lock()
		_, live := o.sessions[c.Value]
		o.mu.Unlock()
		f.mu.Lock()
		revoked := len(f.revoked)
		f.mu.Unlock()
		if !live && revoked == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("an idle session is ended and revoked without another login")
		}
	}
}

func TestLogout(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	c := cookieNamed(t, login(t, o, f, ""), SessionCookie)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.AddCookie(c)
	NewShared(o, nil).Routes().ServeHTTP(rec, r)
	var out struct {
		LogoutURL string `json:"logoutURL"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if !strings.HasPrefix(out.LogoutURL, f.srv.URL+"/logout?") || strings.Contains(out.LogoutURL, "id_token_hint") ||
		!strings.Contains(out.LogoutURL, "client_id=") ||
		cookieNamed(t, rec, SessionCookie).MaxAge != -1 || o.Session(withCookie(c)) != nil || len(f.revoked) != 1 {
		t.Fatalf("logout %s revoked %v", rec.Body, f.revoked)
	}
	rec = httptest.NewRecorder()
	o.Logout(rec, httptest.NewRequest(http.MethodPost, "/auth/logout", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "logoutURL") {
		t.Fatalf("logout without a session %d %s", rec.Code, rec.Body)
	}
	// A provider without a revocation endpoint, and one that refuses it.
	f2 := newIdP(t)
	f2.noRevoke = true
	o2 := newTestOIDC(t, f2)
	c2 := cookieNamed(t, login(t, o2, f2, ""), SessionCookie)
	o2.end(r.Context(), c2.Value)
	if len(f2.revoked) != 0 {
		t.Fatal("no revocation endpoint, no revocation")
	}
	c = cookieNamed(t, login(t, o, f, ""), SessionCookie)
	f.srv.Config.Handler = http.NotFoundHandler()
	if s, _ := o.end(r.Context(), c.Value); s == nil {
		t.Fatal("a failed revocation still ends the session")
	}
}

func TestSafeReturnAndRESTConfig(t *testing.T) {
	if !SafeReturn("/#/delivery/inbox?x=1") || SafeReturn("/%zz") {
		t.Fatal("SafeReturn")
	}
	c := RESTConfig(&rest.Config{Host: "https://k", BearerToken: "sa-token", Username: "u",
		TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca"), CertData: []byte("cert")}}, "id-token")
	if c.Host != "https://k" || c.BearerToken != "id-token" || string(c.CAData) != "ca" || c.CertData != nil || c.Username != "" {
		t.Fatalf("the per-request config must carry only the cluster and the caller's token: %+v", c)
	}
}
