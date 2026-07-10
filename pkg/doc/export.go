package doc

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"github.com/trianalab/pacto/v2/pkg/dashboard"
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

	routes := map[string]any{
		"/api/services/" + d.Name:                 d,
		"/api/graph":                              g,
		"/api/services/" + d.Name + "/versions":   []dashboard.Version{{Version: d.Version, IsCurrent: true}},
		"/api/services/" + d.Name + "/dependents": []any{},
	}
	// payload holds only marshalable structs; Marshal cannot fail here.
	payload, _ := json.Marshal(map[string]any{"service": d.Name, "routes": routes})

	script := "<script>window.__PACTO_STATIC__ = " + string(payload) + ";</script>"
	injected := strings.Replace(string(idx), "</head>", script+"</head>", 1)
	out["index.html"] = []byte(injected)
	return out, nil
}
