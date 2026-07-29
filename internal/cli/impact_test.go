package cli_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/internal/cli"
)

// impactErrWriter always fails writes, to drive the JSON encode error path.
type impactErrWriter struct{}

func (impactErrWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write error") }

// writeOrders writes the changed service `orders` at version/workload into dir.
// A workload change between two revisions is BREAKING; a version change is not.
func writeOrders(t *testing.T, dir, version, workload string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: orders
  version: "`+version+`"
workload: `+workload+`
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
}

// writeConsumer writes a webapp bundle declaring a dependency on orders with the
// given compatibility range, so orders gains a dependent in the fleet.
func writeConsumer(t *testing.T, dir, compat string) {
	t.Helper()
	mustWrite(t, filepath.Join(dir, "pacto.yaml"), `pactoVersion: "2.0"
service:
  name: webapp
  version: "1.0.0"
  owner:
    team: frontend
dependencies:
  - name: orders
    ref: oci://x/orders
    required: true
    compatibility: "`+compat+`"
workload: service
state:
  type: stateless
  persistence:
    scope: local
    durability: ephemeral
  dataCriticality: low
`)
}

// impactFleet builds a fleet root (orders + a webapp consumer with compat) plus a
// target-state file that makes webapp an active target.
func impactFleet(t *testing.T, compat string) (root, evidence string) {
	t.Helper()
	root = t.TempDir()
	writeOrders(t, filepath.Join(root, "orders"), "2.0.0", "service")
	writeConsumer(t, filepath.Join(root, "webapp"), compat)
	evidence = filepath.Join(t.TempDir(), "targets.yaml")
	mustWrite(t, evidence, `schemaVersion: pacto.dev/fleet-targets/v1
targets:
  - scope: prod
    kind: k8s
    name: webapp
    service: webapp
    compliance: Compliant
`)
	return root, evidence
}

// TestImpactCommand_BreakingBlocksActiveConsumer proves the non-zero exit when a
// breaking change lands on a live, incompatible consumer, and the rich text view.
func TestImpactCommand_BreakingBlocksActiveConsumer(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "2.0.0", "job") // workload change → BREAKING; 2.0.0 fails ^1.0.0
	root, ev := impactFleet(t, "^1.0.0")

	out, _, err := execFleet(t, "impact", oldDir, newDir, "--local", root, "--target-state", ev)
	if err == nil {
		t.Fatal("expected a non-zero exit for a breaking change on an active incompatible consumer")
	}
	for _, want := range []string{
		"Impact: orders 1.0.0 -> 2.0.0", "Classification:", "BREAKING",
		"Breaking changes:", "Affected consumers", "webapp", "direct",
		"compat=incompatible", "owner=frontend", "Active targets",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

// TestImpactCommand_JSON proves JSON output; the same fixture still exits non-zero
// after printing.
func TestImpactCommand_JSON(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "2.0.0", "job")
	root, ev := impactFleet(t, "^1.0.0")

	out, _, err := execFleet(t, "impact", oldDir, newDir, "--local", root, "--target-state", ev, "--output-format", "json")
	if err == nil {
		t.Fatal("expected error for breaking+incompatible in json mode too")
	}
	for _, want := range []string{`"schemaVersion"`, `"classification"`, `"consumers"`, `"webapp"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

// TestImpactCommand_BreakingCompatibleConsumer proves a breaking change with only
// compatible active consumers exits zero; also exercises --include-observed.
func TestImpactCommand_BreakingCompatibleConsumer(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "2.0.0", "job")
	root, ev := impactFleet(t, ">=1.0.0") // 2.0.0 satisfies → compatible

	out, _, err := execFleet(t, "impact", oldDir, newDir, "--local", root, "--target-state", ev, "--include-observed")
	if err != nil {
		t.Fatalf("expected success when the active consumer stays compatible: %v", err)
	}
	if !strings.Contains(out, "compat=compatible") {
		t.Errorf("expected a compatible verdict:\n%s", out)
	}
}

// TestImpactCommand_NonBreakingNoConsumersPartial covers the non-breaking exit,
// the no-consumers branch, the partial-completeness warning and --freshness.
func TestImpactCommand_NonBreakingNoConsumersPartial(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "1.0.0", "service") // identical → NON_BREAKING

	root := t.TempDir()
	writeOrders(t, filepath.Join(root, "orders"), "1.0.0", "service") // no dependents
	missing := filepath.Join(t.TempDir(), "missing.yaml")

	out, stderr, err := execFleet(t, "impact", oldDir, newDir, "--local", root, "--target-state", missing, "--freshness", "1h")
	if err != nil {
		t.Fatalf("non-breaking impact: %v", err)
	}
	if !strings.Contains(out, "No affected consumers.") {
		t.Errorf("expected no-consumers line:\n%s", out)
	}
	if !strings.Contains(stderr, "warning: answer is partial") {
		t.Errorf("expected partial warning on stderr:\n%s", stderr)
	}
}

// TestImpactCommand_ResolveError covers the svc.Impact error path (bad old ref).
func TestImpactCommand_ResolveError(t *testing.T) {
	newDir := t.TempDir()
	writeOrders(t, newDir, "1.0.0", "service")
	if _, _, err := execFleet(t, "impact", "/nonexistent/old", newDir); err == nil {
		t.Error("expected error for a nonexistent old path")
	}
}

// TestImpactCommand_WriteError covers the printImpactResult JSON encode error path.
func TestImpactCommand_WriteError(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "1.0.0", "service")
	root := t.TempDir()
	writeOrders(t, filepath.Join(root, "orders"), "1.0.0", "service")

	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	svc := app.NewService(nil, nil)
	rootCmd := cli.NewRootCommand(svc, cli.VersionInfo{Version: "test"})
	rootCmd.SetArgs([]string{"impact", oldDir, newDir, "--local", root, "--output-format", "json"})
	rootCmd.SetOut(impactErrWriter{})
	rootCmd.SetErr(impactErrWriter{})
	if err := rootCmd.Execute(); err == nil {
		t.Error("expected error when the output writer fails")
	}
}
