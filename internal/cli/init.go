package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/internal/pactify"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/override"
)

// initDynamic is the cluster client pacto init --cluster reads with.
// ponytail: a package variable so tests swap it; move it into deps when root.go next changes.
var initDynamic = k8sclient.NewDynamic

func newInitCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	var opts app.InitOptions
	var cluster pactify.ClusterOptions
	var fromCluster bool
	var from, trust string
	var runPlugins []string
	cmd := &cobra.Command{
		Use:   "init [dir...]",
		Short: "Pactify a service: write its contract, or a blank one",
		Long: `Writes the contract for the service in dir (default: the current folder).

An empty or new folder gets a blank contract with the bundle structure
(interfaces/, configuration/) and the owner and domain you pass; with no
--owner the owner is a commented placeholder that Unknown names. A folder that
already holds a service gets a contract read from its Dockerfile and
Kubernetes manifests, and a list of what Pacto found and what it could not
tell (Unknown). The workload's environment, including the ConfigMaps it reads,
becomes a configuration schema with each value as its default. A URL in it
that names another service in the cluster becomes a dependency: with that
service's contract in --fleet, its bundle and a range from its version;
without it, a placeholder range that the comment above it and Unknown name. A
readiness, liveness or startup probe becomes a health check.

Either contract also gets a publish section, for pacto publish, with its
bundle under --registry (default: the workload's image registry, else the
registry.example placeholder). To describe the service and leave delivery out,
pass --describe-only: the contract then has no publish section and needs no
signing key. --delivery also writes the cluster pieces: the Pacto resource
that checks the running service, and per environment a Stage with the Flux
OCIRepository and Kustomization it applies through. Each Stage starts at
observe: it plans every new version and moves nothing, and the OCIRepository
stays pinned to the tag you deploy, so GitOps keeps applying what it did. The
last environment's Stage also asks a person to approve every promote.

--owner is the team's bare name, such as billing: usually the team label on
the workload. --domain is the business domain, such as payments. An AI agent
is a service too: its tools are an mcp interface, the model it calls is a
dependency and its eval suite is a checks entry; see
https://pacto.run/latest/contract-reference/checks/#an-agent-is-a-service.

Several folders are each pactified, with a summary at the end. --cluster
drafts a contract for every workload in the namespaces that no Pacto
describes yet, in <out>/<namespace>/<name>: its manifests are read from the
cluster, and the services its pods were seen calling become dependencies
pinned to the version seen running, for you to widen. Drafts are only
written, never published, and a draft that is not valid fails the command.

With --from the service is created from a golden path: an oci:// reference or
a directory whose contract declares service.template. The path's contract is
copied with its identity reset: the name, version 0.1.0, --owner (required)
and --domain, with --values and --set applied over any field and
service.origin recording the path it came from. A path read from a registry
is pulled by digest; when the trust config (--trust, else
$PACTO_DELIVERY_TRUST) names a template key, the path must be signed by one
or nothing is created, and otherwise the signature is reported Unknown. The
created contract is validated with its references resolved before anything is
written, and the Status block says what was checked and what Pacto cannot
tell yet. Each plugin the path names is listed with the binary it resolves
to; only the ones you name with --run-plugins run, never with --plan, and
the files they return are added like the path's own. --delivery plans the
cluster pieces from the created contract when the path publishes manifests
under publish.deploy; otherwise the delivery step says why none were planned.

It only adds files and missing .pactoignore lines; any other file already
there is yours and is left as it is, so running it twice is safe. --plan shows exactly what would be added and
changes nothing.`,
		Example: `  pacto init my-service
  pacto init my-service --owner billing --domain payments --registry ghcr.io/acme
  pacto init --plan
  pacto init --describe-only --owner billing
  pacto init --delivery --owner payments --fleet ../contracts
  pacto init --delivery --no-transparency-log --insecure-registry
  pacto init --delivery --registry-secret registry-creds
  pacto init services/checkout services/payments --plan
  pacto init --cluster --namespace shop --out drafts
  pacto init gift-cards --from oci://ghcr.io/acme/paths/http-service:1.0.0 --owner checkout --domain commerce`,
		RunE: func(cmd *cobra.Command, args []string) error {
			format := v.GetString(outputFormatKey)
			overrides := getOverrides(cmd)
			if err := fromChecks(from, trust, runPlugins, fromCluster, len(args), overrides); err != nil {
				return err
			}
			switch {
			case fromCluster && len(args) > 0:
				return errors.New("--cluster reads the cluster, not folders: drop the folders")
			case !fromCluster && (cluster.Out != "" || len(cluster.Namespaces) > 0):
				return errors.New("--out and --namespace go with --cluster")
			case fromCluster:
				dyn, err := initDynamic()
				if err != nil {
					return err
				}
				b, err := pactify.Cluster(cmd.Context(), dyn, opts, cluster, os.WriteFile)
				return printBulk(cmd, b, err, format)
			case len(args) > 1:
				b, err := pactify.Many(cmd.Context(), opts, args, os.WriteFile)
				return printBulk(cmd, b, err, format)
			}
			opts.Dir = "."
			if len(args) == 1 {
				opts.Dir = args[0]
			}
			result, err := initOne(cmd, svc, opts, app.InitFrom{From: from, Overrides: overrides, RunPlugins: runPlugins}, trust)
			if err != nil {
				return err
			}
			return printInitResult(cmd, result, format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Plan, "plan", false, "show what would be added and change nothing")
	f.BoolVar(&opts.DescribeOnly, "describe-only", false, "write a contract that only describes the service: no publish section, so no signing key")
	f.BoolVar(&opts.Delivery, "delivery", false, "also write the cluster pieces: the Pacto resource, a Stage per environment and its Flux OCIRepository and Kustomization")
	f.StringVar(&opts.Name, "name", "", "service name (default: the workload's name, then the folder's)")
	f.StringVar(&opts.Owner, "owner", "", "the team that owns the service, as a bare name such as billing (default: the workload's team label)")
	f.StringVar(&opts.Domain, "domain", "", "business domain, such as payments")
	f.StringVar(&opts.Registry, "registry", "", "where to publish, as host/path (default: read from the workload's image)")
	f.StringSliceVar(&opts.Environments, "env", []string{"test", "prod"}, "environments for --delivery, in promotion order")
	f.BoolVar(&opts.NoTransparencyLog, "no-transparency-log", false, "write transparencyLog: false under publish.sign, so signatures stay out of the public transparency log (offline signing, a throwaway key)")
	f.BoolVar(&opts.InsecureRegistry, "insecure-registry", false, "with --delivery: the Flux OCIRepositories read the registry over plain HTTP (a local registry only)")
	f.StringVar(&opts.RegistrySecret, "registry-secret", "", "with --delivery: the docker-registry Secret, in each environment's namespace, that Flux and the Pacto resource log in to a private registry with")
	f.StringSliceVar(&opts.FleetRoots, "fleet", nil, "folders of other services' contracts to find the services that use this one")
	f.BoolVar(&fromCluster, "cluster", false, "draft a contract for every workload in the cluster that no Pacto describes, reading the current kubeconfig context")
	f.StringSliceVar(&cluster.Namespaces, "namespace", nil, "with --cluster: a namespace to read; repeat it for more (default: every namespace)")
	f.StringVar(&cluster.Out, "out", "", "with --cluster: the folder the drafts go in, one <namespace>/<name> folder each")
	f.StringVar(&from, "from", "", "a golden path to create the service from: an oci:// reference or a directory")
	f.StringVar(&trust, "trust", "", "the trust config the golden path's signature is verified with: a PEM public key or a trust config file (default $PACTO_DELIVERY_TRUST)")
	f.StringSliceVar(&runPlugins, "run-plugins", nil, "run these plugins the golden path names, by name (see the plan's plugin steps); each step prints the binary it runs")
	addOverrideFlags(cmd)
	return cmd
}

// fromChecks refuses what cannot go with --from, and what needs it.
func fromChecks(from, trust string, runPlugins []string, fromCluster bool, folders int, overrides override.Overrides) error {
	switch {
	case from != "" && (fromCluster || folders > 1):
		return errors.New("--from creates one service in one folder: drop --cluster and the extra folders")
	case from == "" && (!overrides.IsEmpty() || trust != "" || len(runPlugins) > 0):
		return errors.New("--values, --set, --trust and --run-plugins go with --from")
	}
	return nil
}

// initOne pactifies one folder, or creates it from the golden path from.From
// with the trust config the --trust flag or the environment names.
func initOne(cmd *cobra.Command, svc *app.Service, opts app.InitOptions, from app.InitFrom, trust string) (*app.InitResult, error) {
	if from.From == "" {
		return svc.Init(cmd.Context(), opts)
	}
	t, err := templateTrust(trust)
	if err != nil {
		return nil, err
	}
	from.Trust = t
	return svc.InitFrom(cmd.Context(), opts, from)
}

// templateTrust reads the trust config a golden path is verified with. A bare
// PEM key given to --trust is a template key for every repository, since the
// flag says what it is for; one in $PACTO_DELIVERY_TRUST stays a record key.
func templateTrust(flag string) (*delivery.TrustConfig, error) {
	t, err := deliveryTrust(flag)
	if err != nil || t == nil || flag == "" {
		return t, err
	}
	raw, _ := os.ReadFile(flag) // ponytail: deliveryTrust just read it
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("-----BEGIN")) {
		t.Keys[0].Role = delivery.RoleTemplate
	}
	return t, nil
}

// printBulk prints each service's plan or result, then a summary table, and
// returns err after, so a failed folder or draft still fails the command.
func printBulk(cmd *cobra.Command, b *pactify.Bulk, err error, format string) error {
	if b == nil {
		return err
	}
	if format == "json" {
		if perr := printJSON(cmd, b); perr != nil {
			return perr
		}
		return err
	}
	w := cmd.OutOrStdout()
	for _, r := range b.Results {
		if r != nil {
			_ = printInitResult(cmd, r, format) // ponytail: text output never fails
			_, _ = fmt.Fprintln(w)
		}
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SERVICE\tFOLDER\tADDED\tDEPENDENCIES\tUNKNOWN")
	for _, r := range b.Results {
		if r == nil {
			continue
		}
		added := 0
		for _, f := range r.Files {
			if f.Action != pactify.Keep {
				added++
			}
		}
		deps := "-"
		for i, d := range r.Dependencies {
			if i == 0 {
				deps = d.Name
			} else {
				deps += "," + d.Name
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\n", r.Service, r.Dir, added, deps, len(r.Unknown))
	}
	for _, s := range b.Skipped {
		_, _ = fmt.Fprintf(tw, "%s\t\tskipped: a Pacto describes it\t\t\n", s)
	}
	_ = tw.Flush()
	return err
}
