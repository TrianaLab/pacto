package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

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
	o := delivery.MemberInstallOptions{Engine: flag("from"), HubURL: flag("hub-url"), Chart: flag("chart"), ChartVersion: flag("chart-version"),
		Digest: flag("digest"), InsecureRegistry: insecure}
	matched := func(ctx context.Context) []delivery.ClusterCandidate {
		return visibleJoins(svc.ClusterJoins(ctx, opts), true, nil).Candidates
	}
	if err := installInputs(ctx, dyn, opts, flag("detected"), &o, matched); err != nil {
		return memberInstall{}, err
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

// installInputs fills o from the hub dyn reaches through opts: the
// registry its members publish to, the chart version its detection at
// detected advertises when o names none, and its candidates, with matched
// (the hub's list of them) for the warnings.
func installInputs(ctx context.Context, dyn dynamic.Interface, opts app.JoinOptions, detected string, o *delivery.MemberInstallOptions,
	matched func(context.Context) []delivery.ClusterCandidate) error {
	o.Registry = strings.TrimSuffix(opts.Repository, "/"+delivery.TargetSnapshotRepository)
	var err error
	if o.ChartVersion == "" && detected != "" {
		if o.ChartVersion, err = app.AdvertisedChartVersion(ctx, dyn, detected, time.Now); err != nil {
			return err
		}
	}
	if opts.Candidates == nil {
		return nil
	}
	c, err := opts.Candidates(ctx)
	if err != nil {
		return fmt.Errorf("the candidate clusters were not read: %w", err)
	}
	o.Flux, o.Argo = c.From(detect.SourceFlux), c.From(detect.SourceArgoCD)
	o.Matched = matched(ctx)
	return nil
}

// dashboardInstall hands out, for the engine from, the member install of
// the hub the dashboard reads in cluster through its detection, with list's
// candidates for the warnings, which lead the YAML as comments. It never
// sets a hub URL: a crafted link could otherwise point every new member at
// another hub. nil without joins.
func dashboardInstall(d deps, cluster *rest.Config, detected string, joins *app.JoinOptions,
	list func(context.Context) delivery.ClusterJoinList) func(context.Context, string) ([]byte, error) {
	if joins == nil {
		return nil
	}
	return func(ctx context.Context, from string) ([]byte, error) {
		dyn, err := d.dynamicFor(cluster)
		if err != nil {
			return nil, err
		}
		opts, err := joinsOn(ctx, dyn, "", detected, "")
		if err != nil {
			return nil, err
		}
		if opts.Repository == "" {
			return nil, errors.New("the hub's operator advertises no registry: set registry on it")
		}
		o := delivery.MemberInstallOptions{Engine: from}
		matched := func(ctx context.Context) []delivery.ClusterCandidate { return list(ctx).Candidates }
		if err := installInputs(ctx, dyn, opts, detected, &o, matched); err != nil {
			return nil, err
		}
		res, err := delivery.MemberInstall(o)
		if err != nil {
			return nil, err
		}
		return delivery.MemberInstallDownload(res), nil
	}
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
