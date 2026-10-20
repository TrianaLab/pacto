package authn

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func newTestMCP(t *testing.T, f *fakeIdP, clientID string) *MCP {
	t.Helper()
	m, err := NewMCP(MCPConfig{IssuerURL: f.srv.URL, Audience: "pacto-mcp", ClientID: clientID, ClientSecret: "s", ExchangeAudience: "pacto-cli"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *fakeIdP) accessToken(aud string, exp time.Time) string {
	return f.jwt(map[string]any{"iss": f.srv.URL, "sub": "agent-1", "aud": aud, "exp": exp.Unix(), "scope": "openid mcp"})
}

func TestNewMCP(t *testing.T) {
	if _, err := NewMCP(MCPConfig{IssuerURL: "https://x"}); err == nil {
		t.Fatal("an audience is required")
	}
	if _, err := NewMCP(MCPConfig{IssuerURL: "http://127.0.0.1:1", Audience: "a"}); err == nil {
		t.Fatal("an unreachable issuer fails at start")
	}
}

func TestMCPVerify(t *testing.T) {
	f := newIdP(t)
	m := newTestMCP(t, f, "pacto-mcp")
	tok := f.accessToken("pacto-mcp", time.Now().Add(time.Hour))
	info, err := m.Verify(context.Background(), tok, nil)
	if err != nil || info.UserID != f.srv.URL+"#agent-1" || info.Extra[rawTokenKey] != tok || strings.Join(info.Scopes, ",") != "openid,mcp" {
		t.Fatalf("verify: %+v %v", info, err)
	}
	for name, bad := range map[string]string{
		"wrong audience": f.accessToken("pacto-cli", time.Now().Add(time.Hour)),
		"expired":        f.accessToken("pacto-mcp", time.Now().Add(-time.Hour)),
		"garbage":        "x.y.z",
	} {
		if _, err := m.Verify(context.Background(), bad, nil); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMCPExchange(t *testing.T) {
	f := newIdP(t)
	m := newTestMCP(t, f, "pacto-mcp")
	tok := f.accessToken("pacto-mcp", time.Now().Add(time.Hour))
	info, _ := m.Verify(context.Background(), tok, nil)
	got, err := m.Exchange(context.Background(), info)
	if err != nil || got != "exchanged-pacto-cli" {
		t.Fatalf("exchange: %q %v", got, err)
	}
	ex := f.exchanges[0]
	if ex.Get("grant_type") != "urn:ietf:params:oauth:grant-type:token-exchange" || ex.Get("subject_token") != tok ||
		ex.Get("subject_token_type") != "urn:ietf:params:oauth:token-type:access_token" || ex.Get("audience") != "pacto-cli" {
		t.Fatalf("exchange request %v", ex)
	}
	if _, _ = m.Exchange(context.Background(), info); len(f.exchanges) != 1 {
		t.Fatal("a second call within the lifetime is cached")
	}
	// The cache ends at the earlier expiry: here the exchanged token's 60s.
	m.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, _ = m.Exchange(context.Background(), info); len(f.exchanges) != 2 || len(m.cache) != 1 {
		t.Fatalf("an expired cache entry is dropped and exchanged again: %d %d", len(f.exchanges), len(m.cache))
	}
	m.now = time.Now
	// An exchange with no expires_in is cached until the subject token expires.
	f.exchangeOut = map[string]any{"access_token": "long"}
	info2 := &auth.TokenInfo{Expiration: time.Now().Add(time.Minute), Extra: map[string]any{rawTokenKey: "other"}}
	if got, _ := m.Exchange(context.Background(), info2); got != "long" || !m.cache[keyOf("other")].until.Equal(info2.Expiration) {
		t.Fatal("no expires_in: cached until the subject expires")
	}
	f.exchangeOut = map[string]any{"token_type": "x"}
	if _, err := m.Exchange(context.Background(), &auth.TokenInfo{Extra: map[string]any{rawTokenKey: "empty"}}); err == nil {
		t.Fatal("an exchange answer with no token is an error")
	}
	f.tokenStatus = http.StatusForbidden
	if _, err := m.Exchange(context.Background(), &auth.TokenInfo{Extra: map[string]any{rawTokenKey: "refused"}}); err == nil ||
		!strings.Contains(err.Error(), "invalid_grant nope") {
		t.Fatalf("the IdP's refusal is passed on: %v", err)
	}
	m.tokenURL = "http://127.0.0.1:1/token"
	if _, err := m.Exchange(context.Background(), &auth.TokenInfo{Extra: map[string]any{rawTokenKey: "down"}}); err == nil {
		t.Fatal("an unreachable token endpoint is an error")
	}
	m.tokenURL = "://bad"
	if _, err := m.Exchange(context.Background(), &auth.TokenInfo{Extra: map[string]any{rawTokenKey: "bad"}}); err == nil {
		t.Fatal("a malformed token endpoint is an error")
	}
	none := newTestMCP(t, f, "")
	if none.CanExchange() {
		t.Fatal("no client, no exchange")
	}
	if _, err := none.Exchange(context.Background(), info); err == nil {
		t.Fatal("exchange without a client is refused")
	}
}

func keyOf(s string) [32]byte { return sha256.Sum256([]byte(s)) }
