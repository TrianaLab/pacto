package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/trianalab/pacto/v2/pkg/openapi"
)

// defaultClient is used when the caller does not supply one. It deliberately
// does NOT follow redirects (returning the 3xx to the caller) so that a
// server-issued redirect cannot leak credentials cross-origin or pivot to an
// internal host (SSRF), and it bounds every call with a timeout.
var defaultClient = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Credentials maps a security-scheme name to its credential value.
type Credentials = map[string]string

// Result is the outcome of a live operation invocation.
type Result struct {
	StatusCode int
	Headers    map[string]string
	Body       string
}

// Invoke executes an OpenAPI operation against a live service. Non-2xx responses
// are returned as a normal Result (so the agent sees the status/body); only
// transport-level failures return a Go error.
func Invoke(ctx context.Context, client *http.Client, op openapi.Operation, doc *openapi.Doc, baseURL string, args map[string]any, creds Credentials) (*Result, error) {
	if client == nil {
		client = defaultClient
	}
	target := buildURL(baseURL, op, args)
	body, err := requestBody(op, args)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, op.Method, target, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	applyHeaderParams(req, op, args)
	applyAuth(req, op, doc, creds)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", op.Method, op.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &Result{StatusCode: resp.StatusCode, Headers: flatHeaders(resp.Header), Body: string(data)}, nil
}

// buildURL assembles the request URL. Final URL validity is enforced by
// http.NewRequestWithContext, which parses it.
func buildURL(baseURL string, op openapi.Operation, args map[string]any) string {
	p := op.Path
	q := url.Values{}
	for _, param := range op.Parameters {
		v, ok := args[param.Name]
		if !ok {
			continue
		}
		switch param.In {
		case "path":
			p = strings.ReplaceAll(p, "{"+param.Name+"}", url.PathEscape(toStr(v)))
		case "query":
			q.Set(param.Name, toStr(v))
		}
	}
	full := strings.TrimRight(baseURL, "/") + p
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	return full
}

func requestBody(op openapi.Operation, args map[string]any) (io.Reader, error) {
	if op.RequestBody == nil {
		return nil, nil
	}
	v, ok := args["body"]
	if !ok {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	return bytes.NewReader(data), nil
}

func applyHeaderParams(req *http.Request, op openapi.Operation, args map[string]any) {
	for _, param := range op.Parameters {
		if param.In != "header" {
			continue
		}
		if v, ok := args[param.Name]; ok {
			req.Header.Set(param.Name, toStr(v))
		}
	}
}

func applyAuth(req *http.Request, op openapi.Operation, doc *openapi.Doc, creds Credentials) {
	for _, reqmt := range effectiveSecurity(op, doc) {
		for name := range reqmt {
			cred, ok := creds[name]
			if !ok || cred == "" {
				continue
			}
			applyScheme(req, doc.SecuritySchemes[name], cred)
		}
	}
}

func effectiveSecurity(op openapi.Operation, doc *openapi.Doc) []openapi.SecurityRequirement {
	if op.Security != nil {
		return op.Security
	}
	if doc != nil {
		return doc.Security
	}
	return nil
}

func applyScheme(req *http.Request, scheme openapi.SecurityScheme, cred string) {
	switch scheme.Type {
	case "apiKey":
		if scheme.In == "query" {
			q := req.URL.Query()
			q.Set(scheme.Name, cred)
			req.URL.RawQuery = q.Encode()
		} else {
			req.Header.Set(scheme.Name, cred)
		}
	case "http":
		if strings.EqualFold(scheme.Scheme, "basic") {
			req.Header.Set("Authorization", "Basic "+cred)
		} else {
			req.Header.Set("Authorization", "Bearer "+cred)
		}
	default:
		// oauth2, openIdConnect, or unknown → bearer.
		// ponytail: full OAuth2/OIDC flows are YAGNI; operator supplies a token.
		req.Header.Set("Authorization", "Bearer "+cred)
	}
}

func flatHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// JSON numbers decode to float64; format without exponent notation so
		// large ints/timestamps become "1700000000", not "1.7e+09".
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", t)
	}
}
