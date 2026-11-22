package cli

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

func newPushCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <ref>",
		Short: "Push a contract bundle to an OCI registry",
		Long: "Validates the contract (including remote policy and config refs), builds an OCI artifact and pushes it to the specified registry reference. " +
			"Pushing the same content again succeeds without writing anything; different content under an existing reference is refused unless --force.",
		Example: `  # Push with auto-tag (uses contract version)
  pacto push oci://ghcr.io/acme/my-service-pacto -p my-service

  # Push with explicit tag
  pacto push oci://ghcr.io/acme/my-service-pacto:latest -p my-service

  # Force overwrite an existing artifact
  pacto push oci://ghcr.io/acme/my-service-pacto -p my-service --force

  # Contribute an inferred record, signed with the producer's evidence key
  pacto push oci://ghcr.io/acme/pacto-inferred/my-idp/services -p checkout --sign-key ~/.pacto/keys/my-idp.key`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]
			path, _ := cmd.Flags().GetString("path")
			force, _ := cmd.Flags().GetBool("force")
			index, _ := cmd.Flags().GetString("index")
			format := v.GetString(outputFormatKey)
			opts := app.PushOptions{Ref: ref, Path: path, Force: force, Index: index, Overrides: getOverrides(cmd)}
			if err := withSignKey(cmd, &opts); err != nil {
				return err
			}

			start := time.Now()
			sp := startSpinner(cmd, format, "Pushing "+ref)
			result, err := svc.Push(cmd.Context(), opts)
			if err != nil {
				sp.Stop()
				return err
			}
			sp.StopOK("Pushed", start)

			return printPushResult(cmd, result, format)
		},
	}

	cmd.Flags().StringP("path", "p", "", "path to contract directory (default: current directory)")
	cmd.Flags().BoolP("force", "f", false, "overwrite existing artifact in registry")
	cmd.Flags().Var(new(indexFlag), "index", indexFlagUsage)
	cmd.Flags().String("sign-key", "", "sign the pushed digest with the `file` key, kept outside the pushed directory: a PEM private key or a seed pacto evidence keygen wrote")

	addOverrideFlagsNoShorthand(cmd)

	return cmd
}

// withSignKey sets opts to sign the pushed digest with the --sign-key file,
// when one is given.
func withSignKey(cmd *cobra.Command, opts *app.PushOptions) error {
	file, _ := cmd.Flags().GetString("sign-key")
	if file == "" {
		return nil
	}
	kp, err := loadTargetsKey(file)
	if err != nil {
		return err
	}
	opts.SignKeyPath, opts.Signer = file, signing.Keyed(kp, "")
	opts.Verify = publish.PolicyVerifier(signing.Policy{Key: kp.GetPublicKey()})
	return nil
}
