package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/pkg/contract"
)

func TestCollectSwaggerSpecs(t *testing.T) {
	port := 8080
	specs := CollectSwaggerSpecs([]contract.Interface{
		{Name: "api", Type: "http", Port: &port, Contract: "openapi.yaml"},
		{Name: "grpc", Type: "grpc", Contract: "service.proto"},
		{Name: "web", Type: "http", Port: &port, Contract: "web.yaml"},
		{Name: "bare", Type: "http", Port: &port},
	})

	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	if specs[0].InterfaceName != "api" {
		t.Errorf("expected first spec to be 'api', got %q", specs[0].InterfaceName)
	}
	if specs[1].InterfaceName != "web" {
		t.Errorf("expected second spec to be 'web', got %q", specs[1].InterfaceName)
	}
}

func TestCollectSwaggerSpecs_Empty(t *testing.T) {
	specs := CollectSwaggerSpecs(nil)
	if len(specs) != 0 {
		t.Errorf("expected 0 specs, got %d", len(specs))
	}
}

func TestFilterSpecs(t *testing.T) {
	specs := []SwaggerSpec{
		{InterfaceName: "api", SpecPath: "api.yaml"},
		{InterfaceName: "admin", SpecPath: "admin.yaml"},
	}

	filtered := FilterSpecs(specs, "admin")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(filtered))
	}
	if filtered[0].InterfaceName != "admin" {
		t.Errorf("expected 'admin', got %q", filtered[0].InterfaceName)
	}
}

func TestFilterSpecs_NotFound(t *testing.T) {
	specs := []SwaggerSpec{
		{InterfaceName: "api", SpecPath: "api.yaml"},
	}
	if filtered := FilterSpecs(specs, "missing"); filtered != nil {
		t.Errorf("expected nil for missing interface, got %v", filtered)
	}
}

func TestFilterSpecs_Empty(t *testing.T) {
	if filtered := FilterSpecs(nil, "api"); filtered != nil {
		t.Errorf("expected nil for empty specs, got %v", filtered)
	}
}

func TestTargetFor(t *testing.T) {
	opts := SwaggerOptions{
		Target:  "http://global:3000",
		Targets: map[string]string{"admin": "http://admin:4000"},
	}

	if got := opts.targetFor("admin"); got != "http://admin:4000" {
		t.Errorf("expected per-interface target, got %q", got)
	}
	if got := opts.targetFor("api"); got != "http://global:3000" {
		t.Errorf("expected global target fallback, got %q", got)
	}
}

func TestTargetFor_NoTargets(t *testing.T) {
	opts := SwaggerOptions{Target: "http://global:3000"}
	if got := opts.targetFor("api"); got != "http://global:3000" {
		t.Errorf("expected global target, got %q", got)
	}
}

func TestTargetFor_NoGlobal(t *testing.T) {
	opts := SwaggerOptions{Targets: map[string]string{"admin": "http://admin:4000"}}
	if got := opts.targetFor("api"); got != "" {
		t.Errorf("expected empty for unmatched interface, got %q", got)
	}
}

func TestAllowedTargets(t *testing.T) {
	opts := SwaggerOptions{
		Target:  "http://global:3000",
		Targets: map[string]string{"admin": "http://admin:4000"},
	}
	targets := allowedTargets(opts)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
}

func TestAllowedTargets_Deduplicate(t *testing.T) {
	opts := SwaggerOptions{
		Target:  "http://same:3000",
		Targets: map[string]string{"admin": "http://same:3000"},
	}
	targets := allowedTargets(opts)
	if len(targets) != 1 {
		t.Fatalf("expected 1 deduplicated target, got %d", len(targets))
	}
}

func TestAllowedTargets_Empty(t *testing.T) {
	targets := allowedTargets(SwaggerOptions{})
	if len(targets) != 0 {
		t.Errorf("expected 0 targets, got %d", len(targets))
	}
}

func TestAllowedTargets_OnlyPerInterface(t *testing.T) {
	opts := SwaggerOptions{
		Targets: map[string]string{"admin": "http://admin:4000"},
	}
	targets := allowedTargets(opts)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0] != "http://admin:4000" {
		t.Errorf("expected admin target, got %q", targets[0])
	}
}

func TestServeSwagger(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwagger(ctx, SwaggerOptions{Title: "test"})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("ServeSwagger returned error: %v", err)
	}
}

func TestServeSwagger_ListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	port := ln.Addr().(*net.TCPAddr).Port
	err = ServeSwagger(context.Background(), SwaggerOptions{Title: "test", Port: port})
	if err == nil {
		t.Error("expected error when port is already in use")
	}
}

func TestServeSwaggerOnListener_ClosedListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close()

	err = ServeSwaggerOnListener(context.Background(), SwaggerOptions{Title: "test"}, ln)
	if err == nil {
		t.Error("expected error for closed listener")
	}
}

func TestServeSwaggerOnListener_SingleSpec(t *testing.T) {
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: Test API
  version: "1.0.0"
paths:
  /health:
    get:
      summary: Health check
`)},
	}
	specs := []SwaggerSpec{{InterfaceName: "api", SpecPath: "openapi.yaml"}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{Specs: specs, FS: fsys, Title: "test-svc"}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	// Test the landing page.
	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	html := string(body)
	if !strings.Contains(html, "test-svc - API Explorer") {
		t.Error("expected title in HTML")
	}
	if !strings.Contains(html, "@scalar/api-reference") {
		t.Error("expected Scalar script in HTML")
	}
	if !strings.Contains(html, "/spec/api") {
		t.Error("expected spec URL in HTML")
	}
	// No proxy attr when target is not set.
	if strings.Contains(html, "proxy") {
		t.Error("expected no proxy attribute without target")
	}

	// Test the spec endpoint.
	resp2, err := http.Get(fmt.Sprintf("http://%s/spec/api", addr))
	if err != nil {
		t.Fatalf("GET /spec/api failed: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for spec, got %d", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
	if cors := resp2.Header.Get("Access-Control-Allow-Origin"); cors != "*" {
		t.Errorf("expected CORS header *, got %q", cors)
	}

	specBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("read spec body: %v", err)
	}
	if !strings.Contains(string(specBody), "Test API") {
		t.Error("expected spec content in response")
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Errorf("serve returned error: %v", err)
	}
}

func TestServeSwaggerOnListener_JSONSpec(t *testing.T) {
	fsys := fstest.MapFS{
		"api.json": &fstest.MapFile{Data: []byte(`{"openapi":"3.0.0","info":{"title":"JSON API","version":"1.0.0"},"paths":{}}`)},
	}
	specs := []SwaggerSpec{{InterfaceName: "api", SpecPath: "api.json"}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{Specs: specs, FS: fsys, Title: "test"}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/spec/api", addr))
	if err != nil {
		t.Fatalf("GET /spec/api failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "JSON API") {
		t.Error("expected JSON spec content")
	}

	cancel()
	<-errCh
}

func TestServeSwaggerOnListener_SpecNotFound(t *testing.T) {
	fsys := fstest.MapFS{}
	specs := []SwaggerSpec{{InterfaceName: "api", SpecPath: "missing.yaml"}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{Specs: specs, FS: fsys, Title: "test"}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/spec/api", addr))
	if err != nil {
		t.Fatalf("GET /spec/api failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for missing spec, got %d", resp.StatusCode)
	}

	cancel()
	<-errCh
}

func TestServeSwaggerOnListener_MultiSpec(t *testing.T) {
	fsys := fstest.MapFS{
		"api.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: API
  version: "1.0.0"
paths: {}
`)},
		"admin.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: Admin API
  version: "1.0.0"
paths: {}
`)},
	}
	specs := []SwaggerSpec{
		{InterfaceName: "api", SpecPath: "api.yaml"},
		{InterfaceName: "admin", SpecPath: "admin.yaml"},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{Specs: specs, FS: fsys, Title: "multi-svc"}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	html := string(body)
	if !strings.Contains(html, "multi-svc") {
		t.Error("expected title in multi-spec page")
	}
	if !strings.Contains(html, "/spec/api") {
		t.Error("expected api spec link")
	}
	if !strings.Contains(html, "/spec/admin") {
		t.Error("expected admin spec link")
	}

	cancel()
	<-errCh
}

func TestServeSwaggerOnListener_404ForUnknownPaths(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{Title: "test"}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/unknown", addr))
	if err != nil {
		t.Fatalf("GET /unknown failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}

	cancel()
	<-errCh
}

func TestServeSwaggerOnListener_InvalidSpec(t *testing.T) {
	fsys := fstest.MapFS{
		"bad.json": &fstest.MapFile{Data: []byte(`{not valid json`)},
	}
	specs := []SwaggerSpec{{InterfaceName: "api", SpecPath: "bad.json"}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{Specs: specs, FS: fsys, Title: "test"}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/spec/api", addr))
	if err != nil {
		t.Fatalf("GET /spec/api failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for invalid spec, got %d", resp.StatusCode)
	}

	cancel()
	<-errCh
}

func TestServeSwaggerOnListener_TargetProxy(t *testing.T) {
	fsys := fstest.MapFS{
		"openapi.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: Test API
  version: "1.0.0"
servers:
  - url: https://production.example.com
paths: {}
`)},
	}
	specs := []SwaggerSpec{{InterfaceName: "api", SpecPath: "openapi.yaml"}}

	// Start a fake upstream that the proxy will forward to.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "upstream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "path=%s query=%s method=%s", r.URL.Path, r.URL.RawQuery, r.Method)
	}))
	defer upstream.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{
			Specs:  specs,
			FS:     fsys,
			Title:  "test",
			Target: upstream.URL + "/api",
		}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	// The spec should have servers pointing to the real target URL.
	resp, err := http.Get(fmt.Sprintf("http://%s/spec/api", addr))
	if err != nil {
		t.Fatalf("GET /spec/api failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(body, &spec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	servers := spec["servers"].([]any)
	server := servers[0].(map[string]any)
	if got := server["url"].(string); !strings.Contains(got, "/api") {
		t.Errorf("expected servers to contain target URL with /api, got %q", got)
	}
	if strings.Contains(string(body), "production.example.com") {
		t.Error("expected production URL to be replaced")
	}

	// The HTML should include data-proxy-url.
	pageResp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer func() { _ = pageResp.Body.Close() }()
	pageBody, _ := io.ReadAll(pageResp.Body)
	if !strings.Contains(string(pageBody), `data-proxy-url="/proxy"`) {
		t.Error("expected data-proxy-url attribute in HTML")
	}

	// Requests via /proxy?scalar_url=... should be forwarded to the upstream.
	targetURL := upstream.URL + "/api/governance/status?foo=bar"
	proxyURL := fmt.Sprintf("http://%s/proxy?scalar_url=%s", addr, targetURL)
	resp2, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("GET /proxy failed: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from proxy, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("X-Custom") != "upstream" {
		t.Error("expected upstream response header to be proxied back")
	}

	proxyBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("read proxy body: %v", err)
	}
	bodyStr := string(proxyBody)
	if !strings.Contains(bodyStr, "path=/api/governance/status") {
		t.Errorf("expected target path preserved, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "query=foo=bar") {
		t.Errorf("expected proxied query, got %q", bodyStr)
	}

	cancel()
	<-errCh
}

func TestServeSwaggerOnListener_PerInterfaceTargets(t *testing.T) {
	fsys := fstest.MapFS{
		"api.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: API
  version: "1.0.0"
paths: {}
`)},
		"admin.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: Admin API
  version: "1.0.0"
paths: {}
`)},
	}
	specs := []SwaggerSpec{
		{InterfaceName: "api", SpecPath: "api.yaml"},
		{InterfaceName: "admin", SpecPath: "admin.yaml"},
	}

	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "upstream1")
	}))
	defer upstream1.Close()

	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "upstream2")
	}))
	defer upstream2.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{
			Specs: specs,
			FS:    fsys,
			Title: "multi-target",
			Targets: map[string]string{
				"api":   upstream1.URL,
				"admin": upstream2.URL,
			},
		}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	// Verify api spec has upstream1's URL in servers.
	resp, err := http.Get(fmt.Sprintf("http://%s/spec/api", addr))
	if err != nil {
		t.Fatalf("GET /spec/api failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var specJSON map[string]any
	if err := json.Unmarshal(body, &specJSON); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	servers := specJSON["servers"].([]any)
	if got := servers[0].(map[string]any)["url"].(string); got != upstream1.URL {
		t.Errorf("expected api spec server to be upstream1, got %q", got)
	}

	// Verify admin spec has upstream2's URL in servers.
	resp2, err := http.Get(fmt.Sprintf("http://%s/spec/admin", addr))
	if err != nil {
		t.Fatalf("GET /spec/admin failed: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	body2, _ := io.ReadAll(resp2.Body)
	var adminJSON map[string]any
	if err := json.Unmarshal(body2, &adminJSON); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	adminServers := adminJSON["servers"].([]any)
	if got := adminServers[0].(map[string]any)["url"].(string); got != upstream2.URL {
		t.Errorf("expected admin spec server to be upstream2, got %q", got)
	}

	// Proxy should allow both upstreams.
	proxyURL1 := fmt.Sprintf("http://%s/proxy?scalar_url=%s/test", addr, upstream1.URL)
	pr1, err := http.Get(proxyURL1)
	if err != nil {
		t.Fatalf("proxy to upstream1 failed: %v", err)
	}
	defer func() { _ = pr1.Body.Close() }()
	if pr1.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from proxy to upstream1, got %d", pr1.StatusCode)
	}

	proxyURL2 := fmt.Sprintf("http://%s/proxy?scalar_url=%s/test", addr, upstream2.URL)
	pr2, err := http.Get(proxyURL2)
	if err != nil {
		t.Fatalf("proxy to upstream2 failed: %v", err)
	}
	defer func() { _ = pr2.Body.Close() }()
	if pr2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from proxy to upstream2, got %d", pr2.StatusCode)
	}

	// Page should have proxy attribute.
	pageResp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer func() { _ = pageResp.Body.Close() }()
	pageBody, _ := io.ReadAll(pageResp.Body)
	if !strings.Contains(string(pageBody), "proxyUrl") {
		t.Error("expected proxy attribute in multi-spec page with targets")
	}

	cancel()
	<-errCh
}

func TestProxyHandler_MissingScalarURL(t *testing.T) {
	handler := newProxyHandler([]string{"http://example.com"})
	req := httptest.NewRequest(http.MethodGet, "/proxy", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestProxyHandler_ForbiddenTarget(t *testing.T) {
	handler := newProxyHandler([]string{"http://allowed.example.com"})
	req := httptest.NewRequest(http.MethodGet, "/proxy?scalar_url=http://evil.example.com/path", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestProxyHandler_UpstreamUnreachable(t *testing.T) {
	handler := newProxyHandler([]string{"http://127.0.0.1:1"})
	req := httptest.NewRequest(http.MethodGet, "/proxy?scalar_url=http://127.0.0.1:1/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rr.Code)
	}
}

func TestProxyHandler_MultipleAllowedTargets(t *testing.T) {
	handler := newProxyHandler([]string{"http://a.example.com", "http://b.example.com"})

	// First allowed target.
	req1 := httptest.NewRequest(http.MethodGet, "/proxy?scalar_url=http://a.example.com/path", nil)
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	// Will be 502 (unreachable) not 403 (forbidden) — proving it's allowed.
	if rr1.Code == http.StatusForbidden {
		t.Error("expected target a to be allowed")
	}

	// Second allowed target.
	req2 := httptest.NewRequest(http.MethodGet, "/proxy?scalar_url=http://b.example.com/path", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code == http.StatusForbidden {
		t.Error("expected target b to be allowed")
	}

	// Disallowed target.
	req3 := httptest.NewRequest(http.MethodGet, "/proxy?scalar_url=http://c.example.com/path", nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusForbidden {
		t.Errorf("expected 403 for disallowed target, got %d", rr3.Code)
	}
}

func TestCopyHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("X-Custom", "value")
	src.Set("Connection", "keep-alive") // hop-by-hop, should be skipped

	dst := http.Header{}
	copyHeaders(dst, src)

	if dst.Get("Content-Type") != "application/json" {
		t.Error("expected Content-Type to be copied")
	}
	if dst.Get("X-Custom") != "value" {
		t.Error("expected X-Custom to be copied")
	}
	if dst.Get("Connection") != "" {
		t.Error("expected Connection header to be skipped")
	}
}

func TestEnsureJSON_ValidJSON(t *testing.T) {
	input := []byte(`{"openapi":"3.0.0"}`)
	out, err := ensureJSON(input, "spec.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(input) {
		t.Errorf("expected passthrough, got %q", string(out))
	}
}

func TestEnsureJSON_InvalidJSON(t *testing.T) {
	_, err := ensureJSON([]byte(`{invalid`), "spec.json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestEnsureJSON_YAMLToJSON(t *testing.T) {
	input := []byte("openapi: '3.0.0'\ninfo:\n  title: Test\n")
	out, err := ensureJSON(input, "spec.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), `"openapi"`) {
		t.Error("expected JSON output with openapi key")
	}
	if !strings.Contains(string(out), `"Test"`) {
		t.Error("expected JSON output with title")
	}
}

func TestEnsureJSON_InvalidYAML(t *testing.T) {
	_, err := ensureJSON([]byte(":\n  :\n    - [invalid"), "spec.yaml")
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestOverrideServers(t *testing.T) {
	input := []byte(`{"openapi":"3.0.0","servers":[{"url":"https://old.example.com"}]}`)
	out, err := overrideServers(input, "http://localhost:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var spec map[string]any
	if err := json.Unmarshal(out, &spec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	servers := spec["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].(map[string]any)["url"] != "http://localhost:8080" {
		t.Errorf("expected overridden URL, got %v", servers[0])
	}
}

func TestServeSwaggerOnListener_MultiSpecWithTarget(t *testing.T) {
	fsys := fstest.MapFS{
		"api.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: API
  version: "1.0.0"
paths: {}
`)},
		"admin.yaml": &fstest.MapFile{Data: []byte(`openapi: "3.0.0"
info:
  title: Admin API
  version: "1.0.0"
paths: {}
`)},
	}
	specs := []SwaggerSpec{
		{InterfaceName: "api", SpecPath: "api.yaml"},
		{InterfaceName: "admin", SpecPath: "admin.yaml"},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeSwaggerOnListener(ctx, SwaggerOptions{
			Specs:  specs,
			FS:     fsys,
			Title:  "multi-target",
			Target: "http://localhost:3000",
		}, ln)
	}()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if !strings.Contains(html, `el.dataset.proxyUrl = '/proxy'`) {
		t.Error("expected proxyUrl script line in multi-spec page with target")
	}
	if !strings.Contains(html, "multi-target") {
		t.Error("expected title in multi-spec page")
	}

	cancel()
	<-errCh
}

func TestOverrideServers_InvalidJSON(t *testing.T) {
	_, err := overrideServers([]byte(`{invalid`), "http://localhost:8080")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
