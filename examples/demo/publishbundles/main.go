// Command publishbundles packs + pushes the demo contract bundles to an OCI
// registry as pacto bundles, WITHOUT running contract validation.
//
// Why no validation: the demo set is curated to showcase compliance states,
// so it deliberately includes contracts that a strict `pacto validate`/`push`
// rejects (policy violations, a gRPC .proto interface). `pacto push` gates on
// full validation and so cannot republish them. This tool uses the SAME OCI
// bundle format (pkg/oci Client.Push -> the tar.gz layer + labels pacto pulls),
// so the pushed artifacts resolve identically to a real push — only the
// pre-push validation gate is skipped. It is a demo/staging tool; it is NEVER
// the production publish path.
//
// Each bundle is pushed to <coordinate>/<service>:<contract-version>, so a
// tagless ref (oci://<coordinate>/<service>) resolves to it by best-semver.
//
// Usage:  go run ./publishbundles <bundles-dir> <coordinate>
//
//	e.g.  go run ./publishbundles /tmp/work/bundles localhost:5001/pacto-demo
//
// --seed-cache <bundle-dir> <ref> writes one bundle into Pacto's disk cache
// ($XDG_CACHE_HOME/pacto/oci) under ref, through the cache's own writer and
// with no registry, so a hermetic run resolves ref offline (the tour's beat 13
// and the acceptance demo arc).
//
// Registry credentials, when needed, come from PACTO_REGISTRY_USERNAME /
// PACTO_REGISTRY_PASSWORD / PACTO_REGISTRY_TOKEN (same env as the pacto CLI).
// Prints "<service>:<version> <digest>" per pushed bundle to stdout, and
// "<service>:<version> <digest> unchanged" for a tag that already holds the
// bundle (typed, or untyped as a Pacto before 4.7 pushed it), which is never
// re-pushed.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/ignore"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "publishbundles:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	// --print-digests: compute each bundle's deterministic OCI digests locally and
	// print "<svc>:<version> <typed> <legacy>" WITHOUT pushing. The demo publisher uses this
	// for a byte-exact absent/identical/conflict gate before touching the registry.
	if len(args) == 3 && args[0] == "--seed-cache" {
		return seedCache(args[1], args[2])
	}
	printOnly := false
	if len(args) > 0 && args[0] == "--print-digests" {
		printOnly = true
		args = args[1:]
	}
	if len(args) != 2 {
		return fmt.Errorf("usage: publishbundles [--print-digests] <bundles-dir> <coordinate>")
	}
	bundlesDir, coord := args[0], args[1]

	dirs, err := bundleDirs(bundlesDir)
	if err != nil {
		return err
	}

	var client *oci.Client
	if !printOnly {
		client = oci.NewClient(oci.NewKeychain(oci.CredentialOptions{
			Username: os.Getenv("PACTO_REGISTRY_USERNAME"),
			Password: os.Getenv("PACTO_REGISTRY_PASSWORD"),
			Token:    os.Getenv("PACTO_REGISTRY_TOKEN"),
		}))
	}

	ctx := context.Background()
	for _, dir := range dirs {
		b, err := loadBundle(dir)
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		name := b.Contract.Service.Name
		version := b.Contract.Service.Version
		// A contract with no declared service.version is not publishable (pacto
		// publish refuses it too). The skip goes to stderr: publish-demo-bundles.sh
		// reads stdout as "<tag> <digest>" lines.
		if version == "" {
			_, _ = fmt.Fprintf(stderr, "skipped: %s (no declared version, not publishable)\n", filepath.Base(dir))
			continue
		}
		ds, err := oci.BundleDigests(b)
		if err != nil {
			return fmt.Errorf("digest %s: %w", dir, err)
		}
		if printOnly {
			_, _ = fmt.Fprintf(stdout, "%s:%s %s %s\n", name, version, ds.Typed, ds.Legacy)
			continue
		}
		// Client.Push takes a bare registry ref (no oci:// scheme), matching what
		// the CLI passes after graph.ParseDependencyRef strips the scheme.
		ref := fmt.Sprintf("%s/%s:%s", coord, name, version)
		digest, err := pushOnce(ctx, client, ref, b, ds)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "%s:%s %s\n", name, version, digest)
	}
	return nil
}

// pushOnce pushes b to ref unless ref already holds either digest of b, so a
// tag an older Pacto wrote untyped never moves. An unchanged tag prints its
// remote digest followed by "unchanged": the live-resolution proof still finds
// it. A resolve error other than not-found fails: never push on no answer.
func pushOnce(ctx context.Context, client oci.BundleStore, ref string, b *contract.Bundle, ds oci.Digests) (string, error) {
	remote, err := client.Resolve(ctx, ref)
	var nf *oci.ArtifactNotFoundError
	switch {
	case err == nil && ds.Same(remote):
		return remote + " unchanged", nil
	case err != nil && !errors.As(err, &nf):
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	digest, err := client.Push(ctx, ref, b)
	if err != nil {
		return "", fmt.Errorf("push %s: %w", ref, err)
	}
	return digest, nil
}

// bundleDirs returns every directory under root that holds a pacto.yaml, sorted.
func bundleDirs(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "pacto.yaml" {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// loadBundle mirrors internal/app.loadLocalBundle using public APIs: parse the
// contract and build the ignore-filtered bundle FS (default ignore set + any
// .pactoignore), so the pushed layer matches a real `pacto push`.
func loadBundle(dir string) (*contract.Bundle, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		return nil, err
	}
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	dirFS := os.DirFS(dir)
	matcher, err := ignore.Load(dirFS)
	if err != nil {
		return nil, err
	}
	return &contract.Bundle{Contract: c, RawYAML: raw, FS: ignore.FS(dirFS, matcher)}, nil
}

// seedCache pulls ref through a [oci.CachedStore] whose registry is the local
// bundle at dir, so the cache commits the entry exactly as a real pull would,
// recording the digest the bundle is published under.
func seedCache(dir, ref string) error {
	b, err := loadBundle(dir)
	if err != nil {
		return err
	}
	ds, err := oci.BundleDigests(b)
	if err != nil {
		return err
	}
	// ponytail: seeds the legacy digest, the one every published demo tag and
	// committed lock holds. A demo version first published typed needs the
	// typed digest here; pick per bundle when the first one ships.
	store := oci.NewCachedStore(localStore{bundle: b, digest: ds.Legacy})
	if store.CacheDir() == "" {
		return fmt.Errorf("no cache directory: set XDG_CACHE_HOME")
	}
	_, err = store.Pull(context.Background(), ref)
	return err
}

// localStore is a registry that holds one bundle under every reference.
type localStore struct {
	bundle *contract.Bundle
	digest string
}

func (s localStore) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", fmt.Errorf("read-only")
}
func (s localStore) Pull(context.Context, string) (*contract.Bundle, error) { return s.bundle, nil }
func (s localStore) Resolve(context.Context, string) (string, error)        { return s.digest, nil }
func (s localStore) ListTags(context.Context, string) ([]string, error) {
	return []string{s.bundle.Contract.Service.Version}, nil
}
