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
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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
	cmd.Flags().String("digest", "", "pin the Flux bootstrap to an install pushed earlier, sha256:<64 hex> (--from flux only)")
	cmd.Flags().Bool("insecure-registry", false, "members, and --push, reach the registry over plain HTTP, for a local registry only (the member list read follows PACTO_INSECURE_REGISTRIES)")
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
	matched := func(ctx context.Context) delivery.ClusterJoinList {
		return visibleJoins(svc.ClusterJoins(ctx, opts), true, nil)
	}
	if err := installInputs(ctx, dyn, opts, flag("detected"), &o, matched); err != nil {
		return memberInstall{}, err
	}
	res, err := delivery.MemberInstall(o)
	if err != nil || !push {
		return memberInstall{Engine: o.Engine, Files: res.Files, Warnings: res.Warnings}, err
	}
	var hosts []string
	if insecure {
		host, _, _ := strings.Cut(o.Registry, "/")
		hosts = append(hosts, host)
	}
	repo, err := d.newRecordRepo(opts.Repository, hosts...)
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

// notRuledOut leads every warning that says why clusters already running
// Pacto may get no warning of their own.
const notRuledOut = "clusters that already run Pacto could not all be ruled out: "

// installInputs fills o from the hub dyn reaches through opts: the
// registry its members publish to, the chart version its detection at
// detected advertises when o names none, and its candidates, with joins
// (the hub's list of them) for the warnings. Candidates that were not read,
// are stale or short of complete warn; Flux, whose files are built from its
// refs, refuses refs it could not read rather than calling them absent. A
// failed read of the hub is a dashboard.HubReadError.
func installInputs(ctx context.Context, dyn dynamic.Interface, opts app.JoinOptions, detected string, o *delivery.MemberInstallOptions,
	joins func(context.Context) delivery.ClusterJoinList) error {
	o.Registry, o.HubUID = strings.TrimSuffix(opts.Repository, "/"+delivery.TargetSnapshotRepository), opts.Hub
	var err error
	if o.ChartVersion == "" && detected != "" {
		if o.ChartVersion, err = app.AdvertisedChartVersion(ctx, dyn, detected, time.Now); err != nil {
			return dashboard.HubReadError{Err: err}
		}
	}
	flux := o.Engine == delivery.InstallFlux
	if opts.Candidates == nil {
		if flux {
			return errors.New("the Flux kubeconfig refs come from the hub's detection: pass --detected")
		}
		o.Warnings = append(o.Warnings, notRuledOut+"the hub's candidate clusters were not read (pass --detected)")
		return nil
	}
	c, err := opts.Candidates(ctx)
	if err != nil {
		return dashboard.HubReadError{Err: fmt.Errorf("the candidate clusters were not read: %w", err)}
	}
	if c.AsOf.IsZero() {
		const off = "the hub's operator runs no cluster inventory: set clusterInventory.enabled on it"
		if flux {
			return errors.New(off)
		}
		o.Warnings = append(o.Warnings, notRuledOut+off)
		return nil
	}
	o.Flux, o.Argo = c.From(detect.SourceFlux), c.From(detect.SourceArgoCD)
	if s, ok := c.Source(detect.SourceArgoCD); ok {
		o.ArgoNamespaces = s.ControlPlane
	}
	if flux {
		if err := fluxUnread(c); err != nil {
			return err
		}
	}
	l := joins(ctx)
	o.Matched = l.Candidates
	o.Warnings = append(o.Warnings, joinWarnings(l)...)
	return nil
}

// fluxUnread says why c's Flux refs cannot stand for every ref the hub has:
// an unknown source, or none listed from a source short of complete or a
// stale read. nil when they can.
func fluxUnread(c detect.Clusters) error {
	refs := len(c.From(detect.SourceFlux))
	if s, ok := c.Source(detect.SourceFlux); ok && (s.Completeness == detect.Unknown || refs == 0 && s.Completeness != detect.Complete) {
		return fmt.Errorf("the hub's Flux kubeconfig refs were not read: %s", s.Reason)
	}
	if refs == 0 && time.Since(c.AsOf) > detect.StaleAfter {
		return fmt.Errorf("the hub's Flux kubeconfig refs were read at %s, over %s ago: check the operator's detection", c.AsOf.UTC().Format(time.RFC3339), detect.StaleAfter)
	}
	return nil
}

// joinWarnings say what of l's members and candidates was not read, so a
// cluster that already runs Pacto may get no warning of its own.
func joinWarnings(l delivery.ClusterJoinList) []string {
	var out []string
	if l.Completeness == fleet.CompletenessPartial {
		out = append(out, notRuledOut+"the member list is partial, so a candidate may state the UID of a member not read")
	}
	for _, lim := range l.Limitations {
		if lim.Source == delivery.CandidatesID {
			out = append(out, notRuledOut+lim.Message)
		}
	}
	return out
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
			return nil, dashboard.HubReadError{Err: err}
		}
		opts, err := joinsOn(ctx, dyn, "", detected, "")
		if err != nil {
			return nil, dashboard.HubReadError{Err: err}
		}
		if opts.Repository == "" {
			return nil, errors.New("the hub's operator advertises no registry: set registry on it")
		}
		o := delivery.MemberInstallOptions{Engine: from}
		if err := installInputs(ctx, dyn, opts, detected, &o, list); err != nil {
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
