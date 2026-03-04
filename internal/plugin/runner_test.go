package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test-plugin source code compiled at test time
// ---------------------------------------------------------------------------

const successPluginSrc = `package main

import (
	"encoding/json"
	"os"
)

func main() {
	var req map[string]interface{}
	json.NewDecoder(os.Stdin).Decode(&req)
	resp := map[string]interface{}{
		"files": []map[string]interface{}{
			{"path": "out.txt", "content": "hello"},
		},
		"message": "done",
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}
`

const errorPluginSrc = `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "plugin error message")
	os.Exit(1)
}
`

const invalidJSONPluginSrc = `package main

import "fmt"

func main() { fmt.Println("not valid json") }
`

const silentErrorPluginSrc = `package main

import "os"

func main() { os.Exit(1) }
`

// buildTestPlugin compiles a small Go source file into a binary in dir.
func buildTestPlugin(t *testing.T, dir, name, src string) string {
	t.Helper()
	srcFile := filepath.Join(dir, name+".go")
	if err := os.WriteFile(srcFile, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "pacto-plugin-"+name)
	cmd := exec.Command("go", "build", "-o", binPath, srcFile)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return binPath
}

// ---------------------------------------------------------------------------
// findPlugin tests
// ---------------------------------------------------------------------------

func TestFindPlugin_InPATH(t *testing.T) {
	dir := t.TempDir()

	// Build a tiny binary so exec.LookPath finds it.
	buildTestPlugin(t, dir, "test", successPluginSrc)

	// Replace PATH with only the temp dir to isolate the lookup.
	t.Setenv("PATH", dir)

	path, err := findPlugin("test")
	if err != nil {
		t.Fatalf("expected to find plugin in PATH: %v", err)
	}
	if !strings.Contains(path, "pacto-plugin-test") {
		t.Fatalf("unexpected path: %s", path)
	}
}

func TestFindPlugin_InConfigDir(t *testing.T) {
	home := t.TempDir()

	// Create the config directory tree with a fake plugin file.
	pluginDir := filepath.Join(home, ".config", "pacto", "plugins")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	pluginFile := filepath.Join(pluginDir, "pacto-plugin-test")
	if err := os.WriteFile(pluginFile, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Point HOME to our temp dir so UserHomeDir returns it, clear
	// XDG_CONFIG_HOME so PactoConfigDir falls back to $HOME/.config, and
	// empty PATH so LookPath won't find anything.
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PATH", "")

	path, err := findPlugin("test")
	if err != nil {
		t.Fatalf("expected to find plugin in config dir: %v", err)
	}
	if path != pluginFile {
		t.Fatalf("expected %s, got %s", pluginFile, path)
	}
}

func TestFindPlugin_NotFound(t *testing.T) {
	home := t.TempDir() // empty — no plugins inside

	t.Setenv("PATH", "")
	t.Setenv("HOME", home)

	_, err := findPlugin("nonexistent")
	if err == nil {
		t.Fatal("expected an error when plugin is not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SubprocessRunner tests
// ---------------------------------------------------------------------------

func TestSubprocessRunner_Run_Success(t *testing.T) {
	dir := t.TempDir()
	buildTestPlugin(t, dir, "success", successPluginSrc)
	t.Setenv("PATH", dir)

	runner := &SubprocessRunner{}
	resp, err := runner.Run(context.Background(), "success", GenerateRequest{
		ProtocolVersion: ProtocolVersion,
		BundleDir:       "/tmp/bundle",
		OutputDir:       "/tmp/output",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Message != "done" {
		t.Fatalf("expected message 'done', got %q", resp.Message)
	}
	if len(resp.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(resp.Files))
	}
	if resp.Files[0].Path != "out.txt" {
		t.Fatalf("expected path 'out.txt', got %q", resp.Files[0].Path)
	}
	if resp.Files[0].Content != "hello" {
		t.Fatalf("expected content 'hello', got %q", resp.Files[0].Content)
	}
}

func TestSubprocessRunner_Run_PluginNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PATH", "")
	t.Setenv("HOME", home)

	runner := &SubprocessRunner{}
	_, err := runner.Run(context.Background(), "does-not-exist", GenerateRequest{})
	if err == nil {
		t.Fatal("expected error for missing plugin")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' in error, got: %v", err)
	}
}

func TestSubprocessRunner_Run_PluginStderrError(t *testing.T) {
	dir := t.TempDir()
	buildTestPlugin(t, dir, "errplugin", errorPluginSrc)
	t.Setenv("PATH", dir)

	runner := &SubprocessRunner{}
	_, err := runner.Run(context.Background(), "errplugin", GenerateRequest{})
	if err == nil {
		t.Fatal("expected error from failing plugin")
	}
	if !strings.Contains(err.Error(), "plugin error message") {
		t.Fatalf("expected stderr text in error, got: %v", err)
	}
}

func TestSubprocessRunner_Run_PluginSilentError(t *testing.T) {
	dir := t.TempDir()
	buildTestPlugin(t, dir, "silent", silentErrorPluginSrc)
	t.Setenv("PATH", dir)

	runner := &SubprocessRunner{}
	_, err := runner.Run(context.Background(), "silent", GenerateRequest{})
	if err == nil {
		t.Fatal("expected error from silently failing plugin")
	}
	// Should wrap the exec error since stderr is empty.
	if !strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected exec error in message, got: %v", err)
	}
}

func TestSubprocessRunner_Run_MarshalError(t *testing.T) {
	dir := t.TempDir()
	buildTestPlugin(t, dir, "marshal", successPluginSrc)
	t.Setenv("PATH", dir)

	runner := &SubprocessRunner{}
	// A channel value in Options causes json.Marshal to fail.
	_, err := runner.Run(context.Background(), "marshal", GenerateRequest{
		Options: map[string]any{
			"bad": make(chan int),
		},
	})
	if err == nil {
		t.Fatal("expected marshal error")
	}
	if !strings.Contains(err.Error(), "failed to marshal plugin input") {
		t.Fatalf("expected 'failed to marshal plugin input' in error, got: %v", err)
	}
}

func TestSubprocessRunner_Run_PluginInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	buildTestPlugin(t, dir, "badjson", invalidJSONPluginSrc)
	t.Setenv("PATH", dir)

	runner := &SubprocessRunner{}
	_, err := runner.Run(context.Background(), "badjson", GenerateRequest{})
	if err == nil {
		t.Fatal("expected error for invalid JSON output")
	}
	if !strings.Contains(err.Error(), "invalid output") {
		t.Fatalf("expected 'invalid output' in error, got: %v", err)
	}
}
