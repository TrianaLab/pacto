package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// errPlanNotReady fails `pacto publish --plan` when publishing would be refused.
var errPlanNotReady = errors.New("publishing this version would be refused")

func newPublishCommand(svc *app.Service, v *viper.Viper, d deps, rec *runRecorder) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish [dir]",
		Short: "Publish what the contract declares, as the version it states",
		Long: "Publishes every artifact the contract's publish section declares (pactoVersion 2.1), the contract " +
			"bundle and the deploy artifact Flux applies, all tagged with service.version. Docker or the declared " +
			"command builds. Pacto pushes each artifact to a new tag, signs every digest with Sigstore (built in) and attaches " +
			"the SBOM files the build wrote. A breaking change since the last published version is refused below a major bump " +
			"(a minor before 1.0).\n\n" +
			"--plan shows what is missing and changes nothing. Running publish again from the same commit finishes what is " +
			"missing and changes nothing else. --propose hands the deploy artifact to Pacto delivery as a promote on a Stage, " +
			"once the Stage records it as published, meaning Flux polled the candidate OCIRepository and verified the " +
			"signature. It waits up to --propose-timeout for that.",
		Example: `  # What would be published, and what refuses it
  pacto publish --plan

  # The same, for the contract in ./payments
  pacto publish --plan ./payments

  # Publish, then propose the version on the test Stage
  pacto publish --propose test/payments`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var out *runrecord.Record
			defer rec.save(cmd, &out, nil)
			path, propose, ns, stage, err := publishArgs(cmd, args)
			if err != nil {
				return err
			}
			plan, _ := cmd.Flags().GetBool("plan")
			revision, _ := cmd.Flags().GetString("revision")
			format := v.GetString(outputFormatKey)
			start := time.Now()
			sp := startSpinner(cmd, format, "Publishing")
			opts := app.PublishOptions{Path: path, Overrides: getOverrides(cmd), Revision: revision, Plan: plan, Getenv: os.Getenv}
			opts.Image, _ = cmd.Flags().GetString("image")
			opts.Registry, _ = cmd.Flags().GetString("registry")
			opts.SignKey, _ = cmd.Flags().GetString("sign-key")
			opts.NoTransparencyLog, _ = cmd.Flags().GetBool("no-transparency-log")
			opts.Index, _ = cmd.Flags().GetString("index")
			res, err := d.publish(svc, cmd.Context(), opts)
			if err != nil || plan {
				sp.Stop()
			} else {
				sp.StopOK("Published", start)
			}
			if res == nil {
				return err
			}
			failed := err != nil // refused or failed: say nothing about what is published
			if !plan {
				out = publishRecord(res, err)
			}
			if err == nil && plan && !res.Plan.Ready {
				err = errPlanNotReady
			}
			if err == nil && propose != "" && res.Deploy != "" {
				timeout, _ := cmd.Flags().GetDuration("propose-timeout")
				err = proposePublished(cmd, d, ns, stage, res, timeout, format)
			}
			if format == "json" {
				return printJSONExit(cmd, res, err)
			}
			_ = printPublishResult(cmd, res, format, failed) // ponytail: text and markdown output never fail
			return err
		},
	}
	cmd.Flags().StringP("path", "p", "", "path to contract directory, the same as [dir] (default: current directory)")
	cmd.Flags().Bool("plan", false, "show what is missing and what refuses it; change nothing")
	cmd.Flags().String("revision", "", "source revision stamped on every artifact (default: the git commit of the contract directory, which must have no uncommitted changes)")
	cmd.Flags().String("propose", "", "after publishing, propose the deploy artifact on <namespace>/<stage> (needs delivery.enabled on the pacto-operator chart)")
	cmd.Flags().String("on-behalf-of", "", "with --propose, the person an agent acts for, as the Kubernetes username the cluster authenticates them as (what kubectl auth whoami prints for them, any OIDC prefix included); asserted, not verified; required when an agent proposes, never a service account or other system user, and that person may not approve the change")
	cmd.Flags().String("correlation-id", "", "with --propose, an id that ties the promote to an incident or another change, such as a ticket; "+
		"kept with the change and its record, filtered by pacto delivery history --correlation-id")
	cmd.Flags().String("image", "", "with no publish section: the image to deploy, pinned by digest (repo@sha256:...); Pacto builds, signs and copies none")
	cmd.Flags().String("registry", "", "with no publish section: the registry to publish to (default: what the CI context or the workload image names)")
	cmd.Flags().String("sign-key", "", "with no publish section: sign with this cosign key file instead of keyless")
	cmd.Flags().Bool("no-transparency-log", false, "with --sign-key: keep the signatures out of the transparency log")
	cmd.Flags().Var(new(indexFlag), "index", indexFlagUsage)
	cmd.Flags().Duration("propose-timeout", 10*time.Minute, "how long --propose waits for the Stage to record the version as published")
	addOverrideFlagsNoShorthand(cmd)
	addStatsFlags(cmd)
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
	// Checked before publishing, so a bad id never leaves a version published but not proposed.
	if id, _ := cmd.Flags().GetString("correlation-id"); id != "" {
		if propose == "" {
			return "", "", "", "", errors.New("--correlation-id needs --propose")
		}
		if err := delivery.CheckCorrelationID(id); err != nil {
			return "", "", "", "", err
		}
	}
	return path, propose, ns, stage, nil
}

// publishPoll is how often --propose reads the Stage while it waits.
var publishPoll = 5 * time.Second

// proposePublished raises a promote of the published deploy artifact as the
// caller, through the same client `pacto delivery propose` uses, once the
// Stage recorded it as published.
func proposePublished(cmd *cobra.Command, d deps, ns, stage string, res *publish.Result, timeout time.Duration, format string) error {
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
	behalf, _ := cmd.Flags().GetString("on-behalf-of")
	correlation, _ := cmd.Flags().GetString("correlation-id")
	op, _, err := c.Propose(cmd.Context(), delivery.Request{Namespace: ns, Stage: stage, To: digest,
		Reason: "pacto publish " + res.Plan.Service + " " + res.Plan.Version, OnBehalfOf: behalf, CorrelationID: correlation})
	if err != nil {
		return fmt.Errorf("published, but proposing it on %s/%s failed: %w", ns, stage, err)
	}
	// Under json, stdout holds the one result document, so the line goes to stderr.
	w := cmd.OutOrStdout()
	if format == "json" {
		w = cmd.ErrOrStderr()
	}
	_, _ = fmt.Fprintf(w, "Proposed %s on %s/%s as Operation %s\n", digest, ns, stage, op.Metadata.Name)
	return nil
}

// waitPublished waits until the Stage records digest as published. The
// promoter refuses a promote from a candidate OCIRepository or the registry
// watch until then, and it only happens after Flux, or the promoter's registry
// watch, read the new tag and verified its signature. A Stage that takes its
// candidates from another Stage has nothing to wait for.
func waitPublished(cmd *cobra.Command, c *app.Delivery, ns, stage, digest string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		s, err := c.Stage(cmd.Context(), ns, stage)
		if err != nil {
			return err
		}
		cands := s.Spec.Candidates
		if (cands.OCIRepositoryRef == nil && cands.Registry == nil) || slices.Contains(s.Status.Published, digest) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return notPublishedYet(s, digest, timeout)
		}
		time.Sleep(publishPoll)
	}
}

// notPublishedYet says why a Stage did not record digest as published.
func notPublishedYet(s *delivery.StageObject, digest string, timeout time.Duration) error {
	if s.Spec.Candidates.Registry != nil {
		return fmt.Errorf("the Stage did not record %s as published within %s: the registry watch has not accepted %s yet: "+
			"see pacto delivery trust %s; propose it later with `pacto delivery propose`", digest, timeout, digest, s.Spec.Service.Name)
	}
	return fmt.Errorf("the Stage did not record %s as published within %s: Flux has not polled the candidate OCIRepository %s "+
		"or verified its signature yet; propose it later with `pacto delivery propose`", digest, timeout, s.Spec.Candidates.OCIRepositoryRef.Name)
}

// indexFlagUsage is the help of --index on publish and push.
const indexFlagUsage = "organization index repository for this version's entry, as <registry>/pacto-index with no tag or digest (default <registry>/pacto-index from the contract's repository; none writes no entry)"

// indexFlag is an --index value: none, or a <registry>/pacto-index
// repository with no tag or digest, refused at parse time since a reader
// binds every entry to that fixed name.
type indexFlag string

func (f *indexFlag) String() string { return string(*f) }

// Type is "string", so GetString reads the flag.
func (f *indexFlag) Type() string { return "string" }

func (f *indexFlag) Set(v string) error {
	repo, tag, dig := contract.SplitOCIRef(contract.TrimOCIScheme(v))
	if v != app.IndexNone && (tag != "" || dig != "" || !strings.HasSuffix(repo, "/"+orgindex.RepositoryName)) {
		return errors.New("--index must name <registry>/" + orgindex.RepositoryName + " with no tag or digest, or none")
	}
	*f = indexFlag(v)
	return nil
}

// printIndex prints where the organization index entry was written, or, on
// stderr, the warning that it was not: a missing entry never fails the run.
func printIndex(cmd *cobra.Command, index string) {
	switch {
	case strings.HasPrefix(index, "not indexed"):
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+index)
	case index != "":
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Index: "+index)
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
		printIndex(cmd, res.Index)
		for _, n := range res.Notes {
			_, _ = fmt.Fprintln(w, "note: "+n)
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
