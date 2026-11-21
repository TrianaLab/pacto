package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/publish"
)

func TestPublishInferred(t *testing.T) {
	host, reg := publishRegistry(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	contract := "pactoVersion: \"2.0\"\nservice:\n  name: checkout\n  version: \"1.0.0\"\ndependencies:\n  - name: stripe\n    external: true\n"
	if err := os.WriteFile(filepath.Join(dir, "checkout", "pacto.yaml"), []byte(contract), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	svc := &Service{Version: "4.7.0", sys: sysDeps{publishRegistry: func() publish.Registry { return reg }}}
	opts := PublishInferredOptions{Fleet: FleetOptions{LocalRoots: []string{dir}}, Registry: "oci://" + host + "/demo", Signer: edSigner(priv), Verify: keyPolicy(pub)}
	rep, err := svc.PublishInferred(context.Background(), opts)
	if err != nil || len(rep.Published) != 1 || rep.Published[0].Repository != host+"/demo/pacto-inferred/stripe" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	opts.Fleet.ExternalCatalogRef = "oci://" + host + "/catalog:latest"
	if _, err := svc.PublishInferred(context.Background(), opts); err == nil {
		t.Error("a fleet that cannot be built published")
	}
}
