package authn

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// browse plays the person's browser: it opens the login address, lets the
// fake identity provider log sub in, then follows the redirect back to the
// loopback listener. tweak may change the answer before it is sent.
func browse(t *testing.T, f *fakeIdP, sub string, asked *url.Values, tweak func(code string, q url.Values) url.Values) func(string) {
	return func(loc string) {
		code, q := f.authorize(t, loc, sub)
		*asked = q
		back := url.Values{"state": {q.Get("state")}, "code": {code}}
		if tweak != nil {
			back = tweak(code, q)
		}
		go func() {
			// A stranger on the port first: refused, and the wait goes on.
			if resp, err := http.Get(q.Get("redirect_uri") + "?state=guess&code=x"); err == nil {
				_ = resp.Body.Close()
			}
			if resp, err := http.Get(q.Get("redirect_uri") + "?" + back.Encode()); err == nil {
				_ = resp.Body.Close()
			}
		}()
	}
}

func TestFreshIDToken(t *testing.T) {
	f := newIdP(t)
	var asked url.Values
	raw, err := FreshIDToken(context.Background(), FreshConfig{IssuerURL: f.srv.URL, ClientID: "pacto-approve",
		ExtraScopes: []string{"audience:server:client_id:pacto-cli"}, Prompt: browse(t, f, "alice", &asked, nil)})
	expect(t, err == nil && raw != "", "fresh login: %v", err)
	expect(t, asked.Get("prompt") == "login" && asked.Get("max_age") == "0", "the login is forced: %v", asked)
	expect(t, asked.Get("code_challenge_method") == "S256" && strings.HasPrefix(asked.Get("redirect_uri"), "http://127.0.0.1:"), "PKCE on loopback: %v", asked)
	expect(t, strings.Contains(asked.Get("scope"), "audience:server:client_id:pacto-cli"), "extra scopes: %v", asked)
}

func TestFreshIDTokenRefusals(t *testing.T) {
	ctx := context.Background()
	run := func(f *fakeIdP, tweak func(string, url.Values) url.Values, mod func(*FreshConfig)) error {
		var asked url.Values
		c := FreshConfig{IssuerURL: f.srv.URL, ClientID: "pacto-approve", HTTPClient: &http.Client{Timeout: 5 * time.Second},
			Prompt: browse(t, f, "alice", &asked, tweak)}
		if mod != nil {
			mod(&c)
		}
		_, err := FreshIDToken(ctx, c)
		return err
	}
	has := func(err error, want string) bool { return err != nil && strings.Contains(err.Error(), want) }

	// A login the identity provider did not repeat: an old auth_time.
	f := newIdP(t)
	f.authTime = time.Now().Add(-time.Hour)
	expect(t, has(run(f, nil, nil), "did not ask you to log in again"), "a stale login is refused")

	f = newIdP(t)
	f.noAuthTime = true
	expect(t, has(run(f, nil, nil), "no auth_time"), "a token without auth_time is refused")

	f = newIdP(t)
	expect(t, has(run(f, func(code string, q url.Values) url.Values {
		f.mu.Lock()
		g := f.codes[code]
		g.nonce = "other"
		f.codes[code] = g
		f.mu.Unlock()
		return url.Values{"state": {q.Get("state")}, "code": {code}}
	}, nil), "not for this login"), "a token for another login is refused")

	expect(t, has(run(newIdP(t), func(_ string, q url.Values) url.Values {
		return url.Values{"state": {q.Get("state")}, "error": {"access_denied"}, "error_description": {"no"}}
	}, nil), "refused the login: access_denied no"), "the identity provider's refusal ends the wait")

	f = newIdP(t)
	f.tokenStatus = http.StatusBadRequest
	expect(t, has(run(f, nil, nil), "exchanging the login code"), "a failed exchange is refused")

	expect(t, has(run(newIdP(t), nil, func(c *FreshConfig) { c.now = func() time.Time { return time.Now().Add(3 * time.Hour) } }),
		"does not verify"), "an expired token is refused")

	expect(t, has(run(newIdP(t), nil, func(c *FreshConfig) { c.IssuerURL = "http://127.0.0.1:1" }), "reading the identity provider"),
		"an unreachable identity provider is reported")

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	port := busy.Addr().(*net.TCPAddr).Port
	expect(t, has(run(newIdP(t), nil, func(c *FreshConfig) { c.Port = port }), "listening for the login's answer"), "a busy port is reported")

	gone, cancel := context.WithCancel(ctx)
	cancel()
	f = newIdP(t)
	_, err = FreshIDToken(gone, FreshConfig{IssuerURL: f.srv.URL, ClientID: "pacto-approve", Prompt: func(string) {}})
	expect(t, has(err, "waiting for the login"), "giving up the wait is reported: %v", err)
}
