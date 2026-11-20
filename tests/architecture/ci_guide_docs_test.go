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

// The CI recipe tolerates exit 2 (a BREAKING change with compatible consumers),
// so it must fail a BREAKING change whose answer is partial itself: an
// unverified or unreachable target snapshot lists no consumers. Every
// read-audience warning names the evidence the snapshot carries, cost included.
func TestCIGuideImpactRecipeRefusesPartial(t *testing.T) {
	if !strings.Contains(readRepoFile(t, "docs", "ci.md"), `jq -e '.classification != "BREAKING" or .completeness == "complete"' impact.json`) {
		t.Error("docs/ci.md: the impact recipe does not fail a partial BREAKING answer")
	}
	for _, path := range [][]string{
		{"docs", "ci.md"},
		{"integrations", "kubernetes", "docs", "delivery-records.md"},
		{"integrations", "kubernetes", "charts", "pacto-operator", "templates", "NOTES.txt"},
	} {
		if !strings.Contains(strings.Join(strings.Fields(readRepoFile(t, path...)), " "), "provisioning and usage record (cost included)") {
			t.Errorf("%s: the pacto-targets read audience does not name the evidence it carries", strings.Join(path, "/"))
		}
	}
}
