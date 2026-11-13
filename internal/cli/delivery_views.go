package cli

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
)

// deliveryViews are the dashboard's delivery views a command reads: the same
// route, the same parameters and the same answer as the dashboard and the
// pacto_delivery_* read tools.
var deliveryViews = []struct{ route, short string }{
	{"inbox", "Show what waits on you: held changes your teams may answer, frozen Stages and the Errored changes on them"},
	{"environments", "Show every Stage by service and environment: what it runs and whether it is verified, failed, frozen or held"},
	{"autonomy", "Show the path to autonomy: each Stage's level, the next level Pacto proposes and what blocks it"},
	{"published", "Show the versions the Stages know per service and whether Flux verified their signature"},
	{"refusals", "Show the publishes Pacto refused and the version bump each change needs"},
}

func newDeliveryViewCommands(d deps) []*cobra.Command {
	var out []*cobra.Command
	for _, view := range deliveryViews {
		out = append(out, newDeliveryViewCommand(d, view.route, view.short))
	}
	return out
}

// newDeliveryViewCommand runs one dashboard delivery view in process, as the
// kubeconfig user. Each route parameter is a flag of the same name.
func newDeliveryViewCommand(d deps, route, short string) *cobra.Command {
	params, description, _ := pactomcp.DeliveryReadParams(route) // every view is a read route
	cmd := &cobra.Command{
		Use:   route,
		Short: short,
		Long: description + "\n\nIt runs the dashboard's own " + route + " view with your kubeconfig identity, so the apiserver decides what you see. " +
			"Without -n it reads every namespace you can list; one you cannot makes the answer partial and is named in unreadable. " +
			"The answer is the dashboard's JSON in every output format.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("namespace") {
				ns = ""
			}
			cluster, _ := d.clusterConfig() // nil: no discovery and no namespace listing
			reads := mcpDeliveryReads(baseDashboardIdentity(cluster, d, c.Trust, c.PolicyNamespace), nil, ns)(c, "")
			body, err := reads(cmd.Context(), route, viewQuery(cmd, params))
			if err != nil {
				return err
			}
			// ponytail: JSON for every format; a table per view when people ask for one.
			return printJSON(cmd, json.RawMessage(body))
		},
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
