package dashboard

import (
	"bytes"
	"os"
	"testing"
)

// The generated artifacts under pactos/pacto-dashboard are gitignored, so the
// demo ships its own committed copy of the dashboard's OpenAPI contract. Without
// this test that copy rots silently and the demo's MCP tools 404 exactly when
// the demo is being shown. The comparison is byte-exact because ExportOpenAPI is
// byte-stable (Huma marshals with sorted keys); the one normalisation is the
// trailing newline cmd/genbundle appends when it writes the file.
func TestCommittedDashboardBundleOpenAPIDrift(t *testing.T) {
	want, err := ExportOpenAPI()
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	got, err := os.ReadFile("../../examples/demo/pacto-dashboard/interfaces/openapi.json")
	if err != nil {
		t.Fatalf("read committed spec: %v", err)
	}
	if !bytes.Equal(append(want, '\n'), got) {
		t.Error("the committed dashboard OpenAPI spec is stale; " +
			"run `make generate-dashboard-openapi` and commit the result")
	}
}
