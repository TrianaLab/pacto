package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// TestPublishedBundleCarriesNoCredential: the files pactify generates for a
// folder that names a credential URL, a JWT and a dotted password reach the
// published bundle with none of them, nor does the inferred record; the
// user's own manifests and values.yaml are published as written and are not
// checked here.
func TestPublishedBundleCarriesNoCredential(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := filepath.Join(t.TempDir(), "checkout")
	if err := os.CopyFS(dir, os.DirFS("../pactify/testdata/externals")); err != nil {
		t.Fatal(err)
	}
	deploy := filepath.Join(dir, "k8s", "deployment.yaml")
	data, _ := os.ReadFile(deploy)
	_ = os.WriteFile(deploy, []byte(strings.ReplaceAll(string(data), "registry.example/shop", host)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644)

	r, err := pactify.Init(context.Background(), pactify.Options{Dir: dir, Owner: "shop", Registry: host, NoTransparencyLog: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	signingKey(t, dir)
	res, err := publishService(&fakeRun{}, reg).Publish(context.Background(), PublishOptions{Path: dir, Revision: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := reg.Pull(t.Context(), strings.TrimPrefix(c.Publish.Bundle, "oci://")+"@"+res.Digests[publish.BundleItem])
	if err != nil {
		t.Fatal(err)
	}
	checked, schema := 0, false
	for _, f := range r.Files {
		got, err := fs.ReadFile(b.FS, f.Path)
		if err != nil {
			continue // cosign.key, .pactoignore: generated, never published
		}
		checked++
		schema = schema || f.Path == "configuration/schema.json"
		for _, bad := range []string{"user:", "pass@", "/app", "password", "eyJhbGciOi", "s3cr3t"} {
			if strings.Contains(string(got), bad) {
				t.Errorf("%s in the bundle holds %q", f.Path, bad)
			}
		}
	}
	if checked == 0 || !schema {
		t.Fatalf("checked %d files", checked)
	}
	recordHoldsExternals(t, b.FS)
}

// recordHoldsExternals fails unless the externals the folder names are in
// the bundle's inferred record alone, with no credential.
func recordHoldsExternals(t *testing.T, fsys fs.FS) {
	t.Helper()
	pacto, _ := fs.ReadFile(fsys, "pacto.yaml")
	record, _ := fs.ReadFile(fsys, contract.InferredRecordFile)
	if strings.Contains(string(pacto), "aws/rds") || !strings.Contains(string(record), "identity: aws/rds") {
		t.Fatalf("pacto.yaml:\n%s\nrecord:\n%s", pacto, record)
	}
	for _, bad := range []string{"user:", "pass@", "/app", "password", "eyJhbGciOi", "s3cr3t"} {
		if strings.Contains(string(record), bad) {
			t.Errorf("the record holds %q", bad)
		}
	}
}
