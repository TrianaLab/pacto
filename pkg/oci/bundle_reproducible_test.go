package oci

import (
	"bytes"
	"io"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"
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

// BundleDigest (the pre-push, buffered-layer digest the demo immutability gate
// compares against the remote) MUST equal the digest Push actually produces from
// the streamed layer — otherwise the gate would falsely conflict on identical
// content. bundleToImage's streamed layer finalizes its digest only after the
// stream is consumed, so consume it, then compare.
func TestBundleDigestMatchesStreamedPush(t *testing.T) {
	b := testBundle()
	static, err := BundleDigest(b)
	if err != nil {
		t.Fatalf("BundleDigest: %v", err)
	}
	img, err := bundleToImage(b)
	if err != nil {
		t.Fatalf("bundleToImage: %v", err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatalf("layers: %v", err)
	}
	rc, err := layers[0].Compressed()
	if err != nil {
		t.Fatalf("compressed: %v", err)
	}
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	streamed, err := img.Digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if static != streamed.String() {
		t.Fatalf("BundleDigest %s != streamed push digest %s — the pre-push gate would falsely conflict on identical content", static, streamed.String())
	}
}
