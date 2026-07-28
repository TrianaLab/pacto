package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Release-safety item 6: a non-release main-push (docs.yml) must NEVER deploy the
// versioned public site. It only validates the docs (strict build); the release
// transaction (release.yml unit k8s-docs) is the sole publisher of the versioned
// site and the only thing that moves the `latest` alias. docs.yml no longer
// publishes an unreleased `next` snapshot: that cluttered the public version
// selector (and was the fallback the selector landed on). With this gate green,
// merging a breaking PR before its Version PR releases cannot alter stable docs,
// move latest, or add a `next` entry to the version dropdown.

// makeRecipe returns the recipe lines of a make target from a Makefile-like text.
func makeRecipe(text, target string) string {
	lines := strings.Split(text, "\n")
	var out []string
	in := false
	head := regexp.MustCompile(`^([A-Za-z0-9_.-]+):`)
	for _, l := range lines {
		if strings.HasPrefix(l, target+":") {
			in = true
			continue
		}
		if in {
			if strings.HasPrefix(l, "\t") || strings.TrimSpace(l) == "" {
				out = append(out, l)
				continue
			}
			if head.MatchString(l) { // next target
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

func TestDocsDevDeployNeverMovesLatest(t *testing.T) {
	root := repoRoot(t)
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(b)
	}
	ci := read("ci.mk")

	// The RELEASE docs-deploy target is the only one that moves the latest alias.
	rel := makeRecipe(ci, "docs-deploy")
	if !strings.Contains(rel, "latest") {
		t.Errorf("the RELEASE docs-deploy target must move the latest alias; recipe:\n%s", rel)
	}

	// The unreleased `next` preview was removed, so its deploy target must be gone.
	if makeRecipe(ci, "docs-deploy-dev") != "" {
		t.Error("ci.mk must no longer define docs-deploy-dev (the `next` preview was removed)")
	}

	// The non-release workflow must NOT deploy the site at all: no `mike deploy`,
	// no release docs-deploy, and no leftover dev-deploy call. It only validates.
	docs := read(".github/workflows/docs.yml")
	if strings.Contains(docs, "mike deploy") {
		t.Error("docs.yml (non-release) must not run `mike deploy` (deploys are release-only)")
	}
	if strings.Contains(docs, "docs-deploy-dev") {
		t.Error("docs.yml must not call docs-deploy-dev (the `next` preview was removed)")
	}
	if regexp.MustCompile(`make docs-deploy(\s|$)`).MatchString(docs) {
		t.Error("docs.yml (non-release) must NOT call the release `make docs-deploy` (it moves latest)")
	}
	if strings.Contains(docs, "release/release-manifest.json") {
		t.Error("docs.yml must not trigger on release/release-manifest.json (that double-fires with the release docs job)")
	}

	relwf := read(".github/workflows/release.yml")
	if !regexp.MustCompile(`make docs-deploy(\s|$)`).MatchString(relwf) {
		t.Error("release.yml docs job must call the release `make docs-deploy`")
	}
}
