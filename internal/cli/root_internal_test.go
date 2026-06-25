package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v2/internal/app"
	"github.com/trianalab/pacto/v2/internal/update"
)

func TestNewRootCommand_PanicRecovery(t *testing.T) {
	old := checkForUpdateFn
	checkForUpdateFn = func(string) *update.CheckResult {
		panic("injected panic for test")
	}
	defer func() { checkForUpdateFn = old }()

	t.Setenv("PACTO_NO_UPDATE_CHECK", "")

	svc := app.NewService(nil, nil)
	root := NewRootCommand(svc, VersionInfo{Version: "v1.0.0"})
	root.SetArgs([]string{"version"})
	var out bytes.Buffer
	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBannerOnRootHelpTTY(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	_ = root.Execute()
	if !strings.Contains(buf.String(), "\033[36m") {
		t.Fatalf("expected colored banner on root help, got %q", buf.String())
	}
}

func TestNoAnimFlagSetsAnimDisabled(t *testing.T) {
	animDisabled = false
	t.Cleanup(func() { animDisabled = false })
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	root.SetArgs([]string{"--no-anim", "version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !animDisabled {
		t.Fatal("--no-anim should set animDisabled in PreRun")
	}
}

func TestPactoNoAnimEnvSetsAnimDisabled(t *testing.T) {
	animDisabled = false
	t.Cleanup(func() { animDisabled = false })
	t.Setenv("PACTO_NO_ANIM", "1")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !animDisabled {
		t.Fatal("PACTO_NO_ANIM env should set animDisabled via viper")
	}
}

func TestNoBannerWhenNotTTY(t *testing.T) {
	withTTY(t, false)
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	_ = root.Execute()
	if strings.Contains(buf.String(), "\033[36m") {
		t.Fatalf("expected no banner without TTY, got %q", buf.String())
	}
}

func TestNoBannerOnSubcommandHelp(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"version", "--help"})
	_ = root.Execute()
	if strings.Contains(buf.String(), "\033[36m") {
		t.Fatalf("banner should only show on root help, got %q", buf.String())
	}
}

func TestBannerStaticWhenAnimDisabled(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "")
	t.Setenv("PACTO_NO_ANIM", "1")
	root := NewRootCommand(nil, VersionInfo{Version: "dev"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetArgs([]string{"--help"})
	_ = root.Execute()
	if !strings.Contains(buf.String(), "\033[36m") {
		t.Fatalf("expected colored banner on root help, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "service contracts") {
		t.Fatalf("expected full banner, got %q", buf.String())
	}
}
