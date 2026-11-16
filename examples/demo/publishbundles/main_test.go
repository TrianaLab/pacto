package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBundle(t *testing.T, dir, contract string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "chart"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chart", "Chart.yaml"), []byte("apiVersion: v2\nname: newsletter-service\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An undeclared service.version is skipped on stderr in both modes; stdout keeps
// only "<tag> <digest>" lines, which publish-demo-bundles.sh --check parses.
func TestPublishSkipsUndeclaredVersion(t *testing.T) {
	const skip = "skipped: newsletter-service (no declared version, not publishable)"

	t.Run("print", func(t *testing.T) {
		root := t.TempDir()
		writeBundle(t, filepath.Join(root, "newsletter-service"), "")
		writeBundle(t, filepath.Join(root, "ledger", "v1.0.0"), "pactoVersion: \"2.5\"\nservice:\n  name: ledger\n  version: 1.0.0\n")
		var out, errOut bytes.Buffer
		if err := run([]string{"--print-digests", root, "example.test/demo"}, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "newsletter") || !strings.HasPrefix(out.String(), "ledger:1.0.0 sha256:") {
			t.Fatalf("stdout = %q", out.String())
		}
		if !strings.Contains(errOut.String(), skip) {
			t.Fatalf("stderr = %q", errOut.String())
		}
	})

	t.Run("push", func(t *testing.T) {
		root := t.TempDir()
		writeBundle(t, filepath.Join(root, "newsletter-service"), "")
		var out, errOut bytes.Buffer
		if err := run([]string{root, "example.test/demo"}, &out, &errOut); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 || !strings.Contains(errOut.String(), skip) {
			t.Fatalf("stdout = %q, stderr = %q", out.String(), errOut.String())
		}
	})
}
