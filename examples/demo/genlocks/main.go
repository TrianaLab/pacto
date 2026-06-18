// Command genlocks generates committed pacto.lock + .pactoignore files for the
// dependency-bearing demo bundles, OFFLINE and DETERMINISTICALLY.
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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/pkg/contract"
	"github.com/trianalab/pacto/pkg/graph"
	"github.com/trianalab/pacto/pkg/ignore"
	"github.com/trianalab/pacto/pkg/lock"
	"github.com/trianalab/pacto/pkg/semver"
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
			if err := writeLockFiles(sv.dir, l); err != nil {
				return err
			}
			written = append(written, sv.dir)
		}
	}

	sort.Strings(written)
	for _, d := range written {
		fmt.Println("wrote", filepath.Join(d, lock.FileName), "+", filepath.Join(d, ignore.IgnoreFileName))
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

// hashBundle returns the deterministic content hash of the bundle at dir, over the
// DEFAULT ignore set only — it deliberately does NOT honor the bundle's own
// .pactoignore. The default set excludes pacto.lock and .pactoignore, so the hash
// is over the pristine bundle content and is unaffected by the lock files this tool
// writes. Honoring a bundle's .pactoignore (which re-includes pacto.lock) would
// fold the lock back into its own hash and break regeneration determinism.
func hashBundle(dir string) (string, error) {
	dirFS := os.DirFS(dir)
	return lock.HashFS(ignore.FS(dirFS, ignore.New(nil)))
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
// config/policy reference closure for sv, pinning each to the deterministic
// content hash of the target bundle (resolved by service name within the demo).
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
		for _, d := range referenceDecls(c) {
			if seen[d.ref] {
				continue
			}
			seen[d.ref] = true
			target, err := latest(idx, serviceName(d.ref))
			if err != nil {
				return fmt.Errorf("%s reference %q: %w", d.kind, d.name, err)
			}
			out = append(out, lock.Reference{
				Kind:    d.kind,
				Name:    d.name,
				Source:  "oci",
				Ref:     d.ref,
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

type refDecl struct{ kind, name, ref string }

// referenceDecls returns a contract's declared config/policy references (policies
// first, then configurations), skipping inline schemas. Mirrors the real builder.
func referenceDecls(c *contract.Contract) []refDecl {
	var out []refDecl
	for _, p := range c.Policies {
		if p.Ref != "" {
			out = append(out, refDecl{kind: "policy", name: p.Name, ref: p.Ref})
		}
	}
	for _, cfg := range c.Configurations {
		if cfg.Ref != "" {
			out = append(out, refDecl{kind: "config", name: cfg.Name, ref: cfg.Ref})
		}
	}
	return out
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

// writeLockFiles writes pacto.lock (deterministically marshaled) and a
// .pactoignore that re-includes it (!pacto.lock) into dir, so the committed lock
// travels with the bundle when it is packed/embedded.
func writeLockFiles(dir string, l *lock.Lock) error {
	data, err := l.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, lock.FileName), data, 0o644); err != nil {
		return err
	}
	return ensurePactoignore(filepath.Join(dir, ignore.IgnoreFileName))
}

// ignoreHeader documents why the demo re-includes pacto.lock in its bundles.
const ignoreHeader = `# Re-include the committed lockfile in packed/embedded bundles.
# pacto.lock is default-ignored; the demo ships it so the dashboard can show pins.
`

// ensurePactoignore writes a .pactoignore whose effective rules un-ignore
// pacto.lock. If a .pactoignore already exists without an "!pacto.lock" rule, the
// rule is appended; otherwise the canonical file is written. The result is stable
// across runs.
func ensurePactoignore(path string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if hasUnignoreRule(string(existing)) {
		return nil
	}
	if len(existing) == 0 {
		return os.WriteFile(path, []byte(ignoreHeader+"!pacto.lock\n"), 0o644)
	}
	content := string(existing)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "!pacto.lock\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

// hasUnignoreRule reports whether content already re-includes pacto.lock.
func hasUnignoreRule(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "!pacto.lock" {
			return true
		}
	}
	return false
}
