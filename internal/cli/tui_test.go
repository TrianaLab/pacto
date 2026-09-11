package cli

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/tui"
	"github.com/trianalab/pacto/v3/pkg/logging"
)

func TestTUIRequiresATerminal(t *testing.T) {
	withTTY(t, false)
	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"tui"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("err = %v, want an interactive-terminal error", err)
	}
}

func TestTUIReportsAnExecutableLookupFailure(t *testing.T) {
	withTTY(t, true)
	orig := osExecutable
	t.Cleanup(func() { osExecutable = orig })
	osExecutable = func() (string, error) { return "", errors.New("no exe") }

	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"tui"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "no exe") {
		t.Fatalf("err = %v, want the executable lookup error", err)
	}
}

func TestTUIPassesTheFlagsThroughToTheProgram(t *testing.T) {
	withTTY(t, true)
	origExe := osExecutable
	t.Cleanup(func() { osExecutable = origExe })
	osExecutable = func() (string, error) { return "/fake/pacto", nil }

	origRun := tuiRun
	t.Cleanup(func() { tuiRun = origRun })
	var got tui.Options
	tuiRun = func(_ context.Context, o tui.Options) error {
		got = o
		return nil
	}

	dir := t.TempDir()
	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"tui", "--local", dir, "--read-only"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	if !got.ReadOnly {
		t.Error("--read-only did not reach Options.ReadOnly")
	}
	if got.Exe != "/fake/pacto" {
		t.Errorf("Exe = %q, want %q", got.Exe, "/fake/pacto")
	}
	if len(got.Fleet.LocalRoots) != 1 || got.Fleet.LocalRoots[0] != dir {
		t.Errorf("LocalRoots = %v, want [%s]", got.Fleet.LocalRoots, dir)
	}
	if got.Svc == nil {
		t.Error("Options.Svc is nil, the program has no service to query")
	}
}

// TestTUICarriesTheSourceFlagsForYanking pins the four properties a yanked fleet
// query depends on: a repeatable flag keeps every value, a bool is rendered in
// --name=value form so it cannot swallow a positional, a flag left at its default
// contributes nothing, and a non-source flag is not smuggled onto the line.
func TestTUICarriesTheSourceFlagsForYanking(t *testing.T) {
	withTTY(t, true)
	origExe := osExecutable
	t.Cleanup(func() { osExecutable = origExe })
	osExecutable = func() (string, error) { return "/fake/pacto", nil }

	origRun := tuiRun
	t.Cleanup(func() { tuiRun = origRun })
	var got tui.Options
	tuiRun = func(_ context.Context, o tui.Options) error {
		got = o
		return nil
	}

	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"tui", "--local", "a", "--local", "b", "--k8s", "--read-only"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	// Visit walks the set flags in lexical order, so this is the whole line.
	want := []string{"--k8s=true", "--local=a", "--local=b"}
	if !slices.Equal(got.SourceArgs, want) {
		t.Fatalf("SourceArgs = %v, want %v", got.SourceArgs, want)
	}
}

func TestTUISourceArgsAreEmptyWhenNothingWasTyped(t *testing.T) {
	// A bare `pacto tui` must not manufacture a --local=. that the reader never
	// typed: an unset flag has to stay off the yanked line.
	cmd := newTUICommand(newTestService(t), viper.New())
	if args := fleetSourceArgs(cmd); len(args) != 0 {
		t.Fatalf("fleetSourceArgs on an untouched command = %v, want nothing", args)
	}
}

func TestTUIDeclaresTheSharedSourceFlags(t *testing.T) {
	cmd := newTUICommand(newTestService(t), viper.New())
	for _, name := range fleetFlagNames() {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("pacto tui does not declare %q", name)
		}
	}
	if cmd.Flags().Lookup("read-only") == nil {
		t.Error("pacto tui does not declare --read-only")
	}
}

// TestTUIKeepsTheLoggerOffTheAltScreen pins A7: root.go points the logger at
// stderr, and stderr here is the terminal bubbletea has taken over, so a
// warning from a snapshot build lands on top of the frame with nothing to
// repaint it.
func TestTUIKeepsTheLoggerOffTheAltScreen(t *testing.T) {
	withTTY(t, true)
	origExe := osExecutable
	t.Cleanup(func() { osExecutable = origExe })
	osExecutable = func() (string, error) { return "/fake/pacto", nil }

	origRun := tuiRun
	t.Cleanup(func() { tuiRun = origRun })
	var got context.Context
	tuiRun = func(ctx context.Context, _ tui.Options) error {
		got = ctx
		return nil
	}

	var out, errOut strings.Builder
	root := NewRootCommand(newTestService(t), VersionInfo{Version: "dev"})
	root.SetOut(&out)
	root.SetErr(&errOut)
	// -v is what docs/developers.md tells readers to pass, and it is the case
	// that turns the whole of pkg/oci into a Debug writer on every reload.
	root.SetArgs([]string{"tui", "-v"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() = %v, want nil", err)
	}

	logging.LoggerFromContext(got).Warn("a snapshot source is unreachable")
	if errOut.Len() != 0 || out.Len() != 0 {
		t.Fatalf("the logger reached the terminal the alt screen owns: out=%q err=%q", out.String(), errOut.String())
	}
}
