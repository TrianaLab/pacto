package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/update"
)

func TestNewRootCommand_PanicRecovery(t *testing.T) {
	d := defaultDeps()
	d.checkForUpdate = func(string) *update.CheckResult {
		panic("injected panic for test")
	}

	t.Setenv("PACTO_NO_UPDATE_CHECK", "")

	svc := app.NewService(nil, nil)
	root := newRootCommand(svc, VersionInfo{Version: "v1.0.0"}, d)
	root.SetArgs([]string{"version"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBannerOnRootHelpTTY(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf ttyBuffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	_ = root.Execute()
	if !strings.Contains(buf.String(), ansiIndigo) {
		t.Fatalf("expected colored banner on root help, got %q", buf.String())
	}
}

func TestNoAnimFlagDisablesOnContext(t *testing.T) {
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var enabled bool
	probe := &cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		enabled = animationsEnabled(cmd)
		return nil
	}}
	root.AddCommand(probe)
	root.SetArgs([]string{"--no-anim", "probe"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if enabled {
		t.Fatal("--no-anim should disable animations for the running command")
	}
}

func TestPactoNoAnimEnvSetsContext(t *testing.T) {
	t.Setenv("PACTO_NO_ANIM", "1")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var disabled bool
	probe := &cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		disabled = animDisabledFromCmd(cmd)
		return nil
	}}
	root.AddCommand(probe)
	root.SetArgs([]string{"probe"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !disabled {
		t.Fatal("PACTO_NO_ANIM env should set no-anim via viper into the command context")
	}
}

func TestNoBannerWhenNotTTY(t *testing.T) {
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	_ = root.Execute()
	if strings.Contains(buf.String(), ansiIndigo) {
		t.Fatalf("expected no banner without TTY, got %q", buf.String())
	}
}

func TestNoBannerOnSubcommandHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf ttyBuffer
	root.SetOut(&buf)
	root.SetArgs([]string{"version", "--help"})
	_ = root.Execute()
	if strings.Contains(buf.String(), ansiIndigo) {
		t.Fatalf("banner should only show on root help, got %q", buf.String())
	}
}

func TestBannerStaticWhenAnimDisabled(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("PACTO_NO_ANIM", "1")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf ttyBuffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	_ = root.Execute()
	if !strings.Contains(buf.String(), ansiIndigo) {
		t.Fatalf("expected colored banner on root help, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "service contracts") {
		t.Fatalf("expected full banner, got %q", buf.String())
	}
}

func TestRootSecondExecuteAfterErrorDoesNotDeadlock(t *testing.T) {
	t.Setenv("PACTO_NO_UPDATE_CHECK", "1")
	root := NewRootCommand(app.NewService(nil, nil), VersionInfo{Version: "dev"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	// Add a test command that fails in RunE (after PreRun succeeds and fills the channel).
	failCmd := &cobra.Command{
		Use: "fail-test",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("intentional failure")
		},
	}
	root.AddCommand(failCmd)

	// First run fails in RunE, so cobra skips PersistentPostRunE and nothing drains
	// the update channel.
	root.SetArgs([]string{"fail-test"})
	if err := root.Execute(); err == nil {
		t.Fatal("want an error from the fail-test command")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		root.SetArgs([]string{"version"})
		_ = root.Execute()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("second Execute deadlocked on the update channel")
	}
}

// A fresh cached release newer than this one is announced after a text command and never after a
// machine-readable one. The wait is unbounded here: the cache read is the code under test, and under
// load it can outlast the 200ms a user is made to wait.
func TestUpdateNotification(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"version"}, true},
		{[]string{"version", "--output-format", "json"}, false},
		{[]string{"version", "--output-format", "markdown"}, false},
	} {
		tmpDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", tmpDir)
		t.Setenv("PACTO_NO_UPDATE_CHECK", "")
		if err := os.MkdirAll(filepath.Join(tmpDir, "pacto"), 0o700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(map[string]any{"checked_at": time.Now(), "latest_version": "v99.0.0"})
		if err := os.WriteFile(filepath.Join(tmpDir, "pacto", "update-check.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		d := defaultDeps()
		d.updateWait = time.Hour
		root := newRootCommand(app.NewService(nil, nil), VersionInfo{Version: "v0.0.1"}, d)
		root.SetArgs(tc.args)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := strings.Contains(out.String(), "A new version of pacto is available"); got != tc.want {
			t.Errorf("%v: notified %v, want %v\n%s", tc.args, got, tc.want, out.String())
		}
	}
}
