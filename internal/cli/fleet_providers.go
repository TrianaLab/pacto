package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func newFleetProvidersCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List the providers Pacto detected, with the evidence and what each is used for",
		Long: "Lists what Pacto detected in each source it read: the evidence it found, what it uses each " +
			"provider for and its status, with the limitations that make a row partial. A Kubernetes source " +
			"Pacto could not read is listed as not read, never as nothing detected.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			return printFleetProviders(cmd, q.Providers(), v.GetString(outputFormatKey))
		},
	}
}

func printFleetProviders(cmd *cobra.Command, res *fleet.ProvidersResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		// A detection Pacto could not read is named instead of "nothing detected".
		if !printAdvisorySources(w, res.Meta.Sources) && len(res.Providers) == 0 && res.KubernetesSources == 0 {
			_, _ = fmt.Fprintln(w, "No providers detected: this view reads no Kubernetes cluster (add --k8s)")
		}
		if len(res.Providers) > 0 {
			clusters := map[string]bool{}
			for _, p := range res.Providers {
				clusters[p.Cluster] = true
			}
			many := len(clusters) > 1
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			if many {
				_, _ = fmt.Fprint(tw, "CLUSTER\t")
			}
			_, _ = fmt.Fprintln(tw, "PROVIDER\tEVIDENCE\tUSED FOR")
			for _, p := range res.Providers {
				if many {
					_, _ = fmt.Fprintf(tw, "%s\t", p.Cluster)
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", providerName(p), p.Evidence, providerUsedFor(p))
			}
			_ = tw.Flush()
		}
		for _, s := range res.NotRead {
			reason := ""
			if s.Error != nil {
				reason = " (" + s.Error.Message + ")"
			}
			_, _ = fmt.Fprintf(w, "%s  not read%s\n", s.ID, reason)
		}
		if more := res.NotReadTotal - len(res.NotRead); more > 0 {
			_, _ = fmt.Fprintf(w, "... %d more not read\n", more)
		}
		if res.Truncated {
			_, _ = fmt.Fprintf(w, "... %d of %d rows shown\n", len(res.Providers), res.Total)
		}
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
}

// providerUsedFor is the USED FOR cell: the tokens with spaces, then the
// status and its first reason when the row is not available.
func providerUsedFor(p fleet.Provider) string {
	out := strings.Join(p.UsedFor, ", ")
	// A detected row's UsedFor is a sentence ("sign-in" keeps its hyphen);
	// a reported row's are tokens.
	if p.Provenance != fleet.ProvenanceInferred {
		out = strings.ReplaceAll(out, "-", " ")
	}
	if p.Status == fleet.SourceAvailable {
		return out
	}
	// Build and Restrict give every non-available row a reason; a row built
	// any other way still prints its status alone.
	if len(p.Limitations) == 0 {
		return out + " (" + string(p.Status) + ")"
	}
	return out + fmt.Sprintf(" (%s: %s)", p.Status, p.Limitations[0].Message)
}

// providerName is the PROVIDER cell: the name, marked when Pacto detected it.
func providerName(p fleet.Provider) string {
	if p.Provenance == fleet.ProvenanceInferred {
		return p.Name + " (inferred)"
	}
	return p.Name
}
