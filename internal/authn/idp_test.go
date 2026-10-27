package authn

import (
	"cmp"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIdP is an OpenID provider good enough for the flows authn runs:
// discovery, keys, the authorization code with PKCE, refresh, token exchange,
// revocation and end-session.
type fakeIdP struct {
	t      *testing.T
	srv    *httptest.Server
	signer jose.Signer
	key    *rsa.PrivateKey
	now    func() time.Time

	mu        sync.Mutex
	codes     map[string]grant // code -> what the login asked for
	refreshes map[string]grant
	revoked   []string
	exchanges []url.Values
	// knobs
	noAuthTime  bool
	noRevoke    bool
	tokenStatus int
	authTime    time.Time
	refreshID   string // overrides the refreshed id_token
	// rotate makes each refresh token single-use, as Okta, Auth0 and
	// Keycloak's revoke-on-use do; reuse is refused.
	rotate       bool
	refreshCalls int
	exchangeOut  map[string]any
	// public is the issuer URL the IdP names, when it differs from where the
	// test reaches it.
	public string
}

// issuer is the URL the IdP names in its discovery document and tokens.
func (f *fakeIdP) issuer() string { return cmp.Or(f.public, f.srv.URL) }

type grant struct {
	nonce, challenge, sub string
	authTime              time.Time
}

func newIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{t: t, signer: signer, key: key, now: time.Now, codes: map[string]grant{}, refreshes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		f.mu.Unlock()
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) discovery(w http.ResponseWriter, _ *http.Request) {
	u := f.issuer()
	m := map[string]any{"issuer": u, "authorization_endpoint": u + "/auth", "token_endpoint": u + "/token",
		"jwks_uri": u + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}, "end_session_endpoint": u + "/logout"}
	if !f.noRevoke {
		m["revocation_endpoint"] = u + "/revoke"
	}
	_ = json.NewEncoder(w).Encode(m)
}

// jwt signs claims with the IdP's key.
func (f *fakeIdP) jwt(claims map[string]any) string {
	s, err := jwt.Signed(f.signer).Claims(claims).Serialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fakeIdP) idToken(aud string, g grant) string {
	c := map[string]any{"iss": f.issuer(), "sub": g.sub, "aud": aud, "exp": f.now().Add(time.Hour).Unix(), "iat": f.now().Unix()}
	if g.nonce != "" {
		c["nonce"] = g.nonce
	}
	if !f.noAuthTime {
		c["auth_time"] = g.authTime.Unix()
	}
	return f.jwt(c)
}

func (f *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	clientID, _, _ := r.BasicAuth()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokenStatus != 0 {
		w.WriteHeader(f.tokenStatus)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"nope"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		g, ok := f.codes[r.PostForm.Get("code")]
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		rt := "rt-" + r.PostForm.Get("code")
		f.refreshes[rt] = grant{sub: g.sub, authTime: g.authTime}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600,
			"refresh_token": rt, "id_token": f.idToken(clientID, g)})
	case "refresh_token":
		f.refreshCalls++
		old := r.PostForm.Get("refresh_token")
		g, known := f.refreshes[old]
		next := "rt-next"
		if f.rotate {
			if !known {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token reused"}`))
				return
			}
			delete(f.refreshes, old)
			next = fmt.Sprintf("rt-%d", f.refreshCalls)
			f.refreshes[next] = g
		}
		id := f.refreshID
		if id == "" {
			id = f.idToken(clientID, g)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at2", "token_type": "Bearer", "expires_in": 3600,
			"refresh_token": next, "id_token": id})
	default:
		f.exchanges = append(f.exchanges, r.PostForm)
		out := f.exchangeOut
		if out == nil {
			out = map[string]any{"access_token": "exchanged-" + r.PostForm.Get("audience"), "expires_in": 60}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

// authorize plays the browser and the IdP's login page: it reads the
// redirect /auth/login produced and registers a code for it.
func (f *fakeIdP) authorize(t *testing.T, loc string, sub string) (code string, q url.Values) {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	q = u.Query()
	code = "c" + q.Get("state")
	at := f.authTime
	if at.IsZero() {
		at = f.now()
	}
	f.mu.Lock()
	f.codes[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), sub: sub, authTime: at}
	f.mu.Unlock()
	return code, q
}
