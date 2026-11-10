package architecture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot resolves the repository root relative to this file, so no gate
// depends on the working directory the test runs from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// readRepoFile reads a file named by its path parts under the repository root.
func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	raw, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// avalRun matches a make recipe's call of a suite, `$(AVAL_RUN) <suite>`.
var avalRun = regexp.MustCompile(`\$\(AVAL_RUN\) ([a-z0-9.-]+)`)

// expandAvalRuns replaces each `$(AVAL_RUN) <suite>` in text with the command
// .github/aval/run.sh runs for it: runner.command of aval.<suite>.yaml at the
// repository root, joined by spaces. A suite with no contract stays as written.
func expandAvalRuns(t *testing.T, text string) string {
	t.Helper()
	return avalRun.ReplaceAllStringFunc(text, func(call string) string {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "aval."+avalRun.FindStringSubmatch(call)[1]+".yaml"))
		if err != nil {
			return call
		}
		for line := range strings.SplitSeq(string(raw), "\n") {
			if list, ok := strings.CutPrefix(line, "  command: "); ok {
				var argv []string
				if err := json.Unmarshal([]byte(list), &argv); err != nil {
					t.Fatalf("aval contract for %q: runner.command is not a one-line list of strings: %v", call, err)
				}
				return strings.Join(argv, " ")
			}
		}
		t.Fatalf("aval contract for %q has no one-line runner.command", call)
		return call
	})
}
