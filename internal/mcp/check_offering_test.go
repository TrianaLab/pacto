package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckToolReturnsParameterCodes: pacto_check reports each of the four
// parameter codes for environment values outside a tiered offering's
// parameter space, each ending with its next step, and its description says
// so.
func TestCheckToolReturnsParameterCodes(t *testing.T) {
	root := t.TempDir()
	for rel, data := range map[string]string{
		"pg/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: postgresql\n  version: 16.1.0\n  offering: postgresql\n  owner:\n    team: team-data\n" +
			"configurations:\n  - {name: default, schema: schema.json, required: false}\n",
		"pg/schema.json": `{"type":"object","additionalProperties":false,"properties":{"class":{"enum":["small","large"],"x-pacto-tier":"tunable"},` +
			`"storageGB":{"type":"integer"},"replicas":{"type":"integer","maximum":5,"x-pacto-tier":"tunable"}}}`,
		"orders/pacto.yaml": "pactoVersion: \"2.5\"\nservice:\n  name: orders\n  version: 1.0.0\n" +
			"configurations:\n  - name: database\n    ref: ../pg\n    required: true\n    values: {class: small, storageGB: 20}\n" +
			"environments:\n  - {name: production, file: production.yaml}\n",
		"orders/production.yaml": "database:\n  class: medium\n  storageGB: 100\n  replicas: 9\n  replica: 2\n",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Check(context.Background(), realValidator, filepath.Join(root, "orders"), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range res.Errors {
		if e.Path != "environments[0].file" || !strings.Contains(e.Message, ". Next: ") {
			t.Errorf("finding %+v", e)
		}
		got[e.Code] = true
	}
	for _, code := range []string{"TierNotTunable", "ParameterOutOfRange", "ParameterNotDeclared", "ClassNotOffered"} {
		if !got[code] {
			t.Errorf("no %s in %+v", code, res.Errors)
		}
	}
	if !strings.Contains(checkTool().Description, "Environment values for an offering may change only its tunable fields (codes TierNotTunable, ParameterOutOfRange, ParameterNotDeclared and ClassNotOffered); each finding ends with the next step.") {
		t.Errorf("description %q", checkTool().Description)
	}
}
