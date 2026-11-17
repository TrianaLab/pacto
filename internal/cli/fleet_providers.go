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
		k8s, unread := kubernetesSources(res.Meta.Sources)
		if len(res.Providers) == 0 && !k8s {
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
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Evidence, providerUsedFor(p))
			}
			_ = tw.Flush()
		}
		for _, s := range unread {
			reason := ""
			if s.Error != nil {
				reason = " (" + s.Error.Message + ")"
			}
			_, _ = fmt.Fprintf(w, "%s  not read%s\n", s.ID, reason)
		}
		if res.Truncated {
			_, _ = fmt.Fprintf(w, "... %d of %d rows shown\n", len(res.Providers), res.Total)
		}
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
}

// kubernetesSources reports whether the answer read any Kubernetes source and
// lists the ones it could not read.
func kubernetesSources(sources []fleet.SourceState) (bool, []fleet.SourceState) {
	read, unread := false, []fleet.SourceState(nil)
	for _, s := range sources {
		if s.Kind != fleet.SourceKindKubernetes && s.Kind != fleet.SourceKindKubernetesWorkloads {
			continue
		}
		read = true
		if s.Status == fleet.SourceUnavailable {
			unread = append(unread, s)
		}
	}
	return read, unread
}

// providerUsedFor is the USED FOR cell: the tokens with spaces, then the
// status and its first reason when the row is not available.
func providerUsedFor(p fleet.Provider) string {
	out := strings.ReplaceAll(strings.Join(p.UsedFor, ", "), "-", " ")
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
