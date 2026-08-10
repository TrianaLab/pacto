// Command genlocks generates committed pacto.lock files for the dependency-bearing
// demo bundles, OFFLINE and DETERMINISTICALLY.
//
// Why offline: the demo's refs point at oci://ghcr.io/trianalab/pacto/<svc>,
// which does not resolve without a live registry, and an ephemeral throwaway host
// would pollute the committed refs. Every referenced service already exists in the
// embedded demo set, so the closure is resolved BY SERVICE NAME within ./bundles,
// mirroring how EmbedSource derives the graph.
//
// Pin method: the lockfile's `digest` carries the demo registry's content address
// for the target bundle, NOT a live OCI manifest digest. Computing a real registry
// digest offline is impractical (the go-containerregistry image build is unexported
// and embeds file mtimes into the tar, which would leak into the digest and break
// determinism). See contractDigest for why it must be that exact identifier. The
// original declared oci:// ref is preserved verbatim in `ref`.
//
// Run from examples/demo:  go run ./genlocks   (or: make demo-locks)
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/graph"
	"github.com/trianalab/pacto/v3/pkg/lock"
	"github.com/trianalab/pacto/v3/pkg/semver"
)

const (
	bundlesDir   = "bundles"
	partnersDir  = "partners"
	contractFile = "pacto.yaml"
	// pinPactoVersion is a fixed, content-free version stamp for the demo locks.
	// A real `pacto lock` stamps the building CLI version; here it must be stable
	// across runs (no build metadata leaking in), so it is pinned to "0.0.0".
	pinPactoVersion = "0.0.0"
)

// svcVersion is one indexed bundle: its declaring contract, its on-disk directory
// and the demo registry's content address for it.
type svcVersion struct {
	contract *contract.Contract
	dir      string
	hash     string // "sha256:..." — see contractDigest
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
	var written []string
	// One index per bundle tree, and no index ever sees another. The trees are
	// separate DOMAINS in the demo fleet, and a reference resolves inside its own
	// domain — the partners tree publishes its own platform-app-config, and a lock
	// that pinned the core tree's same-named bundle would hand the reader forged
	// evidence for exactly the cross-domain link the demo exists to disprove.
	for _, root := range []string{bundlesDir, partnersDir} {
		idx, err := buildIndex(root)
		if err != nil {
			return err
		}

		// Generate a lock for every bundle that bears dependencies and/or config/policy
		// references. Leaf bundles (no deps, no refs) get nothing.
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
		name, ver := c.Service.Name, c.Service.Version
		if idx[name] == nil {
			idx[name] = map[string]*svcVersion{}
		}
		idx[name][ver] = &svcVersion{contract: c, dir: dir, hash: contractDigest(raw)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// contractDigest returns the demo registry's content address for a bundle:
// sha256 over its raw pacto.yaml. That is the identifier EmbedSource computes and
// the demo fleet publishes as every revision's Digest (and inside its @sha256:
// resolved ref), so it is what the lock has to record.
//
// It has to be that identifier and nothing richer. A lock's `digest` says WHICH
// ARTIFACT the resolution landed on, and the fleet reader correlates it against
// the revisions it holds to learn the destination's real service identity. A hash
// over the whole bundle FS describes the same bundle but addresses nothing the
// demo publishes, so a reference pinned with it correlates to no revision at all
// and the product reports it unresolved — the honest answer to a lock pointing at
// an unknown artifact, and useless as a fixture. Real `pacto lock` writes the OCI
// manifest digest here for the same reason: the registry's address, not a second
// opinion on content.
//
// It also makes regeneration trivially deterministic: pacto.yaml never contains
// the lock this tool writes, so no exclusion dance is needed to keep the lock out
// of its own hash.
func contractDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
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
				// A real `pacto lock` fails closed here. This generator records
				// nothing and carries on, because the demo deliberately ships a
				// reference that resolves to nothing (the partners domain publishes
				// no http policy bundle) and "no pin" is precisely how an
				// unresolvable reference is represented: the reader then reports it
				// unresolved instead of being handed a destination to invent.
				fmt.Printf("unpinned: %s reference %q -> %s (%v)\n", d.Kind, d.Name, d.Ref, err)
				continue
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
