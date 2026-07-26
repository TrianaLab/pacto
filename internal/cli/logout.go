package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v3/pkg/oci"
)

func newLogoutCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "logout <registry>",
		Short:   "Remove stored credentials for an OCI registry",
		Long:    "Removes credentials for an OCI registry from ~/.config/pacto/config.json.",
		Example: "  pacto logout ghcr.io",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := args[0]

			removed, err := removePactoConfig(registry)
			if err != nil {
				return err
			}

			if removed {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Logout succeeded for %s\n", registry)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No stored credentials for %s\n", registry)
			}

			return nil
		},
	}

	return cmd
}

// removePactoConfig removes credentials for a registry from ~/.config/pacto/config.json.
// Returns true if an entry was removed, false if no entry existed.
func removePactoConfig(registry string) (bool, error) {
	configPath, err := oci.PactoConfigPath()
	if err != nil {
		return false, fmt.Errorf("failed to determine config path: %w", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		// No config file → no credentials stored
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	var cfg oci.PactoConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("failed to parse existing %s: %w", configPath, err)
	}

	if cfg.Auths == nil {
		return false, nil
	}

	_, exists := cfg.Auths[registry]
	if !exists {
		return false, nil
	}

	delete(cfg.Auths, registry)

	out, err := jsonMarshalIndentFn(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("failed to marshal config: %w", err)
	}

	// Directory must exist since we just read the config file from it
	if err := os.WriteFile(configPath, out, 0600); err != nil {
		return false, fmt.Errorf("failed to write %s: %w", configPath, err)
	}

	return true, nil
}
