// Package skills reads a bundle's optional domain-knowledge documents
// (skills/*.md): workflows, business rules, and operational guidance that an
// interface alone cannot express. Skills are bundle-level knowledge and are
// independent of interface type, so they live in their own package rather than
// under any single interface capability.
package skills

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const dir = "skills"

// List returns the basenames of skills/*.md in the bundle FS, sorted. It returns
// nil (not an error) when there is no skills directory.
func List(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
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

// Read reads one skill markdown file by basename, rejecting path traversal.
func Read(fsys fs.FS, name string) (string, error) {
	base := path.Base(name)
	if base != name || base == "." || base == ".." {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	data, err := fs.ReadFile(fsys, path.Join(dir, base))
	if err != nil {
		return "", fmt.Errorf("read skill %q: %w", name, err)
	}
	return string(data), nil
}
