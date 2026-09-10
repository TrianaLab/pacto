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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
	"github.com/trianalab/pacto/v3/pkg/ignore"
	"github.com/trianalab/pacto/v3/pkg/lock"
)

// maxScanDepth bounds how deep LocalSource descends below its root.
const maxScanDepth = 8

// maxUnreadableNotes bounds how many refused directories are reported one by
// one before the rest are summarised as a count.
const maxUnreadableNotes = 10

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
	unreadable := 0
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			unreadable++
			return s.noteUnreadable(col, p, unreadable, walkErr)
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
	if unreadable > maxUnreadableNotes {
		// One line instead of eighty. A home directory on macOS refuses around a
		// hundred TCC-guarded paths, and a limitation per refusal would bury the
		// gaps a reader can act on under privacy directories they cannot.
		col.Limitations = append(col.Limitations, fleet.Limitation{
			Code: fleet.LimitationSourcePartial, Source: s.id,
			Message: fmt.Sprintf("%d directories could not be read in total; only the first %d are listed", unreadable, maxUnreadableNotes),
		})
	}
	return col, nil
}

// noteUnreadable records a directory the walk could not read and keeps going.
// One refused directory is not a reason to throw away every bundle beside it:
// a scan rooted at a macOS home directory reaches TCC-guarded paths like
// ~/Library/Accounts within milliseconds, and failing there reported the whole
// source unavailable -- zero services -- while hundreds of readable bundles sat
// further down. Report the gap and carry on, exactly as an unparseable bundle
// does below.
//
// The root is the one exception. Nothing was read at all, so there is no
// partial answer to report and an unavailable source is the honest result.
func (s *LocalSource) noteUnreadable(col *fleet.Collection, p string, n int, err error) error {
	if p == s.root {
		return err
	}
	if n > maxUnreadableNotes {
		return fs.SkipDir
	}
	// The reason, not the wrapper: a *fs.PathError stringifies to "open
	// <absolute path>: permission denied", which would put the caller's home
	// directory back into a message the relative path was chosen to keep it out
	// of. Everything else keeps its whole text -- there is no path in it to strip.
	reason := err.Error()
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		reason = pathErr.Err.Error()
	}
	col.Limitations = append(col.Limitations, fleet.Limitation{
		Code: fleet.LimitationSourcePartial, Source: s.id,
		Message: "could not read " + relPathSafe(s.root, p) + ": " + reason,
	})
	return fs.SkipDir
}

// relPathSafe returns a scanned path relative to the scan root for display,
// falling back to its last element when it cannot be made relative. Only the
// relative form is shown: a scan root is usually an absolute path off the
// caller's machine, and the message is the same message an agent reads.
func relPathSafe(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return filepath.Base(p)
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
	c, err := contract.Parse(bytes.NewReader(data))
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
	rev := fleet.RawRevision{Bundle: b, RequestedRef: "file://" + dir}
	if h, err := lock.HashFS(fsys); err == nil {
		rev.Digest = h
	}
	if info, err := os.Stat(filepath.Join(dir, "pacto.yaml")); err == nil {
		t := info.ModTime()
		rev.FetchedAt = &t
	}
	return rev, nil
}
