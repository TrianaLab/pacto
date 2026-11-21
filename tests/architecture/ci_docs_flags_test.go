package architecture

import (
	"regexp"
	"strings"
	"testing"
)

var (
	// A pacto ci command up to the end of its code span or line, and a flag in it.
	ciCommand = regexp.MustCompile("\\bpacto ci( init)?\\b[^`|;#]*")
	ciFlag    = regexp.MustCompile(`(?:^|\s)(--[a-z][a-z0-9-]*)`)
)

// cliSection returns the text of docs/cli-reference.md from the heading that
// is exactly heading up to the next heading of any level.
func cliSection(t *testing.T, ref, heading string) string {
	t.Helper()
	_, rest, ok := strings.Cut(ref, "\n"+heading+"\n")
	if !ok {
		t.Fatalf("docs/cli-reference.md has no %q", heading)
	}
	if i := regexp.MustCompile(`(?m)^#{2,3} `).FindStringIndex(rest); i != nil {
		rest = rest[:i[0]]
	}
	return rest
}

// Every --flag a CI page writes after pacto ci or pacto ci init is one the
// regenerated CLI reference lists for that command or as a global flag, so a
// page cannot teach a flag the CLI does not have.
func TestCIDocsFlagsExist(t *testing.T) {
	ref := readRepoFile(t, "docs", "cli-reference.md")
	global := cliSection(t, ref, "## Global flags")
	known := map[string]string{
		"":      cliSection(t, ref, "## `pacto ci`") + global,
		" init": cliSection(t, ref, "### `pacto ci init`") + global,
	}
	seen := 0
	for _, page := range []string{"ci.md", "ci-from-pipeline.md", "github-actions.md"} {
		for n, line := range strings.Split(readRepoFile(t, "docs", page), "\n") {
			for _, cmd := range ciCommand.FindAllStringSubmatch(line, -1) {
				for _, f := range ciFlag.FindAllStringSubmatch(cmd[0], -1) {
					seen++
					listed := regexp.MustCompile(`(?m)^\s+(-\w, )?` + regexp.QuoteMeta(f[1]) + `[\s\[]`)
					if !listed.MatchString(known[cmd[1]]) {
						t.Errorf("docs/%s:%d: pacto ci%s has no flag %s in docs/cli-reference.md", page, n+1, cmd[1], f[1])
					}
				}
			}
		}
	}
	if seen == 0 {
		t.Error("no pacto ci flag found in the CI pages: the scan reads nothing")
	}
}
