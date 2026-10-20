// Package authn is how a person or an agent reaching Pacto over the network is
// identified, for `pacto dashboard` and `pacto mcp`. It holds no Kubernetes
// rights of its own: every delivery call is made with the caller's own token,
// so the apiserver decides what they may do, sees their username in its audit
// log and derives their login time from the identity provider's signed token.
// There is no impersonation.
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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"k8s.io/client-go/rest"
)

// RESTConfig is the cluster config a caller's request runs with: the
// cluster's address and CA, never the process's own credentials, and the
// caller's token as the bearer.
func RESTConfig(cluster *rest.Config, token string) *rest.Config {
	c := rest.AnonymousClientConfig(cluster)
	c.BearerToken = token
	return c
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
