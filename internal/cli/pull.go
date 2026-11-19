package cli

import (
	"errors"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

func newPullCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull <ref>",
		Short: "Pull a contract bundle from an OCI registry",
		Long:  "Pulls a contract bundle from the specified OCI reference and extracts it to a local directory.",
		Example: `  # Pull a specific version
  pacto pull oci://ghcr.io/acme/my-service-pacto:1.0.0

  # Pull the latest available version
  pacto pull oci://ghcr.io/acme/my-service-pacto`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			output, _ := cmd.Flags().GetString("output")
			format := v.GetString(outputFormatKey)

			start := time.Now()
			sp := startSpinner(cmd, format, "Pulling "+ref)
			verify, err := pullVerify(cmd)
			if err != nil {
				return err
			}
			result, err := svc.Pull(cmd.Context(), app.PullOptions{
				Ref:    ref,
				Output: output,
				Verify: verify,
			})
			if err != nil {
				sp.Stop()
				return err
			}
			sp.StopOK("Pulled", start)

			return printPullResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output directory (default: service name)")
	cmd.Flags().String("verify-identity", "", "pull only a bundle signed keyless by this certificate identity, such as an email (needs --verify-issuer)")
	cmd.Flags().String("verify-issuer", "", "the OIDC issuer of --verify-identity")
	cmd.Flags().String("verify-key", "", "pull only a bundle signed by the key of this PEM public key file")

	return cmd
}

// pullVerify is the signature --verify-identity and --verify-issuer, or
// --verify-key, require; nil when none is set.
func pullVerify(cmd *cobra.Command) (*contract.PublishSign, error) {
	identity, _ := cmd.Flags().GetString("verify-identity")
	issuer, _ := cmd.Flags().GetString("verify-issuer")
	key, _ := cmd.Flags().GetString("verify-key")
	switch {
	case key != "" && (identity != "" || issuer != ""):
		return nil, errors.New("--verify-key or --verify-identity with --verify-issuer: a signature is checked one way")
	case key != "":
		return &contract.PublishSign{Key: key}, nil
	case (identity == "") != (issuer == ""):
		return nil, errors.New("--verify-identity and --verify-issuer go together")
	case identity != "":
		return &contract.PublishSign{Keyless: true, Identity: identity, Issuer: issuer}, nil
	}
	return nil, nil
}
