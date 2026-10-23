package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/trianalab/pacto/v4/internal/app"
)

func newInitCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	var opts app.InitOptions
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Pactify a service: write its contract, or a blank one",
		Long: `Writes the contract for the service in dir (default: the current folder).

An empty or new folder gets a blank contract with the bundle structure
(interfaces/, configuration/). A folder that already holds a service gets a
contract read from its Dockerfile and Kubernetes manifests, publish section
included, and a list of what Pacto found and what it could not tell
(Unknown). --delivery also writes the cluster pieces: the Pacto resource that
checks the running service, and per environment a Stage with the Flux
OCIRepository and Kustomization it applies through.

It only adds files and missing .pactoignore lines; any other file already
there is yours and is left as it is, so running it twice is safe. --plan shows exactly what would be added and
changes nothing.`,
		Example: `  pacto init my-service
  pacto init --plan
  pacto init --delivery --owner payments --fleet ../contracts`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Dir = "."
			if len(args) == 1 {
				opts.Dir = args[0]
			}
			result, err := svc.Init(cmd.Context(), opts)
			if err != nil {
				return err
			}
			return printInitResult(cmd, result, v.GetString(outputFormatKey))
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Plan, "plan", false, "show what would be added and change nothing")
	f.BoolVar(&opts.Delivery, "delivery", false, "also write the cluster pieces: the Pacto resource, a Stage per environment and its Flux OCIRepository and Kustomization")
	f.StringVar(&opts.Name, "name", "", "service name (default: the workload's name, then the folder's)")
	f.StringVar(&opts.Owner, "owner", "", "the team that owns the service (default: the workload's team label)")
	f.StringVar(&opts.Registry, "registry", "", "where to publish, as host/path (default: read from the workload's image)")
	f.StringSliceVar(&opts.Environments, "env", []string{"test", "prod"}, "environments for --delivery, in promotion order")
	f.StringSliceVar(&opts.FleetRoots, "fleet", nil, "folders of other services' contracts to find the services that use this one")
	return cmd
}
