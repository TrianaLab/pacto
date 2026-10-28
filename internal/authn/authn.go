// Package authn is how a person or an agent reaching Pacto over the network is
// identified, for `pacto dashboard` and `pacto mcp`. Every delivery call is
// made with the caller's own token, so the apiserver decides what they may do,
// sees their username in its audit log and derives their login time from the
// identity provider's signed token. There is no impersonation. The one right
// the server uses for a signed-in caller is asking the apiserver what that
// person may read (SubjectAccessReview), so the fleet views show only the
// namespaces Kubernetes RBAC lets them see (see Viewer).
//
// The dashboard signs people in with OpenID Connect (authorization code with
// PKCE), keeps the tokens in a server-side session behind a __Host- cookie and
// builds a per-request client from the session's ID token. The remote MCP
// server verifies a bearer token on every request and exchanges it (RFC 8693)
// for one the apiserver accepts, because the MCP token must never be passed
// through.
package authn

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/pkg/logging"
)

// RESTConfig is the cluster config a caller's request runs with: the
// cluster's address and CA, never the process's own credentials, and the
// caller's token as the bearer.
func RESTConfig(cluster *rest.Config, token string) *rest.Config {
	c := rest.AnonymousClientConfig(cluster)
	c.BearerToken = token
	return c
}

// Refused logs a request authn turned away, so an operator can tell a
// misconfigured issuer or audience from someone guessing. It names where the
// request came from and why, never a token. r may be nil.
func Refused(ctx context.Context, r *http.Request, what string, args ...any) {
	if r != nil {
		args = append(args, "remote", r.RemoteAddr, "method", r.Method, "path", r.URL.Path)
		if id := r.Header.Get("X-Request-Id"); id != "" {
			args = append(args, "request", id)
		}
	}
	logging.LoggerFromContext(ctx).Warn(what, args...)
}

// random returns n random bytes, hex encoded.
func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return hex.EncodeToString(b)
}

// endpoints are the provider metadata go-oidc does not decode itself.
type endpoints struct {
	Revocation string `json:"revocation_endpoint"`
	EndSession string `json:"end_session_endpoint"`
}

// discover reads the issuer's metadata. Key fetches later use the client too,
// on a background context, so a request ending never breaks the key set.
func discover(client *http.Client, issuer string) (*oidc.Provider, endpoints, error) {
	ctx, cancel := context.WithTimeout(oidc.ClientContext(context.Background(), client), 30*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, endpoints{}, fmt.Errorf("reading the identity provider %s: %w", issuer, err)
	}
	var e endpoints
	_ = p.Claims(&e) // the metadata already decoded once
	return p, e, nil
}

// systemRoots is the system's CA pool; a test replaces it.
var systemRoots = x509.SystemCertPool

// IssuerClient is the HTTP client that reaches the identity provider at
// issuer. With caFile set it trusts the PEM certificates in that file on top
// of the system roots, for an issuer whose certificate a corporate or
// self-signed CA signs. With internalURL set, every request for the issuer's
// scheme and host goes to internalURL instead (the issuer's path replaced by
// internalURL's), so a server inside the cluster reads discovery, keys and
// tokens at an internal address while browsers use the public one. go-oidc
// still asks for the public issuer URL, so the discovery document and every ID
// token must name the public issuer.
func IssuerClient(issuer, internalURL, caFile string) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("reading the identity provider's CA bundle: %w", err)
		}
		pool, err := systemRoots()
		if err != nil {
			return nil, fmt.Errorf("reading the system CA roots: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no PEM certificate", caFile)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: tr}
	if internalURL == "" {
		return client, nil
	}
	from, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("the issuer URL: %w", err)
	}
	to, err := url.Parse(internalURL)
	if err != nil {
		return nil, fmt.Errorf("the internal issuer URL: %w", err)
	}
	if (to.Scheme != "https" && to.Scheme != "http") || to.Host == "" || to.RawQuery != "" || to.Fragment != "" {
		return nil, errors.New("the internal issuer URL must be an http or https URL with a host and no query")
	}
	client.Transport = &issuerRewrite{from: from, to: to, next: tr}
	return client, nil
}

// issuerRewrite sends requests for the public issuer to its internal address.
// Requests for any other host pass through unchanged.
type issuerRewrite struct {
	from, to *url.URL
	next     http.RoundTripper
}

func (t *issuerRewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme == t.from.Scheme && r.URL.Host == t.from.Host {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host, r.Host, r.URL.RawPath = t.to.Scheme, t.to.Host, "", ""
		prefix := strings.TrimSuffix(t.from.Path, "/")
		if rest, ok := strings.CutPrefix(r.URL.Path, prefix); ok && (rest == "" || rest[0] == '/') {
			r.URL.Path = strings.TrimSuffix(t.to.Path, "/") + rest
		}
	}
	return t.next.RoundTrip(r)
}

// postForm sends an OAuth form request authenticated as the client and
// decodes a JSON answer into out (nil ignores the body).
func postForm(ctx context.Context, client *http.Client, endpoint, clientID, secret string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("%s answered %s: %s %s", endpoint, resp.Status, e.Error, e.Description)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
