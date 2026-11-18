package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/cli"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

func runMigrate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "test"})
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"migrate"}, args...))
	err := root.Execute()
	return out.String(), err
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestMigrateCommandExitCodes(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		".pactoignore":           "archive/\n",
		"archive/old/pacto.yaml": "pactoVersion: \"2.3\"\n",
		"kept/pacto.yaml":        "pactoVersion: \"2.3\"\nbogus: 1\n",
		"legacy/pacto.yaml":      "pactoVersion: \"1.0\"\n",
	})
	out, err := runMigrate(t, dir, t.TempDir())
	if err != nil || !strings.Contains(out, "   kept at 2.3: ") || !strings.Contains(out, "legacy/pacto.yaml: ") ||
		!strings.Contains(out, "archive:  ") || !strings.Contains(out, "skipped (from .pactoignore:1)") || !strings.Contains(out, "  no pacto.yaml found\n") ||
		!strings.HasSuffix(out, "0 rewritten, 0 current, 1 kept, 1 manual, 1 skipped\n") {
		t.Fatalf("kept and manual: %v\n%s", err, out)
	}

	out, err = runMigrate(t, "--to", "9.9", dir)
	if err == nil || !strings.Contains(err.Error(), `--to "9.9" is not a supported pactoVersion`) || out != "" {
		t.Fatalf("--to 9.9: %v\n%s", err, out)
	}
}

// A file that cannot be read prints every line and exits 1, in text and JSON.
func TestMigrateCommandExitsOneOnUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := writeFiles(t, map[string]string{"kept/pacto.yaml": "pactoVersion: \"2.3\"\n", "legacy/pacto.yaml": "pactoVersion: \"1.0\"\n"})
	p := filepath.Join(dir, "kept", "pacto.yaml")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	out, err := runMigrate(t, dir)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 1 || !strings.Contains(out, "   cannot read: permission denied") ||
		!strings.Contains(out, "legacy/pacto.yaml: ") || !strings.HasSuffix(out, ", 1 failed\n") {
		t.Fatalf("unreadable: %v\n%s", err, out)
	}
	out, err = runMigrate(t, "--output-format", "json", dir)
	var doc struct {
		app.MigrateResult
		Exit struct{ Code int } `json:"exit"`
	}
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil || !errors.As(err, &exit) || doc.Failed != 1 || doc.Exit.Code != 1 {
		t.Fatalf("json: %v %v\n%s", jerr, err, out)
	}
}

func TestMigrateCommandDryRunAndTarget(t *testing.T) {
	prev := contract.SupportedVersions[len(contract.SupportedVersions)-2]
	doc := "pactoVersion: \"2.0\"\nservice: {name: s, version: 1.0.0}\n"
	dir := writeFiles(t, map[string]string{"s/pacto.yaml": doc})
	out, err := runMigrate(t, "--dry-run", "--to", prev, dir)
	got, _ := os.ReadFile(filepath.Join(dir, "s", "pacto.yaml"))
	if err != nil || !strings.Contains(out, `set pactoVersion to "`+prev+`" (was "2.0")`) || !strings.HasSuffix(out, "(dry run: nothing written)\n") || string(got) != doc {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestMigrateHasNoCheckFlag(t *testing.T) {
	root := cli.NewRootCommand(app.NewService(nil, nil), cli.VersionInfo{Version: "test"})
	cmd, _, err := root.Find([]string{"migrate"})
	if err != nil || cmd.Name() != "migrate" || cmd.Flags().Lookup("check") != nil || cmd.Flags().Lookup("to") == nil || cmd.Flags().Lookup("dry-run") == nil {
		t.Fatalf("%v %v", cmd, err)
	}
}
