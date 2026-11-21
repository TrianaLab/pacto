package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/ci"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// impactEventFlags are the what-if flags of `pacto impact`, in the order
// usage errors name them.
var impactEventFlags = []string{impact.EventUnavailable, impact.EventRemoved, impact.EventRegressed}

// newImpactCommand builds `pacto impact <old> <new>`: it composes a semantic
// contract diff with the operational graph to report a change's real blast
// radius. With an event flag it answers a what-if about one fleet service
// instead. Source flags mirror `pacto fleet`; override flags mirror `pacto diff`.
func newImpactCommand(svc *app.Service, v *viper.Viper, d deps, rec *runRecorder) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact <old> <new> | --running <new> | --unavailable <service> | --removed <service> | --regressed <service>@<revision>",
		Short: "Analyze which consumers a change affects across the fleet",
		Long: "Composes a semantic contract diff (old→new) with the operational " +
			"graph to answer which consumers a change really affects, how strong " +
			"the evidence is and whether their declared compatibility still " +
			"holds.\n\n" +
			"When no fleet source holds the changed service, the new revision is added " +
			"to the snapshot so the services that depend on it are found.\n\n" +
			"With --running, impact judges <new> against the revisions of its service that run on known targets instead of " +
			"an <old> revision, and keeps the worst gate. A running consumer whose range rejects <new> blocks it whatever the " +
			"classification, and a partial answer never clears. A local <new> takes the fleet domain that holds its service name; a " +
			"name several domains hold exits 1 with their keys (judge the oci:// reference instead). With no running revision the new one is a first version: it " +
			"is blocked when a running consumer's range rejects it, and clear only on a complete answer whose running consumers " +
			"all accept it. A running revision Pacto could not link, pull or trust, one linked by tag only, an inferred workload " +
			"of the service, a workload source Pacto could not read and a local <new> in no fleet domain each make the answer " +
			"unverified at best. Running revisions are " +
			"pulled only from the hosts of the target snapshots and of an oci:// <new>, plus the comma-separated hosts in " +
			"$PACTO_RUNNING_REGISTRIES.\n\n" +
			"With --unavailable, --removed or --regressed, impact answers what happens if that " +
			"service becomes unavailable, is removed or a revision of it regressed. --unavailable and --removed " +
			"also take offering:<name>, which asks the same of every service that provides the platform offering. Consumers are " +
			"graded breaks (every edge on their path is required), may degrade (an optional edge) " +
			"or unknown. The gate is unverified (exit 2) when anything depends on the service and " +
			"clear (exit 0) only when the complete snapshot shows nothing does. --regressed with an " +
			"artifact digest reads delivery's recorded failures with your own cluster credentials " +
			"when a cluster is reachable.\n\n" +
			"The answer ends in a release gate, and the exit status follows it:\n" +
			"  0  clear: the change is not BREAKING and every service that references its policy passes the new policy, " +
			"or the snapshot is complete and no service depends on the changed one.\n" +
			"  1  blocked: a consumer that runs on a known target does not accept the new version of a BREAKING change, " +
			"or its contract fails the changed service's new policy, whatever the classification.\n" +
			"  2  unverified: nothing is shown to block the change, but the answer cannot clear it: a BREAKING change " +
			"whose answer is partial or whose consumers none is shown to be safe, or a service that references the " +
			"changed policy and fails it running nowhere known, could not be checked or could not be resolved. " +
			"A run over --local bundles alone knows no targets, so a BREAKING change with consumers exits 2.\n" +
			"A command that could not run also exits 1, with nothing on stdout. Add --target-state " +
			"(or a live source) so the gate can tell deployed impact apart.\n\n" +
			"Pacto finds consumers only in the sources you pass, such as --local, --root, " +
			"--oci, --cache, --k8s, --evidence-url, --traces and the target snapshot. With --local alone " +
			"Pacto sees only the contracts in those folders, so \"No affected " +
			"consumers\" is not evidence of safety.\n\n" +
			"When you pass no --k8s, --target-state, --evidence-url or --target-snapshot, impact reads the signed " +
			"target snapshot each cluster's operator publishes (--target-snapshot auto, the default): from " +
			"the oci:// ref of the change it tries <prefix>/pacto-targets under each path prefix of its repository, deepest first " +
			"and at most three segments deep (the registry value the operator publishes under), then <host>/pacto-targets, " +
			"and prints a Registry line saying where it looked and a Targets line per cluster snapshot it read. " +
			"auto reads only the clusters whose snapshot exists at that moment, so CI that knows its clusters pins " +
			"them with --target-snapshot oci://<registry>/pacto-targets:cluster-<uid> (repeatable). --delivery-trust " +
			"names the trust config that verifies each snapshot; an unverified or stale snapshot never clears a " +
			"BREAKING change. --target-snapshot off reads none.\n\n" +
			"From the same ref impact also reads the consumers published to the organization index, " +
			"<host>/<first path segment>/pacto-index then <host>/pacto-index, checks them against the target snapshot it reads " +
			"and prints a Consumers line; --index names another <registry>/pacto-index, --index none reads none, a --root list skips it " +
			"and a pair of --local folders reads no index.",
		Example: `  # Impact of upgrading a local service against the local fleet
  pacto impact ./svc-v1 ./svc-v2 --local .

  # Impact of a published service against the clusters that run it, read
  # from the target snapshot next to its registry
  pacto impact oci://ghcr.io/acme/svc:1.0.0 ./svc --delivery-trust trust.yaml

  # Judge a folder's contract against the revisions that run in the pinned
  # clusters, as a GitOps render gate does
  pacto impact --running . --target-snapshot oci://ghcr.io/acme/pacto-targets:cluster-<uid> --delivery-trust trust.yaml

  # Include observed (runtime) evidence and emit JSON
  pacto impact oci://ghcr.io/acme/svc:1.0.0 ./svc --include-observed --output-format json

  # What breaks if redis becomes unavailable, or postgresql is removed
  pacto impact --unavailable redis --local .
  pacto impact --removed postgresql --local . --target-state targets.yaml

  # What breaks if every provider of the postgresql offering becomes unavailable
  pacto impact --unavailable offering:postgresql --local .

  # What a regressed artifact affects, with delivery's recorded failures
  pacto impact --regressed payments-service@sha256:<digest> --local .`,
		Args: impactArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var out *runrecord.Record
			var revs revisions
			defer rec.save(cmd, &out, &revs)
			format := v.GetString(outputFormatKey)
			includeObserved, _ := cmd.Flags().GetBool("include-observed")
			var traces []byte
			if tracesPath, _ := cmd.Flags().GetString("traces"); tracesPath != "" {
				var readErr error
				if traces, readErr = os.ReadFile(tracesPath); readErr != nil {
					return readErr
				}
				includeObserved = true // supplying traces implies observed analysis
			}

			auto, err := impactSnapshotAuto(cmd)
			if err != nil {
				return err
			}
			start := time.Now()
			sp := startSpinner(cmd, format, "Analyzing impact")
			var result *impact.Result
			if kind, value := impactEventFlag(cmd); kind != "" {
				result, err = runImpactEvent(cmd, svc, d, kind, value, traces)
			} else {
				oldOverrides, newOverrides := getDiffOverrides(cmd)
				opts := app.ImpactOptions{
					NewPath:            args[len(args)-1],
					OldOverrides:       oldOverrides,
					NewOverrides:       newOverrides,
					Fleet:              fleetOptions(cmd),
					IncludeObserved:    includeObserved,
					Traces:             traces,
					OnResolved:         rec.hook(cmd, &revs),
					TargetSnapshotAuto: auto,
				}
				if running, _ := cmd.Flags().GetBool("running"); running {
					opts.RunningRegistries = runningRegistries(cmd)
					result, err = svc.ImpactRunning(cmd.Context(), opts, nil)
				} else {
					opts.OldPath = args[0]
					result, err = svc.Impact(cmd.Context(), opts)
				}
			}
			if err != nil {
				sp.Stop()
				return err
			}
			sp.StopOK("Analyzed", start)
			out = impactRecord(revs, result)

			// Fail closed. A blocked change exits 1, as `pacto diff` does on a
			// breaking change. An unverified one exits 2, so a CI job never reads
			// "could not tell" as "safe", while a promotion job that gates on
			// deployed impact alone can still tell the two apart.
			var failed error
			switch result.Gate {
			case impact.GateBlocked:
				failed = errors.New(result.GateReason)
			case impact.GateUnverified:
				failed = &ExitError{Code: 2, Reason: "unverified: " + result.GateReason}
			}
			if format == "json" {
				noteUnverified(cmd, result)
				return printJSONExit(cmd, result, failed)
			}
			_ = printImpactResult(cmd, result, format) // ponytail: text and markdown output never fail
			noteUnverified(cmd, result)
			return failed
		},
	}

	for _, f := range impactEventFlags {
		cmd.Flags().String(f, "", impactEventFlagHelp[f])
	}
	cmd.Flags().Bool("running", false, "judge <new> against the revisions of its service that run on known targets instead of an <old> revision; "+
		"a running revision with the new digest is not a baseline; with no running revision only consumers' ranges are checked")
	cmd.Flags().Bool("include-observed", false, "let observed (runtime) relationships raise consumer confidence")
	// Declared before the shared set so this narrowing wins: impact takes ONE
	// trace document and reads it itself (see divergentFleetFlagTypes).
	cmd.Flags().String("traces", "", "OTLP/JSON trace file; its observed edges corroborate and surface consumers (implies --include-observed)")
	// Every other fleet source flag, from the one declaration `pacto fleet` uses.
	addFleetSourceFlags(cmd.Flags())
	addStatsFlags(cmd)
	addDiffOverrideFlags(cmd)

	return cmd
}

// impactEventFlagHelp is the help of each event flag.
var impactEventFlagHelp = map[string]string{
	impact.EventUnavailable: "what happens if this service (a fleet service key, or a name only one domain holds) becomes unavailable; offering:<name> asks it of every provider of an offering",
	impact.EventRemoved:     "what happens if this service (a fleet service key, or a name only one domain holds) is removed; offering:<name> asks it of every provider of an offering",
	impact.EventRegressed:   "what happens if <service>@<revision> regressed; an artifact digest reads delivery's recorded failures",
}

// impactArgs takes two revisions, one with --running, or none with exactly
// one event flag.
func impactArgs(cmd *cobra.Command, args []string) error {
	if running, _ := cmd.Flags().GetBool("running"); running {
		return runningArgs(cmd, args)
	}
	var set []string
	for _, f := range impactEventFlags {
		if cmd.Flags().Changed(f) {
			set = append(set, "--"+f)
		}
	}
	switch {
	case len(set) > 1:
		return fmt.Errorf("pass one of %s, not several", strings.Join(set, ", "))
	case len(set) == 1 && changedFlag(cmd, impactChangeOnlyFlags) != "":
		return fmt.Errorf("--%s applies to <old> <new>, not %s: an event always reads observed callers and diffs no contract",
			changedFlag(cmd, impactChangeOnlyFlags), set[0])
	case len(set) == 1 && len(args) > 0:
		return fmt.Errorf("%s takes no <old> <new> arguments, got %d", set[0], len(args))
	case len(set) == 0 && len(args) != 2:
		return fmt.Errorf("accepts <old> <new> (2 arguments) or one of --unavailable, --removed, --regressed, received %d arguments", len(args))
	}
	return nil
}

// runningArgs takes the one revision --running judges, with no event flag
// and no flag that shapes an old revision: the running revisions are the
// old side.
func runningArgs(cmd *cobra.Command, args []string) error {
	if f := changedFlag(cmd, append([]string{"old-values", "old-set"}, impactEventFlags...)); f != "" {
		return fmt.Errorf("--running judges <new> against the revisions that run, so it takes no --%s", f)
	}
	if len(args) != 1 {
		return fmt.Errorf("--running takes one argument, <new>, received %d arguments", len(args))
	}
	return nil
}

// impactSnapshotAuto reads --target-snapshot for pacto impact: none or auto
// derives the snapshot from the change, off reads none and refs read those.
// auto or off beside another value is a usage error.
func impactSnapshotAuto(cmd *cobra.Command) (bool, error) {
	values, _ := cmd.Flags().GetStringArray("target-snapshot")
	refs := targetSnapshotRefs(values)
	if len(refs) < len(values) && len(values) > 1 {
		return false, errors.New("--target-snapshot takes auto, off or oci:// refs, not a mix")
	}
	return len(refs) == 0 && !slices.Contains(values, "off"), nil
}

// impactChangeOnlyFlags only shape a contract change answer.
var impactChangeOnlyFlags = []string{"include-observed", "old-values", "old-set", "new-values", "new-set", "index"}

// changedFlag is the first of names set on cmd, "" when none is.
func changedFlag(cmd *cobra.Command, names []string) string {
	for _, f := range names {
		if cmd.Flags().Changed(f) {
			return f
		}
	}
	return ""
}

// impactEventFlag is the event flag set on cmd and its value, "" when none.
func impactEventFlag(cmd *cobra.Command) (string, string) {
	for _, f := range impactEventFlags {
		if cmd.Flags().Changed(f) {
			value, _ := cmd.Flags().GetString(f)
			return f, value
		}
	}
	return "", ""
}

// runImpactEvent answers a what-if about one fleet service. A regressed
// revision reads delivery with the caller's own cluster credentials; when no
// cluster can be reached the answer says so in a limitation.
func runImpactEvent(cmd *cobra.Command, svc *app.Service, d deps, kind, value string, traces []byte) (*impact.Result, error) {
	ev, err := app.ParseEventFlag(kind, value)
	if err != nil {
		return nil, err
	}
	opts := app.ImpactEventOptions{Event: ev, Fleet: fleetOptions(cmd), Traces: traces}
	opts.ReadRegression = func(ctx context.Context, ev impact.Event) *impact.Regression {
		return readRegression(ctx, cmd, d, ev)
	}
	return svc.ImpactEvent(cmd.Context(), opts)
}

// readRegression reads the delivery evidence about a regressed revision with
// the ambient cluster credentials, bounded by ctx (the request's, so a
// cancelled MCP call stops the walk).
func readRegression(ctx context.Context, cmd *cobra.Command, d deps, ev impact.Event) *impact.Regression {
	c, _, err := deliveryClient(cmd, d)
	if err != nil {
		return &impact.Regression{Unavailable: "no cluster to read delivery from: " + err.Error()}
	}
	return c.RegressionEvidence(ctx, ev)
}

// maxEvidenceLines bounds the evidence lines printed per consumer.
const maxEvidenceLines = 3

// impactLanes are the event lanes in the order the text prints them.
var impactLanes = []struct{ effect, title string }{
	{impact.EffectBreaks, "BREAKS"},
	{impact.EffectMayDegrade, "MAY DEGRADE"},
	{impact.EffectUnknown, "UNKNOWN"},
}

// printImpactResult renders an impact result as text or JSON.
// noteUnverified writes the stderr line an unverified answer exits 2 with.
func noteUnverified(cmd *cobra.Command, result *impact.Result) {
	if result.Gate == impact.GateUnverified {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "unverified: "+result.GateReason)
	}
}

func printImpactResult(cmd *cobra.Command, result *impact.Result, format string) error {
	return formatResult(cmd, format, result, func() error {
		w := cmd.OutOrStdout()
		if result.Event != nil {
			printEventHeader(w, result)
		} else {
			printChangeHeader(w, result)
		}
		printTargetSnapshots(w, result)
		printImpactConsumers(w, result)
		if len(result.ConsumerTallies.ByOutcome) > 0 && len(result.Consumers) > 0 {
			_, _ = fmt.Fprintf(w, "By outcome: %s\n", result.ConsumerTallies.OutcomeSummary())
		}
		if len(result.ConsumerTallies.ByOwner) > 0 {
			_, _ = fmt.Fprintf(w, "Most affected owners: %s\n", result.ConsumerTallies.OwnerSummary())
		}
		if len(result.ActiveTargets) > 0 {
			_, _ = fmt.Fprintf(w, "Active targets (%d): %v\n", len(result.ActiveTargets), result.ActiveTargets)
		}
		if result.Hidden > 0 {
			_, _ = fmt.Fprintf(w, "Consumers you may not see: %d\n", result.Hidden)
		}
		if len(result.Templates) > 0 {
			_, _ = fmt.Fprintf(w, "Golden paths to review (they run nowhere, so they never hold the gate): %s\n", strings.Join(result.Templates, ", "))
		}
		if result.Event != nil && result.Event.Kind == impact.EventRegressed {
			printRegression(w, result)
		}
		_, _ = fmt.Fprintf(w, "Release gate: %s\n", result.Gate)

		// The impact result carries completeness inline (not a fleet.Meta), so
		// wrap it to reuse the shared partial-answer warning.
		warnPartial(cmd, fleet.Meta{
			Completeness: result.Completeness,
			AsOf:         result.AsOf,
			Limitations:  result.Limitations,
		})
		return nil
	}, nil)
}

// printChangeHeader writes the header of a contract change answer.
func printChangeHeader(w io.Writer, result *impact.Result) {
	_, _ = fmt.Fprintf(w, "Impact: %s %s -> %s\n", result.Service, orDash(result.OldVersion), orDash(result.NewVersion))
	_, _ = fmt.Fprintf(w, "Classification: %s\n", colorClassification(w, result.Classification))
	_, _ = fmt.Fprintf(w, "Breaking changes: %d\n", len(result.BreakingChanges))
	_, _ = fmt.Fprintf(w, "Potentially breaking changes: %d\n", len(result.PotentiallyBreakingChanges))
	if result.NewRevisionAdded {
		_, _ = fmt.Fprintf(w, "%s is in none of the fleet sources; the new revision was added to the snapshot to find its consumers.\n", result.Service)
	}
	printRunning(w, result.Running)
}

// printRunning writes what a --running answer judged against; nothing for
// any other answer.
func printRunning(w io.Writer, r *impact.RunningBaseline) {
	if r == nil {
		return
	}
	also := ""
	if len(r.Excluded) > 0 {
		also = "; the new revision also runs and is not its own baseline"
	}
	switch {
	case r.Judged != "":
		noun := "revisions"
		if len(r.Refs) == 1 {
			noun = "revision"
		}
		_, _ = fmt.Fprintf(w, "Running: %d %s (judged against %s)%s\n", len(r.Refs), noun, r.Judged, also)
	case len(r.Unread) > 0:
		_, _ = fmt.Fprintf(w, "Running: none Pacto could read (judged as a first version)%s\n", also)
	case also != "":
		_, _ = fmt.Fprintln(w, "Running: only the new revision, which is not its own baseline (judged as a first version)")
	default:
		_, _ = fmt.Fprintln(w, "Running: none on a known target (first version)")
	}
	if len(r.Unread) > 0 {
		_, _ = fmt.Fprintf(w, "Not read: %s\n", strings.Join(r.Unread, "; "))
	}
}

// printTargetSnapshots writes where impact looked for a target snapshot and
// one line per snapshot it read.
func printTargetSnapshots(w io.Writer, result *impact.Result) {
	if l := result.Registry; l != nil && l.Found != "" {
		clusters := "clusters"
		if l.Clusters == 1 {
			clusters = "cluster"
		}
		_, _ = fmt.Fprintf(w, "Registry: %s (found %s, %d %s read)\n", path.Dir(l.Found), l.Found, l.Clusters, clusters)
	} else if l != nil {
		var tried []string
		for _, r := range l.Tried {
			tried = append(tried, r.Repository+" ("+registryOutcomeText[r.Outcome]+")")
		}
		_, _ = fmt.Fprintf(w, "Registry: no pacto-targets at %s; consumers come only from the sources you passed\n", strings.Join(tried, " or "))
	}
	for _, s := range result.TargetSnapshots {
		_, _ = fmt.Fprintf(w, "Targets: %s\n", targetSnapshotLine(s, result))
	}
	if text := ci.IndexText(result); text != "" {
		_, _ = fmt.Fprintf(w, "Consumers: %s\n", text)
	}
}

// registryOutcomeText says each registry lookup outcome in words.
var registryOutcomeText = map[string]string{
	impact.RegistryNotFound:     "not found",
	impact.RegistryDenied:       "denied (check registry read credentials)",
	impact.RegistryNoClusterTag: "no cluster tag",
}

// targetSnapshotLine is what a Targets line says about one snapshot source:
// its pinned ref, cluster, age, status and who signed it.
func targetSnapshotLine(s fleet.SourceState, result *impact.Result) string {
	cluster := "-"
	if s.Cluster != nil {
		cluster = s.Cluster.Name
	}
	if s.Ref == "" {
		return fmt.Sprintf("%s (cluster %s, %s, not used)", s.ID, cluster, s.Status)
	}
	age := "-"
	if s.LastSuccessfulSync != nil {
		age = fmt.Sprintf("%dm", int(result.AsOf.Sub(*s.LastSuccessfulSync).Minutes()))
	}
	status := string(s.Status)
	if s.Status == fleet.SourceAvailable {
		status = string(fleet.CompletenessComplete)
	}
	signer := "unverified"
	switch {
	case s.Signer == "":
	case s.SignerUnscoped:
		signer = "signed by " + s.Signer + " (not scoped to this cluster)"
	case s.Publisher != "":
		signer = "signed by " + s.Signer + " (publisher " + s.Publisher + ")"
	default:
		signer = "signed by " + s.Signer
	}
	return fmt.Sprintf("%s (cluster %s, asOf %s, %s, %s)", s.Ref, cluster, age, status, signer)
}

// printEventHeader writes the header of an event answer.
func printEventHeader(w io.Writer, result *impact.Result) {
	subject, at := result.Event.Service, ""
	if result.Event.Revision != "" {
		at = " at " + result.Event.Revision
	}
	if result.Event.Offering != "" {
		subject = "offering " + result.Event.Offering
		if len(result.Providers) > 0 {
			at = " (providers " + strings.Join(result.Providers, ", ") + ")"
		}
	}
	_, _ = fmt.Fprintf(w, "Event: %s %s%s  asOf %s  completeness %s\n", subject, result.Event.Kind, at,
		result.AsOf.Format(time.RFC3339), result.Completeness)
}

// printImpactConsumers writes the consumers: one list for a contract change,
// one section per effect for an event.
func printImpactConsumers(w io.Writer, result *impact.Result) {
	if len(result.Consumers) == 0 {
		_, _ = fmt.Fprintln(w, "No affected consumers.")
		return
	}
	_, _ = fmt.Fprintf(w, "Affected consumers (%d):\n", len(result.Consumers))
	if result.Event == nil {
		for _, c := range result.Consumers {
			printImpactConsumer(w, result, c)
		}
		return
	}
	for _, lane := range impactLanes {
		var in []impact.AffectedConsumer
		for _, c := range result.Consumers {
			if c.Effect == lane.effect {
				in = append(in, c)
			}
		}
		if len(in) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "%s (%d):\n", lane.title, len(in))
		for _, c := range in {
			printImpactConsumer(w, result, c)
		}
	}
}

// printImpactConsumer writes one consumer: its verdict line, why it is a
// consumer (path, provenance and edge types), its evidence and resources.
func printImpactConsumer(w io.Writer, result *impact.Result, c impact.AffectedConsumer) {
	scope := "transitive"
	if c.Direct {
		scope = "direct"
	}
	verdict := "compat=" + c.CompatibilityVerdict
	if c.Compatibility == contract.CompatibilityUnknown {
		verdict += " (" + contract.RangeLabel(c.Compatibility) + ")"
	}
	if c.Effect != "" {
		verdict = "effect=" + c.Effect
	}
	_, _ = fmt.Fprintf(w, "  %-28s %-10s confidence=%-12s %-19s owner=%s\n",
		c.Service, scope, c.Confidence, verdict, orDash(c.Owner))
	_, _ = fmt.Fprintf(w, "    path: %s  provenance: %s  via: %s\n", strings.Join(c.Path, " > "), orDash(c.Provenance), orDash(strings.Join(c.Via, ",")))
	for _, m := range c.Unmet {
		_, _ = fmt.Fprintf(w, "    refused: %s\n", m)
	}
	for i, e := range c.Evidence {
		if i == maxEvidenceLines {
			_, _ = fmt.Fprintf(w, "    and %d more\n", len(c.Evidence)-maxEvidenceLines)
			break
		}
		_, _ = fmt.Fprintf(w, "    evidence: %s %s %s\n", e.Kind, orDash(e.Source), evidenceDetail(result.AsOf, e))
	}
	for _, r := range c.Resources {
		_, _ = fmt.Fprintf(w, "    resources: %s %s%s\n", orDash(r.Environment), r.Phase, map[bool]string{true: " entry " + r.Entry}[r.Entry != ""])
	}
	if c.ResourcesTotal > len(c.Resources) {
		_, _ = fmt.Fprintf(w, "    and %d more resources\n", c.ResourcesTotal-len(c.Resources))
	}
	if c.Note != "" {
		_, _ = fmt.Fprintf(w, "    note: %s\n", c.Note)
	}
}

// evidenceDetail says how old a target or an observed call was at asOf, or
// that it is stale; any other fact is said in its own words.
func evidenceDetail(asOf time.Time, e impact.Evidence) string {
	switch {
	case e.Kind != impact.EvidenceTarget && e.Kind != impact.EvidenceObservedCall:
		return e.Message
	case e.Stale:
		return "stale"
	case e.At == nil:
		return "no observation window"
	}
	return asOf.Sub(*e.At).Truncate(time.Minute).String() + " before asOf"
}

// printRegression writes the delivery evidence about a regressed revision,
// or why it was not read.
func printRegression(w io.Writer, result *impact.Result) {
	if len(result.Regression) > 0 {
		_, _ = fmt.Fprintf(w, "Delivery evidence (%d):\n", len(result.Regression))
		for _, e := range result.Regression {
			from := ""
			if e.Kind == impact.RegressionRollback && e.Record == "" {
				from = " (from status, not signed)"
			}
			_, _ = fmt.Fprintf(w, "  %s/%s %s %s%s\n", e.Namespace, e.Stage, e.Kind, strings.TrimSpace(e.Gate+" "+e.Message), from)
		}
	}
	for _, l := range result.Limitations {
		if l.Code == impact.LimitationDeliveryEvidenceUnavailable {
			_, _ = fmt.Fprintf(w, "Delivery evidence not read: %s\n", l.Message)
		}
	}
}

// runningRegistriesKey carries the running-registries value root read
// (PACTO_RUNNING_REGISTRIES or the config file) on the command context.
type runningRegistriesKey struct{}

// runningRegistries is the extra registry hosts --running may pull from, as
// root read them; PACTO_RUNNING_REGISTRIES when cmd never ran through root.
func runningRegistries(cmd *cobra.Command) []string {
	if ctx := cmd.Context(); ctx != nil {
		if v, ok := ctx.Value(runningRegistriesKey{}).(string); ok {
			return strings.Split(v, ",")
		}
	}
	return strings.Split(os.Getenv("PACTO_RUNNING_REGISTRIES"), ",")
}
