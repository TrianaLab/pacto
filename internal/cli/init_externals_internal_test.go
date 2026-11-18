package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// folderExternals is a copy of pactify's folder-externals fixture.
func folderExternals(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "folder-externals")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "pactify", "testdata", "folder-externals"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

// unreachableCatalog is a catalog reference pinned by digest that no
// registry serves.
const unreachableCatalog = "oci://127.0.0.1:1/acme/external-catalog@sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestInitPrintsExternalSummaryLine(t *testing.T) {
	out, err := runInit(t, "--plan", "--describe-only", folderExternals(t))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^External dependencies: 3 \(rds:orders-db, rds, stripe\), inferred$`).MatchString(out) ||
		!strings.Contains(out, "  catalog default "+externalcatalog.DefaultDigest()+"\n") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestInitExternalCatalogRefusesTag(t *testing.T) {
	dir := folderExternals(t)
	for _, args := range [][]string{
		{"--plan", dir},
		{"--plan", dir, dir},
		{"--plan", "--cluster", "--out", t.TempDir()},
	} {
		_, err := runInit(t, append(args, "--external-catalog", "oci://example.com/cat:latest")...)
		if err == nil || !strings.Contains(err.Error(), "pin it by digest") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestInitExternalCatalogUnreadableIsUnknown(t *testing.T) {
	dir := folderExternals(t)
	out, err := runInit(t, "--plan", "--describe-only", dir, "--external-catalog", unreachableCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "could not be read, so only the default catalog classified external systems") ||
		!strings.Contains(out, "  2 sources not read\n") || strings.Contains(out, "organization sha256:") {
		t.Fatalf("output:\n%s", out)
	}
	// Several folders classify with the same catalog.
	if out, err = runInit(t, "--plan", "--describe-only", dir, folderExternals(t), "--external-catalog", unreachableCatalog); err != nil ||
		!strings.Contains(out, "could not be read") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The organization catalog comes from the environment too, for pack as for
// init: a tag there is refused before anything is read.
func TestPackUsesExternalCatalogEnv(t *testing.T) {
	t.Setenv("PACTO_EXTERNAL_CATALOG", "oci://example.com/cat:latest")
	dir := folderExternals(t)
	for _, args := range [][]string{{"init", "--plan", dir}, {"pack", dir}} {
		root := NewRootCommand(app.NewService(nil, nil), VersionInfo{Version: "test"})
		root.SetArgs(args)
		root.SetOut(&strings.Builder{})
		root.SetErr(&strings.Builder{})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "pin it by digest") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestExplainPrintsExternalSummary(t *testing.T) {
	cmd, buf := testCmd()
	result := &app.ExplainResult{Name: "svc",
		Inferred: []contract.Inferred{
			{Field: "dependencies[stripe]", Value: "stripe", Source: "chart/values.yaml:3", Confidence: "likely",
				Declare:  "dependencies:\n  - name: stripe\n    external: true\n",
				External: &contract.InferredExternal{Identity: "stripe", Sources: []contract.InferredExternalSource{{Kind: "helmValues", Path: "chart/values.yaml", Line: 3, Key: "STRIPE_API_BASE"}}}},
			{Field: "workload", Value: "service", Source: "a.yaml:1", Confidence: "likely"},
		},
		InferredFindings: []contract.InferredFinding{{Code: "DISCOVERY_UNSUPPORTED_FILE", Source: "yarn.lock", Message: "yarn.lock is not read"}}}
	if err := printExplainResult(cmd, result, "text"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Inferred (not part of the contract):\n  External dependencies: 1 (stripe), inferred\n    dependencies[stripe]  external, from chart/values.yaml:3 (STRIPE_API_BASE)\n    1 source not read\n",
		"  workload  inferred  service  from a.yaml:1\n",
		"To declare in pacto.yaml:\n  dependencies:\n    - name: stripe\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "dependencies[stripe]  inferred") {
		t.Errorf("the external is listed twice:\n%s", out)
	}
}
