package authn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// TokenReview names who a Kubernetes bearer token authenticates as.
type TokenReview func(ctx context.Context, token string) (Person, error)

var ssrResource = schema.GroupVersionResource{Group: "authentication.k8s.io", Version: "v1", Resource: "selfsubjectreviews"}

// SelfSubjectReview asks the API server, with the token itself as the only
// credential, who the token is.
func SelfSubjectReview(cluster *rest.Config, newClient func(*rest.Config) (dynamic.Interface, error)) TokenReview {
	return func(ctx context.Context, token string) (Person, error) {
		if cluster == nil {
			return Person{}, errors.New("the dashboard has no cluster to ask who this token is")
		}
		dyn, err := newClient(RESTConfig(cluster, token))
		if err != nil {
			return Person{}, err
		}
		ctx, cancel := context.WithTimeout(ctx, reviewTimeout)
		defer cancel()
		out, err := dyn.Resource(ssrResource).Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview"}}, metav1.CreateOptions{})
		if err != nil {
			return Person{}, err
		}
		user, _, _ := unstructured.NestedString(out.Object, "status", "userInfo", "username")
		groups, _, _ := unstructured.NestedStringSlice(out.Object, "status", "userInfo", "groups")
		return Person{User: user, Groups: groups}, nil
	}
}

// TokenLogin signs people in with a Kubernetes bearer token they paste, for a
// cluster whose API server trusts no identity provider the dashboard can use.
// The token stays in a server-side session behind the session cookie, never
// in browser storage, and every request runs with it, so Kubernetes RBAC
// decides as it does for kubectl. A token session never decides on a held
// change: a decision needs a fresh identity provider sign-in.
// ponytail: one replica's memory, like OIDC sessions.
type TokenLogin struct {
	review   TokenReview
	lifetime Lifetime
	now      func() time.Time

	mu       sync.Mutex
	sessions map[string]*tokenSession
}

type tokenSession struct {
	token, csrf   string
	person        Person
	created, seen time.Time
	// checked is when the API server last accepted the token.
	checked time.Time
}

// tokenRecheck is how often a session's token is shown to the API server
// again, so a token that expired or was revoked ends its session.
const tokenRecheck = 5 * time.Minute

// NewTokenLogin checks every pasted token with review.
func NewTokenLogin(review TokenReview, l Lifetime) *TokenLogin {
	return &TokenLogin{review: review, lifetime: l, now: time.Now, sessions: map[string]*tokenSession{}}
}

// maxTokenBody bounds a sign-in request: a token is a few kilobytes.
const maxTokenBody = 64 << 10

// Login signs in with a token: POST /auth/token with {"token": "..."} from
// the dashboard's own page. The API server must accept the token
// (SelfSubjectReview) before a session exists.
func (t *TokenLogin) Login(w http.ResponseWriter, r *http.Request) {
	refuse := func(code int, msg string) {
		Refused(r.Context(), r, "dashboard refused a token sign-in", "reason", msg)
		http.Error(w, msg, code)
	}
	if !sameOrigin(r) {
		refuse(http.StatusForbidden, "a token sign-in must come from the dashboard's own page")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTokenBody)).Decode(&in); err != nil {
		refuse(http.StatusBadRequest, "send the token as {\"token\": \"...\"}")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.Token), "Bearer "))
	if token == "" {
		refuse(http.StatusBadRequest, "paste a Kubernetes bearer token")
		return
	}
	p, err := t.review(r.Context(), token)
	switch {
	case err != nil:
		refuse(http.StatusUnauthorized, "the cluster did not accept this token: "+err.Error())
		return
	case p.User == "":
		refuse(http.StatusUnauthorized, "the cluster did not say who this token is")
		return
	}
	now := t.now()
	s := &tokenSession{token: token, csrf: random(32), person: p, created: now, seen: now, checked: now}
	id := random(32)
	t.mu.Lock()
	if c, err := r.Cookie(SessionCookie); err == nil {
		delete(t.sessions, c.Value)
	}
	for k, old := range t.sessions { // drop what ended, so the store stays bounded by live sessions
		if t.lifetime.expired(old.created, old.seen, now) {
			delete(t.sessions, k)
		}
	}
	t.sessions[id] = s
	t.mu.Unlock()
	http.SetCookie(w, sessionCookie(id, int(t.lifetime.max().Seconds())))
	w.WriteHeader(http.StatusNoContent)
}

// session returns the request's live token session. Every tokenRecheck the
// API server is asked again, and a token it no longer accepts as the same
// user ends the session.
func (t *TokenLogin) session(r *http.Request) (tokenSession, bool) {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return tokenSession{}, false
	}
	now := t.now()
	t.mu.Lock()
	s, ok := t.sessions[c.Value]
	switch {
	case !ok:
		t.mu.Unlock()
		return tokenSession{}, false
	case t.lifetime.expired(s.created, s.seen, now):
		delete(t.sessions, c.Value)
		t.mu.Unlock()
		return tokenSession{}, false
	}
	s.seen = now
	cp, stale := *s, now.Sub(s.checked) > tokenRecheck
	t.mu.Unlock()
	if !stale {
		return cp, true
	}
	if p, err := t.review(r.Context(), cp.token); err != nil || p.User != cp.person.User {
		Refused(r.Context(), r, "dashboard ended a token session", "reason", "the cluster no longer accepts its token")
		t.mu.Lock()
		delete(t.sessions, c.Value)
		t.mu.Unlock()
		return tokenSession{}, false
	}
	t.mu.Lock()
	s.checked = now
	t.mu.Unlock()
	return cp, true
}

// end removes the request's token session, if it has one.
func (t *TokenLogin) end(r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		t.mu.Lock()
		delete(t.sessions, c.Value)
		t.mu.Unlock()
	}
}
