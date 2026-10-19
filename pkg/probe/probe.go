/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package probe checks a workload's HTTP health and metrics endpoints and the wire protocol of a port. It
// is platform neutral: every function takes a host:port the caller resolved from a trusted binding (a
// Kubernetes Service, a container's address, a bindings file), never from contract text, and a contract
// path only ever lands in the URL's path component (INV-6, SSRF-safe).
package probe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
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
	client  *http.Client
	timeout time.Duration
}

// New creates a Prober with the given timeout.
func New(timeout time.Duration) *Prober {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Prober{
		timeout: timeout,
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

// URL builds the http URL of path on addr (host:port from a trusted binding). A path without its leading
// "/" gets one; a path the capability rules reject (contract.ValidCapabilityPath: a scheme, an authority, a
// fragment) is an error, and even an accepted path only ever fills the URL's path component, so it can never
// move the request to another host.
func URL(addr, path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !contract.ValidCapabilityPath(path) {
		return "", fmt.Errorf("probe path %q is not a plain absolute path", path)
	}
	return (&url.URL{Scheme: "http", Host: addr, Path: path}).String(), nil
}

// h2cHello is the HTTP/2 client connection preface followed by an empty SETTINGS frame (length 0, type 0x4,
// no flags, stream 0). An HTTP/2 server answers with its own SETTINGS frame; an HTTP/1 server rejects the
// "PRI" request line with an HTTP/1.x status line; a TLS server answers the garbage with an alert record.
var h2cHello = append([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"), 0, 0, 0, 0x4, 0, 0, 0, 0, 0)

// ProbeProtocol classifies the wire protocol the port at addr (host:port from a trusted binding) speaks: it
// sends h2cHello and reads the first bytes of the answer. Empty means no answer.
func (p *Prober) ProbeProtocol(ctx context.Context, addr string) evidence.Protocol {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	_, _ = conn.Write(h2cHello) // a failed write leaves nothing to read, which classifies as no answer
	buf := make([]byte, 9)      // one frame header
	n, _ := io.ReadFull(conn, buf)
	return classifyProtocol(buf[:n])
}

// classifyProtocol maps the first bytes a server sent to a protocol family.
func classifyProtocol(b []byte) evidence.Protocol {
	switch {
	case len(b) == 0:
		return ""
	case len(b) == 9 && b[3] == 0x4 && b[5]&0x7f == 0 && b[6] == 0 && b[7] == 0 && b[8] == 0:
		return evidence.ProtocolHTTP2 // a SETTINGS frame on stream 0
	case bytes.HasPrefix(b, []byte("HTTP/1.")):
		return evidence.ProtocolHTTP1
	case b[0] == 0x15 || b[0] == 0x16:
		return evidence.ProtocolTLS // an alert or handshake record
	default:
		return evidence.ProtocolOther
	}
}
