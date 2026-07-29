package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v3/internal/app"
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
	cmd.PersistentFlags().StringArray("local", []string{"."}, "local bundle root(s) to scan (repeatable)")
	cmd.PersistentFlags().StringArray("target-state", nil, "offline target-state fixture file(s) supplying targets — a demo/test adapter, not the signed EvidenceSet protocol (repeatable)")
	cmd.PersistentFlags().Duration("freshness", 0, "mark target evidence older than this as stale (0 disables)")

	cmd.AddCommand(newFleetSearchCommand(svc, v))
	cmd.AddCommand(newFleetGetCommand(svc, v))
	cmd.AddCommand(newFleetGraphCommand(svc, v))
	cmd.AddCommand(newFleetStatusCommand(svc, v))
	cmd.AddCommand(newFleetSnapshotCommand(svc, v))
	cmd.AddCommand(newFleetExplainCommand(svc, v))
	return cmd
}

// fleetOptions reads the shared source flags into app.FleetOptions.
func fleetOptions(cmd *cobra.Command) app.FleetOptions {
	local, _ := cmd.Flags().GetStringArray("local")
	targetState, _ := cmd.Flags().GetStringArray("target-state")
	freshness, _ := cmd.Flags().GetDuration("freshness")
	return app.FleetOptions{LocalRoots: local, TargetStateFiles: targetState, FreshnessWindow: freshness}
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
	cmd.Flags().String("status", "", "filter by aggregate status (Compliant, NonCompliant, Unknown, Invalid, NotEvaluated)")
	cmd.Flags().String("compliance", "", "filter to services with a target of this compliance")
	cmd.Flags().String("source", "", "filter by observing source")
	cmd.Flags().String("workload", "", "filter by workload (service, job, scheduled)")
	cmd.Flags().StringArray("label", nil, "filter by label key=value (repeatable)")
	cmd.Flags().Bool("ready", false, "only operationally ready services")
	cmd.Flags().Bool("not-ready", false, "only services not operationally ready")
	cmd.Flags().Bool("has-capability", false, "only services declaring a capability")
	cmd.Flags().Bool("has-dependency", false, "only services declaring a dependency")
	cmd.Flags().String("scope", "", "correlate to a target with this scope")
	cmd.Flags().Int("limit", 0, "maximum results (0 = default)")
	cmd.Flags().Int("offset", 0, "result offset for paging")
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
	cmd.Flags().Int("limit", 0, "maximum results (0 = default)")
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
