package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestFleetEntitiesAgainstStubCatalog: pacto fleet entities --template over a
// catalog that serves one software template lists it as inferred in JSON.
func TestFleetEntitiesAgainstStubCatalog(t *testing.T) {
	t.Setenv("PACTO_BACKSTAGE_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"apiVersion":"scaffolder.backstage.io/v1beta3","kind":"Template",` +
			`"metadata":{"name":"springboot-service","namespace":"default","title":"Spring Boot"}}],"pageInfo":{}}`))
	}))
	defer srv.Close()
	out, _, err := execFleet(t, "fleet", "entities", "--template", "--local", filepath.Join("..", "..", "examples", "demo", "bundles"),
		"--backstage-url", srv.URL, "--output-format", "json")
	if err != nil {
		t.Fatalf("entities: %v", err)
	}
	var res fleet.EntityList
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if l := res.InferredTemplates; l == nil || len(l.Items) != 1 || l.Items[0].Name != "springboot-service" ||
		l.Items[0].Provenance != fleet.ProvenanceInferred || !l.Items[0].Acceptable {
		t.Errorf("inferredTemplates = %+v", res.InferredTemplates)
	}
}
