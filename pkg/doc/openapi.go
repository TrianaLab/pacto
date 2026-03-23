package doc

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

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

// ReadOpenAPIEndpoints parses an OpenAPI spec (YAML or JSON) and returns its
// endpoints sorted by path (alphabetically) then by HTTP method order.
func ReadOpenAPIEndpoints(fsys fs.FS, path string) ([]Endpoint, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("reading OpenAPI spec %s: %w", path, err)
	}

	spec, err := UnmarshalSpec(data, path)
	if err != nil {
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
		endpoints = append(endpoints, extractPathEndpoints(p, paths[p])...)
	}

	return endpoints, nil
}

// extractPathEndpoints returns the endpoints for a single OpenAPI path entry,
// sorted by standard HTTP method order.
func extractPathEndpoints(path string, raw any) []Endpoint {
	methods, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	var methodKeys []string
	for m := range methods {
		if _, isHTTP := httpMethodOrder[m]; isHTTP {
			methodKeys = append(methodKeys, m)
		}
	}
	sort.Slice(methodKeys, func(i, j int) bool {
		return httpMethodOrder[methodKeys[i]] < httpMethodOrder[methodKeys[j]]
	})

	var endpoints []Endpoint
	for _, m := range methodKeys {
		op, ok := methods[m].(map[string]any)
		if !ok {
			continue
		}
		ep := Endpoint{Method: m, Path: path}
		if summary, ok := op["summary"].(string); ok {
			ep.Summary = summary
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints
}

// UnmarshalSpec parses an OpenAPI spec as JSON (for .json files) or YAML.
func UnmarshalSpec(data []byte, path string) (map[string]any, error) {
	var spec map[string]any
	if strings.HasSuffix(path, ".json") {
		return spec, json.Unmarshal(data, &spec)
	}
	return spec, yaml.Unmarshal(data, &spec)
}
