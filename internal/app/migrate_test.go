package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// simple is a valid contract at version v.
func simple(v string) string {
	return "pactoVersion: \"" + v + "\"\nservice: {name: s, version: 1.0.0}\n"
}

func migrate(t *testing.T, svc *Service, opts MigrateOptions) *MigrateResult {
	t.Helper()
	r, err := svc.Migrate(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// oneRow is the single row of r.
func oneRow(t *testing.T, r *MigrateResult) MigratedFile {
	t.Helper()
	if len(r.Files) != 1 {
		t.Fatalf("rows %+v", r.Files)
	}
	return r.Files[0]
}

// noTemp fails when dir holds a leftover temporary file.
func noTemp(t *testing.T, dir string) {
	t.Helper()
	if m, _ := filepath.Glob(filepath.Join(dir, ".pacto-tmp-*")); len(m) > 0 {
		t.Fatalf("left %v", m)
	}
}

func skipAsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}

func TestMigrateRaisesBehind(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple(prevVersion)})
	f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}}))
	want := `set pactoVersion to "` + contract.Current() + `" (was "` + prevVersion + `")`
	if f.Status != MigrateRewritten || f.Message != want || f.From != prevVersion || f.To != contract.Current() || f.Path != filepath.Join(dir, "s", "pacto.yaml") {
		t.Fatalf("%+v", f)
	}
	if got := read(t, filepath.Join(dir, "s", "pacto.yaml")); got != simple(contract.Current()) {
		t.Fatalf("file %q", got)
	}
}

func TestMigrateRewritesOnlyVersionToken(t *testing.T) {
	orig := "\xef\xbb\xbf# the payments service\r\npactoVersion: '2.3' # raised by pacto migrate\r\nservice: {name: s, version: 1.0.0}\r\n"
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": orig})
	if f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})); f.Status != MigrateRewritten {
		t.Fatalf("%+v", f)
	}
	want := string(bytes.Replace([]byte(orig), []byte("'2.3'"), []byte("'"+contract.Current()+"'"), 1))
	if got := read(t, filepath.Join(dir, "s", "pacto.yaml")); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMigrateKeepsWhenRaiseAddsFinding(t *testing.T) {
	dir := refPair(t, strictSchema, nil)
	before := read(t, filepath.Join(dir, "pacto.yaml"))
	f := oneRow(t, migrate(t, NewService(&mockBundleStore{}, nil), MigrateOptions{Roots: []string{dir}}))
	if f.Status != MigrateKept || f.Finding == nil || f.Finding.Code != "ENVIRONMENT_VALUES_MISSING" || !strings.HasPrefix(f.Message, "kept at 2.3: ENVIRONMENT_VALUES_MISSING at environments") {
		t.Fatalf("%+v", f)
	}
	if read(t, filepath.Join(dir, "pacto.yaml")) != before {
		t.Fatal("kept file changed")
	}
}

func TestMigrateKeepsWhenCheckCannotTell(t *testing.T) {
	failing := &mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) { return nil, errors.New("registry down") }}
	cases := map[string]struct {
		svc  *Service
		dir  func(t *testing.T) string
		want string
	}{
		"invalid": {NewService(nil, nil), func(t *testing.T) string {
			return writeTree(t, "s", map[string]string{"s/pacto.yaml": simple(prevVersion) + "bogus: 1\n"})
		}, "kept at " + prevVersion + ": "},
		"oci ref unresolvable": {NewService(failing, nil), func(t *testing.T) string {
			return writeTree(t, "s", map[string]string{
				"s/pacto.yaml": "pactoVersion: \"2.3\"\nservice: {name: s, version: 1.0.0}\nconfigurations:\n  - {name: db, ref: oci://ghcr.io/acme/pg:1.0.0, required: false}\n",
			})
		}, "kept at 2.3: "},
		"no resolver": {NewService(nil, nil), func(t *testing.T) string { return refPair(t, strictSchema, nil) }, "kept at 2.3: "},
		"not yaml": {NewService(nil, nil), func(t *testing.T) string {
			return writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: [\n"})
		}, "kept: "},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := tc.dir(t)
			before := read(t, filepath.Join(dir, "pacto.yaml"))
			f := oneRow(t, migrate(t, tc.svc, MigrateOptions{Roots: []string{dir}}))
			if f.Status != MigrateKept || !strings.HasPrefix(f.Message, tc.want) || len(f.Message) == len(tc.want) {
				t.Fatalf("%+v", f)
			}
			if read(t, filepath.Join(dir, "pacto.yaml")) != before {
				t.Fatal("kept file changed")
			}
		})
	}
}

func TestMigrateKeepsUnknownVersion(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple("3.0")})
	f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}}))
	if f.Status != MigrateKept || !strings.HasPrefix(f.Message, "kept at 3.0: ") || read(t, filepath.Join(dir, "s", "pacto.yaml")) != simple("3.0") {
		t.Fatalf("%+v", f)
	}
}

func TestMigrateManualFor1x(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple("1.0")})
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})
	f := oneRow(t, r)
	want := `pactoVersion "1.0" is not migrated automatically; see ` + contract.MigrationGuideURL
	if f.Status != MigrateManual || f.Message != want || r.Manual != 1 || read(t, filepath.Join(dir, "s", "pacto.yaml")) != simple("1.0") {
		t.Fatalf("%+v", f)
	}
}

func TestMigrateAlreadyCurrent(t *testing.T) {
	cases := map[string]string{
		simple(contract.Current()):             "already current (" + contract.Current() + ")",
		"service: {name: s, version: 1.0.0}\n": "already current (no pactoVersion; read as " + contract.Current() + ")",
	}
	for doc, want := range cases {
		dir := writeTree(t, "", map[string]string{"s/pacto.yaml": doc})
		f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}}))
		if f.Status != MigrateCurrent || f.Message != want || read(t, filepath.Join(dir, "s", "pacto.yaml")) != doc {
			t.Fatalf("%q: %+v", doc, f)
		}
	}
}

func TestMigrateToOlderTarget(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"old/pacto.yaml": simple("2.0"), "cur/pacto.yaml": simple(contract.Current())})
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}, To: prevVersion})
	if r.To != prevVersion || len(r.Files) != 2 {
		t.Fatalf("%+v", r)
	}
	cur, old := r.Files[0], r.Files[1]
	if cur.Status != MigrateCurrent || cur.Message != `already at or past "`+prevVersion+`" (`+contract.Current()+`)` {
		t.Fatalf("%+v", cur)
	}
	if old.Status != MigrateRewritten || read(t, filepath.Join(dir, "old", "pacto.yaml")) != simple(prevVersion) {
		t.Fatalf("%+v", old)
	}
}

func TestMigrateRejectsUnknownTarget(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple(prevVersion)})
	for _, to := range []string{"9.9", "1.0"} {
		r, err := NewService(nil, nil).Migrate(context.Background(), MigrateOptions{Roots: []string{dir}, To: to})
		if r != nil || err == nil || !strings.Contains(err.Error(), `--to "`+to+`" is not a supported pactoVersion; supported: `+strings.Join(contract.SupportedVersions, ", ")) {
			t.Fatalf("%s: %+v %v", to, r, err)
		}
	}
}

func TestMigrateDryRunWritesNothing(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"a/pacto.yaml": simple(prevVersion), "b/pacto.yaml": simple("1.0")})
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}, DryRun: true})
	if !r.DryRun || r.Rewritten != 1 || r.Manual != 1 || read(t, filepath.Join(dir, "a", "pacto.yaml")) != simple(prevVersion) {
		t.Fatalf("%+v", r)
	}
}

func TestMigrateKeepsSymlink(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/real.yaml": simple(prevVersion)})
	link := filepath.Join(dir, "s", "pacto.yaml")
	if err := os.Symlink("real.yaml", link); err != nil {
		t.Fatal(err)
	}
	f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}}))
	if f.Status != MigrateKept || f.Message != "kept at "+prevVersion+": pacto.yaml is a symbolic link; edit it by hand" {
		t.Fatalf("%+v", f)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 || read(t, link) != simple(prevVersion) {
		t.Fatalf("link replaced: %v", err)
	}
}

func TestMigrateKeepsFileMode(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple(prevVersion)})
	p := filepath.Join(dir, "s", "pacto.yaml")
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})); f.Status != MigrateRewritten {
		t.Fatalf("%+v", f)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
}

func TestMigrateRefusesReadOnlyFile(t *testing.T) {
	skipAsRoot(t)
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple(prevVersion)})
	p := filepath.Join(dir, "s", "pacto.yaml")
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})
	if f := oneRow(t, r); f.Status != MigrateFailed || f.Message != "cannot write: permission denied" || r.Failed != 1 {
		t.Fatalf("%+v", f)
	}
	after, _ := os.Stat(p)
	if !os.SameFile(before, after) || read(t, p) != simple(prevVersion) {
		t.Fatal("read-only file replaced")
	}
	noTemp(t, filepath.Dir(p))
}

func TestMigrateWriteFailures(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]struct {
		set  func(t *testing.T, svc *Service, dir string)
		want string
	}{
		"read-only folder": {func(t *testing.T, _ *Service, dir string) {
			skipAsRoot(t)
			if err := os.Chmod(dir, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		}, "cannot write: permission denied"},
		"write": {func(_ *testing.T, svc *Service, _ string) {
			svc.sys.writeFile = func(string, []byte, os.FileMode) error { return boom }
		}, "cannot write: boom"},
		"rename": {func(_ *testing.T, svc *Service, _ string) {
			svc.sys.rename = func(string, string) error { return boom }
		}, "cannot write: boom"},
		"abs path": {func(_ *testing.T, svc *Service, _ string) {
			svc.sys.absPath = func(string) (string, error) { return "", boom }
		}, "cannot read: boom"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, "", map[string]string{"s/pacto.yaml": simple(prevVersion)})
			dir := filepath.Join(root, "s")
			svc := NewService(nil, nil)
			tc.set(t, svc, dir)
			r := migrate(t, svc, MigrateOptions{Roots: []string{root}})
			if f := oneRow(t, r); f.Status != MigrateFailed || f.Message != tc.want || r.Failed != 1 {
				t.Fatalf("%+v", f)
			}
			if read(t, filepath.Join(dir, "pacto.yaml")) != simple(prevVersion) {
				t.Fatal("original changed")
			}
			noTemp(t, dir)
		})
	}
}

func TestMigrateUnreadable(t *testing.T) {
	skipAsRoot(t)
	dir := writeTree(t, "", map[string]string{
		"a/pacto.yaml":        simple(prevVersion),
		"locked/x/pacto.yaml": simple(prevVersion),
		"secret/pacto.yaml":   simple(prevVersion),
		// a .pactoignore that cannot be read below the root
		"svc/pacto.yaml":               simple(prevVersion),
		"svc/.pactoignore/placeholder": "",
	})
	for _, p := range []string{"locked", "secret/pacto.yaml"} {
		if err := os.Chmod(filepath.Join(dir, p), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, p), 0o755) })
	}
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})
	got := map[string]MigratedFile{}
	for _, f := range r.Files {
		got[strings.TrimPrefix(f.Path, dir+string(filepath.Separator))] = f
	}
	if got["a/pacto.yaml"].Status != MigrateRewritten || r.Failed != 3 {
		t.Fatalf("%+v", r.Files)
	}
	for _, p := range []string{"locked", "secret/pacto.yaml", "svc/pacto.yaml"} {
		if f := got[p]; f.Status != MigrateFailed || !strings.HasPrefix(f.Message, "cannot read") || strings.Contains(f.Message, dir) {
			t.Fatalf("%s: %+v", p, f)
		}
	}
}

func TestMigrateMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nope")
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{root}})
	if f := oneRow(t, r); f.Path != root || f.Status != MigrateFailed || f.Message != "cannot read: no such file or directory" || r.NotFound != nil {
		t.Fatalf("%+v", r)
	}
}

func TestMigrateWalkErrors(t *testing.T) {
	// A root .pactoignore that cannot be read fails the whole walk of that root.
	dir := writeTree(t, "", map[string]string{".pactoignore/x": "", "s/pacto.yaml": simple(prevVersion)})
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})
	if f := oneRow(t, r); f.Path != dir || f.Status != MigrateFailed || f.Message != "cannot read .pactoignore: is a directory" {
		t.Fatalf("%+v", f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, err := NewService(nil, nil).Migrate(ctx, MigrateOptions{Roots: []string{t.TempDir()}}); r != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestMigrateNoContractsFound(t *testing.T) {
	dir := t.TempDir()
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})
	if len(r.Files) != 0 || !reflect.DeepEqual(r.NotFound, []string{dir}) {
		t.Fatalf("%+v", r)
	}
	t.Chdir(dir)
	if r := migrate(t, NewService(nil, nil), MigrateOptions{}); !reflect.DeepEqual(r.NotFound, []string{"."}) || r.To != contract.Current() {
		t.Fatalf("no roots: %+v", r)
	}
	noAbs := NewService(nil, nil) // a root that cannot be made absolute holds nothing seen
	noAbs.sys.absPath = func(string) (string, error) { return "", errors.New("no working directory") }
	if r := migrate(t, noAbs, MigrateOptions{Roots: []string{dir}}); !reflect.DeepEqual(r.NotFound, []string{dir}) {
		t.Fatalf("no abs path: %+v", r)
	}
}

func TestMigrateReportsSkipped(t *testing.T) {
	deep := "d/1/2/3/4/5/6/7/8/pacto.yaml"
	dir := writeTree(t, "", map[string]string{".pactoignore": "archive/\n", "archive/old/pacto.yaml": simple(prevVersion), deep: simple(prevVersion)})
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}})
	want := []SkippedDir{{filepath.Join(dir, "archive"), ".pactoignore:1"}, {filepath.Join(dir, "d/1/2/3/4/5/6/7/8"), "depth limit 8"}}
	if !reflect.DeepEqual(r.SkippedDirs, want) || r.Skipped != 2 || len(r.Files) != 0 || r.NotFound == nil {
		t.Fatalf("%+v", r)
	}
	for _, p := range []string{"archive/old/pacto.yaml", deep} {
		if read(t, filepath.Join(dir, p)) != simple(prevVersion) {
			t.Fatalf("%s changed", p)
		}
	}
}

func TestMigrateDeduplicatesOverlappingRoots(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"payments/pacto.yaml": simple(prevVersion)})
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir, filepath.Join(dir, "payments")}, DryRun: true})
	if f := oneRow(t, r); f.Path != filepath.Join(dir, "payments", "pacto.yaml") || r.NotFound != nil {
		t.Fatalf("%+v", r)
	}
}

func TestMigrateDeterministic(t *testing.T) {
	files := map[string]string{"a/pacto.yaml": simple(prevVersion), "b/pacto.yaml": simple("1.0"), "c/pacto.yaml": simple(contract.Current()), "d/pacto.yaml": simple("3.0")}
	one, two := writeTree(t, "", files), writeTree(t, "", files)
	migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{one}})
	migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{two}})
	for p := range files {
		if read(t, filepath.Join(one, p)) != read(t, filepath.Join(two, p)) {
			t.Fatalf("%s differs", p)
		}
	}
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{one}})
	if r.Rewritten != 0 || r.Current != 2 || r.Kept != 1 || r.Manual != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestMigrateSummary(t *testing.T) {
	cases := []struct {
		r    MigrateResult
		want string
	}{
		{MigrateResult{Rewritten: 1, Current: 1, Kept: 1, Manual: 1}, "1 rewritten, 1 current, 1 kept, 1 manual"},
		{MigrateResult{Rewritten: 3, Skipped: 2, Failed: 1, DryRun: true}, "3 rewritten, 0 current, 0 kept, 0 manual, 2 skipped, 1 failed (dry run: nothing written)"},
	}
	for _, tc := range cases {
		if got := tc.r.Summary(); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
}

func TestMigrateDryRunPredictsReadOnlyRefusal(t *testing.T) {
	skipAsRoot(t)
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple(prevVersion)})
	if err := os.Chmod(filepath.Join(dir, "s", "pacto.yaml"), 0o444); err != nil {
		t.Fatal(err)
	}
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{dir}, DryRun: true})
	if f := oneRow(t, r); f.Status != MigrateFailed || f.Message != "cannot write: permission denied" || r.Failed != 1 {
		t.Fatalf("%+v", f)
	}
}

func TestMigrateRoots(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"s/pacto.yaml": simple(prevVersion), "s/notes.md": "x\n"})
	cases := map[string]MigratedFile{
		filepath.Join(dir, "s", "pacto.yaml"): {Path: filepath.Join(dir, "s", "pacto.yaml"), Status: MigrateRewritten},
		filepath.Join(dir, "s", "notes.md"):   {Path: filepath.Join(dir, "s", "notes.md"), Status: MigrateFailed, Message: "not a directory; name the folder that holds pacto.yaml"},
		"oci://ghcr.io/x/y":                   {Path: "oci://ghcr.io/x/y", Status: MigrateFailed, Message: "pacto migrate rewrites local folders, not OCI references"},
	}
	for root, want := range cases {
		f := oneRow(t, migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{root}, DryRun: true}))
		if f.Path != want.Path || f.Status != want.Status || (want.Message != "" && f.Message != want.Message) {
			t.Fatalf("%s: %+v", root, f)
		}
	}
}

// TestMigrateExplicitRootOverridesEarlierCut: a folder an earlier root's
// .pactoignore cuts and a later root names is migrated, not also skipped.
func TestMigrateExplicitRootOverridesEarlierCut(t *testing.T) {
	dir := writeTree(t, "", map[string]string{"ign/.pactoignore": "sub/\n", "ign/sub/pacto.yaml": simple(prevVersion)})
	ign := filepath.Join(dir, "ign")
	r := migrate(t, NewService(nil, nil), MigrateOptions{Roots: []string{ign, filepath.Join(ign, "sub")}, DryRun: true})
	if r.Rewritten != 1 || r.Skipped != 0 || len(r.SkippedDirs) != 0 || len(r.NotFound) != 0 {
		t.Fatalf("%+v", r)
	}
}
