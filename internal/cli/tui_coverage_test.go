package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v3/internal/tui"
)

// embodiedByTUI are commands the TUI *is* rather than runs: their whole output
// is a screen, so no verb produces their argv. They are covered, not excluded.
var embodiedByTUI = map[string]string{
	"tui":          "this is the command under test",
	"fleet status": "the list screen is a live fleet status",
	"fleet search": "the / filter is a live fleet search",
	"graph":        "the graph screen renders the same tree from a fleet neighborhood",
}

// excludedFromTUI records why each command is deliberately absent, so the
// exclusion is a decision on the record rather than an oversight. Adding a new
// command forces a choice: give it a verb, or add it here with a reason.
var excludedFromTUI = map[string]string{
	"fleet reconcile": "requires a --traces file, which no selection can supply",
	"dashboard":       "a long-running server; the TUI is the terminal equivalent of it",
	"doc":             "writes a file or serves a site, neither of which belongs on a screen stack",
	"evidence keygen": "key material handling belongs at a shell prompt, not behind a keystroke",
	"evidence send":   "key material handling belongs at a shell prompt, not behind a keystroke",
	"evidence serve":  "a long-running server",
	"evidence sign":   "key material handling belongs at a shell prompt, not behind a keystroke",
	"evidence verify": "key material handling belongs at a shell prompt, not behind a keystroke",
	"init":            "scaffolds a new bundle; there is no fleet entity to select yet",
	"pack":            "a build step for pipelines, with no reading to do afterwards",
	"otel observe":    "a long-running collector",
	"fleet snapshot":  "writes a snapshot file; the TUI already holds the snapshot in memory",
	"login":           "reads a secret from the terminal it does not own under an alt screen",
	"logout":          "trivial, and pairs with login",
	"mcp":             "speaks a protocol on stdio",
	"update":          "replaces the running binary, which is the TUI",
	"version":         "shown in the TUI status bar already",
}

func TestEveryCommandIsEitherInTheTUIOrExcluded(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range tui.VerbCommands() {
		covered[c] = true
	}
	for c := range embodiedByTUI {
		covered[c] = true
	}

	var missing []string
	for _, path := range leafPaths(t) {
		if covered[path] {
			continue
		}
		if _, ok := excludedFromTUI[path]; ok {
			continue
		}
		missing = append(missing, path)
	}
	if len(missing) > 0 {
		t.Fatalf("these commands are neither in the TUI nor excluded: %v\n"+
			"give each one a verb in internal/tui/verbs.go, or add it to excludedFromTUI with a reason",
			missing)
	}
}

func TestNoStaleClassification(t *testing.T) {
	leaves := map[string]bool{}
	for _, p := range leafPaths(t) {
		leaves[p] = true
	}
	for _, m := range []map[string]string{excludedFromTUI, embodiedByTUI} {
		for path, reason := range m {
			if !leaves[path] {
				t.Errorf("%q (%s) is classified but no such command exists", path, reason)
			}
			if reason == "" {
				t.Errorf("the classification of %q has no reason", path)
			}
		}
	}
	for path := range excludedFromTUI {
		if _, both := embodiedByTUI[path]; both {
			t.Errorf("%q is both embodied and excluded", path)
		}
	}
}

func TestTheTUICommandIsRegistered(t *testing.T) {
	for _, p := range leafPaths(t) {
		if p == "tui" {
			return
		}
	}
	t.Fatal("pacto tui is not registered on the root command")
}

// leafPaths returns the space-joined path of every runnable command, so
// "fleet get" rather than "get".
func leafPaths(t *testing.T) []string {
	t.Helper()
	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	var out []string
	var walk func(c *cobra.Command, prefix []string)
	walk = func(c *cobra.Command, prefix []string) {
		path := append(append([]string{}, prefix...), c.Name())
		kids := c.Commands()
		if len(kids) == 0 {
			out = append(out, strings.Join(path, " "))
			return
		}
		for _, k := range kids {
			walk(k, path)
		}
	}
	for _, c := range root.Commands() {
		walk(c, nil)
	}
	return out
}
