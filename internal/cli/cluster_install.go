package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
)

// memberInstall is what pacto cluster install produced: the files, what to
// know before applying them and, after a push, the pushed reference.
type memberInstall struct {
	Engine    string                 `json:"engine"`
	Files     []delivery.InstallFile `json:"files"`
	Warnings  []string               `json:"warnings"`
	Reference string                 `json:"reference,omitempty"`
}

func newClusterInstallCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Generate the install of the Pacto chart on every member, for Argo CD or Flux to apply",
		Long: "Prints an Argo CD ApplicationSet (--from argocd) or Flux HelmReleases (--from flux), one per kubeconfig ref the hub's Flux names, " +
			"that install the Pacto chart on every cluster the engine reaches. The values are only registry, hub.url when you pass --hub-url " +
			"and insecureRegistries with --insecure-registry. Pacto writes nothing to Git and applies nothing: you hand the files to the engine. " +
			"Deleting them never uninstalls Pacto from a member. Each new cluster then publishes its snapshot and waits for a person's approval. " +
			"--push writes the files to <registry>/pacto-targets with your registry credentials, tagged by their digest, and prints for Flux " +
			"the bootstrap that pins that digest; --digest pins one pushed earlier.",
		Example: "  pacto cluster install --from argocd --detected k8s:pacto/pacto-detected > pacto-members.yaml\n" +
			"  pacto cluster install --from flux --registry ghcr.io/acme --chart-version 4.6.0 --detected k8s:pacto/pacto-detected --push",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := clusterInstall(cmd, svc, d)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), res, func() error { printMemberInstall(cmd, res); return nil }, nil)
		},
	}
	cmd.Flags().String("from", "", "the engine that applies the install: argocd or flux (required)")
	cmd.Flags().String("hub-url", "", "the hub's https:// or wss:// URL, set as hub.url on every member")
	cmd.Flags().String("chart", delivery.DefaultMemberChart, "the chart reference, for a mirror")
	cmd.Flags().String("chart-version", "", "the chart version to install (default the version the hub's operator advertises)")
	cmd.Flags().Bool("push", false, "push the files to <registry>/pacto-targets with your registry credentials and pin the bootstrap to their digest")
	cmd.Flags().String("digest", "", "pin the Flux bootstrap to an install pushed earlier, sha256:<64 hex>")
	cmd.Flags().Bool("insecure-registry", false, "reach the registry over plain HTTP, for a local registry only")
	_ = cmd.MarkFlagRequired("from")
	cmd.MarkFlagsMutuallyExclusive("push", "digest")
	addClusterHubFlags(cmd)
	return cmd
}

// clusterInstall generates the install cmd asks for from the hub's
// detection, and pushes it with --push.
func clusterInstall(cmd *cobra.Command, svc *app.Service, d deps) (memberInstall, error) {
	ctx := cmd.Context()
	flag := func(name string) string { s, _ := cmd.Flags().GetString(name); return s }
	push, _ := cmd.Flags().GetBool("push")
	insecure, _ := cmd.Flags().GetBool("insecure-registry")
	dyn, opts, err := clusterHub(cmd, d)
	if err != nil {
		return memberInstall{}, err
	}
	o := delivery.MemberInstallOptions{Engine: flag("from"), Registry: strings.TrimSuffix(opts.Repository, "/"+delivery.TargetSnapshotRepository),
		HubURL: flag("hub-url"), Chart: flag("chart"), ChartVersion: flag("chart-version"), Digest: flag("digest"), InsecureRegistry: insecure}
	if o.ChartVersion == "" && flag("detected") != "" {
		if o.ChartVersion, err = app.AdvertisedChartVersion(ctx, dyn, flag("detected"), time.Now); err != nil {
			return memberInstall{}, err
		}
	}
	if opts.Candidates != nil {
		c, err := opts.Candidates(ctx)
		if err != nil {
			return memberInstall{}, fmt.Errorf("the candidate clusters were not read: %w", err)
		}
		o.Flux, o.Argo = candidatesOf(c, detect.SourceFlux), candidatesOf(c, detect.SourceArgoCD)
		o.Matched = visibleJoins(svc.ClusterJoins(ctx, opts), true, nil).Candidates
	}
	res, err := delivery.MemberInstall(o)
	if err != nil || !push {
		return memberInstall{Engine: o.Engine, Files: res.Files, Warnings: res.Warnings}, err
	}
	repo, err := d.newRecordRepo(opts.Repository)
	if err != nil {
		return memberInstall{}, err
	}
	desc, err := deliveryrecord.PushMemberInstall(ctx, repo, o.Engine, delivery.InstallArchive(res.Files))
	if err != nil {
		return memberInstall{}, fmt.Errorf("pushing the member install: %w", err)
	}
	out := memberInstall{Engine: o.Engine, Files: res.Files, Warnings: res.Warnings, Reference: opts.Repository + "@" + desc.Digest.String()}
	if o.Engine == delivery.InstallFlux {
		o.Digest = desc.Digest.String()
		res, _ = delivery.MemberInstall(o) // the same options, which generated already
		out.Files = res.Files
	}
	return out, nil
}

// candidatesOf are the candidates of c from source.
func candidatesOf(c detect.Clusters, source string) []detect.Candidate {
	var out []detect.Candidate
	for _, cand := range c.Candidates {
		if cand.Source == source {
			out = append(out, cand)
		}
	}
	return out
}

// printMemberInstall prints the files to apply on stdout, and the warnings
// and what was pushed on stderr. After a push, stdout holds only what is
// left to apply: the Flux bootstrap, or for Argo CD the pushed reference.
func printMemberInstall(cmd *cobra.Command, res memberInstall) {
	w, e := cmd.OutOrStdout(), cmd.ErrOrStderr()
	for _, warn := range res.Warnings {
		_, _ = fmt.Fprintln(e, "warning: "+warn)
	}
	files, bootstrap := res.Files, false
	if n := len(files); n > 0 && files[n-1].Path == "bootstrap.yaml" {
		bootstrap = true
	}
	switch {
	case res.Reference != "" && bootstrap:
		_, _ = fmt.Fprintf(e, "pushed %s; apply the bootstrap below on the hub, which pins it\n", res.Reference)
		files = files[len(files)-1:]
	case res.Reference != "":
		_, _ = fmt.Fprintf(e, "pushed %s; point an Argo CD Application at it by this digest\n", res.Reference)
		_, _ = fmt.Fprintln(w, res.Reference)
		return
	case res.Engine == delivery.InstallFlux && !bootstrap:
		_, _ = fmt.Fprintln(e, "apply these on the hub, or pass --push (or --digest of an earlier push) for a bootstrap that pins them by digest")
	}
	_, _ = w.Write(delivery.MemberInstallYAML(files))
}
