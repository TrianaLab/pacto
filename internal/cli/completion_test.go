package cli

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestStatusFlagCompletes(t *testing.T) {
	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	out, directive := completeFlag(t, root, []string{"fleet", "search", "--status", ""})
	if len(out) == 0 {
		t.Fatal("--status offers no completions")
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v, want NoFileComp; a closed set must not fall back to filenames", directive)
	}
	for _, s := range out {
		if s == "" {
			t.Fatal("an empty completion candidate was offered")
		}
	}
}

// TestClosedSetFlagsComplete pins every closed-set flag to the exact vocabulary
// the command validates against. It also catches a typo in a registered flag
// name: RegisterFlagCompletionFunc's error is discarded, so an unregistered
// flag simply completes to nothing.
func TestClosedSetFlagsComplete(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		// --status is covered by TestStatusFlagCompletes; its vocabulary is
		// fleet.CanonicalStatuses(), so restating it here would prove nothing.
		// --compliance shares the same vocabulary (validated by ValidStatus).
		{"fleet search --compliance", []string{"fleet", "search", "--compliance", ""}, []string{"Compliant", "Invalid", "NonCompliant", "NotEvaluated", "Reference", "Unknown", "Warning"}},
		{"fleet search --workload", []string{"fleet", "search", "--workload", ""}, []string{"service", "job", "scheduled"}},
		{"fleet graph --direction", []string{"fleet", "graph", "--direction", ""}, []string{"dependencies", "dependents"}},
		{"doc --ui", []string{"doc", "--ui", ""}, []string{"swagger"}},
		{"mcp --transport", []string{"mcp", "--transport", ""}, []string{"stdio", "http"}},
		{"root --output-format", []string{"--output-format", ""}, []string{"text", "json", "markdown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
			out, directive := completeFlag(t, root, tc.args)
			if !slices.Equal(out, tc.want) {
				t.Errorf("completions = %v, want %v", out, tc.want)
			}
			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("directive = %v, want NoFileComp; a closed set must not fall back to filenames", directive)
			}
		})
	}
}

// TestFleetPositionalsOfferNoFilenames pins both halves of the answer for the
// fleet verbs whose positional is a service name: no candidates, because they
// live in a snapshot too expensive to build at tab time, and NoFileComp,
// because without it cobra falls back to filenames for a positional that is
// never a path. Driving __complete rather than reading the field also proves
// the function is reached — an unset one gives candidates and the default
// directive, which is exactly the filename fallback.
func TestFleetPositionalsOfferNoFilenames(t *testing.T) {
	for _, verb := range []string{"get", "graph", "explain"} {
		t.Run(verb, func(t *testing.T) {
			root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
			out, directive := completeFlag(t, root, []string{"fleet", verb, ""})
			if len(out) != 0 {
				t.Errorf("completions = %v, want none; a candidate here means a snapshot was built at tab time", out)
			}
			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("directive = %v, want NoFileComp; this positional is never a path", directive)
			}
		})
	}
}

func TestDocUIAndOutputAreMutuallyExclusive(t *testing.T) {
	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	root.SetArgs([]string{"doc", "--ui", "swagger", "-o", "/tmp/x"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil {
		t.Fatal("doc accepted two mutually exclusive flags")
	}
	if !strings.Contains(err.Error(), "[output ui] were all set") {
		t.Fatalf("expected cobra mutual exclusion message, got: %v", err)
	}
}

func TestValidateDocFlagsStillGuardsTheOneCobraCannot(t *testing.T) {
	// "--interface requires --ui" has no cobra primitive, so it stays
	// hand-rolled in validateDocFlags. Assert it survived the refactor.
	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	root.SetArgs([]string{"doc", "--interface", "api"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil {
		t.Fatal("--interface without --ui was accepted")
	}
	if !strings.Contains(err.Error(), "--interface requires --ui") {
		t.Fatalf("expected validateDocFlags message, got: %v", err)
	}
}

// completeFlag drives cobra's hidden __complete command, which is the only way
// to exercise a completion function through the public API.
//
// stderr goes to io.Discard, not to buf: cobra ends every __complete run with a
// human-readable "Completion ended with directive:" line on stderr that a real
// shell script ignores. Merging the two streams would put that line last and
// hide the machine-readable directive the protocol actually carries.
func completeFlag(t *testing.T, root *cobra.Command, args []string) ([]string, cobra.ShellCompDirective) {
	t.Helper()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{cobra.ShellCompRequestCmd}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("__complete failed: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if buf.Len() == 0 {
		t.Fatal("__complete produced nothing")
	}
	// The last line is ":<directive>"; everything before it is a candidate.
	var d int
	if _, err := fmt.Sscanf(lines[len(lines)-1], ":%d", &d); err != nil {
		t.Fatalf("could not parse the completion directive from %q", lines[len(lines)-1])
	}
	var out []string
	for _, l := range lines[:len(lines)-1] {
		out = append(out, strings.SplitN(l, "\t", 2)[0])
	}
	return out, cobra.ShellCompDirective(d)
}
