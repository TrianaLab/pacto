package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// newFleetCommand builds the `pacto fleet` command group: a fleet-scoped,
// cross-service operational view. It is deliberately distinct from `pacto graph`
// (which is single-root: one service's transitive dependency tree). Fleet
// commands operate over a snapshot composed from many sources.
func newFleetCommand(svc *app.Service, v *viper.Viper, d deps, rec *runRecorder) *cobra.Command {
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
	cmd.AddCommand(newFleetStatusCommand(svc, v, rec))
	cmd.AddCommand(newFleetProvidersCommand(svc, v))
	cmd.AddCommand(newFleetSnapshotCommand(svc, v))
	cmd.AddCommand(newFleetExplainCommand(svc, v))
	cmd.AddCommand(newFleetReconcileCommand(svc, v))
	cmd.AddCommand(newFleetEntitiesCommand(svc, v))
	cmd.AddCommand(newFleetEntityCommand(svc, v, d))
	cmd.AddCommand(newFleetAttentionCommand(svc, v))
	cmd.AddCommand(newFleetNeighborhoodCommand(svc, v))
	cmd.AddCommand(newFleetVersionsCommand(svc, v))
	cmd.AddCommand(newFleetOverviewCommand(svc, v))
	cmd.AddCommand(newFleetPublishInferredCommand(svc, v))
	return cmd
}

// addFleetSourceFlags declares the shared fleet source flags on f. Callers pass
// cmd.PersistentFlags() when subcommands must inherit them (pacto fleet) and
// cmd.Flags() otherwise (pacto tui, pacto impact, pacto mcp).
//
// A flag the caller already declared wins: AddFlagSet ignores a name that is
// present, which is how a command keeps a deliberately narrowed spelling (`pacto
// impact --traces` takes one document and reads it itself) or its own help text
// (`pacto mcp --root` selects a catalog, not a snapshot closure) without having
// to restate the rest.
func addFleetSourceFlags(f *pflag.FlagSet) {
	shared := pflag.NewFlagSet("fleet-source", pflag.ContinueOnError)
	shared.StringArray("local", []string{"."}, "local bundle root(s) to scan (repeatable)")
	shared.StringArray("root", nil, "contract root whose whole dependency closure joins the snapshot: a local bundle path or an oci:// reference (repeatable)")
	shared.StringArray("target-state", nil, "offline target-state fixture file(s) supplying targets, for demos and tests; production targets arrive as signed EvidenceSets (repeatable)")
	shared.StringArray("evidence-url", nil, "base URL of an Evidence Server to consume its read-only operational-graph contribution over HTTP (repeatable)")
	shared.StringArray("mira-source", nil, "base URL of a Mira node whose service map supplies observed calls between services (repeatable)")
	shared.StringArray("traces", nil, "OTLP/JSON trace file supplying runtime-observed dependency edges, folded into the snapshot as observed relationships (repeatable)")
	shared.StringArray("oci", nil, "registry reference to include as a published-baseline revision (repeatable)")
	shared.Bool("cache", false, "include every bundle in the local OCI cache as an offline baseline revision")
	shared.Bool("k8s", false, "include live Pacto CRs from the current Kubernetes cluster as targets")
	shared.StringArray("context", nil, "kubeconfig context to read live Pacto CRs from, like --k8s for the current one; repeatable, each cluster read once")
	shared.String("namespace", "", "namespaces to read Pacto CRs from with --k8s, comma-separated (empty = all namespaces)")
	shared.Duration("freshness", fleet.RecentEvidenceWindow, "mark target evidence older than this as stale (the dashboard's window; 0 skips the check and every answer says so)")
	shared.String("external-catalog", "", "organization external catalog to merge over the default, as oci://REPOSITORY@sha256:DIGEST, else PACTO_EXTERNAL_CATALOG; a tag is refused")
	shared.String("detected", "", "add what the operator detected as inferred rows: k8s:<namespace>/<name> of its <release>-detected ConfigMap, or a file holding the same JSON")
	shared.String("fingerprints", "", "a file (YAML or JSON) of the workload managers and promoters this installation runs, as the chart's fingerprints value: each promoter found reads promotion owned by <product> in the detected providers")
	shared.StringArray("target-snapshot", nil, "signed target snapshot(s) to read targets from: an oci:// pacto-targets repository, or :cluster-<uid> tags or digests "+
		"to pin the clusters CI expects (repeatable); pacto impact, pacto ci and pacto_impact on a stdio pacto mcp also take auto (the default: derived from the provider ref) and off")
	shared.String("delivery-trust", "", "the trust config target snapshots are verified with: a YAML file with a key scoped to each cluster (default $"+app.TrustEnv+")")
	shared.Var(new(indexFlag), "index", "organization index pacto impact and pacto ci read consumers from, as <registry>/pacto-index with no tag or digest (default <registry>/pacto-index from the provider reference; "+
		"none reads none; skipped when --root names the consumers; pacto ci --publish also writes its entries there; other fleet commands ignore it)")
	f.AddFlagSet(shared)
}

// fleetFlagNames lists the shared fleet source flags declared by
// newFleetCommand, in declaration order. It is the single list any other
// command copies from when it wants the same source surface.
func fleetFlagNames() []string {
	return []string{
		"local", "root", "target-state", "evidence-url", "mira-source", "traces", "oci",
		"cache", "k8s", "context", "namespace", "freshness", "external-catalog", "detected", "fingerprints",
		"target-snapshot", "delivery-trust", "index",
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
//
// Discarding the error also swallows a TYPE mismatch, which is the sharper
// edge: a command that declares a shared flag name with a different type parses
// the user's value happily and then contributes nothing here, silently. Two
// commands do that on purpose (`pacto impact` and `pacto fleet reconcile` each
// take a single --traces document and read it themselves), and
// TestSharedFleetFlagTypesAreConsistent fails any third one that appears.
// longRunning marks opts as a process that builds again later (see
// [app.FleetOptions.LongRunning]).
func longRunning(opts app.FleetOptions) app.FleetOptions {
	opts.LongRunning = true
	return opts
}

// fleetNow is the build clock fleetOptions hands every snapshot: nil in
// production, so a build reads time.Now; tests pin it.
var fleetNow func() time.Time

// fleetDeps is where fleetOptions finds the ambient cluster it reads the
// installation's publisher trust from; tests swap it.
var fleetDeps = defaultDeps

// publisherTrust is the fleet provenance checker's reader of the
// installation's publisher trust config and pins over the ambient cluster,
// in the namespace --delivery-namespace names (the chart's default when the
// command has no such flag). detected (--detected) advertises the
// operator's signer, which never makes a publisher. With no cluster config a
// signature is judged against the rule inferred from where it lives: nil, or
// only the signer a --detected file advertises.
func publisherTrust(cmd *cobra.Command, d deps, detected string) func(context.Context) (app.Installation, error) {
	if !hasClusterConfig(d) {
		if detected == "" || strings.HasPrefix(detected, "k8s:") {
			return nil
		}
		return (&app.Delivery{Detected: detected, Now: time.Now}).DetectionTrust
	}
	ns, _ := cmd.Flags().GetString("delivery-namespace")
	ns = cmp.Or(ns, delivery.PolicyNamespace)
	return func(ctx context.Context) (app.Installation, error) {
		dyn, err := d.newDynamic()
		if err != nil {
			return app.Installation{}, err
		}
		c := &app.Delivery{Dynamic: dyn, Records: d.newRecordRepo, Policy: delivery.DefaultClassPolicy(), PolicyNamespace: ns, Detected: detected, Now: time.Now}
		return c.InstallationTrust(ctx)
	}
}

// hasClusterConfig reports whether d finds an ambient cluster config.
func hasClusterConfig(d deps) bool {
	if d.clusterConfig == nil {
		return false
	}
	_, err := d.clusterConfig()
	return err == nil
}

func fleetOptions(cmd *cobra.Command) app.FleetOptions {
	local, _ := cmd.Flags().GetStringArray("local")
	// --root is also `pacto mcp`'s catalog-server selector, where it is mutually
	// exclusive with --fleet, so reading it here never picks up a value the mcp
	// fleet path could have set.
	roots, _ := cmd.Flags().GetStringArray("root")
	targetState, _ := cmd.Flags().GetStringArray("target-state")
	evidenceURLs, _ := cmd.Flags().GetStringArray("evidence-url")
	miraURLs, _ := cmd.Flags().GetStringArray("mira-source")
	traceFiles, _ := cmd.Flags().GetStringArray("traces")
	ociRefs, _ := cmd.Flags().GetStringArray("oci")
	includeCache, _ := cmd.Flags().GetBool("cache")
	// --no-cache is the root's "ignore the disk cache" switch, and the bundle
	// store already honours it. A snapshot that kept the cache source would
	// answer from exactly the stale bundles the reader excluded while the OCI
	// source re-pulls. The root materializes an env- or config-set value back
	// onto the flag, so this one read covers all three.
	noCache, _ := cmd.Flags().GetBool("no-cache")
	includeK8s, _ := cmd.Flags().GetBool("k8s")
	contexts, _ := cmd.Flags().GetStringArray("context")
	freshness, _ := cmd.Flags().GetDuration("freshness")
	externalCatalog, _ := cmd.Flags().GetString("external-catalog")
	detected, _ := cmd.Flags().GetString("detected")
	fingerprints, _ := cmd.Flags().GetString("fingerprints")
	snapshots, _ := cmd.Flags().GetStringArray("target-snapshot")
	index, _ := cmd.Flags().GetString("index")
	prov := app.NewProvenanceChecker()
	prov.Trust = publisherTrust(cmd, fleetDeps(), detected)
	return app.FleetOptions{
		LocalRoots:         local,
		CatalogRoots:       roots,
		TargetStateFiles:   targetState,
		EvidenceURLs:       evidenceURLs,
		MiraURLs:           miraURLs,
		ObservationSources: app.TraceFileSources(traceFiles),
		OCIRefs:            ociRefs,
		IncludeCache:       includeCache && !noCache,
		IncludeK8s:         includeK8s,
		K8sContexts:        contextNames(contexts),
		K8sNamespaces:      flagNamespaces(cmd),
		FreshnessWindow:    freshness,
		ExternalCatalogRef: externalCatalog,
		Detected:           detected,
		Fingerprints:       fingerprints,
		TargetSnapshot:     targetSnapshotRefs(snapshots),
		// --trust where a command declares it (pacto delivery), else --delivery-trust.
		TargetSnapshotTrustFile: trustPath(cmd),
		Provenance:              prov,
		RegistryProbe:           app.NewRegistryProbe(),
		Now:                     fleetNow,
		Index:                   index,
	}
}

// targetSnapshotRefs drops auto and off from the --target-snapshot values:
// pacto impact and pacto ci derive the snapshot themselves, and every other command reads one
// only when given a ref.
func targetSnapshotRefs(values []string) []string {
	var out []string
	for _, v := range values {
		if v != "auto" && v != "off" && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// contextNames is the --context list: trimmed, empty entries and repeated
// names dropped, order kept.
func contextNames(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// flagNamespaces is the --namespace list of cmd; empty when unset or
// undeclared.
func flagNamespaces(cmd *cobra.Command) []string {
	ns, _ := cmd.Flags().GetString("namespace")
	return splitNamespaces(ns)
}

// splitNamespaces reads a comma-separated namespace list: trimmed, sorted,
// deduplicated, empty entries dropped. One value is a list of one.
func splitNamespaces(s string) []string {
	var out []string
	for ns := range strings.SplitSeq(s, ",") {
		if ns = strings.TrimSpace(ns); ns != "" {
			out = append(out, ns)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// deliveryNarrowing is the namespace delivery reads are narrowed to: the one
// namespace of list, or "" (every namespace) for none or several. Delivery
// reads list one namespace or all of them, and always run with the reader's
// own token, so this is a convenience and never an authorization boundary:
// a list of several namespaces reads delivery everywhere the reader may.
// --delivery-namespace names where the identity policy is published and never
// narrows them.
func deliveryNarrowing(list []string) string {
	if len(list) == 1 {
		return list[0]
	}
	return ""
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
//
// A complete answer prints nothing, except a note when freshness was not
// checked: that limitation never makes an answer partial, yet a stale count of
// zero must not read as "nothing is stale".
func warnPartial(cmd *cobra.Command, m fleet.Meta) {
	if m.Completeness == fleet.CompletenessComplete {
		for _, l := range m.Limitations {
			if l.Code == fleet.LimitationFreshnessNotEvaluated {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: [%s] %s\n", l.Code, l.Message)
			}
		}
		return
	}
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(w, "warning: answer is %s (as of %s)\n", m.Completeness, m.AsOf.Format(time.RFC3339))
	// Text drops the per-source split, so two sources reporting one problem
	// (a name collision across two --local roots) would print it twice.
	seen := map[string]bool{}
	for _, l := range m.Limitations {
		line := fmt.Sprintf("  - [%s] %s\n", l.Code, l.Message)
		if !seen[line] {
			seen[line] = true
			_, _ = fmt.Fprint(w, line)
		}
	}
}

func newFleetSearchCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search [text]",
		Short: "Search logical services in the fleet",
		Long: "Search logical services in the fleet. A search on text alone, first page, also " +
			"lists up to 20 external systems whose identity, label or alias holds the text; " +
			"any other filter or --offset lists none.",
		Args: cobra.MaximumNArgs(1),
		// Free text matched against the fleet, never a path — the same reason its
		// three siblings give. Without this the shell falls back to filenames and
		// offers the working directory as if it were a search term.
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			f, err := searchFilterFromCmd(cmd, optionalArg(args))
			if err != nil {
				return err
			}
			res, err := q.Search(f)
			if err != nil {
				return err
			}
			return printFleetSearch(cmd, res, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("owner", "", "filter by owner (team, DRI or contact)")
	cmd.Flags().String("business-domain", "", "filter by business domain, "+fleet.NoBusinessDomain+" for none or "+fleet.UnknownBusinessDomain+" for unknown")
	cmd.Flags().String("offering", "", "filter by the platform offering a service provides (service.offering)")
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
	addTemplateFlag(cmd.Flags())
	addDeclarationFlag(cmd)
	cmd.Flags().Int("limit", 0, fmt.Sprintf("maximum results (0 = %d, capped at %d)", fleet.DefaultSearchLimit, fleet.MaxSearchLimit))
	cmd.Flags().Int("offset", 0, "result offset for paging")

	// Three closed vocabularies the code already owns, so declaring them costs
	// nothing at runtime and turns empty completion into real completion. The
	// remaining string filters (--owner, --source, --scope, --label) take values
	// that come from the fleet data, not from a vocabulary, so guessing at them
	// would be worse than offering nothing.
	_ = cmd.RegisterFlagCompletionFunc("status", staticCompletions(fleet.CanonicalStatuses()...))
	_ = cmd.RegisterFlagCompletionFunc("compliance", staticCompletions(fleet.CanonicalStatuses()...))
	_ = cmd.RegisterFlagCompletionFunc("workload", staticCompletions(
		contract.WorkloadService, contract.WorkloadJob, contract.WorkloadScheduled))
	return cmd
}

func searchFilterFromCmd(cmd *cobra.Command, text string) (fleet.SearchFilter, error) {
	owner, _ := cmd.Flags().GetString("owner")
	businessDomain, _ := cmd.Flags().GetString("business-domain")
	offering, _ := cmd.Flags().GetString("offering")
	status, _ := cmd.Flags().GetString("status")
	compliance, _ := cmd.Flags().GetString("compliance")
	source, _ := cmd.Flags().GetString("source")
	workload, _ := cmd.Flags().GetString("workload")
	scope, _ := cmd.Flags().GetString("scope")
	template, _ := cmd.Flags().GetString("template")
	declaration, _ := cmd.Flags().GetString("declaration")
	labels, _ := cmd.Flags().GetStringArray("label")
	ready, _ := cmd.Flags().GetBool("ready")
	notReady, _ := cmd.Flags().GetBool("not-ready")
	hasCap, _ := cmd.Flags().GetBool("has-capability")
	hasDep, _ := cmd.Flags().GetBool("has-dependency")
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	parsed, err := fleet.ParseLabels(labels)
	return fleet.SearchFilter{
		Text: text, Owner: owner, BusinessDomain: businessDomain, Offering: offering, Labels: parsed, Scope: scope, Status: status,
		Compliance: compliance, Source: source, Workload: workload,
		HasCapability: hasCap, HasDependency: hasDep, ReadyOnly: ready, NotReady: notReady, Template: template, Declaration: declaration,
		Limit: limit, Offset: offset,
	}, err
}

func newFleetGetCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get [service]",
		Short: "Inspect a logical service, an external system or an operational target",
		Long: "Inspect a logical service by name, an external system by external:<identity> " +
			"(a vendor, managed service or legacy host no Pacto contract describes) or an operational target with --target.",
		Args: cobra.MaximumNArgs(1),
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
				return fmt.Errorf("provide a service name, external:<identity> or --target")
			}
			if _, ok := fleet.ParseExternalSubject(name); ok {
				ev, err := q.GetExternal(name)
				if err != nil {
					return err
				}
				return printFleetExternal(cmd, ev, v.GetString(outputFormatKey))
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
			"an exact revision (never 'latest'). The walk also follows observed " +
			"runtime calls, so services no contract connects appear too; each node " +
			"reports its provenance (declared, observed or declared+observed, or " +
			"inferred for an external system's consumer known only from its configuration " +
			"or a runtime call one of whose ends no contract describes). Those inferred calls " +
			"are listed after the nodes with the sources that saw them, and a service asked " +
			"for by the key of an inferred service it replaced says so first. Give " +
			"external:<identity> to list the consumers of an external system (its only direction is dependents).",
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
				IncludeObserved: true,
			})
			if err != nil {
				return err
			}
			return printFleetGraph(cmd, res, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("direction", "", "traversal direction (dependencies, dependents); unset means dependencies, or dependents for an external: root")
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

func newFleetStatusCommand(svc *app.Service, v *viper.Viper, rec *runRecorder) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report services and targets needing attention",
		Long: "Reports what needs attention: non-compliant, unknown, invalid and stale targets, " +
			"revisions without readiness, unresolved dependencies, dependencies whose provider " +
			"version is outside the declared range, services that use a deprecated or past-sunset provider, " +
			"services with no declared owner and undeclared " +
			"runtime dependencies. With no category flag it reports every category.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			res := q.Status(statusQueryFromCmd(cmd))
			// The print result is the exit code; the coverage record never is.
			err = printFleetStatus(cmd, res, v.GetString(outputFormatKey))
			recordCoverage(cmd, rec, q)
			return err
		},
	}
	addCoverageFlags(cmd)
	cmd.Flags().Bool("needs-attention", false, "report every attention category")
	cmd.Flags().Bool("non-compliant", false, "report non-compliant targets")
	cmd.Flags().Bool("unknown", false, "report targets with unknown compliance")
	cmd.Flags().Bool("invalid", false, "report structurally invalid contracts")
	cmd.Flags().Bool("stale", false, "report targets with stale evidence")
	cmd.Flags().Bool("missing-readiness", false, "report revisions without a readiness assessment")
	cmd.Flags().Bool("unresolved-deps", false, "report unresolved declared dependencies")
	cmd.Flags().Bool("unsatisfied-ranges", false, "report dependencies whose provider version is outside the declared range (DEPENDENCY_RANGE_UNSATISFIED)")
	cmd.Flags().Bool("lifecycle", false, "report services that use a deprecated or past-sunset provider (DEPENDENCY_SERVICE_DEPRECATED, DEPENDENCY_SERVICE_SUNSET, SUCCESSOR_OUT_OF_RANGE)")
	cmd.Flags().Bool("unowned", false, "report services with no declared owner")
	cmd.Flags().Bool("undeclared", false, "report runtime calls no contract declares and callers with no contract")
	cmd.Flags().Bool("golden-path-deployed", false, "report running services that declare service.template (GOLDEN_PATH_DEPLOYED)")
	cmd.Flags().Int("limit", 0, fmt.Sprintf("maximum results (0 = %d)", fleet.DefaultStatusLimit))
	return cmd
}

func statusQueryFromCmd(cmd *cobra.Command) fleet.StatusQuery {
	get := func(name string) bool { b, _ := cmd.Flags().GetBool(name); return b }
	limit, _ := cmd.Flags().GetInt("limit")
	// With no category selected the query reports every category; the rule
	// lives in fleet.Query.Status so the MCP tool and the CLI agree.
	return fleet.StatusQuery{
		NeedsAttention: get("needs-attention"), NonCompliant: get("non-compliant"),
		Unknown: get("unknown"), Invalid: get("invalid"), StaleEvidence: get("stale"),
		MissingReadiness: get("missing-readiness"), UnresolvedDeps: get("unresolved-deps"),
		UnsatisfiedRanges: get("unsatisfied-ranges"), Lifecycle: get("lifecycle"), Unowned: get("unowned"), Undeclared: get("undeclared"),
		GoldenPathDeployed: get("golden-path-deployed"), Limit: limit,
	}
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
		Long: "Explain the deterministic reasons for a service or target state. Give external:<identity> " +
			"to list the sources that declare an external system and the catalog entry that classified it.",
		Args: cobra.ExactArgs(1),
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
