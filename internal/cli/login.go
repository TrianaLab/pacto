package cli

import (
	"fmt"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v3/pkg/oci"
	"golang.org/x/term"
)

var readPasswordFn = func(fd int) ([]byte, error) { return term.ReadPassword(fd) }

func newLoginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "login <registry>",
		Short:   "Log in to an OCI registry",
		Long:    "Stores credentials for an OCI registry in ~/.config/pacto/config.json.",
		Example: "  pacto login ghcr.io -u my-username",
		Args:    cobra.ExactArgs(1),
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

			// pkg/oci owns the credential file: it is the same file the keychain
			// reads, and its writer fails closed on a config it cannot read or
			// parse, so a transient error can never rewrite the file from scratch
			// and delete every other registry's credential.
			if err := oci.SetCredential(registry, username, password); err != nil {
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
