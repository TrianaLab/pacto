package doc

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/dashboard"
)

// BuildStaticExport returns the embedded dashboard UI tree (path -> bytes) with
// index.html rewritten to embed the snapshot as window.__PACTO_STATIC__, so the
// single-service view renders offline with no backend.
func BuildStaticExport(d *dashboard.ServiceDetails, g *dashboard.GlobalGraph) (map[string][]byte, error) {
	return buildStaticExport(dashboard.EmbeddedUI(), d, g)
}

// buildStaticExport is the testable inner function that accepts an injectable fs.FS.
func buildStaticExport(uiFS fs.FS, d *dashboard.ServiceDetails, g *dashboard.GlobalGraph) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(uiFS, ".", func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, rerr := fs.ReadFile(uiFS, p)
		if rerr != nil {
			return rerr
		}
		out[p] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading embedded ui: %w", err)
	}

	idx, ok := out["index.html"]
	if !ok {
		return nil, fmt.Errorf("embedded ui missing index.html")
	}

	// Request-semantic static fixtures (ADR-6, requirement, item 1): each fixture
	// names the method + path (+ response) of one operation the offline single-service
	// app calls, so the transport matches by request semantics and an unfixtured
	// operation fails honestly rather than returning a misleading 200 + null. The set
	// is exactly the offline contract: the services list (empty), this service, its
	// graph, its versions, its dependents, and its cross-references (a deliberate,
	// EXPLICIT null - not a universal fallback).
	routes := []map[string]any{
		// The offline export is a NON-Fleet host: it serves the single-service legacy UI
		// and none of the /api/fleet/* product endpoints. Declaring capabilities
		// explicitly (fleet:false) lets the SPA resolve its host class definitively -- so
		// it renders the legacy service view (its only UI here) instead of waiting on an
		// unanswerable capabilities probe, and never offers a dead Fleet nav item.
		{"method": "GET", "path": "/api/capabilities", "response": map[string]any{"fleet": false, "impact": false, "observed": false}},
		{"method": "GET", "path": "/api/services", "response": []any{}},
		{"method": "GET", "path": "/api/services/" + d.Name, "response": d},
		{"method": "GET", "path": "/api/graph", "response": g},
		{"method": "GET", "path": "/api/services/" + d.Name + "/versions", "response": []dashboard.Version{{Version: d.Version, IsCurrent: true}}},
		{"method": "GET", "path": "/api/services/" + d.Name + "/dependents", "response": []any{}},
		{"method": "GET", "path": "/api/services/" + d.Name + "/refs", "response": nil},
	}
	// payload holds only marshalable structs; Marshal cannot fail here.
	payload, _ := json.Marshal(map[string]any{"service": d.Name, "routes": routes})

	script := "<script>window.__PACTO_STATIC__ = " + string(payload) + ";</script>"
	injected := strings.Replace(string(idx), "</head>", script+"</head>", 1)
	out["index.html"] = []byte(injected)
	return out, nil
}
