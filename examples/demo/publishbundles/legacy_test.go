package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

const ledger = "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n  version: 1.0.0\n"

// TestDemoPublisherPrintsBothDigests: --print-digests prints the typed and
// the legacy digest of each bundle, the two the immutability gate accepts.
func TestDemoPublisherPrintsBothDigests(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "ledger"), ledger)
	b, err := loadBundle(filepath.Join(root, "ledger"))
	if err != nil {
		t.Fatal(err)
	}
	ds, _ := oci.BundleDigests(b)
	var out bytes.Buffer
	if err := run([]string{"--print-digests", root, "example.test/demo"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if want := "ledger:1.0.0 " + ds.Typed + " " + ds.Legacy + "\n"; out.String() != want || ds.Typed == ds.Legacy {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

// TestDemoPublisherSkipsLegacyTag: a tag an older Pacto pushed untyped with
// the same content is never re-pushed; a new tag is pushed typed; a registry
// that cannot answer fails the run.
func TestDemoPublisherSkipsLegacyTag(t *testing.T) {
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer srv.Close()
	coord := strings.TrimPrefix(srv.URL, "http://") + "/demo"
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "ledger"), ledger)
	writeBundle(t, filepath.Join(root, "audit"), strings.ReplaceAll(ledger, "ledger", "audit"))
	b, _ := loadBundle(filepath.Join(root, "ledger"))
	ds, _ := oci.BundleDigests(b)
	img, _ := oci.LegacyBundleImage(b)
	ref, _ := name.ParseReference(coord+"/ledger:1.0.0", name.Insecure)
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{root, coord}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ledger:1.0.0 "+ds.Legacy+" unchanged\n") {
		t.Fatalf("stdout = %q", out.String())
	}
	if got, _ := crane.Digest(coord+"/ledger:1.0.0", crane.Insecure); got != ds.Legacy {
		t.Fatalf("the legacy tag moved to %s", got)
	}
	audit, _ := loadBundle(filepath.Join(root, "audit"))
	typed, _ := oci.BundleDigest(audit)
	if got, _ := crane.Digest(coord+"/audit:1.0.0", crane.Insecure); got != typed || !strings.Contains(out.String(), "audit:1.0.0 "+typed+"\n") {
		t.Fatalf("new tag %s, stdout %q", got, out.String())
	}

	srv.Close()
	if err := run([]string{root, coord}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "resolve ") {
		t.Fatalf("an unreachable registry: %v", err)
	}
}

// TestSeedCacheDigestMatchesCommittedLock: the offline transcripts resolve
// postgresql 16.1.0 through the seeded cache, so the seeded digest is the one
// the committed gift-cards lock pins.
func TestSeedCacheDigestMatchesCommittedLock(t *testing.T) {
	const pinned = "sha256:b2586dfe62501774a0cfd8e4949f31f0e9128c56cefb1f0cbc402d0802034832"
	lock, err := os.ReadFile("../bundles/gift-cards/v0.1.0/pacto.lock")
	if err != nil || !strings.Contains(string(lock), "digest: "+pinned) {
		t.Fatalf("the committed lock no longer pins %s (%v)", pinned, err)
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := seedCache("../bundles/postgresql/v16.1.0", "ghcr.io/trianalab/pacto/postgresql:16.1.0"); err != nil {
		t.Fatal(err)
	}
	found := false
	_ = filepath.WalkDir(cache, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(p)
			found = found || bytes.Contains(data, []byte(pinned)) || strings.Contains(p, strings.TrimPrefix(pinned, "sha256:"))
		}
		return nil
	})
	if !found {
		t.Fatalf("the seeded cache does not hold %s", pinned)
	}
}

// failingPush answers not-found and refuses every push.
type failingPush struct{ localStore }

func (failingPush) Resolve(context.Context, string) (string, error) {
	return "", &oci.ArtifactNotFoundError{Ref: "x"}
}
func (failingPush) Push(context.Context, string, *contract.Bundle) (string, error) {
	return "", errors.New("denied")
}

func TestPushOnceReportsPushFailure(t *testing.T) {
	if _, err := pushOnce(context.Background(), failingPush{}, "r", nil, oci.Digests{}); err == nil || err.Error() != "push r: denied" {
		t.Fatalf("err = %v", err)
	}
}
