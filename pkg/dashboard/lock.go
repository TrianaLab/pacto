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

// enrichDetailDriftFromIndex carries the dependency drift (and lock pins) already
// computed for the index onto a freshly fetched ServiceDetails, so the service-
// DETAIL view agrees with the graph and dependents views — which both read from the
// index. The detail path returns a fresh GetService result whose dependencies never
// pass through enrichDrift, so without this they would always render without a drift
// badge. Matching is by the dependency Ref (stable across both: the index only
// rewrites the resolved Name, never the Ref), falling back to the Name for refless
// declarations. Backward compatible: an index entry with empty drift / lock pins
// leaves the detail dependency untouched (no badge), and a service absent from the
// index is a no-op.
func enrichDetailDriftFromIndex(details *ServiceDetails, index map[string]*ServiceDetails) {
	if details == nil {
		return
	}
	indexed := index[details.Name]
	if indexed == nil {
		return
	}
	byRef := make(map[string]*DependencyInfo, len(indexed.Dependencies))
	byName := make(map[string]*DependencyInfo, len(indexed.Dependencies))
	for i := range indexed.Dependencies {
		src := &indexed.Dependencies[i]
		if src.Ref != "" {
			byRef[src.Ref] = src
		}
		byName[src.Name] = src
	}
	for i := range details.Dependencies {
		dep := &details.Dependencies[i]
		src, ok := byRef[dep.Ref]
		if !ok || dep.Ref == "" {
			src, ok = byName[dep.Name]
		}
		if !ok {
			continue
		}
		dep.DriftStatus = src.DriftStatus
		if dep.LockedDigest == "" {
			dep.LockedDigest = src.LockedDigest
		}
		if dep.LockedVersion == "" {
			dep.LockedVersion = src.LockedVersion
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
