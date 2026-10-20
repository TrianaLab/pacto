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
	expect(t, err == nil && got == "exchanged-pacto-cli", "exchange: %q %v", got, err)
	ex := f.exchanges[0]
	expect(t, ex.Get("grant_type") == "urn:ietf:params:oauth:grant-type:token-exchange", "grant %v", ex)
	expect(t, ex.Get("subject_token") == tok, "subject %v", ex)
	expect(t, ex.Get("subject_token_type") == "urn:ietf:params:oauth:token-type:access_token", "type %v", ex)
	expect(t, ex.Get("audience") == "pacto-cli", "audience %v", ex)
	_, _ = m.Exchange(context.Background(), info)
	expect(t, len(f.exchanges) == 1, "a second call within the lifetime is cached")
	// The cache ends at the earlier expiry: here the exchanged token's 60s.
	m.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	_, _ = m.Exchange(context.Background(), info)
	expect(t, len(f.exchanges) == 2 && len(m.cache) == 1, "an expired cache entry is dropped and exchanged again: %d %d", len(f.exchanges), len(m.cache))
	m.now = time.Now
	// An exchange with no expires_in is cached until the subject token expires.
	f.exchangeOut = map[string]any{"access_token": "long"}
	info2 := &auth.TokenInfo{Expiration: time.Now().Add(time.Minute), Extra: map[string]any{rawTokenKey: "other"}}
	got, _ = m.Exchange(context.Background(), info2)
	expect(t, got == "long" && m.cache[keyOf("other")].until.Equal(info2.Expiration), "no expires_in: cached until the subject expires")
}

func TestMCPExchangeFailures(t *testing.T) {
	f := newIdP(t)
	m := newTestMCP(t, f, "pacto-mcp")
	exchange := func(token string) error {
		_, err := m.Exchange(context.Background(), &auth.TokenInfo{Extra: map[string]any{rawTokenKey: token}})
		return err
	}
	f.exchangeOut = map[string]any{"token_type": "x"}
	expect(t, exchange("empty") != nil, "an exchange answer with no token is an error")
	f.tokenStatus = http.StatusForbidden
	err := exchange("refused")
	expect(t, err != nil && strings.Contains(err.Error(), "invalid_grant nope"), "the IdP's refusal is passed on: %v", err)
	m.tokenURL = "http://127.0.0.1:1/token"
	expect(t, exchange("down") != nil, "an unreachable token endpoint is an error")
	m.tokenURL = "://bad"
	expect(t, exchange("bad") != nil, "a malformed token endpoint is an error")
	none := newTestMCP(t, f, "")
	expect(t, !none.CanExchange(), "no client, no exchange")
	_, err = none.Exchange(context.Background(), &auth.TokenInfo{})
	expect(t, err != nil, "exchange without a client is refused")
}

func keyOf(s string) [32]byte { return sha256.Sum256([]byte(s)) }
