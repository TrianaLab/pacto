// Command genlocks generates committed pacto.lock files for the dependency-bearing
// demo bundles, OFFLINE and DETERMINISTICALLY.
//
// Why offline: the demo's refs point at oci://ghcr.io/trianalab/pacto-demo/<svc>,
// which does not resolve without a live registry, and an ephemeral throwaway host
// would pollute the committed refs. Every referenced service already exists in the
// embedded demo set, so the closure is resolved BY SERVICE NAME within ./bundles,
// mirroring how EmbedSource derives the graph.
//
// Pin method: the lockfile's `digest` carries the deterministic content hash of
// the target bundle (lock.HashFS over the ignore-filtered bundle FS), NOT a live
// OCI manifest digest. Computing a real registry digest offline is impractical
// (the go-containerregistry image build is unexported and embeds file mtimes into
// the tar, which would leak into the digest and break determinism). The content
// hash excludes the default-ignored pacto.lock/.pactoignore, so re-running this
// generator produces byte-identical locks (the lock it writes never feeds its own
// hash). The original declared oci:// ref is preserved verbatim in `ref`.
//
// Run from examples/demo:  go run ./genlocks   (or: make demo-locks)
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v2/pkg/contract"
	"github.com/trianalab/pacto/v2/pkg/graph"
	"github.com/trianalab/pacto/v2/pkg/ignore"
	"github.com/trianalab/pacto/v2/pkg/lock"
	"github.com/trianalab/pacto/v2/pkg/semver"
)

const (
	bundlesDir   = "bundles"
	contractFile = "pacto.yaml"
	// pinPactoVersion is a fixed, content-free version stamp for the demo locks.
	// A real `pacto lock` stamps the building CLI version; here it must be stable
	// across runs (no build metadata leaking in), so it is pinned to "0.0.0".
	pinPactoVersion = "0.0.0"
)

// svcVersion is one indexed bundle: its declaring contract plus the deterministic
// content hash of its ignore-filtered FS and its on-disk directory.
type svcVersion struct {
	contract *contract.Contract
	dir      string
	hash     string // "sha256:..." over the ignore-filtered bundle FS
}

// index maps service name -> version -> bundle.
type index map[string]map[string]*svcVersion

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genlocks:", err)
		os.Exit(1)
	}
}

func run() error {
	idx, err := buildIndex(bundlesDir)
	if err != nil {
		return err
	}

	// Generate a lock for every bundle that bears dependencies and/or config/policy
	// references. Leaf bundles (no deps, no refs) get nothing.
	var written []string
	for name, versions := range idx {
		for ver, sv := range versions {
			if !depBearing(sv.contract) {
				continue
			}
			l, err := buildLock(idx, sv)
			if err != nil {
				return fmt.Errorf("%s@%s: %w", name, ver, err)
			}
			if err := writeLock(sv.dir, l); err != nil {
				return err
			}
			written = append(written, sv.dir)
		}
	}

	sort.Strings(written)
	for _, d := range written {
		fmt.Println("wrote", filepath.Join(d, lock.FileName))
	}
	fmt.Printf("genlocks: wrote %d lock(s)\n", len(written))
	return nil
}

// buildIndex walks bundlesDir for pacto.yaml files and indexes each by service
// name and version, computing the deterministic content hash of every bundle.
func buildIndex(root string) (index, error) {
	idx := index{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != contractFile {
			return nil
		}
		dir := filepath.Dir(p)
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		c, err := contract.Parse(strings.NewReader(string(raw)))
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		h, err := hashBundle(dir)
		if err != nil {
			return fmt.Errorf("hash %s: %w", dir, err)
		}
		name, ver := c.Service.Name, c.Service.Version
		if idx[name] == nil {
			idx[name] = map[string]*svcVersion{}
		}
		idx[name][ver] = &svcVersion{contract: c, dir: dir, hash: h}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// hashBundle returns the deterministic content hash of the bundle at dir over its
// pristine content. pacto.lock now ships by default (it is no longer in the
// DefaultPatterns ignore set), so the hash must EXPLICITLY exclude pacto.lock —
// otherwise the lock this tool writes would fold back into its own hash and break
// regeneration determinism. The bundle's own .pactoignore is deliberately not
// honored (we pass an explicit pattern, not ignore.Load).
func hashBundle(dir string) (string, error) {
	dirFS := os.DirFS(dir)
	return lock.HashFS(ignore.FS(dirFS, ignore.New([]string{lock.FileName})))
}

// depBearing reports whether a contract declares any dependency or any config/
// policy reference (a ref, not an inline schema). Those bundles get a lock.
func depBearing(c *contract.Contract) bool {
	if len(c.Dependencies) > 0 {
		return true
	}
	for _, cfg := range c.Configurations {
		if cfg.Ref != "" {
			return true
		}
	}
	for _, p := range c.Policies {
		if p.Ref != "" {
			return true
		}
	}
	return false
}

// buildLock resolves the transitive dependency closure and the transitive
// config/policy reference closure for sv.
//
// Two demo-specific deviations from the real builder in internal/app:
//
//	(a) Pins are content-derived hashes (lock.HashFS over the bundle FS) suitable
//	    for offline e2e, NOT live OCI manifest digests — the demo never contacts a
//	    registry. The `digest` field carries this content hash by design.
//	(b) The closure walk mirrors internal/app's walkClosure / buildReferenceClosure
//	    (policies first, then configs, deduped by ref/name, cycle-terminating) but
//	    resolves every edge OFFLINE by service name within ./bundles instead of
//	    pulling from a registry.
func buildLock(idx index, sv *svcVersion) (*lock.Lock, error) {
	l := &lock.Lock{
		LockVersion: lock.CurrentLockVersion,
		Pacto:       lock.PactoInfo{Version: pinPactoVersion},
		Root:        lock.RootInfo{Name: sv.contract.Service.Name, Version: sv.contract.Service.Version},
	}

	deps, err := dependencyClosure(idx, sv.contract)
	if err != nil {
		return nil, err
	}
	l.Dependencies = deps

	refs, err := referenceClosure(idx, sv.contract)
	if err != nil {
		return nil, err
	}
	l.References = refs

	return l, nil
}

// dependencyClosure flattens the transitive dependency graph into lock entries,
// deduplicated by service name, recording each edge's DependsOn children. Mirrors
// the real builder's walkClosure, but resolves edges by service name offline.
func dependencyClosure(idx index, root *contract.Contract) ([]lock.Entry, error) {
	seen := map[string]bool{}
	var out []lock.Entry
	var walk func(c *contract.Contract) error
	walk = func(c *contract.Contract) error {
		for _, dep := range c.Dependencies {
			name := serviceName(dep.Ref)
			target, err := latest(idx, name)
			if err != nil {
				return fmt.Errorf("dependency %q: %w", dep.Name, err)
			}
			if seen[dep.Name] {
				continue
			}
			seen[dep.Name] = true
			entry := lock.Entry{
				Name:       dep.Name,
				Source:     "oci",
				Ref:        dep.Ref,
				Constraint: dep.Compatibility,
				Version:    target.contract.Service.Version,
				Digest:     target.hash,
				DependsOn:  childNames(target.contract),
			}
			out = append(out, entry)
			if err := walk(target.contract); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return out, nil
}

// childNames returns the dependency names a contract declares, for the DependsOn
// edge list. Order matches Marshal's sort, so it is stable.
func childNames(c *contract.Contract) []string {
	var names []string
	for _, dep := range c.Dependencies {
		names = append(names, dep.Name)
	}
	return names
}

// referenceClosure pins the transitive config/policy reference closure,
// deduplicated by declared ref string (which also terminates cycles), resolving
// each ref to a bundle by service name. Mirrors the real buildReferenceClosure.
func referenceClosure(idx index, root *contract.Contract) ([]lock.Reference, error) {
	seen := map[string]bool{}
	var out []lock.Reference
	var walk func(c *contract.Contract) error
	walk = func(c *contract.Contract) error {
		for _, d := range c.ReferenceRefs() {
			if seen[d.Ref] {
				continue
			}
			seen[d.Ref] = true
			target, err := latest(idx, serviceName(d.Ref))
			if err != nil {
				return fmt.Errorf("%s reference %q: %w", d.Kind, d.Name, err)
			}
			out = append(out, lock.Reference{
				Kind:    d.Kind,
				Name:    d.Name,
				Source:  "oci",
				Ref:     d.Ref,
				Version: target.contract.Service.Version,
				Digest:  target.hash,
			})
			if err := walk(target.contract); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return out, nil
}

// latest returns the highest-semver indexed version of a service.
func latest(idx index, name string) (*svcVersion, error) {
	versions := idx[name]
	if len(versions) == 0 {
		return nil, fmt.Errorf("service %q not found in demo set", name)
	}
	all := make([]string, 0, len(versions))
	for v := range versions {
		all = append(all, v)
	}
	pick := semver.Latest(all)
	if pick == "" {
		sort.Strings(all)
		pick = all[len(all)-1]
	}
	return versions[pick], nil
}

// serviceName extracts the service name from a dependency/reference ref. Strips
// the oci:///file:// scheme, the registry path and any tag/digest — the same
// derivation EmbedSource and the dashboard graph use.
func serviceName(ref string) string {
	loc := graph.ParseDependencyRef(ref).Location
	parts := strings.Split(loc, "/")
	name := parts[len(parts)-1]
	if i := strings.Index(name, "@"); i > 0 {
		name = name[:i]
	}
	if i := strings.Index(name, ":"); i > 0 {
		name = name[:i]
	}
	return name
}

// writeLock writes pacto.lock (deterministically marshaled) into dir. The lock now
// ships inside the bundle by default (no .pactoignore re-include needed), so the
// committed lock travels with the bundle when it is packed or embedded.
func writeLock(dir string, l *lock.Lock) error {
	data, err := l.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, lock.FileName), data, 0o644)
}
