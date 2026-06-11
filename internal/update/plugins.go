package update

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const pluginsRepo = "TrianaLab/pacto-plugins"

// PluginUpdateResult holds the outcome of a single plugin update.
type PluginUpdateResult struct {
	Name    string
	Version string
}

// Testability hook.
var osReadDir = os.ReadDir

// UpdatePlugins discovers installed pacto plugins and updates them to the latest version.
func UpdatePlugins() ([]PluginUpdateResult, error) {
	plugins, execDir, err := discoverInstalledPlugins()
	if err != nil {
		return nil, fmt.Errorf("failed to discover plugins: %w", err)
	}
	if len(plugins) == 0 {
		return nil, nil
	}

	tag, err := fetchLatestRepoVersion(pluginsRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest plugins version: %w", err)
	}

	// Fetch the published checksums once so every plugin binary can be verified
	// before it is installed.
	sums, err := fetchChecksums(checksumsURL(pluginsRepo, tag))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch plugin checksums: %w", err)
	}

	var results []PluginUpdateResult
	for _, name := range plugins {
		url := buildPluginDownloadURL(tag, name)
		ext := ""
		if runtimeGOOS == "windows" {
			ext = ".exe"
		}
		asset := fmt.Sprintf("%s_%s_%s%s", name, runtimeGOOS, runtimeGOARCH, ext)
		expected, ok := sums[asset]
		if !ok {
			return results, fmt.Errorf("no checksum published for plugin %s", name)
		}
		targetPath := filepath.Join(execDir, name+ext)
		if err := downloadAndInstall(url, targetPath, expected); err != nil {
			return results, fmt.Errorf("failed to update plugin %s: %w", name, err)
		}
		results = append(results, PluginUpdateResult{Name: name, Version: tag})
	}
	return results, nil
}

// discoverInstalledPlugins finds pacto-plugin-* binaries in the same directory as the pacto executable.
func discoverInstalledPlugins() ([]string, string, error) {
	execPath, err := osExecutable()
	if err != nil {
		return nil, "", fmt.Errorf("failed to determine executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve executable path: %w", err)
	}
	dir := filepath.Dir(execPath)

	entries, err := osReadDir(dir)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read directory: %w", err)
	}

	var plugins []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".exe")
		if strings.HasPrefix(name, "pacto-plugin-") {
			plugins = append(plugins, name)
		}
	}
	return plugins, dir, nil
}

// buildPluginDownloadURL constructs the download URL for a plugin binary.
func buildPluginDownloadURL(tag, pluginName string) string {
	ext := ""
	if runtimeGOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf(
		"%s/%s/releases/download/%s/%s_%s_%s%s",
		githubDownloadURL, pluginsRepo, tag, pluginName, runtimeGOOS, runtimeGOARCH, ext,
	)
}
