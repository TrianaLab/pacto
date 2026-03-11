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

	"github.com/trianalab/pacto/internal/oci"
)

// Runner executes a plugin by name with the given request.
type Runner interface {
	Run(ctx context.Context, name string, req GenerateRequest) (*GenerateResponse, error)
}

// SubprocessRunner discovers and executes plugin binaries via stdin/stdout JSON.
type SubprocessRunner struct{}

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
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = bytes.NewReader(input)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errMsg := strings.TrimSpace(stderr.String()); errMsg != "" {
			return nil, fmt.Errorf("plugin %s: %s", name, errMsg)
		}
		return nil, fmt.Errorf("plugin %s: %w", name, err)
	}

	var resp GenerateResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
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

	if configDir, err := oci.PactoConfigDir(); err == nil {
		pluginPath := filepath.Join(configDir, "plugins", binaryName)
		if info, err := os.Stat(pluginPath); err == nil && !info.IsDir() {
			return pluginPath, nil
		}
	}

	return "", fmt.Errorf("plugin %q not found (looked for %s in $PATH and ~/.config/pacto/plugins/)", name, binaryName)
}
