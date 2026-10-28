package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/logging"
)

// startServer runs srv on an ephemeral port and returns its base URL. The server
// is stopped when the test ends.
func startServer(t *testing.T, srv *Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.ServeOnListener(ctx, ln) }()
	time.Sleep(50 * time.Millisecond)
	return "http://" + ln.Addr().String()
}

func testUI() fstest.MapFS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
}

func TestServerHealth(t *testing.T) {
	srv := NewServer(Options{UI: testUI(), Version: "1.2.3"})
	base := startServer(t, srv)

	var body map[string]any
	getJSON(t, base+"/health", http.StatusOK, &body)
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", body["status"])
	}
	if body["version"] != "1.2.3" {
		t.Errorf("expected version '1.2.3', got %v", body["version"])
	}
}

// Every answer, API or UI, carries the policy that backs the Markdown
// sanitiser and refuses framing, with or without sign-in configured.
func TestServerSecurityHeaders(t *testing.T) {
	base := startServer(t, NewServer(Options{UI: testUI()}))
	for _, path := range []string{"/", "/health", "/missing"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		csp := resp.Header.Get("Content-Security-Policy")
		for _, want := range []string{"script-src 'self';", "object-src 'none'", "base-uri 'none'", "form-action 'self'", "frame-ancestors 'none'", "default-src 'self'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", path, csp, want)
			}
		}
		if resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: headers %v", path, resp.Header)
		}
	}
}

func TestServerMetrics(t *testing.T) {
	q := demoFleetQuery(t)
	srv := NewServer(Options{UI: testUI(), Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }})
	base := startServer(t, srv)

	var body map[string]any
	getJSON(t, base+"/metrics", http.StatusOK, &body)
	if body["sourceCount"] != float64(1) {
		t.Errorf("expected sourceCount=1, got %v", body["sourceCount"])
	}
	if body["serviceCount"] != float64(1) {
		t.Errorf("expected serviceCount=1, got %v", body["serviceCount"])
	}
}

// A host with no published snapshot still answers /metrics, with zeroes rather
// than a 500 — the nav polls it before the first snapshot exists.
func TestServerMetrics_NoSnapshot(t *testing.T) {
	srv := NewServer(Options{UI: testUI(), Fleet: func(context.Context) (*fleet.Query, error) { return nil, fleet.ErrNoSnapshot }})
	base := startServer(t, srv)

	var body map[string]any
	getJSON(t, base+"/metrics", http.StatusOK, &body)
	if body["serviceCount"] != float64(0) || body["sourceCount"] != float64(0) {
		t.Errorf("expected zeroed metrics, got %v", body)
	}
}

// /api/sources is derived from the published snapshot's own source states, so the
// nav pills describe where the data on screen came from.
func TestServerGetSources(t *testing.T) {
	q := demoFleetQuery(t)
	srv := NewServer(Options{UI: testUI(), Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }})
	base := startServer(t, srv)

	var body struct {
		Sources     []SourceInfo `json:"sources"`
		Discovering bool         `json:"discovering"`
	}
	getJSON(t, base+"/api/sources", http.StatusOK, &body)
	if len(body.Sources) != 1 || body.Sources[0].Type != "local" {
		t.Fatalf("expected one local source pill, got %+v", body.Sources)
	}
	if !body.Sources[0].Enabled || body.Sources[0].Reason == "" {
		t.Errorf("pill must state why it is in that state, got %+v", body.Sources[0])
	}
	if body.Discovering {
		t.Error("expected discovering=false once a snapshot is published")
	}
}

func TestServerGetSources_Discovering(t *testing.T) {
	// A wired provider with nothing published yet is the honest "discovering"
	// state; a host with no provider at all is simply sourceless.
	cases := map[string]struct {
		provider        fleetProvider
		wantDiscovering bool
	}{
		"no snapshot yet": {func(context.Context) (*fleet.Query, error) { return nil, fleet.ErrNoSnapshot }, true},
		"no provider":     {nil, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := NewServer(Options{UI: testUI(), Fleet: tc.provider})
			out, err := srv.getSources(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if out.Body.Discovering != tc.wantDiscovering {
				t.Errorf("discovering = %v, want %v", out.Body.Discovering, tc.wantDiscovering)
			}
			if out.Body.Sources == nil {
				t.Error("sources must serialize as [] rather than null")
			}
		})
	}
}

func TestServerRefresh_Endpoint(t *testing.T) {
	refreshed := 0
	srv := NewServer(Options{UI: testUI(), Refresh: func(context.Context) error {
		refreshed++
		return nil
	}})
	base := startServer(t, srv)

	var body struct {
		Status string `json:"status"`
	}
	resp, err := http.Post(base+"/api/refresh", "application/json", nil) //nolint:noctx // short-lived in-process test request
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", body.Status)
	}
	if refreshed != 1 {
		t.Errorf("expected the fleet to be rebuilt once, got %d", refreshed)
	}
}

func TestServerRefresh_Handler(t *testing.T) {
	// No refresher (the offline demo, whose snapshot is baked in) is a successful
	// no-op; a failing rebuild is reported rather than silently claimed as "ok".
	srv := NewServer(Options{})
	out, err := srv.refresh(context.Background(), nil)
	if err != nil || out.Body.Status != "ok" {
		t.Fatalf("unset refresher should succeed, got %+v err=%v", out, err)
	}

	srv = NewServer(Options{Refresh: func(context.Context) error { return errors.New("registry down") }})
	if _, err := srv.refresh(context.Background(), nil); err == nil {
		t.Fatal("expected a failed rebuild to surface as an error")
	}
}

func TestCORSMiddleware(t *testing.T) {
	const allowed = "http://app.example"
	run := func(corsOrigin, method, origin string) *http.Response {
		s := NewServer(Options{CORSOrigin: corsOrigin})
		h := s.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		r := httptest.NewRequest(method, "http://dash.local/api/x", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Result()
	}

	tests := []struct {
		name       string
		corsOrigin string
		method     string
		origin     string
		wantStatus int
		wantACAO   string
	}{
		{"default options no origin", "", http.MethodOptions, "", http.StatusNoContent, ""},
		{"default get no acao", "", http.MethodGet, "", http.StatusOK, ""},
		{"default post no origin allowed", "", http.MethodPost, "", http.StatusOK, ""},
		{"default post same-origin allowed", "", http.MethodPost, "http://dash.local", http.StatusOK, ""},
		{"default post cross-origin forbidden", "", http.MethodPost, "http://evil.com", http.StatusForbidden, ""},
		{"default post bad-origin forbidden", "", http.MethodPost, string([]byte{0x7f}), http.StatusForbidden, ""},
		{"allowed origin get echoes acao", allowed, http.MethodGet, allowed, http.StatusOK, allowed},
		{"allowed origin options echoes acao", allowed, http.MethodOptions, allowed, http.StatusNoContent, allowed},
		{"allowed origin post allowed", allowed, http.MethodPost, allowed, http.StatusOK, allowed},
		{"allowed config other origin no acao", allowed, http.MethodGet, "http://other", http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := run(tt.corsOrigin, tt.method, tt.origin)
			defer resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status: got %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Errorf("ACAO: got %q, want %q", got, tt.wantACAO)
			}
		})
	}
}

func TestServerLogger(t *testing.T) {
	// A nil logger falls back to slog.Default() rather than leaving it unset.
	if s := NewServer(Options{}); s.opts.Logger != slog.Default() {
		t.Fatal("a nil Logger should fall back to slog.Default()")
	}

	// The middleware carries the configured logger onto the request context so
	// handlers reach it via logging.LoggerFromContext.
	custom := slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
	s := NewServer(Options{Logger: custom})
	var got *slog.Logger
	h := s.corsMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = logging.LoggerFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://dash.local/api/x", nil))
	if got != custom {
		t.Fatal("middleware did not inject the server logger into the request context")
	}
}

func TestServe_CancelledContext(t *testing.T) {
	srv := NewServer(Options{UI: testUI()})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Port 0 lets the OS pick a free port, so Listen succeeds and the cancelled
	// context makes ServeOnListener return at once. Either way Serve must not hang.
	if err := srv.Serve(ctx); err != nil {
		t.Fatalf("cancelled Serve should return cleanly, got %v", err)
	}
}

func TestServe_HostVariants(t *testing.T) {
	for _, host := range []string{"0.0.0.0", ""} {
		srv := NewServer(Options{UI: testUI(), Host: host})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := srv.Serve(ctx); err != nil {
			t.Errorf("Serve(host=%q) = %v", host, err)
		}
	}
}

func TestServe_ListenError(t *testing.T) {
	// Bind a port first, then Serve on the same port to trigger a listen error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck

	srv := NewServer(Options{UI: testUI(), Port: ln.Addr().(*net.TCPAddr).Port})
	if err := srv.Serve(context.Background()); err == nil {
		t.Error("expected listen error for already-bound port")
	}
}

func TestServeOnListener_ServerError(t *testing.T) {
	srv := NewServer(Options{UI: testUI()})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Close the listener so Serve fails immediately through the errCh path.
	_ = ln.Close()

	if err := srv.ServeOnListener(context.Background(), ln); err == nil {
		t.Error("expected error from closed listener")
	}
}

func TestServerListenAddr(t *testing.T) {
	cases := map[string]string{
		"192.168.1.1": "http://192.168.1.1:8080",
		"0.0.0.0":     "http://localhost:8080",
		"":            "http://localhost:8080",
	}
	for host, want := range cases {
		if got := NewServer(Options{Host: host, Port: 8080}).listenAddr; got != want {
			t.Errorf("host %q: listenAddr = %q, want %q", host, got, want)
		}
	}
	if got := NewServer(Options{}).listenAddr; got != "" {
		t.Errorf("no port: listenAddr = %q, want none", got)
	}
}

func TestServerListenAddr_OpenAPIServer(t *testing.T) {
	srv := NewServer(Options{UI: testUI(), Host: "10.0.0.1", Port: 9090})
	base := startServer(t, srv)

	var spec map[string]any
	getJSON(t, base+"/openapi.json", http.StatusOK, &spec)
	servers, ok := spec["servers"].([]any)
	if !ok || len(servers) == 0 {
		t.Fatal("expected servers in OpenAPI spec")
	}
	serverObj := servers[0].(map[string]any)
	if serverObj["url"] != "http://10.0.0.1:9090" {
		t.Errorf("expected server URL http://10.0.0.1:9090, got %v", serverObj["url"])
	}
}

func TestEmbeddedUI(t *testing.T) {
	fsys := EmbeddedUI()
	if fsys == nil {
		t.Fatal("expected non-nil embedded FS")
	}
	// EmbeddedUI returns the ui/ subdir, so index.html is at root.
	f, err := fsys.Open("index.html")
	if err != nil {
		t.Fatalf("expected ui/index.html to exist: %v", err)
	}
	_ = f.Close()
}

// firstLoadBudget is how many bytes of script the page may download before it
// shows anything: the entry chunk and what it preloads. The graph, the charts
// and the diagram renderers load with the views that need them.
const firstLoadBudget = 350_000

// TestFirstLoadBudget keeps cytoscape, d3 and mermaid out of the first download.
func TestFirstLoadBudget(t *testing.T) {
	fsys := EmbeddedUI()
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:src|href)="/(assets/[^"]+\.js)"`).FindAllStringSubmatch(string(index), -1)
	if len(refs) == 0 {
		t.Fatal("index.html loads no script chunk")
	}
	total := 0
	for _, ref := range refs {
		b, err := fs.ReadFile(fsys, ref[1])
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
	}
	if total > firstLoadBudget {
		t.Fatalf("the first load is %d bytes of script, over the %d budget: load the heavy view lazily", total, firstLoadBudget)
	}
}

func TestExportOpenAPI(t *testing.T) {
	data, err := ExportOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("expected valid JSON")
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Errorf("expected OpenAPI 3.1.0, got %v", spec["openapi"])
	}
	info, _ := spec["info"].(map[string]any)
	if info["title"] != "Pacto Dashboard API" {
		t.Errorf("expected title 'Pacto Dashboard API', got %v", info["title"])
	}
	// The document covers BOTH hosts: the live fleet API and the offline export's
	// contract-view endpoints. The generated SDK is built from it, so an operation
	// missing here is a compile error in the frontend.
	paths, _ := spec["paths"].(map[string]any)
	for _, p := range []string{
		"/api/capabilities", "/api/sources", "/api/fleet/overview",
		"/api/services", "/api/services/{name}", "/api/services/{name}/versions",
		"/api/services/{name}/versions/{version}", "/api/services/{name}/sources",
		"/api/services/{name}/dependents", "/api/services/{name}/refs",
		"/api/graph", "/api/diff",
	} {
		if paths[p] == nil {
			t.Errorf("expected %s in the API document", p)
		}
	}
}

// The contract-view operations are answered by the offline static export, which
// has no server. A live host must not pretend to serve them.
func TestOfflineOperationsAreNotServedLive(t *testing.T) {
	srv := NewServer(Options{UI: testUI(), Fleet: func(context.Context) (*fleet.Query, error) { return demoFleetQuery(t), nil }})
	base := startServer(t, srv)

	for _, p := range []string{"/api/services", "/api/graph", "/api/diff", "/api/services/x/refs"} {
		expectStatus(t, base+p, http.StatusNotFound)
	}
}

func TestOfflineHandler(t *testing.T) {
	out, err := offline[ServiceNameInput, getServiceOutput](context.Background(), &ServiceNameInput{})
	if out != nil {
		t.Errorf("expected no body, got %+v", out)
	}
	if err == nil || !strings.Contains(err.Error(), "static export") {
		t.Fatalf("expected a not-implemented error naming the static export, got %v", err)
	}
}

func TestExportConfigSchema(t *testing.T) {
	data, err := ExportConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("expected valid JSON")
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["title"] != "Pacto Dashboard Configuration" {
		t.Errorf("title = %v", schema["title"])
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"PACTO_DASHBOARD_HOST", "PACTO_DASHBOARD_PORT", "PACTO_DASHBOARD_NAMESPACE", "PACTO_DASHBOARD_REPO", "PACTO_NO_CACHE", "PACTO_NO_UPDATE_CHECK", "PACTO_REGISTRY_USERNAME", "PACTO_REGISTRY_PASSWORD", "PACTO_REGISTRY_TOKEN", "PACTO_DASHBOARD_DELIVERY_NAMESPACE"} {
		if props[key] == nil {
			t.Errorf("missing property %s", key)
		}
	}
	port, _ := props["PACTO_DASHBOARD_PORT"].(map[string]any)
	if port["default"] != float64(3000) {
		t.Errorf("port default = %v", port["default"])
	}
	if port["description"] != "HTTP server port" {
		t.Errorf("port description = %v", port["description"])
	}
}
