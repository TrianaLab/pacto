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
	"encoding/json"
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
	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	LayerMediaType = types.MediaType("application/vnd.pacto.bundle.layer.v1.tar+gzip")

	// BundleArtifactType is the manifest artifactType every new bundle push
	// carries, so a registry listing can tell a bundle from other artifacts.
	BundleArtifactType = "application/vnd.pacto.bundle.v1"
	// AnnotationKind is the manifest annotation that names the bundle's kind:
	// KindService, KindTemplate or KindOffering.
	AnnotationKind = "io.pacto.kind"

	KindService  = "service"
	KindTemplate = "template"
	KindOffering = "offering"
)

// BundleKind is the kind a contract publishes as: a template when it declares
// service.template, else an offering when it declares service.offering, else a
// service. A golden path that declares an offering is a template, because
// pacto init --from drops the path's offering.
func BundleKind(c *contract.Contract) string {
	switch {
	case c.Service.Template != nil:
		return KindTemplate
	case c.Service.Offering != "":
		return KindOffering
	default:
		return KindService
	}
}

// tarEpoch is the fixed modification time stamped on every packed tar entry, so a
// bundle's OCI digest depends only on content — never on filesystem timestamps.
var tarEpoch = time.Unix(0, 0).UTC()

// bundleToImage converts a contract.Bundle into the OCI v1.Image a push writes:
// the legacy image plus the io.pacto.kind annotation and the bundle
// artifactType on its manifest.
func bundleToImage(b *contract.Bundle) (v1.Image, error) {
	img, err := legacyBundleImage(b)
	if err != nil {
		return nil, err
	}
	// mutate.Annotations returns a v1.Image for a v1.Image base.
	annotated := mutate.Annotations(img, map[string]string{AnnotationKind: BundleKind(b.Contract)}).(v1.Image)
	return typedImage{Image: annotated}, nil
}

// typedImage adds BundleArtifactType to its image's manifest. go-containerregistry
// has the field but no image mutator for it. Every method computes per call and
// caches nothing: the bundle layer is a stream whose digest is known only once
// remote.Write or a digest drain has read it, so a cached manifest would be
// incomplete.
//
// ponytail: no untyped retry. A registry that refuses artifactType refuses the
// organization index too, which already pushes it.
type typedImage struct{ v1.Image }

func (t typedImage) Manifest() (*v1.Manifest, error) {
	m, err := t.Image.Manifest()
	if err != nil {
		return nil, err
	}
	m = m.DeepCopy()
	m.ArtifactType = BundleArtifactType
	return m, nil
}

func (t typedImage) RawManifest() ([]byte, error) {
	m, err := t.Manifest()
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func (t typedImage) Digest() (v1.Hash, error) {
	raw, err := t.RawManifest()
	if err != nil {
		return v1.Hash{}, err
	}
	h, _, err := v1.SHA256(bytes.NewReader(raw))
	return h, err
}

func (t typedImage) Size() (int64, error) {
	raw, err := t.RawManifest()
	return int64(len(raw)), err
}

// LegacyBundleImage is the image a Pacto before 4.7 pushed for b, with no
// artifactType and no kind annotation. It exists only to recognise a tag such
// a release pushed; nothing pushes it.
func LegacyBundleImage(b *contract.Bundle) (v1.Image, error) {
	return legacyBundleImage(b)
}

// legacyBundleImage packs b as Pacto did before bundles were typed: config
// labels and one tar+gzip layer.
func legacyBundleImage(b *contract.Bundle) (v1.Image, error) {
	if b == nil || b.Contract == nil || b.FS == nil {
		return nil, fmt.Errorf("bundle, contract, and filesystem are required")
	}

	// Start with empty image, store metadata as labels.
	img := empty.Image

	// Neither mutation can fail here: mutate.Config only errors when the base's
	// config or manifest does (empty.Image's never do), and mutate.AppendLayers
	// only on a nil layer.
	img, _ = mutate.Config(img, v1.Config{
		Labels: map[string]string{
			"io.pacto.name":         b.Contract.Service.Name,
			"io.pacto.version":      b.Contract.Service.Version,
			"io.pacto.pactoVersion": b.Contract.PactoVersion,
		},
	})

	// Create a tar.gz layer from bundle FS.
	layer := stream.NewLayer(newBundleTarReader(b.FS), stream.WithMediaType(LayerMediaType))

	img, _ = mutate.AppendLayers(img, layer)

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

// BundleDigest is the digest Push would publish b under, computed without
// pushing: the layer stream is drained so the digest is final.
func BundleDigest(b *contract.Bundle) (string, error) {
	img, err := bundleToImage(b)
	if err != nil {
		return "", err
	}
	return drainedDigest(img)
}

// Digests are the two digests one bundle can be known by on a registry tag:
// Typed is what a push writes now, Legacy what a Pacto before 4.7 wrote.
type Digests struct{ Typed, Legacy string }

// BundleDigests packs b both ways. Call it only where a local bundle is
// compared with a registry tag; BundleDigest is the one-pack digest of a push.
func BundleDigests(b *contract.Bundle) (Digests, error) {
	typed, err := BundleDigest(b)
	if err != nil {
		return Digests{}, err
	}
	// BundleDigest already proved b packs, so the legacy pack cannot fail.
	img, _ := legacyBundleImage(b)
	legacy, err := drainedDigest(img)
	if err != nil {
		return Digests{}, err
	}
	return Digests{Typed: typed, Legacy: legacy}, nil
}

// Same reports whether remote is either digest of the bundle.
func (d Digests) Same(remote string) bool {
	return remote != "" && (remote == d.Typed || remote == d.Legacy)
}

// For is the digest the bundle is known by on a tag holding remote: remote
// when it is either digest, else Typed.
func (d Digests) For(remote string) string {
	if d.Same(remote) {
		return remote
	}
	return d.Typed
}

// drainedDigest reads the single layer stream of a bundle image so its
// manifest digest is final, then returns that digest.
func drainedDigest(img v1.Image) (string, error) {
	// A bundle image has exactly one layer, and reading it can only fail if
	// the bundle's filesystem does, which the digest call below reports too.
	layers, _ := img.Layers()
	rc, _ := layers[0].Compressed()
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	d, err := img.Digest()
	if err != nil {
		return "", err
	}
	return d.String(), nil
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
	// The registry layer reader checks the blob's digest and size only when a
	// read reaches EOF, and the tar reader stops at the end-of-archive blocks.
	// Read the rest, bounded, so bytes that do not match the digest the
	// manifest names fail here instead of being trusted.
	if n, err := io.Copy(io.Discard, io.LimitReader(rc, MaxTotalSize+1)); err != nil {
		return nil, fmt.Errorf("failed to read layer: %w", err)
	} else if n > MaxTotalSize {
		return nil, fmt.Errorf("layer exceeds maximum total size of %d bytes", MaxTotalSize)
	}
	return bundleFromFS(fsys)
}

// bundleFromFS reads and parses pacto.yaml from an extracted bundle filesystem
// into a Bundle. Shared by imageToBundle and the on-disk cache loader. A
// published bundle declares its pactoVersion, service.name and
// service.version; one that leaves any implied is refused.
func bundleFromFS(fsys fs.FS) (*contract.Bundle, error) {
	rawYAML, err := fs.ReadFile(fsys, "pacto.yaml")
	if err != nil {
		return nil, fmt.Errorf("bundle missing pacto.yaml: %w", err)
	}
	c, err := contract.Parse(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract from bundle: %w", err)
	}
	if err := contract.RequireDeclared(c, rawYAML); err != nil {
		return nil, fmt.Errorf("bundle pacto.yaml: %w", err)
	}
	return &contract.Bundle{Contract: c, RawYAML: rawYAML, FS: fsys}, nil
}

// The limits a bundle is read with, here and from a local golden path.
const (
	MaxFileSize  = 10 << 20 // 10 MB per file
	MaxTotalSize = 50 << 20 // 50 MB total
	maxEntries   = 10000    // max number of tar entries (bounds many-tiny-files bombs)
)

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

		name := strings.TrimSuffix(filepath.ToSlash(strings.TrimPrefix(header.Name, "./")), "/")
		if name == "" || name == "." {
			continue
		}
		// fs.ValidPath rejects "..", absolute names and empty components, any of
		// which makes a walk of the extracted FS loop or escape.
		if !fs.ValidPath(name) {
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

		data, err := io.ReadAll(io.LimitReader(tr, MaxFileSize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", name, err)
		}
		if int64(len(data)) > MaxFileSize {
			return nil, fmt.Errorf("file %s exceeds maximum size of %d bytes", name, MaxFileSize)
		}

		totalSize += int64(len(data))
		if totalSize > MaxTotalSize {
			return nil, fmt.Errorf("extracted bundle exceeds maximum total size of %d bytes", MaxTotalSize)
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
	if err := tw.Close(); err != nil {
		return err
	}
	return gw.Close()
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
