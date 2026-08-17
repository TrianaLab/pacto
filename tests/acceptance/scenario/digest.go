package scenario

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/ignore"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

// Digests is the immutable identity every one of the fixture's bundles WILL be
// published under, computed from the materialized bytes without a registry.
//
// The Kubernetes surface never needs this: a cluster run brings a registry up
// before it projects anything that has to name a digest, so it uses the real ones
// the push returned. The Compose surface cannot — its registry does not exist
// until the artifact has been built, distributed and pulled, and the evidence
// payloads INSIDE the artifact have to point at content that will exist.
//
// Packing is content-deterministic, so this is the digest the seed's push
// produces. That is a claim, not an assumption: the seed re-checks each pushed
// digest against the artifact at run time, and the acceptance harness pushes for
// real and compares.
//
// Read from MaterializeFiles' bytes rather than from a directory, because the
// Compose surface — the only caller — never writes one: a Compose OCI artifact
// carries no files, so there is nothing on disk to read back. Packing
// canonicalizes every tar header's mode, owner and mtime (pkg/oci), so a bundle
// packed from these bytes and the same bundle packed from the read-only config
// mounts the demo's seed pushes from have the same digest.
func (s Scenario) Digests(files map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, svc := range s.Services {
		for _, rev := range svc.Revisions {
			digest, err := bundleDigest(bundleFS(files, rev.Dir))
			if err != nil {
				return nil, fmt.Errorf("scenario %s: %s %s: %w", s.Name, svc.Name, rev.Version, err)
			}
			out[DigestKey(svc.Name, rev.Version)] = digest
		}
	}
	return out, nil
}

// bundleFS is one bundle directory's documents, rooted at the bundle.
func bundleFS(files map[string]string, dir string) fs.FS {
	out := fstest.MapFS{}
	for name, body := range files {
		if rel, ok := strings.CutPrefix(name, dir+"/"); ok {
			out[rel] = &fstest.MapFile{Data: []byte(body), Mode: 0o644}
		}
	}
	return out
}

// bundleDigest is the OCI digest a materialized bundle would be published under:
// build the image, consume its layer stream to finalize the digest — a streamed
// layer knows it only once the stream has been read — then read it back.
func bundleDigest(dirFS fs.FS) (string, error) {
	raw, err := fs.ReadFile(dirFS, "pacto.yaml")
	if err != nil {
		return "", err
	}
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	matcher, err := ignore.Load(dirFS)
	if err != nil {
		return "", err
	}
	img, err := oci.BundleImage(&contract.Bundle{Contract: c, RawYAML: raw, FS: ignore.FS(dirFS, matcher)})
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
		_, err = io.Copy(io.Discard, rc)
		_ = rc.Close()
		if err != nil {
			return "", err
		}
	}
	d, err := img.Digest()
	if err != nil {
		return "", err
	}
	return d.String(), nil
}
