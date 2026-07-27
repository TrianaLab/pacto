// Command publishbundles packs + pushes the demo contract bundles to an OCI
// registry as pacto bundles, WITHOUT running contract validation.
//
// Why no validation: the demo set is curated to showcase compliance states,
// so it deliberately includes contracts that a strict `pacto validate`/`push`
// rejects (policy violations, a gRPC .proto interface). `pacto push` gates on
// full validation and so cannot republish them. This tool uses the SAME OCI
// bundle format (pkg/oci Client.Push -> the tar.gz layer + labels pacto pulls),
// so the pushed artifacts resolve identically to a real push — only the
// pre-push validation gate is skipped. It is a demo/staging tool, a sibling of
// genlocks; it is NEVER the production publish path.
//
// Each bundle is pushed to <coordinate>/<service>:<contract-version>, so a
// tagless ref (oci://<coordinate>/<service>) resolves to it by best-semver.
//
// Usage:  go run ./publishbundles <bundles-dir> <coordinate>
//
//	e.g.  go run ./publishbundles /tmp/work/bundles localhost:5001/pacto-demo
//
// Registry credentials, when needed, come from PACTO_REGISTRY_USERNAME /
// PACTO_REGISTRY_PASSWORD / PACTO_REGISTRY_TOKEN (same env as the pacto CLI).
// Prints "<service>:<version> <digest>" per pushed bundle to stdout.
package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/ignore"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "publishbundles:", err)
		os.Exit(1)
	}
}

func run() error {
	// --print-digests: compute each bundle's deterministic OCI digest locally and
	// print "<svc>:<version> <digest>" WITHOUT pushing. The demo publisher uses this
	// for a byte-exact absent/identical/conflict gate before touching the registry.
	args := os.Args[1:]
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
		if printOnly {
			d, err := bundleDigest(b)
			if err != nil {
				return fmt.Errorf("digest %s: %w", dir, err)
			}
			fmt.Printf("%s:%s %s\n", name, version, d)
			continue
		}
		// Client.Push takes a bare registry ref (no oci:// scheme), matching what
		// the CLI passes after graph.ParseDependencyRef strips the scheme.
		ref := fmt.Sprintf("%s/%s:%s", coord, name, version)
		digest, err := client.Push(ctx, ref, b)
		if err != nil {
			return fmt.Errorf("push %s: %w", ref, err)
		}
		fmt.Printf("%s:%s %s\n", name, version, digest)
	}
	return nil
}

// bundleDigest computes the deterministic OCI digest a bundle would be published
// under, WITHOUT pushing: build the image, consume its layer stream to finalize the
// digest (streamed layers know it only after the stream is read), then read it.
// Packing is content-deterministic, so this equals the digest Push produces.
func bundleDigest(b *contract.Bundle) (string, error) {
	img, err := oci.BundleImage(b)
	if err != nil {
		return "", err
	}
	layers, err := img.Layers()
	if err != nil {
		return "", err
	}
	for _, l := range layers {
		rc, err := l.Compressed()
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			_ = rc.Close()
			return "", err
		}
		_ = rc.Close()
	}
	d, err := img.Digest()
	if err != nil {
		return "", err
	}
	return d.String(), nil
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
