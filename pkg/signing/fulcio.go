package signing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// fulcioConfiguration is the part of Fulcio's GET /api/v2/configuration
// answer FulcioTrusts reads: which OIDC issuers it accepts, each with the
// audience a token must carry.
type fulcioConfiguration struct {
	Issuers []struct {
		IssuerURL         string `json:"issuerUrl"`
		WildcardIssuerURL string `json:"wildcardIssuerUrl"`
		Audience          string `json:"audience"`
	} `json:"issuers"`
}

// fulcioWildcardLabel is what one * of a wildcard issuer URL stands for in
// Fulcio: one or more characters of a single host label or path segment.
const fulcioWildcardLabel = "[-_a-zA-Z0-9]+"

// wildcardMatches reports whether issuer matches a Fulcio wildcard issuer
// URL, where each * stands for one host label part.
func wildcardMatches(pattern, issuer string) bool {
	re := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, fulcioWildcardLabel) + "$"
	ok, err := regexp.MatchString(re, issuer)
	return err == nil && ok
}

// FulcioTrusts asks the Fulcio at fulcioURL whether it issues certificates
// for ID tokens from issuer whose audience is audience. A false answer comes
// with the reason; an unreadable configuration is an error, which the caller
// reads as unknown and never as trusted.
func FulcioTrusts(ctx context.Context, client *http.Client, fulcioURL, issuer, audience string) (bool, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(fulcioURL, "/")+"/api/v2/configuration", nil)
	if err != nil {
		return false, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("reading Fulcio's configuration at %s: %w", fulcioURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("reading Fulcio's configuration at %s: %s", fulcioURL, resp.Status)
	}
	var c fulcioConfiguration
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return false, "", fmt.Errorf("reading Fulcio's configuration at %s: %w", fulcioURL, err)
	}
	known := false
	for _, is := range c.Issuers {
		if is.IssuerURL != issuer && (is.WildcardIssuerURL == "" || !wildcardMatches(is.WildcardIssuerURL, issuer)) {
			continue
		}
		if is.Audience == audience {
			return true, "", nil
		}
		known = true
	}
	if known {
		return false, fmt.Sprintf("Fulcio at %s accepts tokens from %s for another audience than %s", fulcioURL, issuer, audience), nil
	}
	return false, fmt.Sprintf("Fulcio at %s does not accept tokens from %s", fulcioURL, issuer), nil
}
