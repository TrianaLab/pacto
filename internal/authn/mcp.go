package authn

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// MCPConfig is the remote MCP server's identity: the issuer its callers'
// tokens come from, the audience they must carry and, for delivery, the OAuth
// client it exchanges them as.
type MCPConfig struct {
	IssuerURL string
	// Audience every incoming token must name (the MCP resource by default).
	Audience string
	// ClientID and ClientSecret are the server's own OAuth client, used only
	// for token exchange. Empty means no exchange.
	ClientID     string
	ClientSecret string
	// ExchangeAudience is the audience asked for in an exchange: one the
	// apiserver accepts.
	ExchangeAudience string
	HTTPClient       *http.Client
}

// MCP verifies bearer tokens and exchanges them for the apiserver.
type MCP struct {
	cfg      MCPConfig
	client   *http.Client
	verifier *oidc.IDTokenVerifier
	tokenURL string
	now      func() time.Time

	mu    sync.Mutex
	cache map[[32]byte]exchanged
}

type exchanged struct {
	token string
	until time.Time
}

// rawTokenKey is where Verify keeps the verified token, for the exchange.
const rawTokenKey = "token"

// NewMCP reads the issuer's metadata. Unlike the dashboard it fails at once:
// a remote MCP server with auth configured can serve nothing without it.
func NewMCP(cfg MCPConfig) (*MCP, error) {
	if cfg.IssuerURL == "" || cfg.Audience == "" {
		return nil, errors.New("remote MCP auth needs an issuer and an audience")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	p, _, err := discover(client, cfg.IssuerURL)
	if err != nil {
		return nil, err
	}
	m := &MCP{cfg: cfg, client: client, tokenURL: p.Endpoint().TokenURL, now: time.Now, cache: map[[32]byte]exchanged{}}
	m.verifier = p.Verifier(&oidc.Config{ClientID: cfg.Audience, Now: func() time.Time { return m.now() }})
	return m, nil
}

// Verify is the bearer check every request goes through (go-sdk's
// auth.TokenVerifier): signature, issuer, audience and expiry. The user id is
// issuer#sub, the apiserver's own username form.
func (m *MCP) Verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	id, err := m.verifier.Verify(ctx, token)
	if err != nil {
		Refused(ctx, r, "remote MCP refused a bearer token", "issuer", m.cfg.IssuerURL, "audience", m.cfg.Audience, "reason", err.Error())
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}
	var c struct {
		Scope string `json:"scope"`
	}
	_ = id.Claims(&c) // Verify already decoded these claims
	return &auth.TokenInfo{UserID: id.Issuer + "#" + id.Subject, Expiration: id.Expiry, Scopes: strings.Fields(c.Scope),
		Extra: map[string]any{rawTokenKey: token}}, nil
}

// PersonFromTokenInfo is who a request Verify accepted is to the apiserver,
// mapped as c names it; no caller is Anonymous.
func (c Claims) PersonFromTokenInfo(info *auth.TokenInfo) Person {
	if info == nil {
		return Anonymous
	}
	token, _ := info.Extra[rawTokenKey].(string)
	return c.Person(token)
}

// CanExchange reports whether token exchange is configured.
func (m *MCP) CanExchange() bool { return m.cfg.ClientID != "" }

// Exchange trades the caller's verified token for one whose audience the
// apiserver accepts (RFC 8693), authenticating as the server's own client.
// The MCP token itself is never sent to the apiserver. Answers are cached by
// the incoming token's hash until the earlier of the two expiries.
func (m *MCP) Exchange(ctx context.Context, info *auth.TokenInfo) (string, error) {
	if !m.CanExchange() {
		return "", errors.New("token exchange is not configured")
	}
	token, _ := info.Extra[rawTokenKey].(string)
	key := sha256.Sum256([]byte(token))
	now := m.now()
	m.mu.Lock()
	for k, e := range m.cache { // drop what expired, so the cache stays bounded by live callers
		if !now.Before(e.until) {
			delete(m.cache, k)
		}
	}
	e, ok := m.cache[key]
	m.mu.Unlock()
	if ok {
		return e.token, nil
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	form := url.Values{
		"grant_type":           {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":        {token},
		"subject_token_type":   {"urn:ietf:params:oauth:token-type:access_token"},
		"requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":             {m.cfg.ExchangeAudience},
	}
	if err := postForm(ctx, m.client, m.tokenURL, m.cfg.ClientID, m.cfg.ClientSecret, form, &out); err != nil {
		return "", fmt.Errorf("exchanging your token for the cluster failed: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("the identity provider's token exchange returned no access token")
	}
	until := info.Expiration
	if out.ExpiresIn > 0 {
		if t := now.Add(time.Duration(out.ExpiresIn) * time.Second); t.Before(until) {
			until = t
		}
	}
	m.mu.Lock()
	m.cache[key] = exchanged{token: out.AccessToken, until: until}
	m.mu.Unlock()
	return out.AccessToken, nil
}
