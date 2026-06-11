package doc

import (
	"io"
	"net/http"
	"net/url"
	"strings"
)

// parsedAllowedTarget is a pre-parsed entry from the proxy allowlist.
type parsedAllowedTarget struct {
	scheme string
	host   string
	path   string
}

// newProxyHandler returns an HTTP handler that forwards requests to the
// target. Scalar sends requests to the proxy with the full upstream URL
// in the scalar_url query parameter. This avoids CORS by keeping browser
// traffic same-origin while showing the real URL in the UI.
//
// The allowed slice lists URL prefixes that the proxy is permitted to
// forward to, preventing open-proxy abuse. Targets are pre-parsed at
// handler creation time and the outbound URL is reconstructed from
// trusted allowlist components (scheme + host) rather than raw user input.
func newProxyHandler(allowed []string) http.HandlerFunc {
	// Pre-parse allowed targets once at creation time.
	var targets []parsedAllowedTarget
	for _, t := range allowed {
		u, err := url.Parse(t)
		if err != nil || u.Host == "" {
			continue
		}
		targets = append(targets, parsedAllowedTarget{scheme: u.Scheme, host: u.Host, path: u.Path})
	}

	return func(w http.ResponseWriter, r *http.Request) {
		rawURL := r.URL.Query().Get("scalar_url")
		if rawURL == "" {
			http.Error(w, "missing scalar_url parameter", http.StatusBadRequest)
			return
		}

		reqURL, parseErr := url.Parse(rawURL)
		if parseErr != nil || reqURL.Host == "" {
			http.Error(w, "invalid target URL", http.StatusBadRequest)
			return
		}

		// Match against allowlist and reconstruct URL from trusted components.
		// Scheme and host come from the server-controlled allowlist, not user input.
		var safeTarget *url.URL
		for _, t := range targets {
			if reqURL.Scheme == t.scheme && reqURL.Host == t.host && strings.HasPrefix(reqURL.Path, t.path) {
				safeTarget = &url.URL{
					Scheme:   t.scheme,
					Host:     t.host,
					Path:     reqURL.Path,
					RawQuery: reqURL.RawQuery,
				}
				break
			}
		}
		if safeTarget == nil {
			http.Error(w, "target not allowed", http.StatusForbidden)
			return
		}

		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, safeTarget.String(), r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		copyHeaders(proxyReq.Header, r.Header)

		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			http.Error(w, "upstream unreachable", http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		copyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// hopByHop lists headers that must not be forwarded by proxies.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		if hopByHop[k] {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
