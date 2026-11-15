package architecture

import (
	"strings"
	"testing"
)

// statsSentence is the one sentence README, the pacto stats help and the
// guide share, so the feature is described in the same words everywhere.
const statsSentence = "Pacto can record what it catches as unsigned statistics in a registry you name; " +
	"`pacto stats` and the dashboard Metrics page total them, and nothing leaves your registry."

// Every surface that mentions run records says they are unsigned statistics,
// README mentions pacto stats once and the guide links the field reference
// and says what a restricted reader sees.
func TestStatsMentionIsConsistent(t *testing.T) {
	// Prose wraps, so every file is read with its whitespace collapsed.
	read := func(parts ...string) string { return strings.Join(strings.Fields(readRepoFile(t, parts...)), " ") }
	readme := read("README.md")
	help := read("internal", "cli", "stats.go")
	mcp := read("internal", "mcp", "stats.go")
	guide := read("docs", "measure-what-pacto-does.md")

	if n := strings.Count(readme, "pacto stats"); n != 1 {
		t.Errorf("README.md mentions pacto stats %d times, want 1", n)
	}
	for name, text := range map[string]string{"README.md": readme, "internal/cli/stats.go": help, "internal/mcp/stats.go": mcp, "docs/measure-what-pacto-does.md": guide} {
		if !strings.Contains(text, "unsigned statistics") {
			t.Errorf("%s does not say unsigned statistics", name)
		}
	}
	// The help text is a Go string, where the backquotes of the markdown
	// sentence are plain.
	plain := strings.ReplaceAll(statsSentence, "`", "")
	for name, text := range map[string]string{"README.md": readme, "docs/measure-what-pacto-does.md": guide} {
		if !strings.Contains(text, statsSentence) {
			t.Errorf("%s lacks the shared sentence %q", name, statsSentence)
		}
	}
	if !strings.Contains(help, plain) {
		t.Errorf("internal/cli/stats.go lacks the shared sentence %q", plain)
	}
	for _, want := range []string{"run-record-reference.md", "dashboard-authentication.md", "partial"} {
		if !strings.Contains(guide, want) {
			t.Errorf("docs/measure-what-pacto-does.md does not mention %s", want)
		}
	}
}
