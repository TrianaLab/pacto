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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/v4/internal/discover"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/lock"
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
	unreadable := unreadableDirs{source: s.id, root: s.root}
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return unreadable.note(p, walkErr)
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
		rev, loadErr := loadRevision(filepath.Dir(p))
		if errors.Is(loadErr, errIncomplete) {
			// Inference only adds: the declared revision stays, and its
			// inferred dependencies are unknown, which makes the answer partial.
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationInferredRecordUnreadable, Source: s.id,
				Message: "bundle at " + relPathSafe(s.root, p) + ": " + loadErr.Error() + "; its inferred dependencies are unknown, not absent",
			})
			loadErr = nil
		}
		if loadErr != nil {
			// A broken contract must not vanish as if it never existed: keep
			// scanning, keep the good bundles, and surface the failure so the
			// source is reported partial. The relative path is safe to show.
			col.Limitations = append(col.Limitations, fleet.Limitation{
				Code: fleet.LimitationSourceRecordInvalid, Source: s.id,
				Message: "bundle at " + relPathSafe(s.root, p) + " could not be loaded: " + loadErr.Error(),
			})
			return nil
		}
		col.Revisions = append(col.Revisions, rev)
		return nil
	})
	if err != nil {
		return nil, err
	}
	col.Limitations = append(col.Limitations, unreadable.limitations()...)
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
// It returns an error (not a silent skip) when the bundle cannot be loaded, so
// the caller can surface a broken contract instead of hiding it.
//
// The FS is ignore-filtered, which is what makes that identity mean anything.
// A content digest is a claim that two bundles are the same bundle, and every
// other place Pacto makes it -- the lockfile, the catalog, a pushed artifact --
// hashes the packaged file set. Hashing the raw directory instead would say a
// contract changed because a .DS_Store appeared beside it, and would hash a
// whole .git tree for a bundle that lives at a repository root. Two views of
// one bundle would then be two revisions of one service at one version, which
// is precisely the shape of a content conflict.
func loadRevision(dir string) (fleet.RawRevision, error) {
	data, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		return fleet.RawRevision{}, fmt.Errorf("read pacto.yaml: %w", err)
	}
	c, doc, err := contract.ParseDocument(data)
	if err != nil {
		return fleet.RawRevision{}, fmt.Errorf("parse pacto.yaml: %w", err)
	}
	dirFS := os.DirFS(dir)
	matcher, err := ignore.Load(dirFS)
	if err != nil {
		return fleet.RawRevision{}, fmt.Errorf("read %s: %w", ignore.IgnoreFileName, err)
	}
	fsys := ignore.FS(dirFS, matcher)
	b := &contract.Bundle{Contract: c, RawYAML: data, FS: fsys}
	name := dir
	if abs, err := filepath.Abs(dir); err == nil {
		name = abs // a root of "." or ".." has no name of its own
	}
	b.Complete(filepath.Base(name))
	if c.Service.Name == "" {
		// Nothing implied a name (a folder like "/" sanitizes to nothing): a
		// revision keyed by "" would read as declared, so it is a record gap.
		return fleet.RawRevision{}, fmt.Errorf("no service.name declared and none could be inferred")
	}
	rev := fleet.RawRevision{Bundle: b, RequestedRef: "file://" + dir, Document: doc}
	// Without any record copy the folder holds, as every local hash reads, so
	// the folder's identity never moves with the record.
	if h, err := lock.HashFS(contract.WithoutInferredRecord(fsys)); err == nil {
		rev.Digest = h
	}
	if info, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
		t := info.ModTime()
		rev.FetchedAt = &t
	}
	// No commit: a fleet read of many folders resolves no git.
	if err := discover.CompleteBundle(b, dir, contract.InferredInputs{}); err != nil {
		return rev, fmt.Errorf("%w: %w", errIncomplete, err)
	}
	return rev, nil
}

// errIncomplete marks a revision loadRevision read but could not complete: it
// is kept as declared, with no inferred record.
var errIncomplete = errors.New("could not be completed")
