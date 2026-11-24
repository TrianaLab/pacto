package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
)

// deliveryViews are the dashboard's delivery views a command reads: the same
// route, the same parameters and the same answer as the dashboard and the
// pacto_delivery_* read tools.
var deliveryViews = []struct{ route, short string }{
	{"caller", "Show who you are to delivery: the user the API server names and your actor class"},
	{"inbox", "Show what waits on you: held changes your teams may answer, frozen Stages, the Errored changes on them and an installation ladder proposal"},
	{"environments", "Show every Stage by service and environment: what it runs, whether it is verified, failed, frozen or held, and where it runs and why"},
	{"autonomy", "Show the path to autonomy: each Stage's level, the next level Pacto proposes and what blocks it"},
	{"published", "Show the versions the Stages know per service and whether Flux or the registry watch verified their signature"},
	{"refusals", "Show the publishes and promotes Pacto refused: the version bump a refused publish needs, the Stage of a refused promote and, with --registry on a hub, the requests its members refused"},
	{"trust", "Show which publishers each service accepts: the rule, the signed policy, the pinned identities and the signers waiting for a person"},
}

// memberViews are the views that also read a hub's members with --registry.
var memberViews = map[string]bool{"refusals": true}

// positionalService are the views whose one optional argument is --service.
var positionalService = map[string]bool{"trust": true}

func newDeliveryViewCommands(svc *app.Service, d deps) []*cobra.Command {
	var out []*cobra.Command
	for _, view := range deliveryViews {
		out = append(out, newDeliveryViewCommand(svc, d, view.route, view.short))
	}
	return out
}

// newDeliveryViewCommand runs one dashboard delivery view in process, as the
// kubeconfig user. Each route parameter is a flag of the same name.
func newDeliveryViewCommand(svc *app.Service, d deps, route, short string) *cobra.Command {
	params, description, _ := pactomcp.DeliveryReadParams(route) // every view is a read route
	cmd := &cobra.Command{
		Use:   route,
		Short: short,
		Long: description + "\n\nIt runs the dashboard's own " + route + " view with your kubeconfig identity, so the apiserver decides what you see. " +
			"Without -n it reads the namespaces you may list Stages in and leaves out, without naming them, those outside your permissions; " +
			"a namespace that refuses the list makes the answer partial and is named in unreadable. " +
			"The answer is the dashboard's JSON in every output format.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				if svc, _ := cmd.Flags().GetString("service"); cmd.Flags().Changed("service") && svc != args[0] {
					return fmt.Errorf("give the service once: as the argument or with --service, not %q and %q", args[0], svc)
				}
				_ = cmd.Flags().Set("service", args[0]) // a positionalService view declares service, a string flag: Set never fails
			}
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("namespace") {
				ns = ""
			}
			cluster, _ := d.clusterConfig() // nil: no discovery and no namespace listing
			id := baseDashboardIdentity(cluster, d, c.Trust, c.PolicyNamespace)
			if registry, _ := cmd.Flags().GetString("registry"); registry != "" {
				id.members = newMemberDelivery(svc, &app.JoinOptions{Resolve: func(ctx context.Context) (app.JoinOptions, error) {
					_, o, err := hubJoins(ctx, d, "", registry, "", c.PolicyNamespace)
					return o, err
				}})
			}
			reads := mcpDeliveryReads(id, nil, ns)(c, "")
			body, err := reads(cmd.Context(), route, viewQuery(cmd, params))
			if err != nil {
				return err
			}
			// ponytail: JSON for every format; a table per view when people ask for one.
			return printJSON(cmd, json.RawMessage(body))
		},
	}
	if memberViews[route] {
		cmd.Flags().String("registry", "", "on a hub: also read the requests its members refused, from the request streams of the members that publish to this registry")
	}
	if positionalService[route] {
		cmd.Use, cmd.Args = route+" [service]", cobra.MaximumNArgs(1)
	}
	for _, p := range params {
		help := p.Description
		if len(p.Enum) > 0 {
			help += " (" + strings.Join(p.Enum, ", ") + ")"
		}
		switch p.Type {
		case "integer":
			cmd.Flags().Int(p.Name, 0, help)
		case "array":
			cmd.Flags().StringArray(p.Name, nil, help)
		default:
			cmd.Flags().String(p.Name, "", help)
		}
	}
	return cmd
}

// viewQuery is the route query of the flags set on cmd.
func viewQuery(cmd *cobra.Command, params []pactomcp.DeliveryReadParam) url.Values {
	q := url.Values{}
	for _, p := range params {
		if !cmd.Flags().Changed(p.Name) {
			continue
		}
		switch p.Type {
		case "integer":
			n, _ := cmd.Flags().GetInt(p.Name)
			q.Set(p.Name, strconv.Itoa(n))
		case "array":
			q[p.Name], _ = cmd.Flags().GetStringArray(p.Name)
		default:
			s, _ := cmd.Flags().GetString(p.Name)
			q.Set(p.Name, s)
		}
	}
	return q
}
