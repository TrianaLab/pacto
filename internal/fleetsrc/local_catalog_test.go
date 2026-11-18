package fleetsrc

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// TestLocalCompletionUsesGivenCatalog: a local folder is completed with the
// catalog the fleet build classifies with, and its record names it.
func TestLocalCompletionUsesGivenCatalog(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "orders")
	for name, body := range map[string]string{
		"pacto.yaml":        "pactoVersion: \"2.5\"\nservice: {name: orders, version: 1.0.0}\n",
		"chart/Chart.yaml":  "apiVersion: v2\nname: orders\nversion: 1.0.0\n",
		"chart/values.yaml": "MQ_URL: amqp://mq.corp.example:5672\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	org := []byte("apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n  - identity: corp/mq\n    class: legacy\n    hosts: [mq.corp.example]\n")
	digest := externalcatalog.Digest(org)
	cat, err := externalcatalog.Assemble(externalcatalog.DefaultRef(), org, externalcatalog.Ref{Origin: externalcatalog.OriginOrganization, Repository: "r.example/cat", Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	src := NewLocalSource("local", root)
	src.Catalog = cat
	col, err := src.Collect(t.Context())
	if err != nil || len(col.Revisions) != 1 {
		t.Fatalf("collect: %v %+v", err, col)
	}
	rec, err := contract.ReadInferredRecord(col.Revisions[0].Bundle.FS)
	if err != nil || rec == nil || !slices.Contains(rec.Inputs.Catalog, "organization "+digest) ||
		!slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.External != nil && f.External.Identity == "corp/mq" }) {
		t.Fatalf("record: %v %+v", err, rec)
	}
}
