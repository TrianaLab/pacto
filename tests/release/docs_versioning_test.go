package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Release-safety item 6: dev docs (a non-release main-push) must publish only an
// unreleased `next` snapshot — never move `latest` and never write a released
// version slot. Only the release transaction (release.yml unit k8s-docs) deploys
// the exact released version + moves latest. This gate encodes the scenario of
// merging this breaking PR while the manifest still names the old stable version:
// with it green, that merge (docs.yml) cannot alter stable docs or the latest alias.

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

	dev := makeRecipe(ci, "docs-deploy-dev")
	if dev == "" {
		t.Fatal("ci.mk has no docs-deploy-dev target")
	}
	if strings.Contains(dev, "latest") || strings.Contains(dev, "update-aliases") {
		t.Errorf("docs-deploy-dev must NOT move latest or use --update-aliases; recipe:\n%s", dev)
	}
	if !strings.Contains(dev, "next") {
		t.Errorf("docs-deploy-dev must deploy the `next` snapshot; recipe:\n%s", dev)
	}

	rel := makeRecipe(ci, "docs-deploy")
	if !strings.Contains(rel, "latest") {
		t.Errorf("the RELEASE docs-deploy target must move the latest alias; recipe:\n%s", rel)
	}

	// The non-release workflow must call the dev target; the release workflow the
	// release target.
	docs := read(".github/workflows/docs.yml")
	if !strings.Contains(docs, "make docs-deploy-dev") {
		t.Error("docs.yml (non-release) must call `make docs-deploy-dev`")
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
