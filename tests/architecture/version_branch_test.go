package architecture

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// versionBranch matches code that decides something from a pactoVersion value:
// the supported-version list, the olderThan helper or a literal comparison.
var versionBranch = regexp.MustCompile(`contract\.SupportedVersions|olderThan\(|[Pp]acto[Vv]ersion\s*(==|!=)\s*"|"\s*(==|!=)\s*[\w.]*[Pp]acto[Vv]ersion\b`)

// versionBranchAllowlist names files that may still branch on pactoVersion
// outside pkg/validation and pkg/contract, each with why. A stale entry fails.
var versionBranchAllowlist = map[string]string{
	"internal/pactify/contract.go": "a hint that names the version publish needs, not a meaning rule; " +
		"added by 513-S1, flagged to its owner",
}

// TestVersionBranchesLiveInValidation keeps every rule that depends on
// pactoVersion where the standard upgrade check sees it: a raise is judged by
// validation, so a branch elsewhere could change meaning unnoticed.
func TestVersionBranchesLiveInValidation(t *testing.T) {
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files", "*.go").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	matched := map[string]bool{}
	for _, file := range strings.Fields(string(out)) {
		if strings.HasSuffix(file, "_test.go") || strings.HasPrefix(file, "pkg/dashboard/ui/") ||
			strings.HasPrefix(file, "pkg/validation/") || strings.HasPrefix(file, "pkg/contract/") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, file)) //nolint:gosec // a tracked file under the repository root
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if !versionBranch.MatchString(line) {
				continue
			}
			if _, ok := versionBranchAllowlist[file]; ok {
				matched[file] = true
				continue
			}
			t.Errorf("%s:%d branches on pactoVersion outside pkg/validation: %s", file, i+1, strings.TrimSpace(line))
		}
	}
	for file := range versionBranchAllowlist {
		if !matched[file] {
			t.Errorf("allowlist entry %s matches nothing; delete it", file)
		}
	}
}
