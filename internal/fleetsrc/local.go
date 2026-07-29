// Package fleetsrc provides concrete, cluster-free [fleet.Source] implementations
// that read real data from the filesystem: a local bundle-directory source
// (contract revisions) and an evidence-file source (operational targets). They
// live in internal because they wire together several public packages; the
// framework-independent fleet layer itself defines the Source seam.
//
// The evidence source is deliberately generic: it ingests pre-produced
// target/evaluation state from a file rather than querying a live environment.
// That is the substrate the future external-EvidenceSet ingestion path builds
// on — a remote environment produces a signed, versioned evidence document, a
// platform ingests it, and the fleet snapshot exposes the result with explicit
// freshness and completeness. A live Kubernetes fleet source (which needs
// client-go) belongs in a k8s-allowed package, not here.
package fleetsrc

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// maxScanDepth bounds how deep LocalSource descends below its root.
const maxScanDepth = 8

// LocalSource discovers pacto.yaml bundles under a root directory and emits one
// contract revision per bundle. It is the offline definition source for the
// fleet and never touches the network.
type LocalSource struct {
	id   string
	root string
}

// NewLocalSource returns a local bundle source rooted at root. id is the source
// identity used as provenance; it defaults to "local" when empty.
func NewLocalSource(id, root string) *LocalSource {
	if id == "" {
		id = "local"
	}
	return &LocalSource{id: id, root: root}
}

// ID implements [fleet.Source].
func (s *LocalSource) ID() string { return s.id }

// Kind implements [fleet.Source].
func (s *LocalSource) Kind() string { return "local" }

// Collect walks the root for pacto.yaml files and projects each into a raw
// revision. A missing/unreadable root is a source error (surfaced as an
// unavailable source), not an empty result. Unparseable individual bundles are
// skipped rather than failing the whole source.
func (s *LocalSource) Collect(ctx context.Context) (*fleet.Collection, error) {
	col := &fleet.Collection{}
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(s.root, p, d) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "pacto.yaml" {
			return nil
		}
		if rev, ok := loadRevision(filepath.Dir(p)); ok {
			col.Revisions = append(col.Revisions, rev)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return col, nil
}

// skipDir reports whether a directory should not be descended into: hidden dirs
// (except the root itself), well-known vendor/dependency dirs, and anything past
// the depth cap.
func skipDir(root, p string, d fs.DirEntry) bool {
	if p == root {
		return false
	}
	name := d.Name()
	if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return true
	}
	return strings.Count(rel, string(filepath.Separator)) >= maxScanDepth
}

// loadRevision parses the bundle in dir into a raw revision. The FS is rooted at
// the bundle directory so referenced interface/config/lock files resolve. A
// content hash over the bundle provides an immutable local revision identity.
func loadRevision(dir string) (fleet.RawRevision, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		return fleet.RawRevision{}, false
	}
	c, err := contract.Parse(bytes.NewReader(data))
	if err != nil {
		return fleet.RawRevision{}, false
	}
	fsys := os.DirFS(dir)
	b := &contract.Bundle{Contract: c, RawYAML: data, FS: fsys}
	rev := fleet.RawRevision{Bundle: b, RequestedRef: "file://" + dir}
	if h, err := lock.HashFS(fsys); err == nil {
		rev.Digest = h
	}
	if info, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
		t := info.ModTime()
		rev.FetchedAt = &t
	}
	return rev, true
}
