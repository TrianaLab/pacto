package authn

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/pkg/logging"
)

var (
	agentSA   = Person{User: "system:serviceaccount:agents:ci-bot", Groups: []string{"system:serviceaccounts"}}
	errBearer = apierrors.NewUnauthorized("Unauthorized")
	// errThrottled is the API server not answering: no refusal of the token.
	errThrottled = apierrors.NewTooManyRequests("throttled", 1)
)

// answers is a fixed answer from one side of MCPTokenReview, counting calls.
type answers struct {
	r     Reviewed
	err   error
	calls int
}

func (a *answers) check(context.Context, string) (Reviewed, error) {
	a.calls++
	return a.r, a.err
}

func TestMCPTokenReviewMatrix(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name          string
		token         string
		aud, bearer   answers
		user          string
		credential    bool
		wantErr       string
		serverSide    bool // a 500, not a 401: the token may be fine
		reviewed      bool // the audience review ran
		bearerChecked bool
	}{
		{name: "SA audience-only", aud: answers{r: Reviewed{Person: agentSA}}, bearer: answers{err: errBearer},
			user: agentSA.User, reviewed: true, bearerChecked: true},
		{name: "SA dual", aud: answers{r: Reviewed{Person: agentSA}}, bearer: answers{r: Reviewed{Person: agentSA, Bearer: true, AuthTime: "1"}},
			user: agentSA.User, credential: true, reviewed: true, bearerChecked: true},
		{name: "SA apiserver-only", aud: answers{err: errNotAudience}, bearer: answers{r: Reviewed{Person: agentSA, Bearer: true}},
			wantErr: "TokenAudienceRequired", reviewed: true, bearerChecked: true},
		{name: "SA wrong audience", aud: answers{err: errNotAudience}, bearer: answers{err: errBearer},
			wantErr: "did not accept this token", reviewed: true, bearerChecked: true},
		{name: "person JWT bearer", token: fakeJWT(`{"sub":"ann"}`), bearer: answers{r: Reviewed{Person: personAnn, Bearer: true}},
			user: personAnn.User, credential: true, bearerChecked: true},
		{name: "person opaque bearer", token: "k8s-aws-v1.aHR0cHM6Ly9zdHM", bearer: answers{r: Reviewed{Person: personAnn}},
			user: personAnn.User, credential: true, bearerChecked: true},
		{name: "person not accepted", aud: answers{r: Reviewed{Person: personAnn}}, bearer: answers{err: errBearer},
			wantErr: "did not accept this token", reviewed: true, bearerChecked: true},
		{name: "review forbidden", aud: answers{err: errReviewDenied}, bearer: answers{err: errBearer},
			wantErr: "may not review tokens", serverSide: true, reviewed: true, bearerChecked: true},
		{name: "SA dual, bearer check throttled", aud: answers{r: Reviewed{Person: agentSA}}, bearer: answers{err: errThrottled},
			wantErr: "did not answer", serverSide: true, bearerChecked: true},
		{name: "person, bearer check times out", token: "k8s-aws-v1.opaque", aud: answers{r: Reviewed{Person: personAnn}},
			bearer: answers{err: context.DeadlineExceeded}, wantErr: "did not answer", serverSide: true, bearerChecked: true},
		{name: "review unreachable", aud: answers{err: errors.New("dial tcp: connection refused")}, bearer: answers{err: errBearer},
			wantErr: "could not review", serverSide: true, reviewed: true, bearerChecked: true},
		{name: "garbage", token: "not-a-token", aud: answers{err: errNotAudience}, bearer: answers{err: errBearer},
			wantErr: "did not accept this token", reviewed: true, bearerChecked: true},
		{name: "oversized", token: strings.Repeat("a", maxTokenBody+1), wantErr: "too large or expired"},
	} {
		t.Run(c.name, func(t *testing.T) {
			token := c.token
			if token == "" {
				token = "tok"
			}
			m := NewTokenMCP(MCPTokenReview(c.aud.check, c.bearer.check))
			info, err := m.Verify(ctx, token, nil)
			expect(t, c.aud.calls > 0 == c.reviewed && c.bearer.calls > 0 == c.bearerChecked,
				"audience asked %d, bearer asked %d", c.aud.calls, c.bearer.calls)
			if c.wantErr != "" {
				expect(t, err != nil && strings.Contains(err.Error(), c.wantErr) && errors.Is(err, auth.ErrInvalidToken) != c.serverSide, "refusal %v", err)
				expect(t, len(m.cache) == 0, "a failure is never cached")
				return
			}
			expect(t, err == nil && info.UserID == c.user && (&TokenMCP{}).PersonOf(info).User == c.user, "verified %+v %v", info, err)
			got, err := m.Exchange(ctx, info)
			if c.credential {
				expect(t, err == nil && got == token, "a credential exchanges to itself: %v", err)
			} else {
				expect(t, errors.Is(err, ErrNoCredential) && got == "", "no credential: %q %v", got, err)
			}
		})
	}
}

func TestMCPTokenReviewDualNeedsSameUser(t *testing.T) {
	other := Person{User: "system:serviceaccount:agents:other"}
	aud, bearer := answers{r: Reviewed{Person: agentSA}}, answers{r: Reviewed{Person: other, Bearer: true}}
	rv, err := MCPTokenReview(aud.check, bearer.check)(context.Background(), "tok")
	expect(t, err == nil && rv.Person.User == agentSA.User && !rv.Bearer, "two users are no credential: %+v %v", rv, err)
}

func TestMCPTokenReviewMatchesDashboardForOpaqueToken(t *testing.T) {
	ctx := context.Background()
	const token = "k8s-aws-v1.opaque"
	aud, bearer := answers{err: errNotAudience}, answers{r: Reviewed{Person: personAnn}}
	want, err := DashboardTokenReview(aud.check, bearer.check)(ctx, token)
	expect(t, err == nil && want.Bearer, "the dashboard accepts it: %+v %v", want, err)
	got, err := MCPTokenReview(aud.check, bearer.check)(ctx, token)
	expect(t, err == nil && got.Bearer && got.Person.User == want.Person.User, "MCP accepts the same person: %+v %v", got, err)

	now := time.Unix(1_000_000, 0)
	m := NewTokenMCP(MCPTokenReview(aud.check, bearer.check))
	m.now = func() time.Time { return now }
	calls := bearer.calls
	info, err := m.Verify(ctx, token, nil)
	expect(t, err == nil && info.Expiration.Equal(now.Add(30*time.Second)), "cached 30s: %+v %v", info, err)
	now = now.Add(29 * time.Second)
	_, _ = m.Verify(ctx, token, nil)
	expect(t, bearer.calls == calls+1, "the second verify is cached: %d", bearer.calls-calls)
}

func TestMCPTokenReviewNeverPostsPersonToken(t *testing.T) {
	review := MCPTokenReview(func(context.Context, string) (Reviewed, error) {
		t.Fatal("a person's accepted token entered a TokenReview body")
		return Reviewed{}, nil
	}, func(context.Context, string) (Reviewed, error) { return Reviewed{Person: personAnn}, nil })
	rv, err := review(context.Background(), "person-token")
	expect(t, err == nil && rv.Bearer, "the person acts as themselves: %+v %v", rv, err)
}

func TestDashboardSignInUnchangedForDualAudienceToken(t *testing.T) {
	aud, bearer := answers{r: Reviewed{Person: agentSA}}, answers{r: Reviewed{Person: agentSA, Bearer: true}}
	rv, err := DashboardTokenReview(aud.check, bearer.check)(context.Background(), "tok")
	expect(t, err == nil && !rv.Bearer && rv.Person.User == agentSA.User, "the dashboard keeps a link session: %+v %v", rv, err)
}

// countingReview accepts every token as a person, counting the reviews.
func countingReview(n *atomic.Int32) TokenReview {
	return func(context.Context, string) (Reviewed, error) {
		n.Add(1)
		return Reviewed{Person: personAnn, Bearer: true}, nil
	}
}

func TestTokenMCPRefusesJunkWithoutReview(t *testing.T) {
	var n atomic.Int32
	m := NewTokenMCP(countingReview(&n))
	expired := fakeJWT(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(-time.Minute).Unix()))
	for _, token := range []string{strings.Repeat("a", maxTokenBody+1), expired} {
		_, err := m.Verify(context.Background(), token, nil)
		expect(t, errors.Is(err, auth.ErrInvalidToken), "refused: %v", err)
	}
	expect(t, n.Load() == 0, "junk was reviewed %d times", n.Load())
	_, err := m.Verify(context.Background(), "k8s-aws-v1.aHR0cHM6Ly9zdHM", nil)
	expect(t, err == nil && n.Load() == 1, "an opaque token is reviewed: %v", err)
}

func TestTokenMCPReviewConcurrencyBounded(t *testing.T) {
	const callers = 20
	entered, release := make(chan struct{}, callers), make(chan struct{})
	var inFlight, peak atomic.Int32
	m := NewTokenMCP(func(context.Context, string) (Reviewed, error) {
		n := inFlight.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		entered <- struct{}{}
		<-release
		inFlight.Add(-1)
		return Reviewed{Person: personAnn, Bearer: true}, nil
	})
	exp := time.Now().Add(time.Hour).Unix()
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			_, err := m.Verify(context.Background(), fakeJWT(fmt.Sprintf(`{"exp":%d,"jti":"%d"}`, exp, i)), nil)
			errs <- err
		})
	}
	for range tokenMCPMaxReviews {
		<-entered
	}
	for range callers - tokenMCPMaxReviews {
		err := <-errs
		expect(t, errors.Is(err, ErrReviewBusy) && !errors.Is(err, auth.ErrInvalidToken), "a busy review: %v", err)
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		expect(t, err == nil, "a bounded review passes: %v", err)
	}
	expect(t, peak.Load() == tokenMCPMaxReviews, "peak %d reviews at once", peak.Load())
}

func TestTokenMCPCacheBoundedByExpiry(t *testing.T) {
	start := time.Unix(1_000_000, 0)
	for _, c := range []struct {
		name  string
		token string
		ttl   time.Duration
	}{
		{"exp in 10s", fakeJWT(fmt.Sprintf(`{"exp":%d}`, start.Add(10*time.Second).Unix())), 10 * time.Second},
		{"exp in 1h", fakeJWT(fmt.Sprintf(`{"exp":%d}`, start.Add(time.Hour).Unix())), 30 * time.Second},
		{"opaque", "k8s-aws-v1.opaque", 30 * time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			var n atomic.Int32
			now := start
			m := NewTokenMCP(countingReview(&n))
			m.now = func() time.Time { return now }
			info, err := m.Verify(context.Background(), c.token, nil)
			expect(t, err == nil && !info.Expiration.IsZero() && info.Expiration.Equal(start.Add(c.ttl)), "deadline %v %v", info, err)
			now = start.Add(c.ttl - time.Second)
			_, _ = m.Verify(context.Background(), c.token, nil)
			expect(t, n.Load() == 1, "cached before the deadline: %d", n.Load())
			now = start.Add(c.ttl)
			_, _ = m.Verify(context.Background(), "k8s-aws-v1.other", nil) // prunes the expired entry
			expect(t, len(m.cache) == 1, "expired entries are pruned: %d", len(m.cache))
			if c.ttl == 30*time.Second {
				_, err = m.Verify(context.Background(), c.token, nil)
				expect(t, err == nil && n.Load() == 3, "re-reviewed after the deadline: %d %v", n.Load(), err)
			}
		})
	}
}

func TestTokenMCPFailureNotCached(t *testing.T) {
	var n atomic.Int32
	m := NewTokenMCP(func(context.Context, string) (Reviewed, error) {
		n.Add(1)
		return Reviewed{}, errBearer
	})
	for range 2 {
		_, err := m.Verify(context.Background(), "tok", nil)
		expect(t, errors.Is(err, auth.ErrInvalidToken), "refused: %v", err)
	}
	expect(t, n.Load() == 2 && len(m.cache) == 0, "a failure is never cached: %d reviews", n.Load())
}

func TestTokenMCPExchangeOnlyForCredentials(t *testing.T) {
	m := NewTokenMCP(nil)
	expect(t, m.CanExchange(), "a token MCP always exchanges")
	got, err := m.Exchange(context.Background(), &auth.TokenInfo{Extra: map[string]any{rawTokenKey: "tok", bearerKey: true}})
	expect(t, err == nil && got == "tok", "a credential: %q %v", got, err)
	got, err = m.Exchange(context.Background(), &auth.TokenInfo{Extra: map[string]any{rawTokenKey: "tok"}})
	expect(t, errors.Is(err, ErrNoCredential) && got == "", "no credential: %q %v", got, err)
	expect(t, (&TokenMCP{}).PersonOf(nil).User == Anonymous.User, "no caller is Anonymous")
}

func TestTokenMCPNeverLogsToken(t *testing.T) {
	logs := &strings.Builder{}
	const secret = "s3cr3t-token-value"
	m := NewTokenMCP(func(context.Context, string) (Reviewed, error) { return Reviewed{}, errBearer })
	r := logged(httptest.NewRequest("POST", "http://dash.example/mcp", nil), logs)
	_, err := m.Verify(r.Context(), secret, r)
	expect(t, err != nil && !strings.Contains(err.Error(), secret), "the error carries the token: %v", err)
	expect(t, strings.Contains(logs.String(), "remote MCP refused a token") && !strings.Contains(logs.String(), secret), "log:\n%s", logs)
	logs.Reset()
	_, err = m.Verify(logging.WithLogger(context.Background(), logging.New(logs, false)), secret+strings.Repeat("x", maxTokenBody), nil)
	expect(t, err != nil && !strings.Contains(err.Error(), secret) && !strings.Contains(logs.String(), secret), "oversized: %v\n%s", err, logs)
}

func TestAPIAudience(t *testing.T) {
	dir := t.TempDir()
	file := func(name, body string) *rest.Config {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return &rest.Config{BearerTokenFile: p}
	}
	for _, c := range []struct {
		name    string
		cluster *rest.Config
		want    string
	}{
		{"string aud", file("s", fakeJWT(`{"aud":"https://kubernetes.default.svc"}`)+"\n"), "https://kubernetes.default.svc"},
		{"array aud", file("a", fakeJWT(`{"aud":["https://oidc.eks.example","sts"]}`)), "https://oidc.eks.example"},
		{"empty array", file("e", fakeJWT(`{"aud":[]}`)), ""},
		{"non-JWT file", file("n", "opaque"), ""},
		{"missing file", &rest.Config{BearerTokenFile: filepath.Join(dir, "missing")}, ""},
		{"no cluster", nil, ""},
	} {
		if got := APIAudience(c.cluster); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestUnverifiedIssuer(t *testing.T) {
	for token, want := range map[string]string{
		fakeJWT(`{"iss":"https://dex.example"}`): "https://dex.example",
		fakeJWT(`{"sub":"x"}`):                   "",
		fakeJWT(`{"iss":7}`):                     "",
		"k8s-aws-v1.opaque":                      "",
	} {
		if got := UnverifiedIssuer(token); got != want {
			t.Errorf("%q: %q, want %q", token, got, want)
		}
	}
}
