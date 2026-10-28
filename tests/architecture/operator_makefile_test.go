package architecture

import (
	"regexp"
	"strings"
	"testing"
)

// makeRecipe returns the recipe lines of one target in a Makefile, joined with
// their backslash continuations, so a rule can read the command as make runs it.
func makeRecipe(t *testing.T, makefile, target string) string {
	t.Helper()
	var recipe []string
	in := false
	for line := range strings.SplitSeq(makefile, "\n") {
		switch {
		case strings.HasPrefix(line, target+":"):
			in = true
		case in && strings.HasPrefix(line, "\t"):
			recipe = append(recipe, strings.TrimSuffix(strings.TrimSpace(line), "\\"))
		case in:
			return strings.Join(recipe, " ")
		}
	}
	if !in {
		t.Fatalf("the Makefile has no %s target", target)
	}
	return strings.Join(recipe, " ")
}

// The operator Dockerfile COPYs go.work, the engine module and the operator
// module, so its build context is the repository root. A local docker-build
// that passes `.` (integrations/kubernetes) fails at the first COPY, and every
// deploy and helm target that depends on it fails with it.
func TestTheOperatorDockerBuildUsesTheRepositoryRootContext(t *testing.T) {
	mk := readRepoFile(t, "integrations", "kubernetes", "Makefile")
	recipe := makeRecipe(t, mk, "docker-build")
	if !regexp.MustCompile(`build -f Dockerfile\b`).MatchString(recipe) {
		t.Errorf("docker-build must name the operator Dockerfile with -f, got: %s", recipe)
	}
	if !regexp.MustCompile(`\s\.\./\.\.\s*$`).MatchString(recipe) {
		t.Errorf("docker-build must pass the repository root (../..) as the build context, got: %s", recipe)
	}
	if !strings.Contains(recipe, "DASHBOARD_IMAGE=") {
		t.Errorf("docker-build dropped the DASHBOARD_IMAGE build-arg, got: %s", recipe)
	}
}
