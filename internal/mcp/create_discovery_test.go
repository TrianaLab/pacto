package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// copyFixture copies the testdata folder at rel under a new folder named
// payments.
func copyFixture(t *testing.T, rel ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "payments")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(rel...))); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPactifyMCPDiscoveryParity: pacto_create with pactify answers what
// pacto init plans: the same Found and Unknown lines and the same record
// fields and findings.
func TestPactifyMCPDiscoveryParity(t *testing.T) {
	dir := copyFixture(t, "..", "pactify", "testdata", "discovery", "payments")
	want, err := pactify.Init(context.Background(), pactify.Options{Dir: dir, Name: "payments", Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, "pacto_create", map[string]any{"name": "payments", "path": dir, "pactify": true, "dry_run": true})
	var got pactify.Result
	if res.IsError || json.Unmarshal([]byte(resultText(t, res)), &got) != nil {
		t.Fatalf("pacto_create: %s", resultText(t, res))
	}
	if got.Inferred == nil || want.Inferred == nil || !reflect.DeepEqual(got.Found, want.Found) || !reflect.DeepEqual(got.Unknown, want.Unknown) ||
		!reflect.DeepEqual(got.Inferred.Fields, want.Inferred.Fields) || !reflect.DeepEqual(got.Inferred.Findings, want.Inferred.Findings) {
		t.Fatalf("pacto_create differs from pacto init:\n%+v\n%+v", got, want)
	}
	if !strings.Contains(strings.Join(got.Found, "\n"), "Helm chart chart read statically") {
		t.Errorf("found %q", got.Found)
	}
	owner := got.Inferred.Fields[slicesIndex(got.Inferred.Fields, "service.owner")]
	if owner.Class != contract.InferredClassOwnerFile || owner.Source != "catalog-info.yaml:7" {
		t.Errorf("owner %+v", owner)
	}
}

func slicesIndex(fields []contract.Inferred, name string) int {
	for i, f := range fields {
		if f.Field == name {
			return i
		}
	}
	return -1
}

// TestPactifyDiscoveryNoValueBytes: no value or URL credential a discovered
// file holds reaches pacto_create's answer.
func TestPactifyDiscoveryNoValueBytes(t *testing.T) {
	for _, fixture := range []string{"helm/values", "helm/creds", "compose/payments", "kustomize/payments", "templates", "docker", "manifests"} {
		dir := copyFixture(t, "..", "discover", "testdata", filepath.FromSlash(fixture))
		res := callTool(t, "pacto_create", map[string]any{"name": "payments", "path": dir, "pactify": true, "dry_run": true})
		text := resultText(t, res)
		if res.IsError {
			t.Fatalf("%s: %s", fixture, text)
		}
		for _, leak := range []string{"hunter2", "s3cr3t-values", "tok3n"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s: pacto_create holds %q", fixture, leak)
			}
		}
	}
}

// TestPactifyDescriptionNamesWhatItReads: an agent reading pacto_create
// learns which files pactify reads, that CODEOWNERS comes from above path,
// what the findings are and when 'inferred' is published.
func TestPactifyDescriptionNamesWhatItReads(t *testing.T) {
	tool := createTool()
	for _, want := range []string{"Helm charts", "Kustomize", "Compose", "AsyncAPI", "catalog-info.yaml", "Argo CD or Flux",
		"CODEOWNERS at the root of the enclosing Git repository, above path", "inferred.findings", "DISCOVERY_UNRESOLVED",
		"for a new contract 'inferred' is what the pacto.yaml it plans leaves out", "only when pacto.yaml declares pactoVersion 2.5"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("pacto_create description lacks %q", want)
		}
	}
	if strings.Contains(tool.Description, "already holds pacto.yaml, pactify completes it without changing it and returns") {
		t.Error("the description still ties 'inferred' to an existing pacto.yaml")
	}
	for _, want := range []string{"SDK manifests and lockfiles", "Terraform resources", "externalSummary", "never in pacto.yaml"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("pacto_create description lacks %q", want)
		}
	}
	if strings.Contains(tool.Description, "commented dependency entries") {
		t.Error("the description still promises commented dependency entries in pacto.yaml")
	}
}
