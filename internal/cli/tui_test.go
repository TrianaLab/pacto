package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/tui"
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
