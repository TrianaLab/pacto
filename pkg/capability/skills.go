// Package capability turns a bundle's OpenAPI interface into executable agent
// tools (BuildTools + Invoke) and serves optional skills/*.md domain knowledge.
// It consumes the parsed OpenAPI model from pkg/openapi and is shared by the MCP
// server and the dashboard.
package capability

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const skillsDir = "skills"

// Skills lists the basenames of skills/*.md in the bundle FS, sorted. It returns
// nil (not an error) when there is no skills directory.
func Skills(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, skillsDir)
	if err != nil {
		return nil, nil // no skills/ dir is not an error
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Skill reads one skill markdown file by basename, rejecting path traversal.
func Skill(fsys fs.FS, name string) (string, error) {
	base := path.Base(name)
	if base != name || base == "." || base == ".." {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	data, err := fs.ReadFile(fsys, path.Join(skillsDir, base))
	if err != nil {
		return "", fmt.Errorf("read skill %q: %w", name, err)
	}
	return string(data), nil
}
