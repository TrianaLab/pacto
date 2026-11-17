package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

func TestExplainShowsClassOutranksFindings(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{Name: "payments",
		Inferred: []contract.Inferred{{Field: "service.owner", Value: "team team-payments", Source: "repository:.github/CODEOWNERS:4", Confidence: "likely",
			Class: contract.InferredClassOwnerFile, Outranks: []string{"deploy/app.yaml:6"}}},
		InferredFindings: []contract.InferredFinding{
			{Code: contract.InferredFindingTruncated, Source: ".", Message: "discovery stopped after 5000 files; 3 files were not read, so results are partial"},
			{Code: "DISCOVERY_CHART_DEPENDENCY_UNLOCKED", Source: "chart/Chart.yaml:9", Message: "chart dependency 'redis' has no Chart.lock digest, not read"}},
		InferredFindingsDropped: 2}
	if err := printExplainResult(cmd, result, "text"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"  service.owner  inferred  team team-payments  from .github/CODEOWNERS:4 (repository file, not in the bundle); read from an owner file, not verified; outranks deploy/app.yaml:6\n",
		"\nDiscovery findings (recognized, not read):\n  DISCOVERY_TRUNCATED  discovery stopped after 5000 files; 3 files were not read, so results are partial\n" +
			"  DISCOVERY_CHART_DEPENDENCY_UNLOCKED  chart dependency 'redis' has no Chart.lock digest, not read\n  and 2 more\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "attested") {
		t.Errorf("explain calls an owner file attested:\n%s", out)
	}
	// Findings print with no inferred field too.
	cmd, buf = testCmd()
	if err := printExplainResult(cmd, &app.ExplainResult{Name: "payments", InferredFindings: result.InferredFindings[1:]}, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Discovery findings") || strings.Contains(buf.String(), "Inferred (not part") {
		t.Errorf("findings only:\n%s", buf.String())
	}
}

// discoveryLeakFixtures are the discover fixtures whose files hold values
// (hunter2, s3cr3t-values) or URL credentials (tok3n) no output may carry.
var discoveryLeakFixtures = []string{"helm/values", "helm/creds", "compose/payments", "kustomize/payments", "templates", "docker", "manifests"}

func TestExplainNeverPrintsEnvValues(t *testing.T) {
	for _, fixture := range discoveryLeakFixtures {
		dir := t.TempDir()
		if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "discover", "testdata", fixture))); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte("pactoVersion: \"2.5\"\nservice: {name: payments, version: 1.0.0}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result, err := app.NewService(nil, nil).Explain(context.Background(), app.ExplainOptions{Path: dir})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Inferred)+len(result.InferredFindings) == 0 && fixture != "helm/creds" { // its pinned dependency is an input, not a field
			t.Fatalf("%s: explain shows nothing discovered", fixture)
		}
		for _, format := range []string{"text", "json"} {
			cmd, buf := testCmd()
			if err := printExplainResult(cmd, result, format); err != nil {
				t.Fatal(err)
			}
			for _, leak := range []string{"hunter2", "s3cr3t-values", "tok3n"} {
				if strings.Contains(buf.String(), leak) {
					t.Errorf("%s %s output holds %q", fixture, format, leak)
				}
			}
		}
	}
}
