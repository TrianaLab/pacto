package cli

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/viper"
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
