package doc

import (
	"fmt"
	"io/fs"
	"sort"

	"gopkg.in/yaml.v3"
)

// Endpoint represents a single HTTP endpoint extracted from an OpenAPI spec.
type Endpoint struct {
	Method  string
	Path    string
	Summary string
}

// httpMethods is the set of valid HTTP methods in standard display order.
var httpMethodOrder = map[string]int{
	"get":     0,
	"post":    1,
	"put":     2,
	"patch":   3,
	"delete":  4,
	"head":    5,
	"options": 6,
	"trace":   7,
}

// readOpenAPIEndpoints parses an OpenAPI YAML file and returns its endpoints
// sorted by path (alphabetically) then by HTTP method order.
func readOpenAPIEndpoints(fsys fs.FS, path string) ([]Endpoint, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading OpenAPI spec %s: %w", path, err)
	}

	var spec map[string]any
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parsing OpenAPI spec %s: %w", path, err)
	}

	pathsRaw, ok := spec["paths"]
	if !ok {
		return nil, nil
	}

	paths, ok := pathsRaw.(map[string]any)
	if !ok {
		return nil, nil
	}

	// Collect and sort path keys.
	pathKeys := make([]string, 0, len(paths))
	for p := range paths {
		pathKeys = append(pathKeys, p)
	}
	sort.Strings(pathKeys)

	var endpoints []Endpoint
	for _, p := range pathKeys {
		methods, ok := paths[p].(map[string]any)
		if !ok {
			continue
		}

		// Collect HTTP methods for this path, sorted by standard order.
		var methodKeys []string
		for m := range methods {
			if _, isHTTP := httpMethodOrder[m]; isHTTP {
				methodKeys = append(methodKeys, m)
			}
		}
		sort.Slice(methodKeys, func(i, j int) bool {
			return httpMethodOrder[methodKeys[i]] < httpMethodOrder[methodKeys[j]]
		})

		for _, m := range methodKeys {
			op, ok := methods[m].(map[string]any)
			if !ok {
				continue
			}

			ep := Endpoint{
				Method: m,
				Path:   p,
			}

			if summary, ok := op["summary"].(string); ok {
				ep.Summary = summary
			}

			endpoints = append(endpoints, ep)
		}
	}

	return endpoints, nil
}
