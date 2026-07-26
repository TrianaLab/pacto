package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// staleOperatorRepoRE matches the archived operator GitHub REPO url. It does not
// match the preserved public coordinates that keep the `pacto-operator` name —
// the image/chart registry path (ghcr.io/trianalab/pacto-operator/…) and the
// Artifact Hub repository (artifacthub.io/…/pacto-operator) — which are correct.
var staleOperatorRepoRE = regexp.MustCompile(`github\.com/[Tt]riana[Ll]ab/pacto-operator`)

// isHistoricalRef lists the paths permitted to reference the old repo for
// history/migration reasons (ADRs, changesets, the migration record, proofs).
// Everywhere else an active source/support link to the archived operator repo is
// a defect (release-safety item 12): active links must point at the monorepo.
func isHistoricalRef(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range []string{
		"docs/adr/",
		".changeset/",
		"release/MIGRATION-STATE.md",
		"release/proofs/",
		"release/artifact-pipeline-ledger.json",
		// This gate's own source names the pattern it forbids.
		"tests/release/stale_links_test.go",
	} {
		if strings.HasPrefix(rel, p) || rel == p {
			return true
		}
	}
	return strings.Contains(rel, "CHANGELOG")
}

// TestNoStaleOperatorRepoLinks fails if any tracked source references the
// archived operator repo as an ACTIVE link, distinguishing that from permitted
// historical/migration references (release-safety item 12).
func TestNoStaleOperatorRepoLinks(t *testing.T) {
	root := repoRoot(t)
	skipDir := map[string]bool{".git": true, "node_modules": true, ".gocache": true, "dist": true}
	textExt := map[string]bool{
		".md": true, ".yaml": true, ".yml": true, ".go": true, ".mjs": true,
		".sh": true, ".txt": true, ".json": true, ".gotmpl": true, ".tpl": true, "": true,
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "pkg/dashboard/ui/") { // built (minified) assets
			return nil
		}
		if isHistoricalRef(rel) || !textExt[filepath.Ext(path)] {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if staleOperatorRepoRE.MatchString(line) {
				t.Errorf("%s:%d references the archived operator repo as an active link — point it at the monorepo (or move it under a historical doc): %s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
