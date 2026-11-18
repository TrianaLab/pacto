package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// mqServiceFolder is a service folder whose values file names the
// corporate message queue.
func mqServiceFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "orders")
	files := map[string]string{
		"chart/Chart.yaml":  "apiVersion: v2\nname: orders\nversion: 1.0.0\n",
		"chart/values.yaml": "MQ_URL: amqp://mq.corp.example:5672\nSTRIPE_API_BASE: https://api.stripe.com/v1\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func pactifyWith(t *testing.T, a Authoring, dir string) *pactify.Result {
	t.Helper()
	res := callToolOn(t, mustNew("test", a, Modes{}), "pacto_create", map[string]any{"name": filepath.Base(dir), "path": dir, "pactify": true, "describe_only": true, "dry_run": true})
	text := resultText(t, res)
	var r pactify.Result
	if res.IsError || json.Unmarshal([]byte(text), &r) != nil {
		t.Fatalf("plan: %s", text)
	}
	return &r
}

// TestCreatePactifyUsesServerCatalog: pactify classifies with the catalog
// the server was started with and records it; the tool takes no catalog.
func TestCreatePactifyUsesServerCatalog(t *testing.T) {
	org := []byte("apiVersion: catalog.pacto.trianalab.io/v1\nentries:\n  - identity: corp/mq\n    class: legacy\n    hosts: [mq.corp.example]\n")
	digest := externalcatalog.Digest(org)
	cat, err := externalcatalog.Assemble(externalcatalog.DefaultRef(), org, externalcatalog.Ref{Origin: externalcatalog.OriginOrganization, Repository: "r.example/cat", Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	r := pactifyWith(t, Authoring{Catalog: cat}, mqServiceFolder(t))
	if r.Inferred == nil || !slices.Contains(r.Inferred.Inputs.Catalog, "organization "+digest) ||
		!slices.ContainsFunc(r.Externals, func(e pactify.External) bool {
			return e.Identity == "corp/mq" && e.Origin == externalcatalog.OriginOrganization
		}) {
		t.Fatalf("result: %+v %+v", r.Inferred, r.Externals)
	}
	// The default catalog alone knows no corporate queue, and a catalog the
	// server could not read is an Unknown line.
	r = pactifyWith(t, Authoring{CatalogUnavailable: "The organization catalog could not be read"}, mqServiceFolder(t))
	if slices.ContainsFunc(r.Externals, func(e pactify.External) bool { return e.Identity == "corp/mq" }) ||
		!slices.Contains(r.Unknown, "The organization catalog could not be read") {
		t.Fatalf("default only: %+v %v", r.Externals, r.Unknown)
	}
	raw, _ := json.Marshal(createTool().InputSchema)
	var schema struct{ Properties map[string]any }
	if err := json.Unmarshal(raw, &schema); err != nil || len(schema.Properties) == 0 {
		t.Fatalf("schema: %v %s", err, raw)
	}
	for name := range schema.Properties {
		if strings.Contains(name, "catalog") {
			t.Errorf("pacto_create takes a catalog argument: %s", name)
		}
	}
}

// TestCreatePactifyReturnsExternalSummaryRecord: the result carries the plan
// line and the record holds every external with its sources.
func TestCreatePactifyReturnsExternalSummaryRecord(t *testing.T) {
	r := pactifyWith(t, Authoring{}, mqServiceFolder(t))
	if len(r.ExternalSummary) < 2 || !regexp.MustCompile(`^External dependencies: 1 \(stripe\), inferred$`).MatchString(r.ExternalSummary[0]) ||
		!strings.Contains(r.ExternalSummary[1], "chart/values.yaml:2 (STRIPE_API_BASE)") {
		t.Fatalf("summary: %q", r.ExternalSummary)
	}
	if r.Inferred == nil || !slices.ContainsFunc(r.Inferred.Fields, func(f contract.Inferred) bool { return f.External != nil && f.External.Identity == "stripe" }) {
		t.Fatalf("record: %+v", r.Inferred)
	}
}

// TestFolderExternalsNeverLeakMCP is the MCP half of pactify's
// TestFolderExternalsNeverLeak: no credential, path or query of the folder
// reaches the pacto_create answer.
func TestFolderExternalsNeverLeakMCP(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "folder-externals")
	if err := os.CopyFS(dir, os.DirFS("../pactify/testdata/folder-externals")); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, "pacto_create", map[string]any{"name": filepath.Base(dir), "path": dir, "pactify": true, "describe_only": true, "dry_run": true})
	text := resultText(t, res)
	if res.IsError || !strings.Contains(text, "External dependencies: 3") {
		t.Fatalf("answer: %s", text)
	}
	for _, leak := range []string{"admin", "hunter2", "s3cr3t", "/app", "pw-hunter2", "npm-user", "npm-token", "maven-token", "registry.example.com", "repo.example.com", "tf-hunter2", "state-hunter2"} {
		if strings.Contains(text, leak) {
			t.Errorf("%q leaked", leak)
		}
	}
}
