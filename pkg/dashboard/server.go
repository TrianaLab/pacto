// Package dashboard serves the Pacto dashboard: a REST API and web UI over the
// operational graph (pkg/fleet), plus the contract-view model that renders a
// single bundle as service details, a dependency graph and a compliance verdict.
//
// Ingestion is NOT this package's job. Every live host builds its fleet snapshot
// with pkg/fleet and hands it over via [Server.SetFleetProvider]; the dashboard
// only reads what it is given. The contract-view half is also consumed offline by
// pkg/doc, which renders a static export with no server at all.
package dashboard

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/logging"
)

// Server serves the dashboard web UI and REST API.
type Server struct {
	fleetQuery     fleetProvider               // optional: enables the read-only operational-graph (fleet) endpoints
	fleetRefresh   func(context.Context) error // optional: rebuilds the fleet snapshot for /api/refresh
	impactProvider impactProviderFunc          // optional: enables the read-only /api/fleet/impact endpoint
	ui             fs.FS
	listenAddr     string // optional: server URL for OpenAPI spec
	version        string // optional: Pacto version to expose via /health
	corsOrigin     string // optional: explicit cross-origin allowed to call the API (startup-only)

	// logger is injected into every request context (see corsMiddleware), so the
	// handler code that logs via logging.LoggerFromContext reaches the
	// command-configured logger rather than the process default. Set from the
	// dashboard command via SetLogger; defaults to slog.Default().
	logger *slog.Logger
}

// APIConfig returns the Huma configuration for the dashboard API.
func APIConfig() huma.Config {
	return huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:   "Pacto Dashboard API",
				Version: "1.0.0",
				Description: "REST API for the Pacto service contract dashboard. " +
					"Serves the operational graph of a service fleet, plus the " +
					"contract-view endpoints the offline static export answers.",
			},
			// A custom schema registry namespaces pkg/fleet types so their short
			// Go names (e.g. GraphNode) do not collide with dashboard types of
			// the same name in the shared OpenAPI component registry. Non-fleet
			// types keep their default names, so existing schemas are unchanged.
			Components: &huma.Components{
				Schemas: huma.NewMapRegistry("#/components/schemas/", fleetSchemaNamer),
			},
		},
		OpenAPIPath:   "/openapi",
		DocsPath:      "/docs",
		SchemasPath:   "/schemas",
		Formats:       huma.DefaultFormats,
		DefaultFormat: "application/json",
	}
}

// unqualifiedSchemaPkgs are the packages whose types own the bare component
// names. pkg/contractview is here because its DTOs ARE the dashboard's published
// contract-view schemas: they were declared in pkg/dashboard until the rendering
// half moved to its own leaf, they are still aliased back from here, and the
// generated TypeScript SDK is built from those exact component names. Qualifying
// them would rename every schema in the published document.
var unqualifiedSchemaPkgs = []string{"/pkg/dashboard", "/pkg/contractview"}

// fleetSchemaNamer disambiguates OpenAPI component names. The fleet and impact
// endpoints pull engine types (contract, finding, readiness, lock, fleet, diff,
// impact) into the shared schema registry, and their short Go names can collide
// with dashboard DTOs of the same name (e.g. contract.Service vs dashboard.Service,
// or diff.Change vs dashboard.DiffChange). The dashboard's own types — including
// the contract-view DTOs it re-exports — keep their canonical names; every other
// package's types are qualified by their package. The qualifier is joined with a
// "." — a character no Go identifier can contain — so a package-qualified name can
// never coincide with a bare dashboard name, and no two distinct types can ever
// map to one component name.
func fleetSchemaNamer(t reflect.Type, hint string) string {
	name := huma.DefaultSchemaNamer(t, hint)
	// Body types arrive as pointers (e.g. *impact.Result), whose PkgPath is empty;
	// dereference to the named element so the qualifier is derived from the real
	// package — otherwise two distinct *pkg.Result bodies both fall back to the
	// bare "Result" and collide.
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	pkg := t.PkgPath()
	if pkg == "" || slices.ContainsFunc(unqualifiedSchemaPkgs, func(p string) bool { return strings.HasSuffix(pkg, p) }) {
		return name
	}
	base := pkg[strings.LastIndex(pkg, "/")+1:]
	return strings.ToUpper(base[:1]) + base[1:] + "." + name
}

// NewServer creates a dashboard server. ui is the embedded filesystem containing
// the web UI assets. Data reaches the server through [Server.SetFleetProvider].
func NewServer(ui fs.FS) *Server {
	return &Server{ui: ui, logger: slog.Default()}
}

// SetFleetRefresher registers the callback /api/refresh invokes to rebuild the
// fleet snapshot. When unset — as in the offline WASM demo, whose snapshot is
// baked in — refresh is a successful no-op.
func (s *Server) SetFleetRefresher(fn func(context.Context) error) {
	s.fleetRefresh = fn
}

// Serve starts the HTTP server on the given host and port and blocks until ctx is cancelled.
// An empty host defaults to 127.0.0.1.
func (s *Server) Serve(ctx context.Context, port int, host ...string) error {
	h := "127.0.0.1"
	if len(host) > 0 && host[0] != "" {
		h = host[0]
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", h, port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return s.ServeOnListener(ctx, ln)
}

// SetListenAddr sets the server URL exposed in the OpenAPI spec.
func (s *Server) SetListenAddr(host string, port int) {
	if host == "" || host == "0.0.0.0" {
		host = "localhost"
	}
	s.listenAddr = fmt.Sprintf("http://%s:%d", host, port)
}

// ServeOnListener starts the HTTP server on an existing listener.
func (s *Server) ServeOnListener(ctx context.Context, ln net.Listener) error {
	mux := http.NewServeMux()

	s.registerAPI(mux)

	// Static UI — served on the raw mux, not through Huma.
	mux.Handle("/", http.FileServer(http.FS(s.ui)))

	// Every request derives from baseCtx, so Ctrl+C can actively cancel slow
	// in-flight handlers instead of Shutdown blocking on them until the deadline.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	srv := &http.Server{
		Handler:           s.corsMiddleware(mux),
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout is intentionally unset: the static handler streams the
		// multi-MB embedded UI bundle, which can be slow on throttled links.
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		// Cancel in-flight request contexts so slow handlers abort at once, then
		// drain with a short bounded timeout. Ctrl+C is a clean, user-initiated
		// stop, so a drain timeout is not an error to surface.
		cancelBase()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// registerAPI registers all Huma operations on the given mux.
func (s *Server) registerAPI(mux *http.ServeMux) {
	cfg := APIConfig()
	if s.listenAddr != "" {
		cfg.Servers = []*huma.Server{{URL: s.listenAddr}}
	}
	api := humago.New(mux, cfg)
	s.RegisterOperations(api)
}

// RegisterOperations registers every operation a LIVE dashboard host serves.
// The contract-view operations are deliberately absent: they are answered only by
// the offline static export, which reads an embedded route table instead of a
// server, so declaring them here would promise a route no host can honour. They
// are still part of the API document — see [registerOfflineOperations].
func (s *Server) RegisterOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
		Description: "Returns service health status.",
		Tags:        []string{"Health"},
	}, s.health)

	huma.Register(api, huma.Operation{
		OperationID: "metrics",
		Method:      http.MethodGet,
		Path:        "/metrics",
		Summary:     "Basic metrics",
		Description: "Returns basic service metrics.",
		Tags:        []string{"Health"},
	}, s.metrics)

	huma.Register(api, huma.Operation{
		OperationID: "get-sources",
		Method:      http.MethodGet,
		Path:        "/api/sources",
		Summary:     "Get detected sources",
		Description: "Returns the list of data sources backing the published snapshot and their status.",
		Tags:        []string{"Sources"},
	}, s.getSources)

	huma.Register(api, huma.Operation{
		OperationID: "refresh",
		Method:      http.MethodPost,
		Path:        "/api/refresh",
		Summary:     "Force refresh all sources",
		Description: "Rebuilds the fleet snapshot from every configured source.",
		Tags:        []string{"Sources"},
	}, s.refresh)

	s.registerCapabilitiesOperation(api)
	s.registerFleetOperations(api)
	s.registerProductOperations(api)
}

// ExportOpenAPI builds the Huma API with every operation registered — the live
// ones and the offline-only contract-view ones — and returns the serialized
// OpenAPI 3.1 specification. This can be called without starting a server.
func ExportOpenAPI() ([]byte, error) {
	mux := http.NewServeMux()
	api := humago.New(mux, APIConfig())

	// Register with stub providers — we only need the schema, not runtime behavior.
	// The stubs make the fleet and product operations register so the exported spec
	// is the complete API contract (the handlers are never invoked here).
	s := &Server{}
	s.stubProvidersForSchemaExport()
	s.RegisterOperations(api)
	registerOfflineOperations(api)

	return api.OpenAPI().MarshalJSON()
}

// ── Health / Metrics types ───────────────────────────────────────────

type healthOutput struct {
	Body struct {
		Status  string `json:"status" example:"ok" doc:"Health status"`
		Version string `json:"version,omitempty" example:"1.2.3" doc:"Pacto version"`
	}
}

type metricsOutput struct {
	Body struct {
		ServiceCount int `json:"serviceCount" doc:"Number of known services"`
		SourceCount  int `json:"sourceCount" doc:"Number of active data sources"`
	}
}

type getSourcesOutput struct {
	Body struct {
		Sources     []SourceInfo `json:"sources" doc:"Data sources backing the published snapshot"`
		Discovering bool         `json:"discovering" doc:"True while the first snapshot is still being built"`
	}
}

type refreshOutput struct {
	Body struct {
		Status string `json:"status" example:"ok" doc:"Refresh result"`
	}
}

// ── Huma operation handlers ─────────────────────────────────────────

// SetVersion sets the Pacto version exposed by the health endpoint.
func (s *Server) SetVersion(v string) {
	s.version = v
}

// SetCORSOrigin allows an explicit cross-origin client to call the API. When
// empty (the default) the API is same-origin only and cross-origin mutating
// requests are rejected. Must be called before Serve.
func (s *Server) SetCORSOrigin(origin string) {
	s.corsOrigin = origin
}

// SetLogger sets the logger injected into every request context, so handlers
// log through it (not the process default). A nil logger falls back to
// slog.Default(). Must be called before Serve.
func (s *Server) SetLogger(lg *slog.Logger) {
	if lg == nil {
		lg = slog.Default()
	}
	s.logger = lg
}

func (s *Server) health(_ context.Context, _ *struct{}) (*healthOutput, error) {
	out := &healthOutput{}
	out.Body.Status = "ok"
	out.Body.Version = s.version
	return out, nil
}

// publishedQuery returns the currently published fleet query, or nil when no
// fleet provider is wired or no snapshot has been built yet.
func (s *Server) publishedQuery(ctx context.Context) *fleet.Query {
	if s.fleetQuery == nil {
		return nil
	}
	q, err := s.fleetQuery(ctx)
	if err != nil {
		return nil
	}
	return q
}

func (s *Server) metrics(ctx context.Context, _ *struct{}) (*metricsOutput, error) {
	out := &metricsOutput{}
	q := s.publishedQuery(ctx)
	out.Body.SourceCount = len(sourcesFromFleet(q))
	if q != nil {
		out.Body.ServiceCount = len(q.SnapshotForSerialization().Services)
	}
	return out, nil
}

// getSources reports the per-kind source pills the nav renders. They are derived
// from the published snapshot's own source states, so the pills describe what the
// data the user is looking at actually came from.
func (s *Server) getSources(ctx context.Context, _ *struct{}) (*getSourcesOutput, error) {
	out := &getSourcesOutput{}
	q := s.publishedQuery(ctx)
	out.Body.Sources = sourcesFromFleet(q)
	// A wired provider with nothing published yet is the honest "still discovering"
	// state; no provider at all is simply a host with no sources.
	out.Body.Discovering = s.fleetQuery != nil && q == nil
	return out, nil
}

func (s *Server) refresh(ctx context.Context, _ *struct{}) (*refreshOutput, error) {
	if s.fleetRefresh != nil {
		if err := s.fleetRefresh(ctx); err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
	}
	out := &refreshOutput{}
	out.Body.Status = "ok"
	return out, nil
}

// corsMiddleware handles CORS and protects mutating endpoints from
// browser-driven cross-origin (CSRF/SSRF) requests. The dashboard UI is served
// same-origin, so by default no Access-Control-Allow-Origin is emitted and
// cross-origin mutating requests are rejected. An explicit cross-origin client
// can be allowed via SetCORSOrigin.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if s.corsOrigin != "" && origin == s.corsOrigin {
			w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Reject cross-origin mutating requests (the CSRF surface on /api/refresh
		// and the POST fleet endpoints). Same-origin requests and non-browser
		// clients (no Origin header) are allowed.
		if isMutatingMethod(r.Method) && !s.originAllowed(r) {
			http.Error(w, "cross-origin request forbidden", http.StatusForbidden)
			return
		}
		// Carry the command-configured logger on the request context so handlers
		// log through it (not the process default) via logging.LoggerFromContext.
		next.ServeHTTP(w, r.WithContext(logging.WithLogger(r.Context(), s.logger)))
	})
}

// originAllowed reports whether r may perform a mutating request: true for
// same-origin requests, the explicitly allowed cross-origin, or clients that
// send no Origin header (e.g. curl, the CLI).
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if s.corsOrigin != "" && origin == s.corsOrigin {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func isMutatingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
