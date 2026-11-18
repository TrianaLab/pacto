package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// paymentsRepo is a repository holding the pactify payments fixture at
// payments/, owned by its CODEOWNERS rule on line 4; pacto is the
// fixture's pacto.yaml, none when empty.
func paymentsRepo(t *testing.T, pacto string) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, "payments")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "pactify", "testdata", "discovery", "payments"))); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{".github/CODEOWNERS": "# owners\n\n\n/payments/ @acme/team-payments\n"}
	if pacto != "" {
		files["payments/pacto.yaml"] = pacto
	}
	for name, body := range files {
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// discoveryBlock is the story's UX block as init prints it for the payments
// fixture.
const discoveryBlock = `
payments inferred:
  configuration.LOG_LEVEL  string from chart/values.yaml:7
  image                    ghcr.io/acme/payments:1.4.0 from chart/values.yaml:2
  port 8080                from Dockerfile:12 (EXPOSE, outranks chart/values.yaml:5)
  owner                    from catalog-info.yaml:7 (attested, outranks CODEOWNERS:4)
  service.summary          Takes card payments from Dockerfile:10
  environments             production, staging from chart/values-production.yaml, chart/values-staging.yaml
finding: chart dependency 'redis' has no Chart.lock digest, not read
`

// TestInitPrintsDiscoveryBlock: a new contract and a completed empty 2.5
// contract print the same rows, the owner file marked attested with the
// CODEOWNERS rule it outranks, and the unlocked chart dependency once.
func TestInitPrintsDiscoveryBlock(t *testing.T) {
	for name, pacto := range map[string]string{"new contract": "", "complete": "pactoVersion: \"2.5\"\n"} {
		out, err := runInit(t, "--plan", paymentsRepo(t, pacto))
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if !strings.Contains(out, discoveryBlock) || strings.Count(out, "'redis'") != 1 {
			t.Errorf("%s: want the block\n%s\nin:\n%s", name, discoveryBlock, out)
		}
	}
}

// TestPactifyDiscoveryNoValueBytes: no value or URL credential a discovered
// file holds reaches init's text or JSON.
func TestPactifyDiscoveryNoValueBytes(t *testing.T) {
	for _, fixture := range []string{"helm/values", "helm/creds", "compose/payments", "kustomize/payments", "templates", "docker", "manifests"} {
		for _, format := range []string{"text", "json"} {
			dir := filepath.Join(t.TempDir(), "payments")
			if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "discover", "testdata", fixture))); err != nil {
				t.Fatal(err)
			}
			out, err := runInit(t, "--plan", "--output-format", format, dir)
			if err != nil {
				t.Fatalf("%s: %v\n%s", fixture, err, out)
			}
			for _, leak := range []string{"hunter2", "s3cr3t-values", "tok3n"} {
				if strings.Contains(out, leak) {
					t.Errorf("%s %s output holds %q", fixture, format, leak)
				}
			}
		}
	}
}

// A folder whose discovery read no value still prints its findings.
func TestInitPrintsFindingsWithoutRows(t *testing.T) {
	var buf strings.Builder
	printDiscovered(&buf, &app.InitResult{Service: "payments", Inferred: &contract.InferredRecord{
		Findings: []contract.InferredFinding{{Code: "DISCOVERY_UNREADABLE", Source: "a.yaml", Message: "a.yaml is not valid YAML, so it is not read"}}}})
	if buf.String() != "\nfinding: a.yaml is not valid YAML, so it is not read\n" {
		t.Errorf("output %q", buf.String())
	}
}
