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
