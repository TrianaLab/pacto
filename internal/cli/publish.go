package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// errPlanNotReady fails `pacto publish --plan` when publishing would be refused.
var errPlanNotReady = errors.New("publishing this version would be refused")

func newPublishCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish what the contract declares, as the version it states",
		Long: "Publishes every artifact the contract's publish section declares (pactoVersion 2.1), the contract " +
			"bundle and the deploy artifact Flux applies, all tagged with service.version. Docker or the declared " +
			"command builds; Pacto pushes, never over an existing tag, signs every digest with cosign and attaches " +
			"SBOMs made by syft. A breaking change since the last published version is refused below a major bump " +
			"(a minor before 1.0).\n\n" +
			"--plan shows what is missing and changes nothing. Running publish again finishes what is missing and " +
			"changes nothing else. --propose hands the deploy artifact to Pacto delivery as a promote on a Stage.",
		Example: `  # What would be published, and what refuses it
  pacto publish --plan

  # Publish, then propose the version on the test Stage
  pacto publish --propose test/payments`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("path")
			plan, _ := cmd.Flags().GetBool("plan")
			revision, _ := cmd.Flags().GetString("revision")
			propose, _ := cmd.Flags().GetString("propose")
			ns, stage, ok := strings.Cut(propose, "/")
			if propose != "" && (!ok || ns == "" || stage == "") {
				return fmt.Errorf("--propose takes <namespace>/<stage>, not %q", propose)
			}
			format := v.GetString(outputFormatKey)
			start := time.Now()
			sp := startSpinner(cmd, format, "Publishing")
			res, err := d.publish(svc, cmd.Context(), app.PublishOptions{Path: path, Overrides: getOverrides(cmd), Revision: revision, Plan: plan})
			if err != nil || plan {
				sp.Stop()
			} else {
				sp.StopOK("Published", start)
			}
			if res == nil {
				return err
			}
			if err == nil && plan && !res.Plan.Ready {
				err = errPlanNotReady
			}
			if err == nil && propose != "" && res.Deploy != "" {
				err = proposePublished(cmd, d, ns, stage, res)
			}
			if perr := printPublishResult(cmd, res, format); perr != nil {
				return perr
			}
			return err
		},
	}
	cmd.Flags().StringP("path", "p", "", "path to contract directory (default: current directory)")
	cmd.Flags().Bool("plan", false, "show what is missing and what refuses it; change nothing")
	cmd.Flags().String("revision", "", "source revision stamped on every artifact (default: the git commit of --path)")
	cmd.Flags().String("propose", "", "after publishing, propose the deploy artifact on <namespace>/<stage> (needs the pacto-delivery chart)")
	addOverrideFlagsNoShorthand(cmd)
	return cmd
}

// proposePublished raises a promote of the published deploy artifact as the
// caller, through the same client `pacto delivery propose` uses.
func proposePublished(cmd *cobra.Command, d deps, ns, stage string, res *publish.Result) error {
	dyn, err := d.newDynamic()
	if err != nil {
		return err
	}
	c := &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry, Now: time.Now,
		Policy: delivery.ClassPolicy{AgentGroups: []string{"pacto:agents"}, BreakGlassGroups: []string{"pacto:break-glass"}}}
	_, digest, _ := strings.Cut(res.Deploy, "@")
	op, _, err := c.Propose(cmd.Context(), delivery.Request{Namespace: ns, Stage: stage, To: digest,
		Reason: "pacto publish " + res.Plan.Service + " " + res.Plan.Version})
	if err != nil {
		return fmt.Errorf("published, but proposing it on %s/%s failed: %w", ns, stage, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Proposed %s on %s/%s as Operation %s\n", digest, ns, stage, op.Metadata.Name)
	return nil
}

func printPublishResult(cmd *cobra.Command, res *publish.Result, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		p := res.Plan
		_, _ = fmt.Fprintf(w, "%s %s (revision %s)\n", p.Service, p.Version, orNone([]string{p.Revision}))
		writePublishChecks(w, p.Checks)
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ARTIFACT\tTYPE\tSTATE\tREF\tREASON")
		for _, it := range p.Items {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", it.Name, it.Type, it.State, it.Ref, it.Reason)
		}
		_ = tw.Flush()
		switch {
		case len(res.Actions) > 0:
			_, _ = fmt.Fprintln(w, strings.Join(res.Actions, "\n"))
		case res.Digests != nil:
			_, _ = fmt.Fprintln(w, "Nothing to publish: every artifact is already published and signed.")
		default:
			_, _ = fmt.Fprintf(w, "%d to publish; nothing was changed (--plan).\n", p.Missing)
		}
		if res.Deploy != "" {
			_, _ = fmt.Fprintln(w, "Deploy: "+res.Deploy)
		}
		return nil
	}, nil)
}

func writePublishChecks(w io.Writer, checks []publish.Check) {
	for _, c := range checks {
		mark := "ok  "
		if !c.Passed {
			mark = "FAIL"
		}
		_, _ = fmt.Fprintf(w, "  %s %s: %s\n", mark, c.Name, c.Message)
	}
}
