// Command oidclogin plays a person's browser against a real identity
// provider, so the kind delivery e2e proves sign-in with real logins instead
// of impersonated people.
//
//	oidclogin dashboard -dashboard URL -user U -password P [-fresh] [-session S]
//	    signs in to the dashboard through the provider and prints the session
//	    cookie; -fresh asks for a login the person must repeat, -session is the
//	    current session the fresh login rotates.
//	oidclogin token -issuer URL -client ID -secret S -redirect URL -scope "..." -user U -password P
//	    runs the authorization code flow as that client and prints the ID token.
//
// The provider's own cookies (its session) are kept in -jar between runs, so a
// second plain login is single sign-on and keeps the first login's auth_time,
// which is how the e2e gets a login older than fifteen minutes. The answer is
// read by hand rather than through a cookie jar because the dashboard's
// __Host- cookies are Secure and the e2e reaches it over plain HTTP on
// loopback, which a browser treats as secure and Go's jar does not.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// sessionCookie is the dashboard's session cookie.
const sessionCookie = "__Host-pacto-session"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "oidclogin:", err)
		os.Exit(1)
	}
}

type browser struct {
	client   *http.Client
	user     string
	password string
	jarPath  string
	// cookies per host
	cookies map[string]map[string]string
}

func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: oidclogin dashboard|token [flags]")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	ca := fs.String("ca", "", "CA certificate the provider's TLS chains to")
	user := fs.String("user", "", "login")
	password := fs.String("password", "", "password")
	jar := fs.String("jar", "", "file keeping the provider's cookies between runs")
	dash := fs.String("dashboard", "", "dashboard base URL")
	fresh := fs.Bool("fresh", false, "ask for a login the person must repeat")
	session := fs.String("session", "", "the current dashboard session, rotated by a fresh login")
	issuer := fs.String("issuer", "", "issuer URL")
	clientID := fs.String("client", "", "client id")
	secret := fs.String("secret", "", "client secret")
	redirect := fs.String("redirect", "", "registered redirect URL")
	scope := fs.String("scope", "openid", "scopes")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	b, err := newBrowser(*ca, *user, *password, *jar)
	if err != nil {
		return err
	}
	var v string
	switch args[0] {
	case "dashboard":
		v, err = b.dashboard(*dash, *fresh, *session)
	case "token":
		v, err = b.token(*issuer, *clientID, *secret, *redirect, *scope)
	default:
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, v)
	return err
}

func newBrowser(ca, user, password, jar string) (*browser, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	b := &browser{user: user, password: password, jarPath: jar, cookies: map[string]map[string]string{},
		client: &http.Client{Timeout: 30 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if jar != "" {
		if raw, err := os.ReadFile(jar); err == nil {
			_ = json.Unmarshal(raw, &b.cookies)
		}
	}
	return b, nil
}

func (b *browser) save() error {
	if b.jarPath == "" {
		return nil
	}
	raw, _ := json.Marshal(b.cookies)
	return os.WriteFile(b.jarPath, raw, 0o600)
}

// do sends a request with the host's cookies and keeps the ones it sets.
func (b *browser) do(method, raw string, form url.Values) (*http.Response, string, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, raw, body)
	if err != nil {
		return nil, "", err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	var parts []string
	for k, v := range b.cookies[req.URL.Host] {
		parts = append(parts, k+"="+v)
	}
	if len(parts) > 0 {
		req.Header.Set("Cookie", strings.Join(parts, "; "))
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if b.cookies[req.URL.Host] == nil {
		b.cookies[req.URL.Host] = map[string]string{}
	}
	for _, c := range resp.Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies[req.URL.Host], c.Name)
		} else {
			b.cookies[req.URL.Host][c.Name] = c.Value
		}
	}
	return resp, string(data), nil
}

var formAction = regexp.MustCompile(`<form[^>]*method="post"[^>]*action="([^"]+)"`)

// follow walks redirects and the provider's login form until a response
// redirects to stop's host, and returns that redirect.
func (b *browser) follow(start, stopHost string) (*url.URL, error) {
	next := start
	for range 20 {
		resp, body, err := b.do(http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		cur, _ := url.Parse(next)
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			loc, err := cur.Parse(resp.Header.Get("Location"))
			if err != nil {
				return nil, err
			}
			if loc.Host == stopHost {
				return loc, b.save()
			}
			next = loc.String()
		case resp.StatusCode == http.StatusOK:
			m := formAction.FindStringSubmatch(body)
			if m == nil {
				return nil, fmt.Errorf("%s answered a page with no login form", next)
			}
			action, _ := cur.Parse(html.UnescapeString(m[1]))
			resp, body, err = b.do(http.MethodPost, action.String(), url.Values{"login": {b.user}, "password": {b.password}, "remember_me": {"on"}})
			if err != nil {
				return nil, err
			}
			if resp.StatusCode < 300 || resp.StatusCode >= 400 {
				return nil, fmt.Errorf("logging in as %s failed: %s %s", b.user, resp.Status, strings.TrimSpace(body))
			}
			loc, _ := action.Parse(resp.Header.Get("Location"))
			if loc.Host == stopHost {
				return loc, b.save()
			}
			next = loc.String()
		default:
			return nil, fmt.Errorf("%s answered %s: %s", next, resp.Status, strings.TrimSpace(body))
		}
	}
	return nil, errors.New("too many redirects")
}

// dashboard signs in to the dashboard and returns the session cookie.
func (b *browser) dashboard(base string, fresh bool, session string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if session != "" {
		b.cookies[u.Host] = map[string]string{sessionCookie: session}
	}
	q := url.Values{"return": {"/"}}
	if fresh {
		q.Set("fresh", "1")
	}
	resp, body, err := b.do(http.MethodGet, base+"/auth/login?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("the dashboard did not start a login: %s %s", resp.Status, body)
	}
	callback, err := b.follow(resp.Header.Get("Location"), u.Host)
	if err != nil {
		return "", err
	}
	resp, body, err = b.do(http.MethodGet, callback.String(), nil)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusFound || b.cookies[u.Host][sessionCookie] == "" {
		return "", fmt.Errorf("the dashboard refused the sign-in: %s %s", resp.Status, strings.TrimSpace(body))
	}
	return b.cookies[u.Host][sessionCookie], nil
}

// token runs the authorization code flow as a confidential client and
// returns the ID token, without anything listening on the redirect URL.
func (b *browser) token(issuer, clientID, secret, redirect, scope string) (string, error) {
	var meta struct {
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
	}
	_, body, err := b.do(http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal([]byte(body), &meta); err != nil {
		return "", fmt.Errorf("reading %s metadata: %w", issuer, err)
	}
	ru, err := url.Parse(redirect)
	if err != nil {
		return "", err
	}
	q := url.Values{"client_id": {clientID}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {scope},
		"state": {"e2e"}, "nonce": {"e2e"}}
	callback, err := b.follow(meta.Authorization+"?"+q.Encode(), ru.Host)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {callback.Query().Get("code")}, "redirect_uri": {redirect}}
	req, _ := http.NewRequest(http.MethodPost, meta.Token, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, secret)
	resp, err := b.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	if tok.IDToken == "" {
		return "", fmt.Errorf("the token endpoint gave no ID token: %s %s", resp.Status, tok.Error)
	}
	return tok.IDToken, nil
}
