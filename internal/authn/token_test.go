package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
		if cfg.BearerToken == "" || cfg.Username != "" || cfg.CertData != nil {
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
	rv, err := SelfSubjectReview(cluster, fakeSSR(t, "system:serviceaccount:a:b", nil))(context.Background(), "tok")
	p := rv.Person
	expect(t, err == nil && rv.Bearer && p.User == "system:serviceaccount:a:b" && slices.Equal(p.Groups, []string{"team-a", "system:authenticated"}), "review %+v %v", p, err)
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

// fakeTokenReview refuses "bad", names nobody for "nobody" and a viewer
// ServiceAccount for any other token, recording the token it was asked about.
func fakeTokenReview(asked *string) TokenReview {
	return func(_ context.Context, token string) (Reviewed, error) {
		*asked = token
		switch token {
		case "bad":
			return Reviewed{}, errors.New("Unauthorized")
		case "nobody":
			return Reviewed{}, nil
		}
		return Reviewed{Person: Person{User: "oidc:viewer", Groups: []string{"team-a"}}, Bearer: true}, nil
	}
}

func TestTokenLogin(t *testing.T) {
	var asked string
	tl := NewTokenLogin(fakeTokenReview(&asked), Lifetime{Idle: time.Minute, Max: time.Hour})
	d := NewShared(nil, tl)
	expect(t, d.SignsIn() && d.TokenLogin() && !d.Login() && !d.Local(), "token-only shared mode")
	routes := d.Routes()
	rec := serve(routes, tokenPost(`{"token":"Bearer good-token"}`, "https://dash.example"))
	expect(t, rec.Code == http.StatusNoContent && asked == "good-token", "sign-in %d with %q", rec.Code, asked)
	c := cookieNamed(t, rec, SessionCookie)
	expect(t, c.HttpOnly && c.Secure && c.MaxAge == 3600, "the session cookie %+v", c)

	var got State
	h := d.Middleware(echo(&got))
	r := httptest.NewRequest(http.MethodGet, "https://dash.example/api/fleet/services", nil)
	r.AddCookie(c)
	expect(t, serve(h, r).Code == http.StatusTeapot, "a token session passes the gate")
	expect(t, got.SignedIn && got.Method == "token" && got.Token == "good-token" &&
		got.Person.User == "oidc:viewer" && got.CSRF != "", "token state %+v", got)

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

func TestTokenLoginRefusals(t *testing.T) {
	var asked string
	routes := NewShared(nil, NewTokenLogin(fakeTokenReview(&asked), Lifetime{Idle: time.Minute, Max: time.Hour})).Routes()
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
}

func TestTokenSessionLifetime(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tl := NewTokenLogin(func(context.Context, string) (Reviewed, error) {
		return Reviewed{Person: Person{User: "u"}, Bearer: true}, nil
	}, Lifetime{Idle: time.Minute, Max: 3 * time.Minute})
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
	answer := Reviewed{Person: Person{User: "u"}, Bearer: true}
	var fail error
	calls := 0
	tl := NewTokenLogin(func(context.Context, string) (Reviewed, error) { calls++; return answer, fail }, Lifetime{Idle: time.Hour, Max: 10 * time.Hour})
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
	answer = Reviewed{Person: Person{User: "someone-else"}, Bearer: true}
	now = now.Add(tokenRecheck + time.Second)
	_, ok = tl.session(withCookie(c))
	expect(t, !ok, "a token that now names someone else ends its session")
}

func TestCallbackReplacesATokenSession(t *testing.T) {
	f := newIdP(t)
	o := newTestOIDC(t, f)
	tl := NewTokenLogin(func(context.Context, string) (Reviewed, error) {
		return Reviewed{Person: Person{User: "u"}, Bearer: true}, nil
	}, Lifetime{})
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

// fakeTokenReviews answers TokenReviews with status, or err.
func fakeTokenReviews(t *testing.T, status map[string]any, err error) dynamic.Interface {
	t.Helper()
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c.PrependReactor("create", "tokenreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if err != nil {
			return true, nil, err
		}
		obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		auds, _, _ := unstructured.NestedStringSlice(obj.Object, "spec", "audiences")
		tok, _, _ := unstructured.NestedString(obj.Object, "spec", "token")
		if !slices.Equal(auds, []string{DashboardAudience}) || tok != "tok" {
			t.Fatalf("the review asks about the token for the dashboard's audience: %v %q", auds, tok)
		}
		obj.Object["status"] = status
		return true, obj, nil
	})
	return c
}

func TestAudienceReviewRequiresAudienceInStatus(t *testing.T) {
	ctx := context.Background()
	user := map[string]any{"username": "system:serviceaccount:pacto:pacto-viewer", "uid": "u-1",
		"groups": []any{"system:serviceaccounts"}, "extra": map[string]any{"k": []any{"v"}}}
	rv, err := AudienceReview(fakeTokenReviews(t, map[string]any{"authenticated": true, "audiences": []any{DashboardAudience}, "user": user}, nil), DashboardAudience)(ctx, "tok")
	expect(t, err == nil && rv.Person.User == "system:serviceaccount:pacto:pacto-viewer" && rv.UID == "u-1" &&
		slices.Equal(rv.Person.Groups, []string{"system:serviceaccounts"}) && rv.Extra["k"][0] == "v" && !rv.Bearer, "accepted %+v %v", rv, err)
	_, err = AudienceReview(fakeTokenReviews(t, map[string]any{"authenticated": true, "audiences": []any{"other"}, "user": user}, nil), DashboardAudience)(ctx, "tok")
	expect(t, err != nil && strings.Contains(err.Error(), "not minted for audience pacto-dashboard"), "another audience: %v", err)
	_, err = AudienceReview(fakeTokenReviews(t, map[string]any{"authenticated": false, "error": "token expired"}, nil), DashboardAudience)(ctx, "tok")
	expect(t, err != nil && strings.Contains(err.Error(), "token expired"), "unauthenticated: %v", err)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "authentication.k8s.io", Resource: "tokenreviews"}, "", errors.New("no"))
	_, err = AudienceReview(fakeTokenReviews(t, nil, forbidden), DashboardAudience)(ctx, "tok")
	expect(t, errors.Is(err, ErrTokenReviewForbidden) && strings.Contains(err.Error(), "create on tokenreviews.authentication.k8s.io"), "forbidden: %v", err)
	_, err = AudienceReview(fakeTokenReviews(t, nil, errors.New("down")), DashboardAudience)(ctx, "tok")
	expect(t, err != nil && !errors.Is(err, ErrTokenReviewForbidden), "an apiserver error: %v", err)
}

func TestSelfSubjectReviewKeepsAuthTime(t *testing.T) {
	cluster := &rest.Config{Host: "https://k"}
	rv, err := SelfSubjectReview(cluster, func(*rest.Config) (dynamic.Interface, error) {
		c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
		c.PrependReactor("create", "selfsubjectreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
			obj := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
			obj.Object["status"] = map[string]any{"userInfo": map[string]any{"username": "oidc:ann",
				"extra": map[string]any{AuthTimeExtra: []any{"1700000000"}}}}
			return true, obj, nil
		})
		return c, nil
	})(context.Background(), "tok")
	expect(t, err == nil && rv.Bearer && rv.AuthTime == "1700000000", "the login time comes from the extra: %+v %v", rv, err)
}

// dashboardRoutes signs in through DashboardTokenReview with fixed answers
// from the audience and bearer paths.
func dashboardRoutes(aud Reviewed, aerr error, br Reviewed, berr error) (*Dashboard, *TokenLogin) {
	review := DashboardTokenReview(
		func(context.Context, string) (Reviewed, error) { return aud, aerr },
		func(context.Context, string) (Reviewed, error) { return br, berr })
	tl := NewTokenLogin(review, Lifetime{Idle: time.Minute, Max: time.Hour})
	return NewShared(nil, tl), tl
}

func signIn(d *Dashboard, body string) *httptest.ResponseRecorder {
	return serve(d.Routes(), tokenPost(body, "https://dash.example"))
}

// stateOf is the State a request with rec's session cookie runs with.
func stateOf(t *testing.T, d *Dashboard, rec *httptest.ResponseRecorder) State {
	t.Helper()
	var got State
	r := httptest.NewRequest(http.MethodGet, "https://dash.example/api/fleet/services", nil)
	r.AddCookie(cookieNamed(t, rec, SessionCookie))
	serve(d.Middleware(echo(&got)), r)
	return got
}

var (
	viewerSA        = Person{User: "system:serviceaccount:shop:viewer", Groups: []string{"system:serviceaccounts"}}
	personAnn       = Person{User: "oidc:ann", Groups: []string{"team-a"}}
	errNotAudience  = errors.New("the token was not minted for audience pacto-dashboard")
	errReviewDenied = fmt.Errorf("%w: grant create on tokenreviews.authentication.k8s.io", ErrTokenReviewForbidden)
)

func TestTokenLoginRefusesServiceAccountWithoutAudience(t *testing.T) {
	d, tl := dashboardRoutes(Reviewed{}, errNotAudience, Reviewed{Person: viewerSA}, nil)
	rec := signIn(d, `{"token":"t"}`)
	body := rec.Body.String()
	expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(body, "TokenAudienceRequired") &&
		strings.Contains(body, "kubectl create token viewer -n shop --audience pacto-dashboard") && len(tl.sessions) == 0, "refusal %d %s", rec.Code, body)
}

func TestTokenLoginReviewErrorSignsNobodyIn(t *testing.T) {
	d, tl := dashboardRoutes(Reviewed{}, errors.New("timeout"), Reviewed{Person: viewerSA}, nil)
	rec := signIn(d, `{"token":"t"}`)
	expect(t, rec.Code == http.StatusUnauthorized && len(tl.sessions) == 0 && rec.Header().Get("Set-Cookie") == "",
		"a ServiceAccount whose audience review failed signs nobody in: %d %s", rec.Code, rec.Body)
	d, _ = dashboardRoutes(Reviewed{}, errors.New("timeout"), Reviewed{}, errors.New("Unauthorized"))
	rec = signIn(d, `{"token":"t"}`)
	expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "the cluster did not accept this token: Unauthorized"), "both fail: %s", rec.Body)
}

func TestTokenLoginReviewForbiddenNamesTheRight(t *testing.T) {
	d, _ := dashboardRoutes(Reviewed{}, errReviewDenied, Reviewed{}, errors.New("Unauthorized"))
	rec := signIn(d, `{"token":"t"}`)
	expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "create on tokenreviews"), "the missing right is the cause: %s", rec.Body)
	d, _ = dashboardRoutes(Reviewed{}, errReviewDenied, Reviewed{Person: personAnn}, nil)
	rec = signIn(d, `{"token":"t"}`)
	expect(t, rec.Code == http.StatusNoContent && stateOf(t, d, rec).Token == "t", "a person's token never needed the right: %d %s", rec.Code, rec.Body)
}

func TestTokenLoginPersonTokenIsBearer(t *testing.T) {
	for _, aud := range []Reviewed{{}, {Person: personAnn}} { // a person the audience review also names still holds a bearer
		d, _ := dashboardRoutes(aud, nil, Reviewed{Person: personAnn, AuthTime: "1700000000"}, nil)
		rec := signIn(d, `{"token":"t"}`)
		st := stateOf(t, d, rec)
		expect(t, rec.Code == http.StatusNoContent && st.Token == "t" && st.AuthTime == "1700000000" && st.Person.User == "oidc:ann" && st.UID == "",
			"a bearer session %d %+v", rec.Code, st)
	}
}

func TestTokenLoginLinkSession(t *testing.T) {
	link := Reviewed{Person: viewerSA, UID: "u-1", Extra: map[string][]string{"k": {"v"}}, AuthTime: "ignored", Bearer: true}
	d, _ := dashboardRoutes(link, nil, Reviewed{}, errors.New("Unauthorized"))
	rec := signIn(d, `{"token":"t","link":true}`)
	st := stateOf(t, d, rec)
	expect(t, rec.Code == http.StatusNoContent && st.Token == "" && st.Method == "token" && st.Person.User == viewerSA.User &&
		st.UID == "u-1" && st.Extra["k"][0] == "v" && st.AuthTime == "", "a link session %d %+v", rec.Code, st)
}

func TestTokenLoginLinkFlagRefusesBearer(t *testing.T) {
	d, tl := dashboardRoutes(Reviewed{}, errNotAudience, Reviewed{Person: personAnn}, nil)
	rec := signIn(d, `{"token":"t","link":true}`)
	expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "SignInLinkNeedsDashboardToken") &&
		rec.Header().Get("Set-Cookie") == "" && len(tl.sessions) == 0, "a link never signs in a person: %d %s", rec.Code, rec.Body)
	d, _ = dashboardRoutes(Reviewed{Person: viewerSA}, nil, Reviewed{}, errors.New("Unauthorized"))
	rec = signIn(d, `{"token":"t","link":true}`)
	expect(t, rec.Code == http.StatusNoContent && stateOf(t, d, rec).Token == "", "a link with a dashboard token: %d %s", rec.Code, rec.Body)
}

func TestTokenRecheckEndsOnKindChange(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	answer := Reviewed{Person: personAnn}
	tl := NewTokenLogin(func(context.Context, string) (Reviewed, error) { return answer, nil }, Lifetime{Idle: time.Hour, Max: 10 * time.Hour})
	tl.now = func() time.Time { return now }
	c := cookieNamed(t, serve(http.HandlerFunc(tl.Login), tokenPost(`{"token":"t"}`, "https://dash.example")), SessionCookie)
	answer.Bearer = true
	now = now.Add(tokenRecheck + time.Second)
	_, ok := tl.session(withCookie(c))
	expect(t, !ok, "a link session whose token now reads as a bearer ends")
}
