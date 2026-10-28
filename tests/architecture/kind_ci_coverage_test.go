package architecture

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// e2eFilter returns the path globs of the `e2e` filter in ci.yml: the paths
// whose change runs the kind and Compose shards.
func pathsFilter(t *testing.T, name string) []string {
	t.Helper()
	wf := code(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	_, after, ok := strings.Cut(wf, "\n            "+name+":\n")
	if !ok {
		t.Fatalf("no `%s:` paths filter in .github/workflows/ci.yml", name)
	}
	var globs []string
	for line := range strings.SplitSeq(after, "\n") {
		item, isItem := strings.CutPrefix(strings.TrimSpace(line), "- ")
		if !isItem {
			break
		}
		globs = append(globs, strings.Trim(item, "'\""))
	}
	return globs
}

// TestPathFiltersCoverTheOperatorImports fails when a core package compiled
// into the operator or pacto-delivery image is missing from the e2e or the
// kubernetes filter. A change to such a package alone would otherwise skip
// every kind shard, or the operator's unit tests, coverage gate and envtest,
// and the `required` gate counts a skipped job as passing.
func TestPathFiltersCoverTheOperatorImports(t *testing.T) {
	const core = "github.com/trianalab/pacto/v4/"
	imports := deps(t, "github.com/trianalab/pacto/integrations/kubernetes/v6/...")
	for _, name := range []string{"e2e", "kubernetes"} {
		globs := pathsFilter(t, name)
		for _, dep := range imports {
			rel, ok := strings.CutPrefix(dep, core)
			if !ok {
				continue
			}
			covered := false
			for _, g := range globs {
				dir, isTree := strings.CutSuffix(g, "/**")
				if isTree && (rel == dir || strings.HasPrefix(rel, dir+"/")) {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("the operator compiles in %s, but the ci.yml %s filter has no '%s/**' entry, so a change there alone skips its jobs", rel, name, rel)
			}
		}
	}
}

// infraCall matches a call to lib.sh's infra wrapper, at the start of a line or
// after a `do`, `;` or `&&`.
var infraCall = regexp.MustCompile(`(^|[;&|]\s*|\bdo\s+)infra\s`)

// TestPactoInstallsNeverGoThroughInfra keeps the rule written above lib.sh's
// infra(): it retries and labels a failure "infrastructure", so a Pacto chart
// install or rollout wrapped in it hides a product flake behind a retry.
func TestPactoInstallsNeverGoThroughInfra(t *testing.T) {
	forbidden := []string{"helm", "pacto-operator", "pacto-delivery", "pacto-node-agent"}
	paths := append(scenarios(t), filepath.Join(kindDir(t), "lib.sh"))
	for _, path := range paths {
		for line := range strings.SplitSeq(code(t, path), "\n") {
			loc := infraCall.FindStringIndex(line)
			if loc == nil {
				continue
			}
			for _, word := range forbidden {
				if strings.Contains(line[loc[1]:], word) {
					t.Errorf("%s: an infra call mentions %q; Pacto installs and rollouts must run unwrapped: %s",
						filepath.Base(path), word, strings.TrimSpace(line))
				}
			}
		}
	}
}
