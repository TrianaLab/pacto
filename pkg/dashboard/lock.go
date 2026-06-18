package dashboard

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/trianalab/pacto/pkg/lock"
)

// Drift status values for a locked dependency edge.
const (
	driftLocked   = "locked"   // running digest matches the locked digest
	driftDrift    = "drift"    // running digest differs from the locked digest
	driftUnlocked = "unlocked" // no locked digest for this dependency
	driftUnknown  = "unknown"  // locked, but no runtime digest available to compare
)

// readLock reads pacto.lock from the bundle directory on disk. The lockfile is
// default-ignored, so it is absent from the ignore-filtered Bundle.FS and must be
// read directly. Returns (nil, nil) when the file does not exist (a lockfile is
// optional); a parse/schema error is returned so callers surface it.
func readLock(dir string) (*lock.Lock, error) {
	data, err := os.ReadFile(filepath.Join(dir, lock.FileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return lock.Parse(data)
}

// applyLock maps a parsed lock onto a ServiceDetails: it sets svc.Lock and pins
// LockedDigest/LockedVersion on each matching dependency (by name), configuration
// reference (kind=config, by name) and policy reference (kind=policy, by name).
// A nil lock leaves svc untouched (backward compatible).
func applyLock(svc *ServiceDetails, l *lock.Lock) {
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
			Constraint: r.Constraint, Version: r.Version, Digest: r.Digest, ContentHash: r.ContentHash,
		})
	}
	svc.Lock = info

	for i := range svc.Dependencies {
		if e, ok := l.Dependency(svc.Dependencies[i].Name); ok {
			svc.Dependencies[i].LockedDigest = e.Digest
			svc.Dependencies[i].LockedVersion = e.Version
		}
	}
	for i := range svc.Configurations {
		if r, ok := l.Reference(lockKindConfig, svc.Configurations[i].Name); ok {
			svc.Configurations[i].LockedDigest = r.Digest
			svc.Configurations[i].LockedVersion = r.Version
		}
	}
	for i := range svc.Policies {
		if r, ok := l.Reference(lockKindPolicy, svc.Policies[i].Name); ok {
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

// enrichDrift sets DriftStatus on every dependency edge that carries a locked
// digest, comparing it against the dependency target's runtime digest. The target
// is looked up in the index by the (already-resolved) dependency name. Edges with
// no locked digest get "unlocked"; with a locked digest but no resolvable runtime
// digest, "unknown".
func enrichDrift(index map[string]*ServiceDetails) {
	for _, d := range index {
		if d == nil {
			continue
		}
		for i := range d.Dependencies {
			dep := &d.Dependencies[i]
			if dep.LockedDigest == "" {
				continue // leave default: no lock entry → no drift assertion
			}
			var rt string
			if target := index[dep.Name]; target != nil {
				rt = runtimeDigest(target)
			}
			dep.DriftStatus = driftStatus(dep.LockedDigest, rt)
		}
	}
}

// runtimeDigest extracts the running content digest of a service from its operator
// status: ResolvedRef first (e.g. "repo@sha256:..."), then CurrentRevision. Returns
// "" when neither carries a digest.
func runtimeDigest(d *ServiceDetails) string {
	for _, candidate := range []string{d.ResolvedRef, d.CurrentRevision} {
		if dp := digestPart(candidate); strings.HasPrefix(dp, "sha256:") {
			return dp
		}
	}
	return ""
}

// driftStatus compares a locked digest against a runtime digest. Both are
// normalized to the digest portion (the part after "@", if present) so a bare
// "sha256:..." compares equal to "repo@sha256:...".
func driftStatus(lockedDigest, runtimeDigest string) string {
	if lockedDigest == "" {
		return driftUnlocked
	}
	if runtimeDigest == "" {
		return driftUnknown
	}
	if digestPart(lockedDigest) == digestPart(runtimeDigest) {
		return driftLocked
	}
	return driftDrift
}

// digestPart returns the digest portion of a ref, i.e. the substring after the
// last "@". Refs with no "@" are returned unchanged.
func digestPart(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}
