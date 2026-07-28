package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// staleOperatorRepoRE matches the archived operator GitHub REPO url. Active
// source/support links must point at the monorepo, never the archived repo. The
// chart NAME `pacto-operator` is still used (Chart.yaml + the Artifact Hub
// listing), but the ghcr registry path now lives under the `pacto` namespace.
var staleOperatorRepoRE = regexp.MustCompile(`github\.com/[Tt]riana[Ll]ab/pacto-operator`)

// isHistoricalRef lists the paths permitted to reference the old repo for
// history/migration reasons (changesets and the verbatim v4 upgrade fixture).
// Everywhere else an active source/support link to the archived operator repo is
// a defect: active links must point at the monorepo.
func isHistoricalRef(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, p := range []string{
		".changeset/",
		// A faithful byte-for-byte snapshot of the published v4 chart (the real
		// v4->v5 upgrade fixture); it MUST keep v4's original repo links.
		"tests/e2e/kind/fixtures/pacto-operator-v4/",
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
// historical/migration references.
func TestNoStaleOperatorRepoLinks(t *testing.T) {
	root := repoRoot(t)
	textExt := map[string]bool{
		".md": true, ".yaml": true, ".yml": true, ".go": true, ".mjs": true,
		".sh": true, ".txt": true, ".json": true, ".gotmpl": true, ".tpl": true, "": true,
	}

	// Only git-TRACKED files are part of the PR; scratch/untracked files (a local
	// .pr-body.md draft, editor temp files) are not the gate's concern.
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	for _, rel := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if rel == "" || strings.HasPrefix(rel, "pkg/dashboard/ui/") { // built (minified) assets
			continue
		}
		if isHistoricalRef(rel) || !textExt[filepath.Ext(rel)] {
			continue
		}
		b, e := os.ReadFile(filepath.Join(root, rel))
		if e != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if staleOperatorRepoRE.MatchString(line) {
				t.Errorf("%s:%d references the archived operator repo as an active link — point it at the monorepo (or move it under a historical doc): %s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}
