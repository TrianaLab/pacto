// Package plugin discovers and executes out-of-process Pacto plugins. A plugin
// is a pacto-plugin-<name> binary that reads a GenerateRequest as JSON on stdin
// and writes a GenerateResponse as JSON on stdout; the runner handles binary
// discovery, process lifecycle, and timeout enforcement so plugins can produce
// deployment artifacts from a contract.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Plugin execution limits, overridable in tests. They bound a buggy or hostile
// plugin: pluginTimeout caps wall-clock, maxPluginOutput caps buffered stdout.
var (
	pluginTimeout   = 60 * time.Second
	maxPluginOutput = 64 << 20 // 64 MB
)

// SubprocessRunner discovers and executes plugin binaries via stdin/stdout JSON.
type SubprocessRunner struct{}

// cappedBuffer buffers up to limit bytes and then discards the rest, recording
// that truncation happened. It never blocks the child process.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := c.limit - c.buf.Len(); remaining > 0 {
		if len(p) > remaining {
			c.buf.Write(p[:remaining])
			c.truncated = true
		} else {
			c.buf.Write(p)
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

// Run finds the plugin binary, spawns it, writes the request JSON to stdin,
// and reads the response JSON from stdout.
func (r *SubprocessRunner) Run(ctx context.Context, name string, req GenerateRequest) (*GenerateResponse, error) {
	slog.Debug("discovering plugin binary", "plugin", name)
	binary, err := findPlugin(name)
	if err != nil {
		return nil, err
	}
	slog.Debug("plugin binary found", "plugin", name, "path", binary)

	input, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal plugin input: %w", err)
	}

	slog.Debug("executing plugin", "plugin", name)
	ctx, cancel := context.WithTimeout(ctx, pluginTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = bytes.NewReader(input)

	stdout := &cappedBuffer{limit: maxPluginOutput}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if stdout.truncated {
		return nil, fmt.Errorf("plugin %s: output exceeded %d bytes", name, maxPluginOutput)
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("plugin %s: timed out after %s", name, pluginTimeout)
		}
		if errMsg := strings.TrimSpace(stderr.String()); errMsg != "" {
			return nil, fmt.Errorf("plugin %s: %s", name, errMsg)
		}
		return nil, fmt.Errorf("plugin %s: %w", name, err)
	}

	var resp GenerateResponse
	if err := json.Unmarshal(stdout.buf.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("plugin %s returned invalid output: %w", name, err)
	}

	return &resp, nil
}

// findPlugin locates a pacto-plugin-<name> binary in PATH or the user plugin directory.
func findPlugin(name string) (string, error) {
	binaryName := "pacto-plugin-" + name

	if path, err := exec.LookPath(binaryName); err == nil {
		return path, nil
	}

	if configDir, err := pactoConfigDir(); err == nil {
		pluginPath := filepath.Join(configDir, "plugins", binaryName)
		if info, err := os.Stat(pluginPath); err == nil && !info.IsDir() {
			return pluginPath, nil
		}
	}

	return "", fmt.Errorf("plugin %q not found (looked for %s in $PATH and ~/.config/pacto/plugins/)", name, binaryName)
}

// userHomeDirFn is a function variable for testing.
var userHomeDirFn = os.UserHomeDir

// pactoConfigDir returns the pacto configuration directory path.
func pactoConfigDir() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		home, err := userHomeDirFn()
		if err != nil {
			return "", err
		}
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "pacto"), nil
}
