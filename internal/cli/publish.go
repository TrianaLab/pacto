package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
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
		Use:   "publish [dir]",
		Short: "Publish what the contract declares, as the version it states",
		Long: "Publishes every artifact the contract's publish section declares (pactoVersion 2.1), the contract " +
			"bundle and the deploy artifact Flux applies, all tagged with service.version. Docker or the declared " +
			"command builds; Pacto pushes, never over an existing tag, signs every digest with Sigstore (no cosign needed) and attaches " +
			"the SBOM files the build wrote. A breaking change since the last published version is refused below a major bump " +
			"(a minor before 1.0).\n\n" +
			"--plan shows what is missing and changes nothing. Running publish again from the same commit finishes what is " +
			"missing and changes nothing else. --propose hands the deploy artifact to Pacto delivery as a promote on a Stage, " +
			"once the Stage recorded it as published: Flux polled the candidate OCIRepository and verified the " +
			"signature. It waits up to --propose-timeout for that.",
		Example: `  # What would be published, and what refuses it
  pacto publish --plan

  # The same, for the contract in ./payments
  pacto publish --plan ./payments

  # Publish, then propose the version on the test Stage
  pacto publish --propose test/payments`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, propose, ns, stage, err := publishArgs(cmd, args)
			if err != nil {
				return err
			}
			plan, _ := cmd.Flags().GetBool("plan")
			revision, _ := cmd.Flags().GetString("revision")
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
			failed := err != nil // refused or failed: say nothing about what is published
			if err == nil && plan && !res.Plan.Ready {
				err = errPlanNotReady
			}
			if err == nil && propose != "" && res.Deploy != "" {
				timeout, _ := cmd.Flags().GetDuration("propose-timeout")
				err = proposePublished(cmd, d, ns, stage, res, timeout)
			}
			if perr := printPublishResult(cmd, res, format, failed); perr != nil {
				return perr
			}
			return err
		},
	}
	cmd.Flags().StringP("path", "p", "", "path to contract directory, the same as [dir] (default: current directory)")
	cmd.Flags().Bool("plan", false, "show what is missing and what refuses it; change nothing")
	cmd.Flags().String("revision", "", "source revision stamped on every artifact (default: the git commit of the contract directory, which must have no uncommitted changes)")
	cmd.Flags().String("propose", "", "after publishing, propose the deploy artifact on <namespace>/<stage> (needs the pacto-delivery chart)")
	cmd.Flags().Duration("propose-timeout", 10*time.Minute, "how long --propose waits for the Stage to record the version as published")
	addOverrideFlagsNoShorthand(cmd)
	return cmd
}

// publishArgs reads the contract directory, given as [dir] or --path, and
// the <namespace>/<stage> --propose names.
func publishArgs(cmd *cobra.Command, args []string) (path, propose, ns, stage string, err error) {
	path, _ = cmd.Flags().GetString("path")
	if len(args) == 1 {
		if path != "" && path != args[0] {
			return "", "", "", "", fmt.Errorf("the contract directory is given twice: %s and --path %s", args[0], path)
		}
		path = args[0]
	}
	propose, _ = cmd.Flags().GetString("propose")
	ns, stage, ok := strings.Cut(propose, "/")
	if propose != "" && (!ok || ns == "" || stage == "") {
		return "", "", "", "", fmt.Errorf("--propose takes <namespace>/<stage>, not %q", propose)
	}
	return path, propose, ns, stage, nil
}

// publishPoll is how often --propose reads the Stage while it waits.
var publishPoll = 5 * time.Second

// proposePublished raises a promote of the published deploy artifact as the
// caller, through the same client `pacto delivery propose` uses, once the
// Stage recorded it as published.
func proposePublished(cmd *cobra.Command, d deps, ns, stage string, res *publish.Result, timeout time.Duration) error {
	dyn, err := d.newDynamic()
	if err != nil {
		return err
	}
	c := &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry, Now: time.Now,
		Policy: delivery.DefaultClassPolicy(), PolicyNamespace: delivery.PolicyNamespace}
	_, digest, _ := strings.Cut(res.Deploy, "@")
	if err := waitPublished(cmd, c, ns, stage, digest, timeout); err != nil {
		return fmt.Errorf("published, but not proposed on %s/%s: %w", ns, stage, err)
	}
	op, _, err := c.Propose(cmd.Context(), delivery.Request{Namespace: ns, Stage: stage, To: digest,
		Reason: "pacto publish " + res.Plan.Service + " " + res.Plan.Version})
	if err != nil {
		return fmt.Errorf("published, but proposing it on %s/%s failed: %w", ns, stage, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Proposed %s on %s/%s as Operation %s\n", digest, ns, stage, op.Metadata.Name)
	return nil
}

// waitPublished waits until the Stage records digest as published. The
// promoter refuses a promote from a candidate OCIRepository until then, and it
// only happens after Flux polled the new tag and verified its signature. A
// Stage that takes its candidates from another Stage has nothing to wait for.
func waitPublished(cmd *cobra.Command, c *app.Delivery, ns, stage, digest string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		s, err := c.Stage(cmd.Context(), ns, stage)
		if err != nil {
			return err
		}
		if s.Spec.Candidates.OCIRepositoryRef == nil || slices.Contains(s.Status.Published, digest) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the Stage did not record %s as published within %s: Flux has not polled the candidate OCIRepository %s "+
				"or verified its signature yet; propose it later with `pacto delivery propose`", digest, timeout, s.Spec.Candidates.OCIRepositoryRef.Name)
		}
		time.Sleep(publishPoll)
	}
}

// printPublishResult prints what a publish did. failed is true when it was
// refused or failed: the error says why, so nothing claims it is all published.
func printPublishResult(cmd *cobra.Command, res *publish.Result, format string, failed bool) error {
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
		case res.Digests == nil:
			_, _ = fmt.Fprintf(w, "%d to publish; nothing was changed (--plan).\n", p.Missing)
		case !failed:
			_, _ = fmt.Fprintln(w, "Nothing to publish: every artifact is already published and signed.")
		}
		if res.Deploy != "" && !failed { // a failed run committed no deploy tag
			_, _ = fmt.Fprintln(w, "Deploy: "+res.Deploy)
		}
		if res.Refusal != "" {
			_, _ = fmt.Fprintln(w, "Refusal: "+res.Refusal)
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
