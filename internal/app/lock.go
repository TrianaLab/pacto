package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/override"
)

// LockOptions configures the lock command.
type LockOptions struct {
	Path        string
	Update      bool
	UpdateNames []string
	Check       bool
	Overrides   override.Overrides
	// OnDepResolved, if non-nil, fires once per unique resolved dependency for
	// progress reporting. Must be goroutine-safe. nil = no-op.
	OnDepResolved func()
}

// LockResult reports the outcome of a lock operation.
type LockResult struct {
	Path         string     `json:"path"`
	Lock         *lock.Lock `json:"lock,omitempty"`
	Written      bool       `json:"written,omitempty"`
	UpToDate     bool       `json:"upToDate,omitempty"`
	Dependencies int        `json:"dependencies"`
	References   int        `json:"references"`
}

// marshalLockFn serializes a lock to bytes; overridable in tests to exercise the
// marshal-error path (yaml.Marshal of a valid lock does not fail in practice).
var marshalLockFn = (*lock.Lock).Marshal

// lockPathFor returns the pacto.lock path beside a local ref. The second return
// value is false for OCI references, which have no on-disk lock.
func lockPathFor(ref string) (string, bool) {
	if isOCIRef(ref) {
		return "", false
	}
	return filepath.Join(ref, lock.FileName), true
}

// Lock creates, updates or checks pacto.lock for a local contract.
func (s *Service) Lock(ctx context.Context, opts LockOptions) (*LockResult, error) {
	ref := defaultPath(opts.Path)
	lockPath, ok := lockPathFor(ref)
	if !ok {
		return nil, errors.New("pacto lock requires a local directory, not an OCI reference")
	}

	bundle, err := s.resolveBundleWithOverrides(ctx, ref, opts.Overrides)
	if err != nil {
		return nil, err
	}

	fresh, err := s.buildLock(ctx, ref, bundle, opts.OnDepResolved)
	if err != nil {
		return nil, err
	}

	existing, existErr := readLockFile(lockPath)

	if opts.Check {
		if existErr != nil {
			return nil, &lock.MissingError{Path: lock.FileName}
		}
		if err := compareLocks(existing, fresh); err != nil {
			return nil, err
		}
		return &LockResult{Path: lockPath, Lock: existing, UpToDate: true,
			Dependencies: len(existing.Dependencies), References: len(existing.References)}, nil
	}

	// Plain `pacto lock`: if an existing, consistent lock is present and no
	// --update was requested, leave it untouched (preserve pins).
	if existErr == nil && !opts.Update {
		if err := compareLocks(existing, fresh); err == nil {
			return &LockResult{Path: lockPath, Lock: existing, UpToDate: true,
				Dependencies: len(existing.Dependencies), References: len(existing.References)}, nil
		}
		// Inconsistent and not --update: keep pins that still satisfy their
		// constraint by copying unchanged entries from the existing lock.
		fresh = mergePreservingPins(existing, fresh, opts.UpdateNames)
	}

	data, err := marshalLockFn(fresh)
	if err != nil {
		return nil, err
	}
	if err := writeFileFn(lockPath, data, 0o644); err != nil {
		return nil, err
	}
	return &LockResult{Path: lockPath, Lock: fresh, Written: true,
		Dependencies: len(fresh.Dependencies), References: len(fresh.References)}, nil
}

// readLockFile reads and parses a pacto.lock file. A non-existent file returns
// an error wrapping fs.ErrNotExist, distinguishable via errors.Is.
func readLockFile(path string) (*lock.Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return lock.Parse(data)
}

// compareLocks returns a typed error if `existing` diverges from `fresh`.
// Dependencies are matched by Name (the closure cannot hold one service at two
// versions — that is a build-time conflict). References are matched by their
// stable identity (OCI Ref / local Path), since transitive references can share
// the same (kind, name) across bundles. A different digest/content-hash yields a
// DriftError/LocalDriftError; a changed membership yields a StaleError.
func compareLocks(existing, fresh *lock.Lock) error {
	if len(existing.Dependencies) != len(fresh.Dependencies) {
		return &lock.StaleError{Detail: "dependency set changed"}
	}
	for _, fe := range fresh.Dependencies {
		ee, ok := existing.Dependency(fe.Name)
		if !ok {
			return &lock.StaleError{Detail: "new dependency " + fe.Name}
		}
		if err := compareEntry(fe.Name, fe.Source, ee.Digest, ee.ContentHash, fe.Digest, fe.ContentHash); err != nil {
			return err
		}
	}

	if len(existing.References) != len(fresh.References) {
		return &lock.StaleError{Detail: "reference set changed"}
	}
	byID := make(map[string]lock.Reference, len(existing.References))
	for _, er := range existing.References {
		byID[referenceID(er)] = er
	}
	for _, fr := range fresh.References {
		er, ok := byID[referenceID(fr)]
		if !ok {
			return &lock.StaleError{Detail: "new reference " + fr.Kind + "/" + fr.Name}
		}
		if err := compareEntry(fr.Kind+"/"+fr.Name, fr.Source, er.Digest, er.ContentHash, fr.Digest, fr.ContentHash); err != nil {
			return err
		}
	}
	return nil
}

// compareEntry compares the pinned identity of a single dependency or reference:
// content hash for local sources, digest otherwise.
func compareEntry(name, source, lockedDigest, lockedHash, freshDigest, freshHash string) error {
	if source == "local" {
		if lockedHash != freshHash {
			return &lock.LocalDriftError{Name: name, Locked: lockedHash, Current: freshHash}
		}
		return nil
	}
	if lockedDigest != freshDigest {
		return &lock.DriftError{Name: name, Locked: lockedDigest, Current: freshDigest}
	}
	return nil
}

// referenceID returns a reference's stable identity: its OCI Ref, or its local
// Path. Two transitive references can share (kind, name); their Ref/Path cannot.
func referenceID(r lock.Reference) string {
	if r.Source == "local" {
		return "local:" + r.Path
	}
	return "oci:" + r.Ref
}

// mergePreservingPins keeps existing dependency pins whose constraint is
// unchanged and which are not named for update, taking everything else from
// fresh. It does not mutate fresh.
func mergePreservingPins(existing, fresh *lock.Lock, updateNames []string) *lock.Lock {
	forced := map[string]bool{}
	for _, n := range updateNames {
		forced[n] = true
	}
	out := *fresh
	out.Dependencies = append([]lock.Entry(nil), fresh.Dependencies...)
	for i, fe := range out.Dependencies {
		if forced[fe.Name] {
			continue
		}
		if ee, ok := existing.Dependency(fe.Name); ok && ee.Constraint == fe.Constraint {
			out.Dependencies[i].Version = ee.Version
			out.Dependencies[i].Digest = ee.Digest
			out.Dependencies[i].ContentHash = ee.ContentHash
		}
	}
	return &out
}

// verifyLockIfPresent enforces a committed lock for any command that resolves the
// closure of a local contract. It is a no-op (nil) for OCI refs or when no lock
// file exists beside a local ref (opt-in, backward compatible). Otherwise it
// re-resolves the closure and hard-fails on the first divergence (go.sum-style).
func (s *Service) verifyLockIfPresent(ctx context.Context, ref string, bundle *contract.Bundle) error {
	lockPath, ok := lockPathFor(ref)
	if !ok {
		return nil
	}
	existing, err := readLockFile(lockPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	// The verify path re-resolves the closure; pass nil so it never fires the
	// progress callback (the user-facing resolve owns the count).
	fresh, err := s.buildLock(ctx, ref, bundle, nil)
	if err != nil {
		return err
	}
	return compareLocks(existing, fresh)
}

// lockCode extracts the LOCK_* code prefix from a pkg/lock error so Validate can
// surface it as a ValidationError code (consistent with PARSE_ERROR handling).
func lockCode(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, ':'); i > 0 {
		return msg[:i]
	}
	return "LOCK_ERROR"
}
