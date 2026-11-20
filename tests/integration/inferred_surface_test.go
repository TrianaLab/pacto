//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestInferredRecordSurfaceValidates: a 2.5 folder naming the external it
// describes, beside a record with a surface for that identity, validates
// through the real CLI. A folder's record is completed again from the folder
// before validation, so the surface rules themselves are pinned by the
// pkg/validation tests on a bundle FS.
func TestInferredRecordSurfaceValidates(t *testing.T) {
	dir := t.TempDir()
	rec := &contract.InferredRecord{
		Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"},
		Surface: &contract.InferredSurface{Identity: "stripe", Class: "vendor-api", AsOf: "2026-11-17T10:00:00Z",
			Completeness: &contract.SurfaceCompleteness{State: "complete"},
			Hosts:        []contract.SurfaceValue{{Value: "api.stripe.com", Sources: []string{"config"}}}},
	}
	files := map[string]string{
		"pacto.yaml":                "pactoVersion: \"2.5\"\nservice:\n  name: stripe\n  version: 0.0.1\n  external: stripe\n",
		contract.InferredRecordFile: string(rec.Marshal()),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := runCommand(t, nil, "validate", dir); err != nil {
		t.Fatalf("pacto validate: %v\n%s", err, out)
	}
}
