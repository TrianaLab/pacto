package diff

import (
	"io/fs"

	"gopkg.in/yaml.v3"
)

// diffOpenAPI compares two OpenAPI spec files and returns changes for
// added/removed paths. This is the integration boundary — a richer
// implementation can be swapped in (e.g., oasdiff) without changing
// the engine interface.
func diffOpenAPI(oldPath, newPath string, oldFS, newFS fs.FS) []Change {
	return diffFileSet(oldPath, newPath, oldFS, newFS, readOpenAPIPaths, "openapi.paths", "API path")
}

// readOpenAPIPaths parses an OpenAPI file and extracts the top-level path keys.
func readOpenAPIPaths(fsys fs.FS, path string) (map[string]bool, error) {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var spec struct {
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, err
	}

	paths := make(map[string]bool, len(spec.Paths))
	for p := range spec.Paths {
		paths[p] = true
	}
	return paths, nil
}
