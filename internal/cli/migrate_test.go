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
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// runMigrate runs pacto migrate with a bundle store, so a local ../pg ref
// resolves and billing is kept for its finding, not for a missing resolver.
func runMigrate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := cli.NewRootCommand(app.NewService(&testutil.MockBundleStore{}, nil), cli.VersionInfo{Version: "test"})
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

// migrateTree copies testdata/migrate into a temporary directory, writes
// orders/pacto.yaml at Current() and changes into it.
func migrateTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("testdata", "migrate"))); err != nil {
		t.Fatal(err)
	}
	orders := "pactoVersion: \"" + contract.Current() + "\"\nservice: {name: orders, version: 1.0.0}\n"
	if err := os.MkdirAll(filepath.Join(dir, "orders"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "orders", "pacto.yaml"), []byte(orders), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// snapshot reads every file under dir, keyed by slash path.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func golden(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "migrate.golden"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "{{current}}", contract.Current())
}

// TestMigrateCommandGolden runs pacto migrate with no argument over a mixed
// repository: three files rise by their version token alone, every other file
// stays byte-identical, and a second run has nothing left to rewrite.
func TestMigrateCommandGolden(t *testing.T) {
	want := golden(t)
	dir := migrateTree(t)
	before := snapshot(t, dir)

	out, err := runMigrate(t)
	if err != nil || out != want {
		t.Fatalf("%v\ngot:\n%s\nwant:\n%s", err, out, want)
	}

	after := snapshot(t, dir)
	cur := contract.Current()
	raised := map[string][2]string{
		"payments/pacto.yaml":  {"'2.3'", "'" + cur + "'"},
		"inventory/pacto.yaml": {`"2.0"`, `"` + cur + `"`},
		"pg/pacto.yaml":        {`"2.4"`, `"` + cur + `"`},
	}
	if len(after) != len(before) {
		t.Fatalf("file set changed: %d -> %d", len(before), len(after))
	}
	for rel, orig := range before {
		exp := orig
		if r, ok := raised[rel]; ok {
			exp = strings.Replace(orig, r[0], r[1], 1)
		}
		if after[rel] != exp {
			t.Errorf("%s:\ngot:  %q\nwant: %q", rel, after[rel], exp)
		}
	}

	out, err = runMigrate(t)
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.Contains(l, "set pactoVersion") {
			t.Errorf("second run rewrote: %s", l)
		}
	}
	if err != nil || !strings.HasSuffix(out, "0 rewritten, 4 current, 1 kept, 1 manual, 2 skipped\n") {
		t.Fatalf("second run: %v\n%s", err, out)
	}
}

func TestMigrateCommandDryRun(t *testing.T) {
	want := strings.TrimSuffix(golden(t), "\n") + " (dry run: nothing written)\n"
	dir := migrateTree(t)
	before := snapshot(t, dir)
	out, err := runMigrate(t, "--dry-run")
	if err != nil || out != want {
		t.Fatalf("%v\ngot:\n%s\nwant:\n%s", err, out, want)
	}
	after := snapshot(t, dir)
	for rel, b := range before {
		if after[rel] != b {
			t.Errorf("%s changed under --dry-run", rel)
		}
	}
}

func TestMigrateCommandJSON(t *testing.T) {
	migrateTree(t)
	out, err := runMigrate(t, "--output-format", "json")
	var r app.MigrateResult
	if jerr := json.Unmarshal([]byte(out), &r); err != nil || jerr != nil {
		t.Fatalf("%v %v\n%s", err, jerr, out)
	}
	if len(r.Files) != 6 || r.Rewritten != 3 || r.Current != 1 || r.Kept != 1 || r.Manual != 1 || r.Failed != 0 || r.Skipped != 2 || r.To != contract.Current() {
		t.Fatalf("counts: %+v", r)
	}
	for _, f := range r.Files {
		if strings.Contains(f.Path, "node_modules") {
			t.Errorf("node_modules listed: %s", f.Path)
		}
	}
	for _, s := range r.SkippedDirs {
		if strings.Contains(s.Path, "node_modules") {
			t.Errorf("node_modules skipped loudly: %s", s.Path)
		}
	}
}

func TestMigrateCommandArgs(t *testing.T) {
	migrateTree(t)
	out, err := runMigrate(t, "--dry-run", "payments", "legacy")
	if err != nil || !strings.HasPrefix(out, "payments/pacto.yaml: set pactoVersion") ||
		!strings.Contains(out, "\nlegacy/pacto.yaml:   pactoVersion \"1.0\"") ||
		!strings.HasSuffix(out, "1 rewritten, 0 current, 0 kept, 1 manual (dry run: nothing written)\n") {
		t.Fatalf("%v\n%s", err, out)
	}
}
