package update

import (
	"fmt"

	"github.com/trianalab/pacto/v4/pkg/plugin"
)

const pluginsRepo = "TrianaLab/pacto-plugins"

// PluginUpdateResult holds the outcome of a single plugin update.
type PluginUpdateResult struct {
	Name    string
	Version string
}

// UpdatePlugins updates every plugin the runner can execute to the latest
// published version. Discovery goes through plugin.Installed so the binary that
// gets rewritten is the one Run would spawn: listing the pacto binary's own
// directory instead missed plugins installed the documented way and, when a
// plugin existed in both places, overwrote the copy that never runs while
// reporting success.
func (u *Updater) UpdatePlugins() ([]PluginUpdateResult, error) {
	plugins := plugin.Installed()
	if len(plugins) == 0 {
		return nil, nil
	}

	tag, err := u.fetchLatestRepoVersion(pluginsRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest plugins version: %w", err)
	}

	// Fetch the published checksums once so every plugin binary can be verified
	// before it is installed.
	sums, err := u.fetchChecksums(u.releaseAssetURL(pluginsRepo, tag, "checksums.txt"))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch plugin checksums: %w", err)
	}

	var results []PluginUpdateResult
	for _, p := range plugins {
		// The plugins release workflow names each binary
		// pacto-plugin-<name>_<os>_<arch>[.exe].
		binary := u.assetName("pacto-plugin-" + p.Name)
		expected, ok := sums[binary]
		if !ok {
			return results, fmt.Errorf("no checksum published for plugin %s", p.Name)
		}
		if err := u.downloadAndInstall(u.releaseAssetURL(pluginsRepo, tag, binary), p.Path, expected); err != nil {
			return results, fmt.Errorf("failed to update plugin %s: %w", p.Name, err)
		}
		results = append(results, PluginUpdateResult{Name: "pacto-plugin-" + p.Name, Version: tag})
	}
	return results, nil
}
