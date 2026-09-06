package dashboard

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The generated artifacts under pactos/pacto-dashboard are gitignored, so the
// demo ships its own committed copy of the dashboard's OpenAPI contract. Without
// this test that copy rots silently and the demo's MCP tools 404 exactly when
// the demo is being shown. Parsed documents are compared, not bytes: key order
// and a trailing newline are not drift.
func TestCommittedDashboardBundleMatchesExportedOpenAPI(t *testing.T) {
	want, err := ExportOpenAPI()
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	got, err := os.ReadFile("../../examples/demo/pacto-dashboard/interfaces/openapi.json")
	if err != nil {
		t.Fatalf("read committed spec: %v", err)
	}
	var wantDoc, gotDoc any
	if err := json.Unmarshal(want, &wantDoc); err != nil {
		t.Fatalf("unmarshal exported: %v", err)
	}
	if err := json.Unmarshal(got, &gotDoc); err != nil {
		t.Fatalf("unmarshal committed: %v", err)
	}
	if !reflect.DeepEqual(wantDoc, gotDoc) {
		t.Error("the committed dashboard OpenAPI spec is stale; regenerate it with " +
			"`go run ./cmd/genbundle dashboard-openapi > examples/demo/pacto-dashboard/interfaces/openapi.json`")
	}
}
