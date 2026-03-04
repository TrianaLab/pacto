package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/internal/oci"
	"golang.org/x/term"
)

var (
	readPasswordFn      = func(fd int) ([]byte, error) { return term.ReadPassword(fd) }
	jsonMarshalIndentFn = json.MarshalIndent
)

func newLoginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login <registry>",
		Short: "Log in to an OCI registry",
		Long:  "Stores credentials for an OCI registry in ~/.config/pacto/config.json.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := args[0]
			username, _ := cmd.Flags().GetString("username")
			password, _ := cmd.Flags().GetString("password")

			if username == "" {
				return fmt.Errorf("--username is required")
			}

			if password == "" {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), "Password: ")
				pw, err := readPasswordFn(int(syscall.Stdin))
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
				password = string(pw)
			}

			if err := writePactoConfig(registry, username, password); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Login succeeded for %s\n", registry)
			return nil
		},
	}

	cmd.Flags().StringP("username", "u", "", "registry username")
	cmd.Flags().StringP("password", "p", "", "registry password")

	return cmd
}

// writePactoConfig writes credentials to ~/.config/pacto/config.json.
func writePactoConfig(registry, username, password string) error {
	configPath, err := oci.PactoConfigPath()
	if err != nil {
		return fmt.Errorf("failed to determine config path: %w", err)
	}

	configDir := filepath.Dir(configPath)

	var cfg oci.PactoConfig

	data, err := os.ReadFile(configPath)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("failed to parse existing %s: %w", configPath, err)
		}
	}

	if cfg.Auths == nil {
		cfg.Auths = make(map[string]oci.PactoAuth)
	}

	// Base64-encode "username:password" per Docker convention.
	encoded := encodeAuth(username, password)
	cfg.Auths[registry] = oci.PactoAuth{Auth: encoded}

	out, err := jsonMarshalIndentFn(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create %s: %w", configDir, err)
	}

	if err := os.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", configPath, err)
	}

	return nil
}

func encodeAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}
