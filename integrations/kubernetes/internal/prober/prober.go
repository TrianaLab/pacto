/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package prober performs HTTP endpoint probing for health and metrics validation.
package prober

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const (
	defaultTimeout = 5 * time.Second
	maxBodyRead    = 4096 // only read first 4KB for content checks
)

// Result describes the outcome of probing a single HTTP endpoint.
type Result struct {
	URL        string
	Reachable  bool
	StatusCode int32
	LatencyMs  int64
	Error      string

	// ContentPresent indicates whether the response body was non-empty.
	ContentPresent bool
	// PrometheusParsed is true only when the response has a compatible Content-Type AND the bounded body
	// parses as Prometheus text-exposition with at least one metric family (Refinement D) — NOT a substring
	// heuristic.
	PrometheusParsed bool
}

// Prober performs HTTP endpoint checks.
type Prober struct {
	client *http.Client
}

// New creates a Prober with the given timeout.
func New(timeout time.Duration) *Prober {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Prober{
		client: &http.Client{
			Timeout: timeout,
			// Do not follow redirects — report them as-is
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Probe performs an HTTP GET to the given URL and reports the result.
func (p *Prober) Probe(ctx context.Context, targetURL string) Result {
	result := Result{URL: targetURL}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		result.Error = fmt.Sprintf("invalid URL: %v", err)
		return result
	}

	start := time.Now()
	resp, err := p.client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		result.Error = sanitizeError(err)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	result.Reachable = true
	result.StatusCode = int32(resp.StatusCode)

	// Read a small prefix of the body for content checks
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
	result.ContentPresent = len(body) > 0

	if result.ContentPresent && metricsContentType(resp.Header.Get("Content-Type")) {
		result.PrometheusParsed = parsePrometheus(body)
	}

	return result
}

// metricsContentType reports whether a Content-Type is Prometheus text-exposition or OpenMetrics.
func metricsContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "text/plain") || strings.Contains(ct, "application/openmetrics-text")
}

// parsePrometheus reports whether the bounded body parses as Prometheus text-exposition with at least one
// metric family. A trailing partial line (from the 4KB cap) is dropped so a complete leading metric still
// parses. The parse error is intentionally not surfaced: a malformed remote body is untrusted input, not a
// programmer invariant — an unparseable body simply reads as "not parsed" (EVIDENCE_INSUFFICIENT upstream).
func parsePrometheus(body []byte) bool {
	if i := bytes.LastIndexByte(body, '\n'); i >= 0 {
		body = body[:i+1]
	}
	// NewTextParser (not a zero-value TextParser, which panics) with permissive UTF-8 name validation.
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, _ := parser.TextToMetricFamilies(bytes.NewReader(body))
	return len(families) > 0
}

// sanitizeError extracts a clean error message, stripping URL repetition from net/http errors.
func sanitizeError(err error) string {
	msg := err.Error()
	// net/http errors often include the full URL which is redundant since we store it separately
	if idx := strings.LastIndex(msg, ": "); idx > 0 {
		suffix := msg[idx+2:]
		if len(suffix) > 0 {
			return suffix
		}
	}
	return msg
}

// BuildURL constructs an in-cluster HTTP URL for a Kubernetes Service endpoint (INV-6, SSRF-safe). The
// authority is built ONLY from the trusted service/namespace/port; the (contract-derived) path goes in the
// url.URL.Path field, so a path such as "//evil.example" or one containing a scheme/userinfo can never
// hijack the host — it stays in the path component.
func BuildURL(serviceName, namespace string, port int32, path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc:%d", serviceName, namespace, port),
		Path:   path,
	}
	return u.String()
}
