package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// newFleetCommand builds the `pacto fleet` command group: a fleet-scoped,
// cross-service operational view. It is deliberately distinct from `pacto graph`
// (which is single-root: one service's transitive dependency tree). Fleet
// commands operate over a snapshot composed from many sources.
func newFleetCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "Query the Pacto operational graph across many services",
		Long: "Compose contracts, contract revisions and operational targets from " +
			"local bundles and ingested evidence into a versioned, navigable graph, " +
			"then search, inspect, traverse and explain it. Every answer reports its " +
			"as-of time and completeness.",
	}
	// Source flags are shared by every subcommand.
	addFleetSourceFlags(cmd.PersistentFlags())

	cmd.AddCommand(newFleetSearchCommand(svc, v))
	cmd.AddCommand(newFleetGetCommand(svc, v))
	cmd.AddCommand(newFleetGraphCommand(svc, v))
	cmd.AddCommand(newFleetStatusCommand(svc, v))
	cmd.AddCommand(newFleetSnapshotCommand(svc, v))
	cmd.AddCommand(newFleetExplainCommand(svc, v))
	cmd.AddCommand(newFleetReconcileCommand(svc, v))
	return cmd
}

// addFleetSourceFlags declares the shared fleet source flags on f. Callers pass
// cmd.PersistentFlags() when subcommands must inherit them (pacto fleet) and
// cmd.Flags() otherwise (pacto tui).
func addFleetSourceFlags(f *pflag.FlagSet) {
	f.StringArray("local", []string{"."}, "local bundle root(s) to scan (repeatable)")
	f.StringArray("target-state", nil, "offline target-state fixture file(s) supplying targets — a demo/test adapter, not the signed EvidenceSet protocol (repeatable)")
	f.StringArray("evidence-url", nil, "base URL of an Evidence Server to consume its read-only operational-graph contribution over HTTP (repeatable)")
	f.StringArray("traces", nil, "OTLP/JSON trace file supplying runtime-observed dependency edges, folded into the snapshot as observed relationships (repeatable)")
	f.StringArray("oci", nil, "registry reference to include as a published-baseline revision (repeatable)")
	f.Bool("cache", false, "include every bundle in the local OCI cache as an offline baseline revision")
	f.Bool("k8s", false, "include live Pacto CRs from the current Kubernetes cluster as targets")
	f.String("namespace", "", "namespace to read Pacto CRs from with --k8s (empty = all namespaces)")
	f.Duration("freshness", 0, "mark target evidence older than this as stale (0 disables)")
}

// fleetFlagNames lists the shared fleet source flags declared by
// newFleetCommand, in declaration order. It is the single list any other
// command copies from when it wants the same source surface.
func fleetFlagNames() []string {
	return []string{
		"local", "target-state", "evidence-url", "traces", "oci",
		"cache", "k8s", "namespace", "freshness",
	}
}

// fleetSourceArgs renders the source flags the caller actually set back into
// argv. Visit walks only flags with Changed set, so a default contributes
// nothing and the line stays as short as what the reader typed. It exists so
// the TUI can hand a reader a fleet query that resolves the same snapshot the
// screen is showing, rather than one rebuilt from the defaults.
func fleetSourceArgs(cmd *cobra.Command) []string {
	var out []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if !slices.Contains(fleetFlagNames(), f.Name) {
			return
		}
		// A repeatable flag holds every value at once, so one --name=value per
		// element; taking Value.String() would emit pflag's "[a,b]" rendering and
		// lose every element after the first.
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			for _, v := range sv.GetSlice() {
				out = append(out, "--"+f.Name+"="+v)
			}
			return
		}
		// --name=value rather than two tokens: it is unambiguous for a bool, where
		// a bare --k8s would swallow the positional that follows it.
		out = append(out, "--"+f.Name+"="+f.Value.String())
	})
	return out
}

// fleetOptions reads the shared source flags into app.FleetOptions. Lookup
// errors are deliberately discarded: a command may declare a narrower subset of
// fleetFlagNames (pacto impact does), and an undeclared flag correctly
// contributes its zero value rather than failing the command.
func fleetOptions(cmd *cobra.Command) app.FleetOptions {
	local, _ := cmd.Flags().GetStringArray("local")
	targetState, _ := cmd.Flags().GetStringArray("target-state")
	evidenceURLs, _ := cmd.Flags().GetStringArray("evidence-url")
	traceFiles, _ := cmd.Flags().GetStringArray("traces")
	ociRefs, _ := cmd.Flags().GetStringArray("oci")
	includeCache, _ := cmd.Flags().GetBool("cache")
	includeK8s, _ := cmd.Flags().GetBool("k8s")
	namespace, _ := cmd.Flags().GetString("namespace")
	freshness, _ := cmd.Flags().GetDuration("freshness")
	return app.FleetOptions{
		LocalRoots:         local,
		TargetStateFiles:   targetState,
		EvidenceURLs:       evidenceURLs,
		ObservationSources: app.TraceFileSources(traceFiles),
		OCIRefs:            ociRefs,
		IncludeCache:       includeCache,
		IncludeK8s:         includeK8s,
		K8sNamespace:       namespace,
		FreshnessWindow:    freshness,
	}
}

// buildQuery assembles the snapshot and returns a pure query over it.
func buildQuery(cmd *cobra.Command, svc *app.Service) (*fleet.Query, error) {
	snap, err := svc.Fleet(cmd.Context(), fleetOptions(cmd))
	if err != nil {
		return nil, err
	}
	return fleet.NewQuery(snap), nil
}

// warnPartial prints a completeness warning and each limitation to stderr for
// text output, so a partial answer is never silently presented as complete.
func warnPartial(cmd *cobra.Command, m fleet.Meta) {
	if m.Completeness == fleet.CompletenessComplete {
		return
	}
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(w, "warning: answer is %s (as of %s)\n", m.Completeness, m.AsOf.Format(time.RFC3339))
	for _, l := range m.Limitations {
		_, _ = fmt.Fprintf(w, "  - [%s] %s\n", l.Code, l.Message)
	}
}

func newFleetSearchCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search [text]",
		Short: "Search logical services in the fleet",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			res, err := q.Search(searchFilterFromCmd(cmd, optionalArg(args)))
			if err != nil {
				return err
			}
			return printFleetSearch(cmd, res, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("owner", "", "filter by owner (team, DRI or contact)")
	// Same reason as the MCP enum: the vocabulary is read from the fleet, so the
	// help text can never advertise fewer statuses than the filter accepts.
	cmd.Flags().String("status", "", "filter by aggregate status ("+strings.Join(fleet.CanonicalStatuses(), ", ")+")")
	cmd.Flags().String("compliance", "", "filter to services with a target of this compliance")
	cmd.Flags().String("source", "", "filter by observing source")
	cmd.Flags().String("workload", "", "filter by workload (service, job, scheduled)")
	cmd.Flags().StringArray("label", nil, "filter by label key=value (repeatable)")
	// "Ready" here is the contract's own declared readiness gate passing (score >=
	// minScore, assessment not expired) -- not a Kubernetes readiness probe and not
	// compliance. Spelled out because three unrelated things in this product are
	// called readiness.
	cmd.Flags().Bool("ready", false, "only services whose readiness gate passes (score >= minScore, not expired)")
	cmd.Flags().Bool("not-ready", false, "only services whose readiness gate does not pass (below minScore, expired or undeclared)")
	cmd.Flags().Bool("has-capability", false, "only services declaring a capability")
	cmd.Flags().Bool("has-dependency", false, "only services declaring a dependency")
	cmd.Flags().String("scope", "", "correlate to a target with this scope")
	cmd.Flags().Int("limit", 0, fmt.Sprintf("maximum results (0 = %d, capped at %d)", fleet.DefaultSearchLimit, fleet.MaxSearchLimit))
	cmd.Flags().Int("offset", 0, "result offset for paging")

	// Two closed vocabularies the code already owns, so declaring them costs
	// nothing at runtime and turns empty completion into real completion. The
	// remaining string filters (--owner, --compliance, --source, --scope,
	// --label) take values that come from the fleet data, not from a vocabulary,
	// so guessing at them would be worse than offering nothing.
	_ = cmd.RegisterFlagCompletionFunc("status", staticCompletions(fleet.CanonicalStatuses()...))
	_ = cmd.RegisterFlagCompletionFunc("workload", staticCompletions(
		contract.WorkloadService, contract.WorkloadJob, contract.WorkloadScheduled))
	return cmd
}

func searchFilterFromCmd(cmd *cobra.Command, text string) fleet.SearchFilter {
	owner, _ := cmd.Flags().GetString("owner")
	status, _ := cmd.Flags().GetString("status")
	compliance, _ := cmd.Flags().GetString("compliance")
	source, _ := cmd.Flags().GetString("source")
	workload, _ := cmd.Flags().GetString("workload")
	scope, _ := cmd.Flags().GetString("scope")
	labels, _ := cmd.Flags().GetStringArray("label")
	ready, _ := cmd.Flags().GetBool("ready")
	notReady, _ := cmd.Flags().GetBool("not-ready")
	hasCap, _ := cmd.Flags().GetBool("has-capability")
	hasDep, _ := cmd.Flags().GetBool("has-dependency")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	return fleet.SearchFilter{
		Text: text, Owner: owner, Labels: parseLabels(labels), Scope: scope, Status: status,
		Compliance: compliance, Source: source, Workload: workload,
		HasCapability: hasCap, HasDependency: hasDep, ReadyOnly: ready, NotReady: notReady,
		Limit: limit, Offset: offset,
	}
}

func parseLabels(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, p := range pairs {
		k, val, ok := strings.Cut(p, "=")
		if ok {
			out[k] = val
		}
	}
	return out
}

func newFleetGetCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [service]",
		Short: "Inspect a logical service or an operational target",
		Args:  cobra.MaximumNArgs(1),
		// A service key comes from the snapshot, and building one at tab time would
		// reach k8s, a registry or the disk cache while the reader holds tab. No
		// candidates is the honest answer; falling back to filenames is not, because
		// this positional is never a path.
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			target, _ := cmd.Flags().GetString("target")
			if target != "" {
				tv, err := q.GetTarget(target)
				if err != nil {
					return err
				}
				return printFleetTarget(cmd, tv, v.GetString(outputFormatKey))
			}
			name := optionalArg(args)
			if name == "" {
				return fmt.Errorf("provide a service name or --target")
			}
			sv, err := q.GetService(name)
			if err != nil {
				return err
			}
			return printFleetService(cmd, sv, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("target", "", "inspect an operational target by key or name")
	return cmd
}

func newFleetGraphCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph <service>",
		Short: "Traverse fleet dependencies or dependents",
		Long: "Traverse the operational graph from an explicit root. Give a service " +
			"name to aggregate across its revisions, or --revision/--target to root " +
			"an exact revision (never 'latest').",
		Args: cobra.MaximumNArgs(1),
		// Same as `fleet get`: the candidates live in a snapshot too expensive to
		// build at the prompt, and this positional is never a path.
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			direction, _ := cmd.Flags().GetString("direction")
			transitive, _ := cmd.Flags().GetBool("transitive")
			maxDepth, _ := cmd.Flags().GetInt("max-depth")
			revision, _ := cmd.Flags().GetString("revision")
			target, _ := cmd.Flags().GetString("target")
			res, err := q.Graph(fleet.GraphQuery{
				Service: optionalArg(args), Revision: fleet.RevisionKey(revision), Target: target,
				Direction: fleet.Direction(direction), Transitive: transitive, MaxDepth: maxDepth,
			})
			if err != nil {
				return err
			}
			return printFleetGraph(cmd, res, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("direction", "dependencies", "traversal direction (dependencies, dependents)")
	cmd.Flags().Bool("transitive", false, "traverse transitively (cycle-safe)")
	cmd.Flags().Int("max-depth", 0, "maximum transitive depth (0 = unlimited)")
	cmd.Flags().String("revision", "", "root an exact contract revision key")
	cmd.Flags().String("target", "", "root the revision linked to this target key or name")

	// fleet.DirectionBoth exists but validateDirection rejects it, so offering it
	// would complete to a value the query errors on.
	_ = cmd.RegisterFlagCompletionFunc("direction", staticCompletions(
		string(fleet.DirectionDependencies), string(fleet.DirectionDependents)))
	return cmd
}

func newFleetStatusCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report services and targets needing attention",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			res := q.Status(statusQueryFromCmd(cmd))
			return printFleetStatus(cmd, res, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().Bool("needs-attention", false, "report every attention category")
	cmd.Flags().Bool("non-compliant", false, "report non-compliant targets")
	cmd.Flags().Bool("unknown", false, "report targets with unknown compliance")
	cmd.Flags().Bool("invalid", false, "report structurally invalid contracts")
	cmd.Flags().Bool("stale", false, "report targets with stale evidence")
	cmd.Flags().Bool("missing-readiness", false, "report revisions without a readiness assessment")
	cmd.Flags().Bool("unresolved-deps", false, "report unresolved declared dependencies")
	cmd.Flags().Int("limit", 0, fmt.Sprintf("maximum results (0 = %d)", fleet.DefaultStatusLimit))
	return cmd
}

func statusQueryFromCmd(cmd *cobra.Command) fleet.StatusQuery {
	get := func(name string) bool { b, _ := cmd.Flags().GetBool(name); return b }
	limit, _ := cmd.Flags().GetInt("limit")
	sq := fleet.StatusQuery{
		NeedsAttention: get("needs-attention"), NonCompliant: get("non-compliant"),
		Unknown: get("unknown"), Invalid: get("invalid"), StaleEvidence: get("stale"),
		MissingReadiness: get("missing-readiness"), UnresolvedDeps: get("unresolved-deps"), Limit: limit,
	}
	// With no category selected, default to the union (most useful default).
	if !sq.NonCompliant && !sq.Unknown && !sq.Invalid && !sq.StaleEvidence &&
		!sq.MissingReadiness && !sq.UnresolvedDeps {
		sq.NeedsAttention = true
	}
	return sq
}

func newFleetSnapshotCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "snapshot",
		Short: "Emit the whole fleet snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			snap, err := svc.Fleet(cmd.Context(), fleetOptions(cmd))
			if err != nil {
				return err
			}
			return printFleetSnapshot(cmd, snap, v.GetString(outputFormatKey))
		},
	}
}

func newFleetExplainCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "explain <subject>",
		Short: "Explain the deterministic reasons for a service or target state",
		Args:  cobra.ExactArgs(1),
		// Same as `fleet get`: the subject resolves to a service or a target, both
		// of which only a snapshot knows, and neither of which is ever a path.
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			res, err := q.Explain(args[0])
			if err != nil {
				return err
			}
			return printFleetExplain(cmd, res, v.GetString(outputFormatKey))
		},
	}
}
