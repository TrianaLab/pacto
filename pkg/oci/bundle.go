// Package oci stores and retrieves Pacto contract bundles in OCI registries. It
// packs a bundle into an OCI artifact and pushes or pulls it through any
// registry (GHCR, ECR, ACR, Docker Hub, Harbor), with authentication, on-disk
// caching, and resolution of untagged or constrained references to a concrete
// semver version. No custom registry is required.
package oci

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/trianalab/pacto/v3/pkg/contract"
)

const (
	LayerMediaType = types.MediaType("application/vnd.pacto.bundle.layer.v1.tar+gzip")
)

// tarEpoch is the fixed modification time stamped on every packed tar entry, so a
// bundle's OCI digest depends only on content — never on filesystem timestamps.
var tarEpoch = time.Unix(0, 0).UTC()

// Function variables for testing.
var (
	mutateConfigFn       = mutate.Config
	mutateAppendLayersFn = mutate.AppendLayers
	tarCloseFn           = func(tw *tar.Writer) error { return tw.Close() }
	gzipCloseFn          = func(gw *gzip.Writer) error { return gw.Close() }
)

// bundleToImage converts a contract.Bundle into an OCI v1.Image.
func bundleToImage(b *contract.Bundle) (v1.Image, error) {
	if b == nil || b.Contract == nil || b.FS == nil {
		return nil, fmt.Errorf("bundle, contract, and filesystem are required")
	}

	// Start with empty image, store metadata as labels.
	img := empty.Image

	img, err := mutateConfigFn(img, v1.Config{
		Labels: map[string]string{
			"io.pacto.name":         b.Contract.Service.Name,
			"io.pacto.version":      b.Contract.Service.Version,
			"io.pacto.pactoVersion": b.Contract.PactoVersion,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to set config: %w", err)
	}

	// Create a tar.gz layer from bundle FS.
	layer := stream.NewLayer(newBundleTarReader(b.FS), stream.WithMediaType(LayerMediaType))

	img, err = mutateAppendLayersFn(img, layer)
	if err != nil {
		return nil, fmt.Errorf("failed to append layer: %w", err)
	}

	img = mutate.MediaType(img, types.OCIManifestSchema1)

	return img, nil
}

// BundleImage exposes the OCI image a bundle publishes (bundleToImage) so a
// publisher can compute the deterministic digest a bundle WOULD be pushed under —
// without pushing — for a byte-exact absent/identical/conflict gate. Packing is
// content-deterministic (walkTar canonicalizes headers), so consuming this image's
// layer stream and reading its Digest() yields exactly the digest Push produces.
func BundleImage(b *contract.Bundle) (v1.Image, error) {
	return bundleToImage(b)
}

// walkTar walks the filesystem and writes each entry as a tar header/data pair.
func walkTar(tw *tar.Writer, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if path == "." {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(path)
		// Canonicalize every header so the packed bundle is byte-deterministic:
		// the tar (hence the OCI layer + manifest digest) reproduces on any host,
		// so a published bundle's identity can be compared by digest rather than a
		// semantic contract diff. Content is the only thing that moves the digest;
		// filesystem mtime/owner/mode/atime/ctime never do.
		header.ModTime = tarEpoch
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		if d.IsDir() {
			header.Mode = 0o755
		} else {
			header.Mode = 0o644
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		f, err := fsys.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		_, err = io.Copy(tw, f)
		return err
	})
}

// newBundleTarReader returns an io.ReadCloser that produces a plain tar stream
// of all files in the given FS. stream.NewLayer handles gzip compression.
func newBundleTarReader(fsys fs.FS) io.ReadCloser {
	pr, pw := io.Pipe()

	go func() {
		tw := tar.NewWriter(pw)
		err := walkTar(tw, fsys)
		_ = tw.Close()
		pw.CloseWithError(err)
	}()

	return pr
}

// imageToBundle extracts a contract.Bundle from an OCI v1.Image.
func imageToBundle(img v1.Image) (*contract.Bundle, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("failed to get layers: %w", err)
	}

	if len(layers) == 0 {
		return nil, fmt.Errorf("image has no layers")
	}
	if len(layers) > 1 {
		return nil, fmt.Errorf("expected 1 layer, got %d", len(layers))
	}

	// Extract the single layer (uncompressed tar).
	rc, err := layers[0].Uncompressed()
	if err != nil {
		return nil, fmt.Errorf("failed to read layer: %w", err)
	}
	defer func() { _ = rc.Close() }()

	fsys, err := extractTar(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to extract layer: %w", err)
	}
	return bundleFromFS(fsys)
}

// bundleFromFS reads and parses pacto.yaml from an extracted bundle filesystem
// into a Bundle. Shared by imageToBundle and the on-disk cache loader.
func bundleFromFS(fsys fs.FS) (*contract.Bundle, error) {
	rawYAML, err := fs.ReadFile(fsys, "pacto.yaml")
	if err != nil {
		return nil, fmt.Errorf("bundle missing pacto.yaml: %w", err)
	}
	c, err := contract.Parse(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract from bundle: %w", err)
	}
	return &contract.Bundle{Contract: c, RawYAML: rawYAML, FS: fsys}, nil
}

const (
	maxFileSize  = 10 << 20 // 10 MB per file
	maxTotalSize = 50 << 20 // 50 MB total
	maxEntries   = 10000    // max number of tar entries (bounds many-tiny-files bombs)
)

// hasDotDotComponent reports whether any path component is "..", i.e. a real
// traversal. Unlike a substring check it does not reject legitimate names that
// merely contain ".." (e.g. "my..file.yaml").
func hasDotDotComponent(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// extractTar reads a tar stream and returns an in-memory FS.
func extractTar(r io.Reader) (fs.FS, error) {
	memFS := fstest.MapFS{}
	tr := tar.NewReader(r)
	var totalSize int64
	var entries int

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}

		entries++
		if entries > maxEntries {
			return nil, fmt.Errorf("tar has too many entries (>%d)", maxEntries)
		}

		name := filepath.ToSlash(strings.TrimPrefix(header.Name, "./"))
		if name == "" || name == "." {
			continue
		}
		if hasDotDotComponent(name) {
			return nil, fmt.Errorf("invalid path in tar: %s", header.Name)
		}

		if header.Typeflag == tar.TypeDir {
			memFS[name] = &fstest.MapFile{Mode: fs.ModeDir | 0755}
			continue
		}
		// Reject symlinks/hardlinks/devices and other non-regular entries
		// rather than silently storing them as empty files.
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unsupported tar entry type %q for %s", string(header.Typeflag), name)
		}

		data, err := io.ReadAll(io.LimitReader(tr, maxFileSize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", name, err)
		}
		if int64(len(data)) > maxFileSize {
			return nil, fmt.Errorf("file %s exceeds maximum size of %d bytes", name, maxFileSize)
		}

		totalSize += int64(len(data))
		if totalSize > maxTotalSize {
			return nil, fmt.Errorf("extracted bundle exceeds maximum total size of %d bytes", maxTotalSize)
		}

		memFS[name] = &fstest.MapFile{Data: data, Mode: 0644}
	}

	return memFS, nil
}

// writeBundleTarGz writes a gzip-compressed tar archive of all files in fsys to w.
func writeBundleTarGz(w io.Writer, fsys fs.FS) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	if err := walkTar(tw, fsys); err != nil {
		return err
	}
	if err := tarCloseFn(tw); err != nil {
		return err
	}
	return gzipCloseFn(gw)
}

// BundleToTarGz creates a tar.gz archive of all files in the bundle FS.
// Used by the pack command to produce a local archive.
func BundleToTarGz(fsys fs.FS) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeBundleTarGz(&buf, fsys); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
