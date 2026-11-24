package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestMCPFleetEntitiesInferredTemplates: template only lists the catalog's
// templates as inferred and unsigned; declaration declared leaves them out.
func TestMCPFleetEntitiesInferredTemplates(t *testing.T) {
	src := fleet.NewMemorySource("catalog", "idp-catalog", &fleet.Collection{Advisory: true, ListsTemplates: true,
		InferredTemplates: []fleet.RawInferredTemplate{{Name: "springboot-service", Entity: "template:default/springboot-service", From: "Catalog"}}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, src)
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	res := callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"template": "only"})
	var got struct {
		InferredTemplates *struct {
			Items []map[string]any `json:"items"`
		} `json:"inferredTemplates"`
	}
	decode(t, res, &got)
	if got.InferredTemplates == nil || len(got.InferredTemplates.Items) != 1 {
		t.Fatalf("template only: %s", resultText(t, res))
	}
	item := got.InferredTemplates.Items[0]
	if _, signed := item["signature"]; signed || item["provenance"] != "inferred" {
		t.Errorf("item = %v", item)
	}
	res = callHandler(t, fleetEntitiesTool(), fleetEntitiesHandler(q, false), map[string]any{"template": "only", "declaration": "declared"})
	if strings.Contains(resultText(t, res), "inferredTemplates") {
		t.Errorf("declared lists inferred templates: %s", resultText(t, res))
	}
	props := fleetEntitiesTool().InputSchema.(map[string]any)["properties"].(map[string]any)
	if !strings.Contains(props["declaration"].(map[string]any)["description"].(string), "inferredTemplates") {
		t.Error("the declaration description does not name inferredTemplates")
	}
}
