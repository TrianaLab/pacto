package cli_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/cli"
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

// TestImpactCommand_Traces covers the --traces path: an observed edge surfaces a
// shadow consumer.
func TestImpactCommand_Traces(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "1.0.0", "service") // non-breaking
	root := t.TempDir()
	writeOrders(t, filepath.Join(root, "orders"), "1.0.0", "service")
	// checkout must be a registered fleet service to be a domain-qualified shadow
	// consumer; an unregistered caller would be preserved as an unresolved
	// limitation, not a phantom default-domain consumer.
	mustWrite(t, filepath.Join(root, "checkout", "pacto.yaml"), "pactoVersion: \"2.0\"\nservice:\n  name: checkout\n  version: \"1.0.0\"\n")

	tf := filepath.Join(t.TempDir(), "traces.json")
	mustWrite(t, tf, `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"checkout"}}]},"scopeSpans":[{"spans":[{"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"orders"}}]}]}]}]}`)

	out, _, err := execFleet(t, "impact", oldDir, newDir, "--local", root, "--traces", tf)
	if err != nil {
		t.Fatalf("impact --traces: %v", err)
	}
	if !strings.Contains(out, "checkout") || !strings.Contains(out, "observed") {
		t.Errorf("expected observed shadow consumer checkout:\n%s", out)
	}
}

// TestImpactCommand_TracesReadError covers the --traces read-error path.
func TestImpactCommand_TracesReadError(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "1.0.0", "service")
	if _, _, err := execFleet(t, "impact", oldDir, newDir, "--traces", filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected read error for a missing traces file")
	}
}

// TestImpactCommand_BreakingBlocksActiveConsumer proves the non-zero exit when a
// breaking change lands on a live, incompatible consumer, and the rich text view.
func TestImpactCommand_BreakingBlocksActiveConsumer(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "2.0.0", "job") // workload change → BREAKING; 2.0.0 fails ^1.0.0
	root, ev := impactFleet(t, "^1.0.0")

	out, _, err := execFleet(t, "impact", oldDir, newDir, "--local", root, "--target-state", ev)
	if err == nil || !strings.Contains(err.Error(), "breaking changes affect active consumers, which run on known targets and do not accept orders 2.0.0: webapp") {
		t.Fatalf("expected the blocked reason as the error, got %v", err)
	}
	if !strings.Contains(out, "Release gate: blocked") {
		t.Errorf("text output missing the gate:\n%s", out)
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

// TestImpactCommand_BreakingCompatibleConsumer proves a breaking change whose
// only consumer matches by an open range exits 2 (unverified), never 0: the range
// match does not show the consumer avoids what the change removes, and the text
// says so. Also exercises --include-observed.
func TestImpactCommand_BreakingCompatibleConsumer(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "2.0.0", "job")
	root, ev := impactFleet(t, ">=1.0.0") // 2.0.0 satisfies → compatible

	out, stderr, err := execFleet(t, "impact", oldDir, newDir, "--local", root, "--target-state", ev, "--include-observed")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("expected exit 2 for an unverified breaking change, got %v", err)
	}
	for _, want := range []string{"compat=compatible", "note: the range >=1.0.0 has no upper bound", "Release gate: unverified"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(stderr, "unverified: the change is BREAKING and 1 consumer depends on orders") {
		t.Errorf("expected the unverified reason on stderr:\n%s", stderr)
	}
}

// TestImpactCommand_AddsNewRevisionToTheSnapshot proves the evaluation's trap is
// closed: a fleet root that holds only the consumers (not the changed service)
// used to answer "No affected consumers" and exit 0. The new revision is now
// added to the snapshot, the consumer is found and the declared-only BREAKING
// answer exits 2.
func TestImpactCommand_AddsNewRevisionToTheSnapshot(t *testing.T) {
	oldDir, newDir := t.TempDir(), t.TempDir()
	writeOrders(t, oldDir, "1.0.0", "service")
	writeOrders(t, newDir, "2.0.0", "job")
	root := t.TempDir()
	writeConsumer(t, filepath.Join(root, "webapp"), "^1.0.0")

	out, _, err := execFleet(t, "impact", oldDir, newDir, "--local", root)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	for _, want := range []string{"orders is in none of the fleet sources", "webapp", "compat=incompatible"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "No affected consumers") {
		t.Errorf("the consumer was missed:\n%s", out)
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
