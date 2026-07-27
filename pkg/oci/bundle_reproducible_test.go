package oci

import (
	"bytes"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// Release-safety item 7: a bundle's packed bytes (hence its OCI layer + manifest
// digest) must depend ONLY on content — never on filesystem timestamps, owners or
// modes — so a published bundle's identity can be compared by digest rather than a
// semantic contract diff. walkTar canonicalizes every header; this proves it.
func TestBundlePackingIsContentDeterministic(t *testing.T) {
	// Same content, wildly different mtime + mode on every entry.
	mk := func(mt time.Time, mode fs.FileMode) fs.FS {
		return fstest.MapFS{
			"pacto.yaml":              &fstest.MapFile{Data: []byte("service:\n  name: x\n"), ModTime: mt, Mode: mode},
			"interfaces":              &fstest.MapFile{Mode: fs.ModeDir | mode},
			"interfaces/openapi.json": &fstest.MapFile{Data: []byte(`{"openapi":"3.1.0"}`), ModTime: mt, Mode: mode},
			"policy/schema.json":      &fstest.MapFile{Data: []byte(`{"type":"object"}`), ModTime: mt, Mode: mode},
		}
	}
	pack := func(fsys fs.FS) []byte {
		r := newBundleTarReader(fsys)
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		_ = r.Close()
		return b
	}

	a := pack(mk(time.Unix(1_000, 0), 0o600))
	b := pack(mk(time.Unix(1_700_000_000, 123), 0o755))
	if !bytes.Equal(a, b) {
		t.Fatalf("bundle tar is not content-deterministic: differing mtime/mode changed the bytes (len %d vs %d)", len(a), len(b))
	}

	// Changing CONTENT must change the bytes (the digest is a real content fingerprint).
	c := pack(fstest.MapFS{
		"pacto.yaml":              &fstest.MapFile{Data: []byte("service:\n  name: y\n")}, // one byte differs
		"interfaces":              &fstest.MapFile{Mode: fs.ModeDir | 0o755},
		"interfaces/openapi.json": &fstest.MapFile{Data: []byte(`{"openapi":"3.1.0"}`)},
		"policy/schema.json":      &fstest.MapFile{Data: []byte(`{"type":"object"}`)},
	})
	if bytes.Equal(a, c) {
		t.Fatal("bundle tar did not change when content changed — digest would not detect a real difference")
	}
}

// BundleImage exposes the publishable image; consuming its layer stream and
// reading Digest() yields the deterministic digest a publisher gates on (and the
// exact digest Push produces). Same content twice -> same digest.
func TestBundleImageDigestIsDeterministic(t *testing.T) {
	digestOf := func(b *contract.Bundle) string {
		img, err := BundleImage(b)
		if err != nil {
			t.Fatalf("BundleImage: %v", err)
		}
		layers, err := img.Layers()
		if err != nil {
			t.Fatalf("Layers: %v", err)
		}
		for _, l := range layers {
			rc, err := l.Compressed()
			if err != nil {
				t.Fatalf("Compressed: %v", err)
			}
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
		d, err := img.Digest()
		if err != nil {
			t.Fatalf("Digest: %v", err)
		}
		return d.String()
	}
	if d1, d2 := digestOf(testBundle()), digestOf(testBundle()); d1 != d2 {
		t.Fatalf("BundleImage digest not deterministic: %s != %s", d1, d2)
	}
}
