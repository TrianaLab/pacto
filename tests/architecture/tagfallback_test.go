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
	"pkg/deliveryrecord/deliveryrecord.go", // PutRecord: the delivery record streams repository
	"pkg/publish/registry.go",              // PutSigTag: the publisher's own artifact and bundle repositories (pacto-sig tags, CLI only)
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
