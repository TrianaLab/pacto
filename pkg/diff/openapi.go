package diff

import (
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true,
	"patch": true, "options": true, "head": true, "trace": true,
}

// diffOpenAPI compares two OpenAPI spec files and returns changes for
// paths, methods, request bodies, and responses.
func diffOpenAPI(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	if oldFS == nil || newFS == nil || oldPath == "" || newPath == "" {
		return nil
	}

	oldSpec, oldErr := readOpenAPISpec(oldFS, oldPath)
	newSpec, newErr := readOpenAPISpec(newFS, newPath)

	if oldErr != nil || newErr != nil {
		return nil
	}

	return diffOpenAPISpecs(oldSpec, newSpec)
}

// openAPISpec holds the parsed paths from an OpenAPI document.
type openAPISpec struct {
	Paths map[string]map[string]any
}

// readOpenAPISpec parses an OpenAPI file and extracts paths with their methods.
func readOpenAPISpec(fsys fs.FS, path string) (*openAPISpec, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	return &openAPISpec{Paths: raw.Paths}, nil
}

// diffOpenAPISpecs compares two parsed OpenAPI specs.
func diffOpenAPISpecs(old, new *openAPISpec) []Change {
	var changes []Change

	for path, oldMethods := range old.Paths {
		newMethods, exists := new.Paths[path]
		if !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s]", path),
				Type:           Removed,
				OldValue:       path,
				Classification: classify("openapi.paths", Removed),
				Reason:         fmt.Sprintf("API path %s removed", path),
			})
			continue
		}
		changes = append(changes, diffPathMethods(path, oldMethods, newMethods)...)
	}

	for path := range new.Paths {
		if _, exists := old.Paths[path]; !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s]", path),
				Type:           Added,
				NewValue:       path,
				Classification: classify("openapi.paths", Added),
				Reason:         fmt.Sprintf("API path %s added", path),
			})
		}
	}

	return changes
}

// diffPathMethods compares the HTTP methods within a single API path.
func diffPathMethods(path string, oldMethods, newMethods map[string]any) []Change {
	var changes []Change

	for method, oldOp := range oldMethods {
		if !httpMethods[method] {
			continue
		}
		upper := strings.ToUpper(method)
		newOp, exists := newMethods[method]
		if !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s]", path, upper),
				Type:           Removed,
				OldValue:       fmt.Sprintf("%s %s", upper, path),
				Classification: classify("openapi.methods", Removed),
				Reason:         fmt.Sprintf("%s %s method removed", upper, path),
			})
			continue
		}
		changes = append(changes, diffOperation(path, upper, oldOp, newOp)...)
	}

	for method := range newMethods {
		if !httpMethods[method] {
			continue
		}
		if _, exists := oldMethods[method]; !exists {
			upper := strings.ToUpper(method)
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s]", path, upper),
				Type:           Added,
				NewValue:       fmt.Sprintf("%s %s", upper, path),
				Classification: classify("openapi.methods", Added),
				Reason:         fmt.Sprintf("%s %s method added", upper, path),
			})
		}
	}

	return changes
}

// diffOperation compares two operations (same path + method) for parameter,
// request body, and response changes.
func diffOperation(path, method string, oldOp, newOp any) []Change {
	oldMap := toStringMap(oldOp)
	newMap := toStringMap(newOp)
	if oldMap == nil && newMap == nil {
		return nil
	}

	var changes []Change

	// Compare parameters.
	changes = append(changes, diffParameters(path, method, toSlice(oldMap["parameters"]), toSlice(newMap["parameters"]))...)

	// Compare request body.
	oldBody, oldHas := oldMap["requestBody"]
	newBody, newHas := newMap["requestBody"]
	bodyPath := fmt.Sprintf("openapi.paths[%s].methods[%s].request-body", path, method)

	if oldHas && !newHas {
		changes = append(changes, Change{
			Path:           bodyPath,
			Type:           Removed,
			OldValue:       fmt.Sprintf("%s %s", method, path),
			Classification: classify("openapi.request-body", Removed),
			Reason:         fmt.Sprintf("%s %s request body removed", method, path),
		})
	} else if !oldHas && newHas {
		changes = append(changes, Change{
			Path:           bodyPath,
			Type:           Added,
			NewValue:       fmt.Sprintf("%s %s", method, path),
			Classification: classify("openapi.request-body", Added),
			Reason:         fmt.Sprintf("%s %s request body added", method, path),
		})
	} else if oldHas && newHas && !yamlEqual(oldBody, newBody) {
		changes = append(changes, Change{
			Path:           bodyPath,
			Type:           Modified,
			OldValue:       fmt.Sprintf("%s %s", method, path),
			NewValue:       fmt.Sprintf("%s %s", method, path),
			Classification: classify("openapi.request-body", Modified),
			Reason:         fmt.Sprintf("%s %s request body modified", method, path),
		})
	}

	// Compare responses.
	oldResponses := toStringMap(oldMap["responses"])
	newResponses := toStringMap(newMap["responses"])
	changes = append(changes, diffResponses(path, method, oldResponses, newResponses)...)

	return changes
}

// paramKey returns a unique key for an OpenAPI parameter: "name:in".
func paramKey(param map[string]any) string {
	name, _ := param["name"].(string)
	in, _ := param["in"].(string)
	return name + ":" + in
}

// paramLabel returns a human-readable label like "query param 'filter'".
func paramLabel(param map[string]any) string {
	name, _ := param["name"].(string)
	in, _ := param["in"].(string)
	return fmt.Sprintf("%s param '%s'", in, name)
}

// diffParameters compares operation parameters identified by name+in.
func diffParameters(path, method string, oldParams, newParams []any) []Change {
	oldByKey := indexParams(oldParams)
	newByKey := indexParams(newParams)

	var changes []Change

	for key, oldParam := range oldByKey {
		newParam, exists := newByKey[key]
		if !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s].parameters[%s]", path, method, key),
				Type:           Removed,
				OldValue:       fmt.Sprintf("%s %s %s", method, path, paramLabel(oldParam)),
				Classification: classify("openapi.parameters", Removed),
				Reason:         fmt.Sprintf("%s %s %s removed", method, path, paramLabel(oldParam)),
			})
			continue
		}
		if !yamlEqual(oldParam, newParam) {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s].parameters[%s]", path, method, key),
				Type:           Modified,
				OldValue:       fmt.Sprintf("%s %s %s", method, path, paramLabel(oldParam)),
				NewValue:       fmt.Sprintf("%s %s %s", method, path, paramLabel(newParam)),
				Classification: classify("openapi.parameters", Modified),
				Reason:         fmt.Sprintf("%s %s %s modified", method, path, paramLabel(oldParam)),
			})
		}
	}

	for key, newParam := range newByKey {
		if _, exists := oldByKey[key]; !exists {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("openapi.paths[%s].methods[%s].parameters[%s]", path, method, key),
				Type:           Added,
				NewValue:       fmt.Sprintf("%s %s %s", method, path, paramLabel(newParam)),
				Classification: classify("openapi.parameters", Added),
				Reason:         fmt.Sprintf("%s %s %s added", method, path, paramLabel(newParam)),
			})
		}
	}

	return changes
}

// indexParams builds a map keyed by "name:in" from a parameter slice.
func indexParams(params []any) map[string]map[string]any {
	m := make(map[string]map[string]any, len(params))
	for _, p := range params {
		pm := toStringMap(p)
		if pm == nil {
			continue
		}
		key := paramKey(pm)
		if key == ":" {
			continue
		}
		m[key] = pm
	}
	return m
}

// diffResponses compares response status codes and their definitions.
func diffResponses(path, method string, oldResp, newResp map[string]any) []Change {
	var changes []Change

	for code, oldVal := range oldResp {
		respPath := fmt.Sprintf("openapi.paths[%s].methods[%s].responses[%s]", path, method, code)
		newVal, exists := newResp[code]
		if !exists {
			changes = append(changes, Change{
				Path:           respPath,
				Type:           Removed,
				OldValue:       fmt.Sprintf("%s %s %s", method, path, code),
				Classification: classify("openapi.responses", Removed),
				Reason:         fmt.Sprintf("%s %s response %s removed", method, path, code),
			})
			continue
		}
		if !yamlEqual(oldVal, newVal) {
			changes = append(changes, Change{
				Path:           respPath,
				Type:           Modified,
				OldValue:       fmt.Sprintf("%s %s %s", method, path, code),
				NewValue:       fmt.Sprintf("%s %s %s", method, path, code),
				Classification: classify("openapi.responses", Modified),
				Reason:         fmt.Sprintf("%s %s response %s modified", method, path, code),
			})
		}
	}

	for code := range newResp {
		if _, exists := oldResp[code]; !exists {
			respPath := fmt.Sprintf("openapi.paths[%s].methods[%s].responses[%s]", path, method, code)
			changes = append(changes, Change{
				Path:           respPath,
				Type:           Added,
				NewValue:       fmt.Sprintf("%s %s %s", method, path, code),
				Classification: classify("openapi.responses", Added),
				Reason:         fmt.Sprintf("%s %s response %s added", method, path, code),
			})
		}
	}

	return changes
}

// toStringMap converts an interface{} to map[string]any.
func toStringMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// toSlice converts an interface{} to []any.
func toSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// yamlEqual compares two values by serializing to YAML. yaml.v3 produces
// deterministic output with sorted map keys.
func yamlEqual(a, b any) bool {
	aBytes, _ := yaml.Marshal(a)
	bBytes, _ := yaml.Marshal(b)
	return string(aBytes) == string(bBytes)
}
