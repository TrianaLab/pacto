package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

const reconcileTraceCLI = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"web"}}]},
  "scopeSpans":[{"spans":[
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"payments"}}]},
    {"kind":3,"attributes":[{"key":"peer.service","value":{"stringValue":"shadow"}}]}
  ]}]}]}`

// writeWebWithDeps writes a web bundle declaring dependencies on the given names.
func writeWebWithDeps(t *testing.T, dir string, deps ...string) {
	t.Helper()
	body := "pactoVersion: \"2.0\"\nservice:\n  name: web\n  version: \"1.0.0\"\ndependencies:\n"
	for _, d := range deps {
		body += "  - name: " + d + "\n    ref: oci://x/" + d + "\n    required: true\n    compatibility: \"^1.0.0\"\n"
	}
	mustWrite(t, filepath.Join(dir, "pacto.yaml"), body)
}

func TestFleetReconcile_Text(t *testing.T) {
	root := t.TempDir()
	writeWebWithDeps(t, filepath.Join(root, "web"), "payments", "cache")
	tf := filepath.Join(t.TempDir(), "traces.json")
	mustWrite(t, tf, reconcileTraceCLI)

	out, _, err := execFleet(t, "fleet", "reconcile", "--traces", tf, "--local", root)
	if err != nil {
		t.Fatalf("fleet reconcile: %v", err)
	}
	if !strings.Contains(out, "1 matched, 1 declared-not-observed, 1 observed-not-declared") {
		t.Errorf("summary missing:\n%s", out)
	}
	for _, want := range []string{"[matched] web -> payments", "[declared-not-observed] web -> cache", "[observed-not-declared] web -> shadow"} {
		if !strings.Contains(out, want) {
			t.Errorf("entry missing %q:\n%s", want, out)
		}
	}
}

func TestFleetReconcile_JSON(t *testing.T) {
	root := t.TempDir()
	writeWebWithDeps(t, filepath.Join(root, "web"), "payments")
	tf := filepath.Join(t.TempDir(), "traces.json")
	mustWrite(t, tf, reconcileTraceCLI)

	out, _, err := execFleet(t, "fleet", "reconcile", "--traces", tf, "--local", root, "--output-format", "json")
	if err != nil {
		t.Fatalf("fleet reconcile json: %v", err)
	}
	for _, want := range []string{`"summary"`, `"matched"`, `"observed-not-declared"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json missing %q:\n%s", want, out)
		}
	}
}

func TestFleetReconcile_MissingTraces(t *testing.T) {
	if _, _, err := execFleet(t, "fleet", "reconcile", "--local", t.TempDir()); err == nil {
		t.Fatal("expected error for missing required --traces")
	}
}

func TestFleetReconcile_ReadError(t *testing.T) {
	if _, _, err := execFleet(t, "fleet", "reconcile", "--traces", filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected read error")
	}
}

func TestFleetReconcile_ParseError(t *testing.T) {
	tf := filepath.Join(t.TempDir(), "bad.json")
	mustWrite(t, tf, "{bad")
	if _, _, err := execFleet(t, "fleet", "reconcile", "--traces", tf, "--local", t.TempDir()); err == nil {
		t.Fatal("expected parse error")
	}
}
