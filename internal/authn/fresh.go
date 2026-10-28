package authn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// FreshConfig is the OpenID Connect client `pacto delivery approve` logs the
// person in with. It is meant to be a client nothing else uses, so a token
// from the person's everyday kubeconfig login never carries the login time
// the cluster needs for an approval.
type FreshConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	ExtraScopes  []string
	// Port is the loopback port the identity provider sends the browser back
	// to; 0 picks a free one.
	Port int
	// Prompt shows the person the address to open.
	Prompt func(url string)
	// HTTPClient reaches the identity provider; nil is a default client.
	HTTPClient *http.Client
	now        func() time.Time
}

// FreshIDToken makes the person log in again in their browser (prompt=login,
// max_age=0) and returns the ID token of that login. It refuses a token whose
// auth_time is older than the call, so a session the identity provider
// already held cannot stand in for the person. The token is never stored: it
// is meant for one request, then dropped.
func FreshIDToken(ctx context.Context, c FreshConfig) (string, error) {
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	now := c.now
	if now == nil {
		now = time.Now
	}
	p, _, err := discover(client, c.IssuerURL)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.Port)))
	if err != nil {
		return "", fmt.Errorf("listening for the login's answer: %w", err)
	}
	conf := oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: p.Endpoint(),
		RedirectURL: "http://" + ln.Addr().String() + "/callback",
		Scopes:      append([]string{oidc.ScopeOpenID, "profile", "groups"}, c.ExtraScopes...)}
	state, nonce, verifier := random(16), random(16), oauth2.GenerateVerifier()
	started := now()

	codes, refused := make(chan string, 1), make(chan error, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: answer(state, codes, refused)}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	c.Prompt(conf.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "login"), oauth2.SetAuthURLParam("max_age", "0")))
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	var code string
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("waiting for the login: %w", ctx.Err())
	case err := <-refused:
		return "", err
	case code = <-codes:
	}
	ctx = oidc.ClientContext(ctx, client)
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", fmt.Errorf("exchanging the login code: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.Verifier(&oidc.Config{ClientID: c.ClientID, Now: now}).Verify(ctx, raw)
	if err != nil {
		return "", fmt.Errorf("the identity provider's ID token does not verify: %w", err)
	}
	if err := fresh(id, nonce, started); err != nil {
		return "", err
	}
	return raw, nil
}

// answer is the loopback listener: it hands the login's code, or the
// identity provider's refusal, to FreshIDToken. Anything else on the port
// (another local process guessing) is refused without ending the wait.
func answer(state string, codes chan<- string, refused chan<- error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/callback" || q.Get("state") != state {
			http.Error(w, "this is not the login pacto started", http.StatusBadRequest)
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, "the identity provider refused the login", http.StatusUnauthorized)
			select {
			case refused <- fmt.Errorf("the identity provider refused the login: %s %s", e, q.Get("error_description")):
			default:
			}
			return
		}
		_, _ = fmt.Fprintln(w, "Logged in. Go back to the terminal.")
		select {
		case codes <- q.Get("code"):
		default:
		}
	})
}

// fresh checks the ID token is this login's, carries an auth_time and the
// login happened after started (any login does for the zero time).
func fresh(id *oidc.IDToken, nonce string, started time.Time) error {
	at, ok := authTime(id)
	switch {
	case id.Nonce != nonce:
		return errors.New("the ID token is not for this login")
	case !ok:
		return errors.New("the ID token carries no auth_time, so it cannot show the login is fresh")
	case at.Before(started.Add(-skew)):
		return errors.New("the identity provider did not ask you to log in again; a decision needs a fresh login")
	}
	return nil
}
