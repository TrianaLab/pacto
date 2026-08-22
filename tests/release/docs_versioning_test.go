package release

import (
	"os"
	"os/exec"
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

// The documentation site serves its own Mermaid rather than fetching one from a
// CDN, and release/scripts/mkdocs_mermaid_hook.py fails the build closed when the
// runtime is missing. That prerequisite is a real dependency of BUILDING the site,
// not a convenience of one gate: any supported entry point that reaches mkdocs or
// mike without it aborts on a clean checkout.
//
// It has already been wrong once. The PR gate installed the runtime while
// `docs-serve`, `docs-deploy` and the strict build in docs.yml did not, so the PR
// went green while the post-merge validation and the versioned release publisher
// would both have failed before building a single page. The gates below are about
// the paths nobody runs on a developer laptop, where node_modules is already there
// and every one of them passes regardless.

// mermaidRuntimeInstall is the one command that puts the pinned runtime on disk —
// the recipe of $(MERMAID_RUNTIME) in the Makefile. Matched loosely enough to
// survive a change of npm flags, strictly enough to mean the frontend's lockfile.
const mermaidRuntimeInstall = "pkg/dashboard/frontend && npm ci"

// makeDryRun returns what `make -n -B <target>` WOULD run. -B forces every
// prerequisite to be considered out of date, so a node_modules that already exists
// locally cannot hide a missing dependency, and -n prints without executing. This
// interrogates the dependency GRAPH: a target that loses the prerequisite fails
// here even while the words are still somewhere in the Makefile.
func makeDryRun(t *testing.T, root, target string) string {
	t.Helper()
	cmd := exec.Command("make", "-n", "-B", target)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n -B %s: %v\n%s", target, err, out)
	}
	return string(out)
}

func TestEveryDocsEntryPointInstallsTheDiagramRuntime(t *testing.T) {
	root := repoRoot(t)
	// Every repository-owned way to build, preview, gate or publish the site.
	for _, target := range []string{
		"docs", "docs-build", "docs-serve", "docs-check",
		"docs-build-strict", "docs-deploy", "test-browser-docs-site",
	} {
		t.Run(target, func(t *testing.T) {
			if !strings.Contains(makeDryRun(t, root, target), mermaidRuntimeInstall) {
				t.Errorf("`make %s` reaches the site without installing the pinned Mermaid "+
					"runtime; on a clean checkout it aborts in mkdocs_mermaid_hook.py. "+
					"Add $(MERMAID_RUNTIME) to its prerequisites.", target)
			}
		})
	}
}

func TestTheDiagramRuntimeIsInstalledBeforeTheSiteIsBuilt(t *testing.T) {
	root := repoRoot(t)
	// Presence is not enough: the install has to be SCHEDULED first. These two are
	// the paths the PR gate never exercises — the local preview and the release
	// publisher release.yml drives.
	for _, tc := range []struct{ target, builder string }{
		{"docs-serve", "mkdocs serve"},
		{"docs-deploy", "mike deploy"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			out := makeDryRun(t, root, tc.target)
			install := strings.Index(out, mermaidRuntimeInstall)
			build := strings.Index(out, tc.builder)
			if install < 0 || build < 0 || install > build {
				t.Errorf("`make -n -B %s` must schedule the Mermaid runtime install before %q "+
					"(install at %d, %s at %d); recipe:\n%s",
					tc.target, tc.builder, install, tc.builder, build, out)
			}
		})
	}
}

// directSiteBuilds returns the lines of a workflow that run `mkdocs` or `mike` as a
// shell command. Comments and step names are skipped: both mention the tools as
// prose, and prose cannot bypass a Make prerequisite.
func directSiteBuilds(workflow string) []string {
	cmd := regexp.MustCompile(`(?:^|[\s;&|(])(?:mkdocs|mike)\s`)
	var out []string
	for _, line := range strings.Split(workflow, "\n") {
		t := strings.TrimSpace(line)
		t = strings.TrimPrefix(t, "- ")
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "name:") {
			continue
		}
		if cmd.MatchString(t) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestDocsWorkflowsBuildTheSiteThroughMake(t *testing.T) {
	root := repoRoot(t)
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(b)
	}

	// A workflow that calls mkdocs or mike itself has stepped around the Make target
	// that owns the runtime prerequisite, which is exactly how the defect shipped.
	// Going through make is also what keeps the install expressed ONCE, instead of an
	// `npm ci` copy-pasted into every job that touches the docs.
	for _, wf := range []string{".github/workflows/docs.yml", ".github/workflows/release.yml"} {
		if direct := directSiteBuilds(read(wf)); len(direct) > 0 {
			t.Errorf("%s invokes the site builder directly, bypassing the Make target that "+
				"installs the pinned Mermaid runtime:\n\t%s", wf, strings.Join(direct, "\n\t"))
		}
	}

	// ...and each still reaches the site through its own guarded target.
	if !strings.Contains(read(".github/workflows/docs.yml"), "make docs-build-strict") {
		t.Error("docs.yml must run its strict validation via `make docs-build-strict`")
	}
	if !regexp.MustCompile(`make docs-deploy(\s|$)`).MatchString(read(".github/workflows/release.yml")) {
		t.Error("release.yml must publish the versioned site via the guarded `make docs-deploy`")
	}

	// docs.yml is the post-merge net for precisely this failure. If a change to the
	// hook, to the lockfile that pins the runtime bytes or to the Make wiring that
	// installs them cannot trigger it, the net has a hole shaped like the defect.
	paths := read(".github/workflows/docs.yml")
	for _, p := range []string{
		"release/scripts/mkdocs_mermaid_hook.py",
		"pkg/dashboard/frontend/package.json",
		"pkg/dashboard/frontend/package-lock.json",
		"Makefile",
		"ci.mk",
	} {
		if !strings.Contains(paths, "'"+p+"'") {
			t.Errorf("docs.yml path filter is missing %q; a change there can break the strict "+
				"build with no post-merge validation to catch it", p)
		}
	}
}
