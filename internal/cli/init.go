package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/internal/pactify"
)

// initDynamic is the cluster client pacto init --cluster reads with.
// ponytail: a package variable so tests swap it; move it into deps when root.go next changes.
var initDynamic = k8sclient.NewDynamic

func newInitCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	var opts app.InitOptions
	var cluster pactify.ClusterOptions
	var fromCluster bool
	cmd := &cobra.Command{
		Use:   "init [dir...]",
		Short: "Pactify a service: write its contract, or a blank one",
		Long: `Writes the contract for the service in dir (default: the current folder).

An empty or new folder gets a blank contract with the bundle structure
(interfaces/, configuration/). A folder that already holds a service gets a
contract read from its Dockerfile and Kubernetes manifests, publish section
included, and a list of what Pacto found and what it could not tell
(Unknown). --delivery also writes the cluster pieces: the Pacto resource that
checks the running service, and per environment a Stage with the Flux
OCIRepository and Kustomization it applies through. Each Stage starts at
observe: it plans every new version and moves nothing, and the OCIRepository
stays pinned to the tag you deploy, so GitOps keeps applying what it did. The
last environment's Stage also asks a person to approve every promote.

Several folders are each pactified, with a summary at the end. --cluster
drafts a contract for every workload in the namespaces that no Pacto
describes yet, in <out>/<namespace>/<name>: its manifests are read from the
cluster, and the services its pods were seen calling become dependencies
pinned to the version seen running, for you to widen. Drafts are only
written, never published, and a draft that is not valid fails the command.

It only adds files and missing .pactoignore lines; any other file already
there is yours and is left as it is, so running it twice is safe. --plan shows exactly what would be added and
changes nothing.`,
		Example: `  pacto init my-service
  pacto init --plan
  pacto init --delivery --owner payments --fleet ../contracts
  pacto init --delivery --no-transparency-log --insecure-registry
  pacto init --delivery --registry-secret registry-creds
  pacto init services/checkout services/payments --plan
  pacto init --cluster --namespace shop --out drafts`,
		RunE: func(cmd *cobra.Command, args []string) error {
			format := v.GetString(outputFormatKey)
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
			result, err := svc.Init(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return printInitResult(cmd, result, format)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Plan, "plan", false, "show what would be added and change nothing")
	f.BoolVar(&opts.Delivery, "delivery", false, "also write the cluster pieces: the Pacto resource, a Stage per environment and its Flux OCIRepository and Kustomization")
	f.StringVar(&opts.Name, "name", "", "service name (default: the workload's name, then the folder's)")
	f.StringVar(&opts.Owner, "owner", "", "the team that owns the service (default: the workload's team label)")
	f.StringVar(&opts.Registry, "registry", "", "where to publish, as host/path (default: read from the workload's image)")
	f.StringSliceVar(&opts.Environments, "env", []string{"test", "prod"}, "environments for --delivery, in promotion order")
	f.BoolVar(&opts.NoTransparencyLog, "no-transparency-log", false, "write transparencyLog: false under publish.sign, so signatures stay out of the public transparency log (offline signing, a throwaway key)")
	f.BoolVar(&opts.InsecureRegistry, "insecure-registry", false, "with --delivery: the Flux OCIRepositories read the registry over plain HTTP (a local registry only)")
	f.StringVar(&opts.RegistrySecret, "registry-secret", "", "with --delivery: the docker-registry Secret, in each environment's namespace, that Flux and the Pacto resource log in to a private registry with")
	f.StringSliceVar(&opts.FleetRoots, "fleet", nil, "folders of other services' contracts to find the services that use this one")
	f.BoolVar(&fromCluster, "cluster", false, "draft a contract for every workload in the cluster that no Pacto describes, reading the current kubeconfig context")
	f.StringSliceVar(&cluster.Namespaces, "namespace", nil, "with --cluster: a namespace to read; repeat it for more (default: every namespace)")
	f.StringVar(&cluster.Out, "out", "", "with --cluster: the folder the drafts go in, one <namespace>/<name> folder each")
	return cmd
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
