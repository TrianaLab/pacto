package contractview

import (
	"errors"
	"io/fs"

	"github.com/trianalab/pacto/v3/pkg/lock"
)

// lockFromFS reads pacto.lock from a bundle FS. Since pacto.lock now ships inside
// the bundle (no longer default-ignored), it is present in every source's bundle
// FS — local, OCI and cache — so this single reader covers them all. Returns
// (nil, nil) when the FS is nil or the lockfile is absent (a lockfile is
// optional); a parse/schema error is returned so callers surface it.
func lockFromFS(fsys fs.FS) (*lock.Lock, error) {
	if fsys == nil {
		return nil, nil
	}
	data, err := fs.ReadFile(fsys, lock.FileName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return lock.Parse(data)
}

// ApplyLock maps a parsed lock onto a ServiceDetails: it sets svc.Lock and pins
// LockedDigest/LockedVersion on each matching dependency (by name), configuration
// reference (kind=config, by name) and policy reference (kind=policy, by name).
// A nil lock leaves svc untouched (backward compatible).
//
// It is exported so out-of-package sources (e.g. the WASM demo's EmbedSource,
// which reads an embedded pacto.lock rather than one on disk) can surface lock
// pins through the same code path the on-disk LocalSource uses.
func ApplyLock(svc *ServiceDetails, l *lock.Lock) {
	if l == nil {
		return
	}
	// RootInfo carries name+version only; there is no root digest in the lock
	// model, so RootDigest stays empty (reserved for forward compatibility).
	info := &LockInfo{Present: true}
	for _, e := range l.Dependencies {
		info.Dependencies = append(info.Dependencies, LockDepInfo{
			Name: e.Name, Source: e.Source, Ref: e.Ref, Path: e.Path,
			Constraint: e.Constraint, Version: e.Version, Digest: e.Digest, ContentHash: e.ContentHash,
		})
	}
	for _, r := range l.References {
		info.References = append(info.References, LockRefInfo{
			Kind: r.Kind, Name: r.Name, Source: r.Source, Ref: r.Ref, Path: r.Path,
			Version: r.Version, Digest: r.Digest, ContentHash: r.ContentHash,
		})
	}
	svc.Lock = info

	for i := range svc.Dependencies {
		if e, ok := l.Dependency(svc.Dependencies[i].Name); ok {
			svc.Dependencies[i].LockedDigest = e.Digest
			svc.Dependencies[i].LockedVersion = e.Version
		}
	}
	// Only the ROOT contract's own entries pin its declared references. The lock
	// holds the transitive closure, so a same-named entry declared by some bundle
	// deeper in it is an authoritative resolution of a DIFFERENT reference; pinning
	// it here would show this service a digest it never resolved. A lock with no
	// occurrence identity (pre-v2) pins nothing rather than guessing.
	for i := range svc.Configurations {
		if r, ok := l.RootReference(lockKindConfig, svc.Configurations[i].Name); ok {
			svc.Configurations[i].LockedDigest = r.Digest
			svc.Configurations[i].LockedVersion = r.Version
		}
	}
	for i := range svc.Policies {
		if r, ok := l.RootReference(lockKindPolicy, svc.Policies[i].Name); ok {
			svc.Policies[i].LockedDigest = r.Digest
			svc.Policies[i].LockedVersion = r.Version
		}
	}
}

// Reference kinds in pacto.lock.
const (
	lockKindConfig = "config"
	lockKindPolicy = "policy"
)
