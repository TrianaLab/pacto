package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pacto ci raises an older pactoVersion in memory when that keeps the
// contract's meaning, so no guide asks a reader to gate on a migrate check.
func TestNoGuideRunsMigrateCheck(t *testing.T) {
	root := repoRoot(t)
	scan := func(dir string, keep func(string) bool) {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "superpowers" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if d.IsDir() || !keep(p) {
				return nil
			}
			raw, err := os.ReadFile(p) //nolint:gosec // a path under the repository root
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), "migrate --check") {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("%s runs migrate --check: pacto ci migrates an older contract in memory", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	md := func(p string) bool { return filepath.Ext(p) == ".md" }
	scan("docs", md)
	scan("examples", md)
	scan(filepath.Join("cmd", "gendocs", "prose"), func(string) bool { return true })
	scan(filepath.Join(".github", "workflows"), func(p string) bool { return filepath.Ext(p) == ".yml" })
	for _, f := range []string{"README.md", "MIGRATION.md"} {
		if strings.Contains(readRepoFile(t, f), "migrate --check") {
			t.Errorf("%s runs migrate --check", f)
		}
	}
}

// The ci guide documents in-memory migration and the fleet reference names
// the inferred migratedFrom key (508-S8's rewrite keeps both).
func TestCIGuideDocumentsInMemoryMigration(t *testing.T) {
	ci := readRepoFile(t, "docs", "ci.md")
	for _, want := range []string{"migrated in memory", "pacto.inferred.yaml"} {
		if !strings.Contains(ci, want) {
			t.Errorf("docs/ci.md does not mention %q", want)
		}
	}
	if !strings.Contains(readRepoFile(t, "docs", "contract-reference", "validation.md"), "migratedFrom") {
		t.Error("docs/contract-reference/validation.md does not document migratedFrom")
	}
}
