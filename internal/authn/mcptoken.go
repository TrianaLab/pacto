package authn

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"k8s.io/client-go/rest"
)

// ErrNoCredential is a verified caller whose token reads the fleet only: the
// cluster does not accept it as a bearer, so nothing is sent to it.
var ErrNoCredential = errors.New("this token reads the fleet only: it is no credential for the cluster")

// ErrReviewBusy is a token not reviewed because too many reviews are in
// flight. It never wraps auth.ErrInvalidToken: the client should retry, not
// sign in again.
var ErrReviewBusy = errors.New("too many token reviews in flight; retry")

const (
	// tokenMCPCacheTTL is the longest a verdict is reused before the API
	// server is asked again, so a revoked token stops working within it.
	tokenMCPCacheTTL = 30 * time.Second
	// tokenMCPMaxReviews bounds uncached reviews in flight.
	// ponytail: one process-wide semaphore; per-client buckets if one client
	// starves the rest, and a 503 with Retry-After needs a ResponseWriter
	// wrapper around the SDK middleware.
	tokenMCPMaxReviews = 8

	personKey = "person"
	bearerKey = "bearer"
)

// TokenMCP verifies Kubernetes tokens for the remote MCP server (go-sdk's
// auth.TokenVerifier plus the exchange the server asks for) with the
// dashboard's sign-in policy (MCPTokenReview).
type TokenMCP struct {
	review TokenReview
	now    func() time.Time
	sem    chan struct{}

	// ponytail: one cache map under a mutex, bounded by live tokens; add a
	// size bound if a dashboard ever serves more agent tokens than fit.
	mu    sync.Mutex
	cache map[[32]byte]tokenVerdict
}

type tokenVerdict struct {
	person Person
	bearer bool
	until  time.Time
}

// NewTokenMCP verifies every token with review.
func NewTokenMCP(review TokenReview) *TokenMCP {
	return &TokenMCP{review: review, now: time.Now, sem: make(chan struct{}, tokenMCPMaxReviews), cache: map[[32]byte]tokenVerdict{}}
}

// Verify names who a bearer is. A token over the sign-in bound or a JWT whose
// exp has passed is refused before any review. A verdict is reused until the
// earlier of 30s and the JWT's exp, and a refusal is never reused. The token
// never reaches a log line, an error or the cache key in clear.
func (m *TokenMCP) Verify(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	now := m.now()
	until, ok := preReview(token, now)
	if !ok {
		Refused(ctx, r, "remote MCP refused a token", "reason", "the token is too large or expired")
		return nil, fmt.Errorf("%w: the token is too large or expired", auth.ErrInvalidToken)
	}
	key := sha256.Sum256([]byte(token))
	v, ok := m.cachedVerdict(key, now)
	if !ok {
		var err error
		if v, err = m.reviewOnce(ctx, r, token, until); err != nil {
			return nil, err
		}
		m.store(key, v, now)
	}
	return &auth.TokenInfo{UserID: v.person.User, Expiration: v.until,
		Extra: map[string]any{rawTokenKey: token, personKey: v.person, bearerKey: v.bearer}}, nil
}

// preReview refuses what needs no review and returns until when a verdict may
// be reused: 30s from now, or the JWT's exp when that comes first.
func preReview(token string, now time.Time) (time.Time, bool) {
	if len(token) > maxTokenBody {
		return time.Time{}, false
	}
	until := now.Add(tokenMCPCacheTTL)
	if exp := tokenExpiry(token); !exp.IsZero() {
		if !now.Before(exp) {
			return time.Time{}, false
		}
		if exp.Before(until) {
			until = exp
		}
	}
	return until, true
}

func (m *TokenMCP) cachedVerdict(key [32]byte, now time.Time) (tokenVerdict, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.cache[key]
	return v, ok && now.Before(v.until)
}

func (m *TokenMCP) store(key [32]byte, v tokenVerdict, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, old := range m.cache { // drop what expired, so the cache stays bounded by live callers
		if !now.Before(old.until) {
			delete(m.cache, k)
		}
	}
	m.cache[key] = v
}

// reviewOnce asks the API server about token, when a review slot is free.
func (m *TokenMCP) reviewOnce(ctx context.Context, r *http.Request, token string, until time.Time) (tokenVerdict, error) {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	default:
		return tokenVerdict{}, ErrReviewBusy
	}
	rv, err := m.review(ctx, token)
	if why := refuseReviewed(rv, err, false); why != "" {
		Refused(ctx, r, "remote MCP refused a token", "reason", why)
		return tokenVerdict{}, fmt.Errorf("%w: %s", auth.ErrInvalidToken, why)
	}
	return tokenVerdict{person: rv.Person, bearer: rv.Bearer, until: until}, nil
}

// CanExchange is always true: Exchange decides per caller.
func (m *TokenMCP) CanExchange() bool { return true }

// Exchange returns the caller's own token when the cluster accepts it as a
// bearer, else ErrNoCredential: the caller reads the fleet only.
func (m *TokenMCP) Exchange(_ context.Context, info *auth.TokenInfo) (string, error) {
	if bearer, _ := info.Extra[bearerKey].(bool); bearer {
		token, _ := info.Extra[rawTokenKey].(string)
		return token, nil
	}
	return "", ErrNoCredential
}

// PersonOf is who TokenMCP.Verify named; no caller is Anonymous.
func PersonOf(info *auth.TokenInfo) Person {
	if info == nil {
		return Anonymous
	}
	p, _ := info.Extra[personKey].(Person)
	return p
}

// APIAudience is the first audience of the dashboard's own projected token,
// the audience the API server accepts, or "" when it cannot be read. A
// refusal names it, so an agent knows the second audience its token needs.
func APIAudience(cluster *rest.Config) string {
	if cluster == nil {
		return ""
	}
	b, err := os.ReadFile(cluster.BearerTokenFile)
	if err != nil {
		return ""
	}
	raw := jwtClaims(strings.TrimSpace(string(b)))["aud"]
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil && len(many) > 0 {
		return many[0]
	}
	return ""
}
