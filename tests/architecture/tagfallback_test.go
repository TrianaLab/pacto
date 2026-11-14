package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestOnlyRunRecordsFallBackToTags keeps oras-go's referrers-tag fallback
// away from evidence: delivery, refusal and evidence records require the
// native Referrers API, and only run records (unsigned statistics) may share a
// mutable index.
func TestOnlyRunRecordsFallBackToTags(t *testing.T) {
	root := repoRoot(t)
	allowed := []string{"pkg/ocireferrer/repository.go", "pkg/runrecord/registry/registry.go"}
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".claude", "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "AllowTagFallback") {
			rel, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if !slices.Contains(allowed, f) {
			t.Errorf("%s sets AllowTagFallback: only run records may fall back to the referrers tag", f)
		}
	}
	if len(found) != len(allowed) {
		t.Errorf("AllowTagFallback found in %v, want exactly %v", found, allowed)
	}
}
