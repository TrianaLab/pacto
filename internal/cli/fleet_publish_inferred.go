package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

func newFleetPublishInferredCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish-inferred",
		Short: "Sign and push the inferred contract of every external system the fleet knows to <registry>/" + contract.InferredRepository,
		Long: "Builds the fleet snapshot from the source flags and, for every external system it knows with at least one consumer, " +
			"publishes an inferred contract to <registry>/" + contract.InferredRepository + "/<identity>: a pacto.yaml naming the " +
			"external and a pacto.inferred.yaml listing the hosts, ports and client majors the fleet sees, each with the sources that " +
			"saw it. A new 0.0.<n> tag is pushed only when that surface changed, annotated " + contract.AnnotationProvenance + ": " +
			contract.ProvenanceInferred + " and signed with --key. It names no consumer, and a range is never checked against it.\n\n" +
			"Clusters publish from the operator once the chart's registry value is set. This command is for installations that run " +
			"no operator, such as the Compose demo; never run it against a registry an operator already writes.\n\n" +
			"--key takes a PEM private key or a `pacto evidence keygen` seed. Readers recognise its signature as the operator's only when " +
			"their --detected advertisement names its public key; otherwise they say the signature could not be checked.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			registry, _ := cmd.Flags().GetString("registry")
			keyPath, _ := cmd.Flags().GetString("key")
			kp, err := loadTargetsKey(keyPath)
			if err != nil {
				return err
			}
			rep, err := svc.PublishInferred(cmd.Context(), app.PublishInferredOptions{Fleet: fleetOptions(cmd), Registry: registry,
				Signer: signing.Keyed(kp, ""), Verify: publish.PolicyVerifier(signing.Policy{Key: kp.GetPublicKey()})})
			if err != nil {
				return err
			}
			var failed error
			if len(rep.Errors) > 0 {
				failed = &ExitError{Code: 1, Reason: fmt.Sprintf("%d inferred contracts could not be published", len(rep.Errors))}
			}
			if v.GetString(outputFormatKey) == "json" {
				return printJSONExit(cmd, rep, failed)
			}
			w := cmd.OutOrStdout()
			for _, e := range rep.Published {
				_, _ = fmt.Fprintf(w, "Published %s:%s (%s)\n  next: pacto impact --changed external:%s\n", e.Repository, e.Version, e.Digest, e.Identity)
			}
			for _, e := range rep.Repaired {
				_, _ = fmt.Fprintf(w, "Signed %s:%s, pushed earlier without its signature\n", e.Repository, e.Version)
			}
			for _, e := range rep.Errors {
				_, _ = fmt.Fprintf(w, "Error %s %s: %s\n", e.Repository, e.Version, e.Error)
			}
			_, _ = fmt.Fprintf(w, "%d published, %d unchanged, %d skipped\n", len(rep.Published), rep.Unchanged, rep.Skipped)
			if rep.Capped {
				_, _ = fmt.Fprintln(w, "The per-run bound was reached; the rest are published on the next run")
			}
			return failed
		},
	}
	cmd.Flags().String("registry", "", "registry host and path the installation repositories live under, as <registry>/"+contract.InferredRepository+"/<identity>")
	cmd.Flags().String("key", "", "the installation signing key: a PEM private key or a seed that pacto evidence keygen wrote")
	_ = cmd.MarkFlagRequired("registry")
	_ = cmd.MarkFlagRequired("key")
	return cmd
}
