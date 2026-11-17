package authn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// TokenReview names who a Kubernetes token authenticates as.
type TokenReview func(ctx context.Context, token string) (Reviewed, error)

// Reviewed is what the API server said about a token.
type Reviewed struct {
	// Person is the user and groups the token authenticates as.
	Person Person
	// UID is the user's uid, when the review names one (TokenReview only).
	UID string
	// Extra is the user's extra attributes, when the review names them
	// (TokenReview only).
	Extra map[string][]string
	// AuthTime is the login time the API server mapped into the user's extra
	// (AuthTimeExtra), when it did.
	AuthTime string
	// Bearer is true when the API server accepts the token as a credential
	// of its own. A token reviewed only for the dashboard's audience is no
	// credential and is never sent to the API server.
	Bearer bool
}

// DashboardAudience is the audience a ServiceAccount token must be minted for
// to sign in to the dashboard: kubectl create token --audience pacto-dashboard.
const DashboardAudience = "pacto-dashboard"

// AudienceCheck reviews a token for one audience.
type AudienceCheck func(ctx context.Context, token string) (Reviewed, error)

// ErrTokenReviewForbidden is the dashboard lacking the right to review tokens.
var ErrTokenReviewForbidden = errors.New("the dashboard may not review tokens")

// TokenRefusal is a token the API server accepts that the dashboard still
// refuses, with a code the sign-in screen shows inside the message.
type TokenRefusal struct{ Code, Message string }

func (e *TokenRefusal) Error() string { return e.Code + ": " + e.Message }

var (
	ssrResource = schema.GroupVersionResource{Group: "authentication.k8s.io", Version: "v1", Resource: "selfsubjectreviews"}
	trResource  = schema.GroupVersionResource{Group: "authentication.k8s.io", Version: "v1", Resource: "tokenreviews"}
)

// AudienceReview checks a token with a TokenReview for audience, made with the
// dashboard's own client: the API server must authenticate the token and
// name audience among the token's audiences.
func AudienceReview(own dynamic.Interface, audience string) AudienceCheck {
	return func(ctx context.Context, token string) (Reviewed, error) {
		ctx, cancel := context.WithTimeout(ctx, reviewTimeout)
		defer cancel()
		out, err := own.Resource(trResource).Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "authentication.k8s.io/v1", "kind": "TokenReview",
			"spec": map[string]any{"token": token, "audiences": []any{audience}}}}, metav1.CreateOptions{})
		if apierrors.IsForbidden(err) {
			return Reviewed{}, fmt.Errorf("%w: grant create on tokenreviews.authentication.k8s.io", ErrTokenReviewForbidden)
		}
		if err != nil {
			return Reviewed{}, err
		}
		var r struct {
			Status struct {
				Authenticated bool     `json:"authenticated"`
				Audiences     []string `json:"audiences"`
				Error         string   `json:"error"`
				User          struct {
					Username string              `json:"username"`
					UID      string              `json:"uid"`
					Groups   []string            `json:"groups"`
					Extra    map[string][]string `json:"extra"`
				} `json:"user"`
			} `json:"status"`
		}
		b, _ := out.MarshalJSON()
		_ = json.Unmarshal(b, &r) // an answer without status authenticates nobody
		st := r.Status
		switch {
		case !st.Authenticated:
			return Reviewed{}, fmt.Errorf("the token is not valid for audience %s: %s", audience, st.Error)
		case !slices.Contains(st.Audiences, audience):
			return Reviewed{}, fmt.Errorf("the token was not minted for audience %s", audience)
		}
		return Reviewed{Person: Person{User: st.User.Username, Groups: st.User.Groups}, UID: st.User.UID, Extra: st.User.Extra}, nil
	}
}

// DashboardTokenReview tells a link token from a person's token with what the
// API server says alone. A ServiceAccount token audience authenticates is a
// link token, never sent to the API server (Bearer false). Any other token
// must be a bearer the API server accepts, for someone other than a
// ServiceAccount: a ServiceAccount signs in only with a token minted for the
// dashboard. A failed audience review never falls back to the bearer path for
// a ServiceAccount.
func DashboardTokenReview(audience AudienceCheck, bearer TokenReview) TokenReview {
	return func(ctx context.Context, token string) (Reviewed, error) {
		link, aerr := audience(ctx, token)
		if aerr == nil && isServiceAccount(link.Person.User) {
			link.Bearer, link.AuthTime = false, ""
			return link, nil
		}
		return bearerReview(ctx, bearer, token, aerr)
	}
}

// bearerReview is DashboardTokenReview's bearer path. When the API server
// refuses the token and the dashboard may not review tokens, the missing
// right is the cause reported: an audience-only token always fails here.
func bearerReview(ctx context.Context, bearer TokenReview, token string, aerr error) (Reviewed, error) {
	r, err := bearer(ctx, token)
	switch {
	case err != nil && errors.Is(aerr, ErrTokenReviewForbidden):
		return Reviewed{}, aerr
	case err != nil:
		return Reviewed{}, err
	case isServiceAccount(r.Person.User) && errors.Is(aerr, ErrTokenReviewForbidden):
		return Reviewed{}, aerr // the dashboard could not check the audience, so minting another token would not help
	case isServiceAccount(r.Person.User):
		ns, name := serviceAccountName(r.Person.User)
		return Reviewed{}, &TokenRefusal{Code: "TokenAudienceRequired", Message: "a ServiceAccount signs in only with a token minted for the dashboard: " +
			"kubectl create token " + name + " -n " + ns + " --audience " + DashboardAudience}
	}
	r.Bearer = true
	return r, nil
}

const serviceAccountPrefix = "system:serviceaccount:"

func isServiceAccount(user string) bool { return strings.HasPrefix(user, serviceAccountPrefix) }

// serviceAccountName splits system:serviceaccount:<namespace>:<name>.
func serviceAccountName(user string) (namespace, name string) {
	namespace, name, _ = strings.Cut(strings.TrimPrefix(user, serviceAccountPrefix), ":")
	return namespace, name
}

// AuthTimeExtra is the userInfo.extra key structured authentication maps the
// OIDC auth_time claim to.
const AuthTimeExtra = "delivery.pacto.trianalab.io/auth-time"

// ReviewSelf asks the API server who dyn's credentials are (a
// SelfSubjectReview): the person and the extra their authenticator added. It
// is the one place Pacto learns who a caller is from the cluster.
func ReviewSelf(ctx context.Context, dyn dynamic.Interface) (Person, map[string][]string, error) {
	out, err := dyn.Resource(ssrResource).Create(ctx, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview"}}, metav1.CreateOptions{})
	if err != nil {
		return Person{}, nil, err
	}
	var r struct {
		Status struct {
			UserInfo struct {
				Username string              `json:"username"`
				Groups   []string            `json:"groups"`
				Extra    map[string][]string `json:"extra"`
			} `json:"userInfo"`
		} `json:"status"`
	}
	b, _ := out.MarshalJSON()
	_ = json.Unmarshal(b, &r) // an answer without userInfo names nobody
	ui := r.Status.UserInfo
	return Person{User: ui.Username, Groups: ui.Groups}, ui.Extra, nil
}

// SelfSubjectReview asks the API server, with the token itself as the only
// credential, who the token is. A token it accepts is a bearer.
func SelfSubjectReview(cluster *rest.Config, newClient func(*rest.Config) (dynamic.Interface, error)) TokenReview {
	return func(ctx context.Context, token string) (Reviewed, error) {
		if cluster == nil {
			return Reviewed{}, errors.New("the dashboard has no cluster to ask who this token is")
		}
		dyn, err := newClient(RESTConfig(cluster, token))
		if err != nil {
			return Reviewed{}, err
		}
		ctx, cancel := context.WithTimeout(ctx, reviewTimeout)
		defer cancel()
		p, extra, err := ReviewSelf(ctx, dyn)
		if err != nil {
			return Reviewed{}, err
		}
		r := Reviewed{Person: p, Bearer: true}
		if at := extra[AuthTimeExtra]; len(at) > 0 {
			r.AuthTime = at[0]
		}
		return r, nil
	}
}

// TokenLogin signs people in with a Kubernetes bearer token they paste, for a
// cluster whose API server trusts no identity provider the dashboard can use.
// The token stays in a server-side session behind the session cookie, never
// in browser storage. A person's token (a bearer session) runs every request
// with it, so Kubernetes RBAC decides as it does for kubectl, and the person
// decides on held changes under the approver rule. A ServiceAccount token
// minted for the dashboard (a link session) is never sent to the API server:
// it reads the fleet only.
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
	// bearer is a person's token the API server accepts; false is a link
	// session, whose token is never sent to it.
	bearer   bool
	authTime string
	// uid and extra are what the TokenReview named for a link session.
	uid   string
	extra map[string][]string
	// checked is when the API server last accepted the token.
	checked time.Time
	// expires is the token's exp claim, zero when it has none: the session
	// ends there, without waiting for the next recheck.
	expires time.Time
}

// tokenExpiry reads a JWT's exp claim, unverified: the review already
// accepted the token, and an expiry only ever ends a session sooner.
func tokenExpiry(token string) time.Time {
	var exp float64
	if json.Unmarshal(jwtClaims(token)["exp"], &exp) != nil || exp <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(exp), 0)
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
// the dashboard's own page. The review must name who the token is before a
// session exists. A post from a sign-in link ({"link": true}) signs in only
// as a link session, so a crafted link never signs someone in as another
// person.
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
		Link  bool   `json:"link"`
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
	rv, err := t.review(r.Context(), token)
	if why := refuseReviewed(rv, err, in.Link); why != "" {
		refuse(http.StatusUnauthorized, why)
		return
	}
	now := t.now()
	s := &tokenSession{token: token, csrf: random(32), person: rv.Person, created: now, seen: now, checked: now,
		bearer: rv.Bearer, authTime: rv.AuthTime, uid: rv.UID, extra: rv.Extra, expires: tokenExpiry(token)}
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

// refuseReviewed says why a reviewed token signs nobody in, or "".
func refuseReviewed(rv Reviewed, err error, link bool) string {
	var refusal *TokenRefusal
	switch {
	case errors.As(err, &refusal) || errors.Is(err, ErrTokenReviewForbidden):
		return err.Error()
	case err != nil:
		return "the cluster did not accept this token: " + err.Error()
	case rv.Person.User == "":
		return "the cluster did not say who this token is"
	case link && rv.Bearer:
		return (&TokenRefusal{Code: "SignInLinkNeedsDashboardToken",
			Message: "a sign-in link carries only a token minted for the dashboard: paste your own token in the form instead"}).Error()
	}
	return ""
}

// session returns the request's live token session. A session ends when its
// token's exp passes (so a 10-minute sign-in link reads for 10 minutes). Every
// tokenRecheck the API server is asked again, and a token it no longer accepts as the same
// user, or as the same kind of session, ends the session.
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
	case t.lifetime.expired(s.created, s.seen, now) || (!s.expires.IsZero() && !now.Before(s.expires)):
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
	if rv, err := t.review(r.Context(), cp.token); err != nil || rv.Person.User != cp.person.User || rv.Bearer != cp.bearer {
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
