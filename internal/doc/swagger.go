package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/trianalab/pacto/pkg/contract"

	"gopkg.in/yaml.v3"
)

// SwaggerSpec pairs an interface name with the path to its OpenAPI spec file.
type SwaggerSpec struct {
	InterfaceName string
	SpecPath      string
}

// CollectSwaggerSpecs returns the HTTP interfaces that have an OpenAPI contract.
func CollectSwaggerSpecs(interfaces []contract.Interface) []SwaggerSpec {
	var specs []SwaggerSpec
	for _, iface := range interfaces {
		if iface.Type == contract.InterfaceTypeHTTP && iface.Contract != "" {
			specs = append(specs, SwaggerSpec{
				InterfaceName: iface.Name,
				SpecPath:      iface.Contract,
			})
		}
	}
	return specs
}

// FilterSpecs returns only the spec matching the given interface name.
// It returns nil if no match is found.
func FilterSpecs(specs []SwaggerSpec, name string) []SwaggerSpec {
	for _, s := range specs {
		if s.InterfaceName == name {
			return []SwaggerSpec{s}
		}
	}
	return nil
}

// SwaggerOptions configures the interactive API explorer server.
type SwaggerOptions struct {
	Specs   []SwaggerSpec
	FS      fs.FS
	Title   string
	Port    int
	Target  string            // global target; applies to all interfaces
	Targets map[string]string // per-interface targets; overrides Target
}

// targetFor returns the target URL for a specific interface.
// Per-interface targets take precedence over the global target.
func (o SwaggerOptions) targetFor(name string) string {
	if t, ok := o.Targets[name]; ok {
		return t
	}
	return o.Target
}

// allowedTargets returns all unique target URLs from both global and
// per-interface targets, used for proxy validation.
func allowedTargets(opts SwaggerOptions) []string {
	seen := make(map[string]bool)
	var targets []string
	if opts.Target != "" {
		seen[opts.Target] = true
		targets = append(targets, opts.Target)
	}
	for _, t := range opts.Targets {
		if !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	return targets
}

// ServeSwagger starts a local HTTP server that renders an interactive API
// explorer (Scalar) for every HTTP interface that has an OpenAPI contract.
// It blocks until the context is cancelled.
func ServeSwagger(ctx context.Context, opts SwaggerOptions) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", opts.Port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	return ServeSwaggerOnListener(ctx, opts, ln)
}

// ServeSwaggerOnListener is like ServeSwagger but accepts an existing
// net.Listener. This is useful in tests where port 0 is used to obtain a
// random port and the caller needs the address before blocking.
func ServeSwaggerOnListener(ctx context.Context, opts SwaggerOptions, ln net.Listener) error {
	mux := http.NewServeMux()

	targets := allowedTargets(opts)
	if len(targets) > 0 {
		mux.HandleFunc("/proxy", newProxyHandler(targets))
	}

	for _, s := range opts.Specs {
		target := opts.targetFor(s.InterfaceName)
		registerSpecHandler(mux, s, opts.FS, target)
	}

	hasProxy := len(targets) > 0
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, buildSwaggerPage(opts.Specs, opts.Title, hasProxy))
	})

	srv := &http.Server{Handler: mux}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		return srv.Close()
	case err := <-errCh:
		return err
	}
}

// registerSpecHandler registers a GET handler that serves the OpenAPI spec
// as JSON. Scalar works best with JSON, so YAML specs are converted.
// If target is non-empty, the spec's servers array is overridden so Scalar
// shows the target URL (requests still go through the local proxy).
func registerSpecHandler(mux *http.ServeMux, s SwaggerSpec, fsys fs.FS, target string) {
	pattern := "/spec/" + s.InterfaceName
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		data, err := fs.ReadFile(fsys, s.SpecPath)
		if err != nil {
			http.Error(w, "spec not found", http.StatusNotFound)
			return
		}

		jsonData, err := ensureJSON(data, s.SpecPath)
		if err != nil {
			http.Error(w, "invalid spec", http.StatusInternalServerError)
			return
		}

		if target != "" {
			// overrideServers cannot fail here because ensureJSON above
			// already produced valid JSON.
			jsonData, _ = overrideServers(jsonData, target)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write(jsonData)
	})
}

// overrideServers replaces the "servers" array in an OpenAPI JSON spec
// with a single entry pointing to the given URL.
func overrideServers(specJSON []byte, serverURL string) ([]byte, error) {
	var spec map[string]any
	if err := json.Unmarshal(specJSON, &spec); err != nil {
		return nil, err
	}
	spec["servers"] = []map[string]any{
		{"url": serverURL, "description": "Target server"},
	}
	return json.Marshal(spec)
}

// newProxyHandler returns an HTTP handler that forwards requests to the
// target. Scalar sends requests to the proxy with the full upstream URL
// in the scalar_url query parameter. This avoids CORS by keeping browser
// traffic same-origin while showing the real URL in the UI.
//
// The allowed slice lists URL prefixes that the proxy is permitted to
// forward to, preventing open-proxy abuse.
func newProxyHandler(allowed []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("scalar_url")
		if targetURL == "" {
			http.Error(w, "missing scalar_url parameter", http.StatusBadRequest)
			return
		}

		ok := false
		for _, t := range allowed {
			if strings.HasPrefix(targetURL, t) {
				ok = true
				break
			}
		}
		if !ok {
			http.Error(w, "target not allowed", http.StatusForbidden)
			return
		}

		proxyReq, _ := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
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

// ensureJSON returns JSON bytes. If the input is a YAML file (by extension)
// it converts it to JSON first.
func ensureJSON(data []byte, path string) ([]byte, error) {
	if strings.HasSuffix(path, ".json") {
		// Validate it's actual JSON.
		if !json.Valid(data) {
			return nil, fmt.Errorf("invalid JSON in %s", path)
		}
		return data, nil
	}
	var obj any
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("parsing YAML %s: %w", path, err)
	}
	return json.Marshal(convertYAMLToJSON(obj))
}

// convertYAMLToJSON recursively converts map[string]any (from YAML) into
// structures that json.Marshal handles correctly. The gopkg.in/yaml.v3
// decoder produces map[string]any for mappings, which is already compatible,
// but nested values may contain non-JSON types.
func convertYAMLToJSON(v any) any {
	switch val := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[k] = convertYAMLToJSON(v)
		}
		return m
	case []any:
		a := make([]any, len(val))
		for i, v := range val {
			a[i] = convertYAMLToJSON(v)
		}
		return a
	default:
		return val
	}
}

func buildSwaggerPage(specs []SwaggerSpec, title string, useProxy bool) string {
	if len(specs) == 1 {
		return buildSingleSpecPage(specs[0], title, useProxy)
	}
	return buildMultiSpecPage(specs, title, useProxy)
}

func buildSingleSpecPage(spec SwaggerSpec, title string, useProxy bool) string {
	proxyAttr := ""
	if useProxy {
		proxyAttr = ` data-proxy-url="/proxy"`
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head>
  <meta charset="utf-8">
  <title>%s - API Explorer</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
</head><body>
  <script id="api-reference" data-url="/spec/%s"%s></script>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body></html>`, title, spec.InterfaceName, proxyAttr)
}

func buildMultiSpecPage(specs []SwaggerSpec, title string, useProxy bool) string {
	var links strings.Builder
	for _, s := range specs {
		fmt.Fprintf(&links, `      <li><a href="#" onclick="loadSpec('/spec/%s','%s');return false">%s</a></li>`+"\n",
			s.InterfaceName, s.InterfaceName, s.InterfaceName)
	}

	proxyLine := ""
	if useProxy {
		proxyLine = `      el.dataset.proxyUrl = '/proxy';` + "\n"
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html><head>
  <meta charset="utf-8">
  <title>%s - API Explorer</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    nav { background: #1a1a2e; padding: 12px 24px; display: flex; align-items: center; gap: 24px; }
    nav h1 { color: #fff; font-size: 16px; margin: 0; font-family: sans-serif; }
    nav ul { list-style: none; margin: 0; padding: 0; display: flex; gap: 16px; }
    nav a { color: #a8b2d1; text-decoration: none; font-family: sans-serif; font-size: 14px; }
    nav a:hover, nav a.active { color: #fff; }
  </style>
</head><body>
  <nav>
    <h1>%s</h1>
    <ul>
%s    </ul>
  </nav>
  <div id="api-container"></div>
  <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  <script>
    function loadSpec(url, name) {
      document.querySelectorAll('nav a').forEach(a => a.classList.remove('active'));
      event.target.classList.add('active');
      const container = document.getElementById('api-container');
      container.innerHTML = '';
      const el = document.createElement('script');
      el.id = 'api-reference';
      el.dataset.url = url;
%s      container.appendChild(el);
    }
    // Load first spec by default.
    document.querySelector('nav a').click();
  </script>
</body></html>`, title, title, links.String(), proxyLine)
}
