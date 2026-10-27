package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

func TestClaimsPerson(t *testing.T) {
	tok := fakeJWT(`{"iss":"https://idp","sub":"u1","email":"u1@example.com","preferred_username":"ann","groups":["a","system:masters"],"roles":"admins","n":7}`)
	for _, c := range []struct {
		claims Claims
		user   string
		groups []string
	}{
		{Claims{}, "https://idp#u1", []string{"a"}},
		{Claims{UsernameClaim: "email"}, "u1@example.com", []string{"a"}},
		{Claims{UsernameClaim: "preferred_username", UsernamePrefix: "oidc:"}, "oidc:ann", []string{"a"}},
		{Claims{UsernamePrefix: "-"}, "u1", []string{"a"}},
		{Claims{GroupsPrefix: "oidc:"}, "https://idp#u1", []string{"oidc:a", "oidc:system:masters"}},
		// A single group sent as a string.
		{Claims{GroupsClaim: "roles"}, "https://idp#u1", []string{"admins"}},
		{Claims{GroupsClaim: "missing"}, "https://idp#u1", []string{}},
	} {
		p := c.claims.Person(tok)
		if p.User != c.user || !slices.Equal(p.Groups, c.groups) {
			t.Errorf("%+v: %+v, want %s %v", c.claims, p, c.user, c.groups)
		}
	}
	// A claim that is no string names nobody.
	if p := (Claims{UsernameClaim: "n"}).Person(tok); p.User != Anonymous.User {
		t.Errorf("a number claim named %+v", p)
	}
}

// fakeSSR answers SelfSubjectReviews as the user it is given.
func fakeSSR(t *testing.T, user string, err error) func(*rest.Config) (dynamic.Interface, error) {
	t.Helper()
	return func(cfg *rest.Config) (dynamic.Interface, error) {
		if cfg.BearerToken == "" || cfg.Username != "" || cfg.TLSClientConfig.CertData != nil {
			t.Fatalf("the review runs with the pasted token alone: %+v", cfg)
		}
		c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
		c.PrependReactor("create", "selfsubjectreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if err != nil {
				return true, nil, err
			}
			obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
			_ = unstructured.SetNestedField(obj.Object, user, "status", "userInfo", "username")
			_ = unstructured.SetNestedStringSlice(obj.Object, []string{"team-a", "system:authenticated"}, "status", "userInfo", "groups")
			return true, obj, nil
		})
		return c, nil
	}
}

func TestSelfSubjectReview(t *testing.T) {
	cluster := &rest.Config{Host: "https://k", Username: "admin", TLSClientConfig: rest.TLSClientConfig{CertData: []byte("x")}}
	p, err := SelfSubjectReview(cluster, fakeSSR(t, "system:serviceaccount:a:b", nil))(context.Background(), "tok")
	expect(t, err == nil && p.User == "system:serviceaccount:a:b" && slices.Equal(p.Groups, []string{"team-a", "system:authenticated"}), "review %+v %v", p, err)
	_, err = SelfSubjectReview(cluster, fakeSSR(t, "", errors.New("Unauthorized")))(context.Background(), "tok")
	expect(t, err != nil, "a refused token is an error")
	_, err = SelfSubjectReview(nil, nil)(context.Background(), "tok")
	expect(t, err != nil, "no cluster, no review")
	_, err = SelfSubjectReview(cluster, func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("bad config") })(context.Background(), "tok")
	expect(t, err != nil, "a client that cannot be built is an error")
}

func tokenPost(body string, origin string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://dash.example/auth/token", strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func TestTokenLogin(t *testing.T) {
	var asked string
	review := func(_ context.Context, token string) (Person, error) {
		asked = token
		switch token {
		case "bad":
			return Person{}, errors.New("Unauthorized")
		case "nobody":
			return Person{}, nil
		}
		return Person{User: "system:serviceaccount:shop:viewer", Groups: []string{"system:serviceaccounts"}}, nil
	}
	tl := NewTokenLogin(review, Lifetime{Idle: time.Minute, Max: time.Hour})
	d := NewShared(nil, tl)
	expect(t, d.SignsIn() && d.TokenLogin() && !d.Login() && !d.Local(), "token-only shared mode")
	routes := d.Routes()
	for _, c := range []struct {
		name, body, origin string
		want               int
	}{
		{"cross origin", `{"token":"t"}`, "https://evil.example", http.StatusForbidden},
		{"no origin", `{"token":"t"}`, "", http.StatusForbidden},
		{"not json", `token=t`, "https://dash.example", http.StatusBadRequest},
		{"empty", `{"token":"  "}`, "https://dash.example", http.StatusBadRequest},
		{"refused", `{"token":"bad"}`, "https://dash.example", http.StatusUnauthorized},
		{"nobody", `{"token":"nobody"}`, "https://dash.example", http.StatusUnauthorized},
	} {
		if got := serve(routes, tokenPost(c.body, c.origin)).Code; got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	rec := serve(routes, tokenPost(`{"token":"Bearer good-token"}`, "https://dash.example"))
	expect(t, rec.Code == http.StatusNoContent && asked == "good-token", "sign-in %d with %q", rec.Code, asked)
	c := cookieNamed(t, rec, SessionCookie)
	expect(t, c.HttpOnly && c.Secure && c.MaxAge == 3600, "the session cookie %+v", c)

	var got State
	h := d.Middleware(echo(&got))
	r := httptest.NewRequest(http.MethodGet, "https://dash.example/api/fleet/services", nil)
	r.AddCookie(c)
	expect(t, serve(h, r).Code == http.StatusTeapot, "a token session passes the gate")
	expect(t, got.SignedIn && !got.Fresh && got.Method == "token" && got.Token == "good-token" &&
		got.Person.User == "system:serviceaccount:shop:viewer" && got.CSRF != "", "token state %+v", got)

	// Signing in again replaces the session; expired ones are dropped.
	again := serve(routes, tokenPost(`{"token":"good-token"}`, "https://dash.example", c))
	expect(t, len(tl.sessions) == 1, "the old session is gone: %d", len(tl.sessions))
	c = cookieNamed(t, again, SessionCookie)

	// Logout ends it and clears the cookie, with no identity provider to visit.
	r = httptest.NewRequest(http.MethodPost, "https://dash.example/auth/logout", nil)
	r.AddCookie(c)
	rec = serve(routes, r)
	var out map[string]any
	expect(t, json.Unmarshal(rec.Body.Bytes(), &out) == nil && len(out) == 0 && cookieNamed(t, rec, SessionCookie).MaxAge == -1, "logout %s", rec.Body)
	_, ok := tl.session(withCookie(c))
	expect(t, !ok, "the session is gone after logout")
	expect(t, serve(routes, httptest.NewRequest(http.MethodGet, "/auth/login", nil)).Code == http.StatusNotFound, "no OIDC, no /auth/login")
}

func TestTokenSessionLifetime(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tl := NewTokenLogin(func(context.Context, string) (Person, error) { return Person{User: "u"}, nil }, Lifetime{Idle: time.Minute, Max: 3 * time.Minute})
	tl.now = func() time.Time { return now }
	login := func() *http.Cookie {
		return cookieNamed(t, serve(http.HandlerFunc(tl.Login), tokenPost(`{"token":"t"}`, "https://dash.example")), SessionCookie)
	}
	c := login()
	for i := 0; i < 2; i++ { // seen within the idle timeout, twice
		now = now.Add(50 * time.Second)
		_, ok := tl.session(withCookie(c))
		expect(t, ok, "a session in use lives on")
	}
	now = now.Add(2 * time.Minute)
	_, ok := tl.session(withCookie(c))
	expect(t, !ok, "an idle session ends")
	c = login()
	for i := 0; i < 4; i++ {
		now = now.Add(50 * time.Second)
		tl.session(withCookie(c))
	}
	_, ok = tl.session(withCookie(c))
	expect(t, !ok, "a session ends at its maximum age however busy")
	_, ok = tl.session(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, !ok, "no cookie, no session")
	// A sign-in sweeps sessions that ended.
	stale := login()
	now = now.Add(time.Hour)
	login()
	_, ok = tl.sessions[stale.Value]
	expect(t, !ok && len(tl.sessions) == 1, "an ended session is swept at the next sign-in: %d", len(tl.sessions))
	tl.end(httptest.NewRequest(http.MethodPost, "/", nil)) // no cookie: nothing to end
}

func TestTokenSessionEndsWhenTheClusterStopsAcceptingTheToken(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	answer := Person{User: "u"}
	var fail error
	calls := 0
	tl := NewTokenLogin(func(context.Context, string) (Person, error) { calls++; return answer, fail }, Lifetime{Idle: time.Hour, Max: 10 * time.Hour})
	tl.now = func() time.Time { return now }
	login := func() *http.Cookie {
		return cookieNamed(t, serve(http.HandlerFunc(tl.Login), tokenPost(`{"token":"t"}`, "https://dash.example")), SessionCookie)
	}
	c := login()
	now = now.Add(time.Minute)
	_, ok := tl.session(withCookie(c))
	expect(t, ok && calls == 1, "within the recheck interval the cluster is not asked again: %d", calls)
	now = now.Add(tokenRecheck)
	_, ok = tl.session(withCookie(c))
	expect(t, ok && calls == 2, "past it the cluster is asked and still accepts the token: %d", calls)
	now = now.Add(time.Minute)
	_, ok = tl.session(withCookie(c))
	expect(t, ok && calls == 2, "an accepted recheck counts as a fresh check: %d", calls)
	now = now.Add(tokenRecheck)
	fail = errors.New("Unauthorized")
	_, ok = tl.session(withCookie(c))
	expect(t, !ok && len(tl.sessions) == 0, "a token the cluster refuses ends its session")
	fail = nil
	c = login()
	answer = Person{User: "someone-else"}
	now = now.Add(tokenRecheck + time.Second)
	_, ok = tl.session(withCookie(c))
	expect(t, !ok, "a token that now names someone else ends its session")
}

func TestCallbackReplacesATokenSession(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	tl := NewTokenLogin(func(context.Context, string) (Person, error) { return Person{User: "u"}, nil }, Lifetime{})
	d := NewShared(o, tl)
	tc := cookieNamed(t, serve(d.Routes(), tokenPost(`{"token":"t"}`, "https://dash.example")), SessionCookie)
	// The identity provider sign-in comes back with the token session's cookie.
	rec := httptest.NewRecorder()
	o.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	code, q := f.authorize(t, rec.Header().Get("Location"), "u-1")
	cb := httptest.NewRequest(http.MethodGet, "/auth/callback?code="+code+"&state="+q.Get("state"), nil)
	cb.AddCookie(cookieNamed(t, rec, loginCookie))
	cb.AddCookie(tc)
	out := serve(d.Routes(), cb)
	expect(t, out.Code == http.StatusFound, "callback %d %s", out.Code, out.Body)
	expect(t, len(tl.sessions) == 0, "the token session ended with the identity provider sign-in")
	var got State
	r := httptest.NewRequest(http.MethodGet, "https://dash.example/api/session", nil)
	r.AddCookie(cookieNamed(t, out, SessionCookie))
	serve(d.Middleware(echo(&got)), r)
	expect(t, got.Method == "oidc" && strings.HasSuffix(got.Person.User, "#u-1"), "the OIDC session wins: %+v", got)
}

func TestOIDCLifetimeIsConfigurable(t *testing.T) {
	l := Lifetime{}
	expect(t, l.idle() == idleTimeout && l.max() == absoluteTimeout, "defaults %v %v", l.idle(), l.max())
	l = Lifetime{Idle: time.Minute, Max: 2 * time.Hour}
	start := time.Unix(0, 0)
	expect(t, !l.expired(start, start, start.Add(59*time.Second)), "within both")
	expect(t, l.expired(start, start, start.Add(61*time.Second)), "past idle")
	expect(t, l.expired(start, start.Add(2*time.Hour), start.Add(2*time.Hour+time.Second)), "past max")
}
