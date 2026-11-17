package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCreatePactifyReturnsExternals: pacto_create with pactify returns the
// externals the folder names and the catalog they were classified with, with
// no credential from the folder.
func TestCreatePactifyReturnsExternals(t *testing.T) {
	res := callTool(t, "pacto_create", map[string]any{"name": "checkout", "path": "../pactify/testdata/externals",
		"pactify": true, "describe_only": true, "dry_run": true})
	text := resultText(t, res)
	var plan struct {
		Externals []struct{ Identity, Host string }
		Catalog   string
	}
	if res.IsError || json.Unmarshal([]byte(text), &plan) != nil || plan.Catalog == "" {
		t.Fatalf("plan: %s", text)
	}
	got := map[string]string{}
	for _, e := range plan.Externals {
		if e.Host != "" {
			got[e.Identity] = e.Host
		}
	}
	if got["stripe"] != "api.stripe.com" || got["aws/rds"] != "db.xyz.rds.amazonaws.com" {
		t.Fatalf("externals: %+v", plan.Externals)
	}
	ext, _ := json.Marshal(plan)
	for _, bad := range []string{"user:", "pass@", "eyJhbGciOi", "s3cr3t"} {
		if strings.Contains(string(ext), bad) {
			t.Errorf("%q leaked", bad)
		}
	}
}
