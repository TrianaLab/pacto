package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestOnlyRunRecordsFallBackToTags keeps oras-go's referrers-tag fallback
// away from evidence: delivery, refusal and evidence records require the
// native Referrers API, and only run records (unsigned statistics) may share a
// mutable index. Write-once tags (ocireferrer.PushWriteOnce) are a different
// thing: one write-once, content-addressed tag per record in the streams
// repository, never the shared sha256- index.
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

// writeOnceCallers are the only production files that may call
// ocireferrer.PushWriteOnce, each with the repository it writes. A story that
// adds a caller adds its row here, so no one tags a user's apply or contract
// repository through the primitive unnoticed (#452 decision 11).
var writeOnceCallers = []string{
	// PutRecord and Push: the delivery record streams repository (records,
	// approvals, intents and identity pins) and the approvals repository; the
	// operator's recordstore.go and recorder.go and the CLI write through it.
	"pkg/deliveryrecord/deliveryrecord.go",
	// The publisher's own artifact and bundle repositories (pacto-sig tags,
	// CLI only); sign.go and apply.go (SBOMs) write through writeReferrer.
	"pkg/publish/registry.go",
}

// TestOnlyAllowedCallersWriteOnceTags keeps write-once tags where the
// operator may push. Tests and the primitive itself are exempt.
func TestOnlyAllowedCallersWriteOnceTags(t *testing.T) {
	root := repoRoot(t)
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
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || rel == "pkg/ocireferrer/writeonce.go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "PushWriteOnce(") {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if !slices.Contains(writeOnceCallers, f) {
			t.Errorf("%s calls ocireferrer.PushWriteOnce: add it to writeOnceCallers with the repository it writes, if #452 decision 11 lets the operator push there", f)
		}
	}
	if !slices.Equal(found, writeOnceCallers) {
		t.Errorf("PushWriteOnce callers %v, want exactly %v", found, writeOnceCallers)
	}
}

// tagIndexBuilders are the only production files that may name a referrers
// tag index (a sha256-<hex> tag): oras-go's fallback for run records.
var tagIndexBuilders = []string{"pkg/ocireferrer/repository.go", "pkg/runrecord/registry/registry.go"}

// sha256TagLiterals reports whether a Go source holds a string literal that
// starts with "sha256-", the shared referrers tag index cosign and
// go-containerregistry write on a registry without the Referrers API.
func sha256TagLiterals(name string, src []byte) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		return false, err
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			v, _ := strconv.Unquote(lit.Value)
			found = found || strings.HasPrefix(v, "sha256-")
		}
		return !found
	})
	return found, nil
}

// TestNoPactoReaderBuildsTheReferrersTagIndex keeps the shared sha256-<hex>
// index out of evidence: anyone with push can rewrite it, so no Pacto reader
// builds its tag (publisher signatures use pacto-sig tags, records use
// write-once stream tags). Comments are not literals and stay free to name it.
func TestNoPactoReaderBuildsTheReferrersTagIndex(t *testing.T) {
	for src, want := range map[string]bool{
		`package p; var t = "sha256-" + hex`:                        true,
		"package p; var t = fmt.Sprintf(`sha256-%s`)":               true,
		`package p; var t = "pacto-sig-" + hex`:                     false,
		"package p\n// the sha256-<hex> index\nvar d = \"sha256:\"": false,
	} {
		if got, err := sha256TagLiterals("x.go", []byte(src)); err != nil || got != want {
			t.Errorf("sha256TagLiterals(%q) = %v, %v; want %v", src, got, err, want)
		}
	}
	root := repoRoot(t)
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
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || slices.Contains(tagIndexBuilders, rel) {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
		if err != nil {
			return err
		}
		found, err := sha256TagLiterals(rel, src)
		if err != nil {
			return err
		}
		if found {
			t.Errorf("%s holds a \"sha256-\" literal: Pacto never reads the shared referrers tag index", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
