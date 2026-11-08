package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func newFleetEntitiesCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "entities [text]",
		Short: "List services, owners, business domains or platform offerings",
		Long: "Lists services, owners, business domains and platform offerings, paged. " +
			"Use --kind owner to learn the owner keys (team:NAME, dri:NAME) that --owner-key takes.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			if k, _ := cmd.Flags().GetString("kind"); k != "" && !slices.Contains(fleet.ProductKinds(), k) {
				return fmt.Errorf("--kind %q: want one of %s", k, strings.Join(fleet.ProductKinds(), ", "))
			}
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			res, err := q.Entities(entityFilterFromCmd(cmd, optionalArg(args)))
			if err != nil {
				return err
			}
			return printFleetEntities(cmd, res, v.GetString(outputFormatKey))
		},
	}
	f := cmd.Flags()
	f.String("kind", "", "entity kind (service, owner, domain, offering; empty lists all four)")
	f.String("owner", "", "free-text owner search over team, DRI and contacts")
	f.String("owner-key", "", "exact owner key, such as team:platform or dri:alice")
	f.String("business-domain", "", "exact business domain, "+fleet.NoBusinessDomain+" for none or "+fleet.UnknownBusinessDomain+" for unknown")
	f.String("offering", "", "services that provide this platform offering, and the offering itself")
	f.String("consumes", "", "services that depend on or reference a provider of this platform offering")
	f.String("phase", "", "with --consumes: only the consumers with a provisioning status of that offering in this phase")
	f.String("ownership", "", "how a service's revisions declare ownership (consistent, conflicting, unowned, unidentified: no single canonical owner)")
	f.String("status", "", "canonical compliance status")
	addTemplateFlag(f)
	f.String("created-from", "", "services created from this golden path, by key")
	f.String("sort", "", "order: name, health (least healthy first), services (owners with the most services first) or nonCompliant (owners and domains with the most non-compliant services first)")
	f.Int("limit", 0, fmt.Sprintf("maximum results (0 = %d, capped at %d)", fleet.DefaultEntityLimit, fleet.MaxEntityLimit))
	f.Int("offset", 0, "result offset for paging")
	_ = cmd.RegisterFlagCompletionFunc("kind", staticCompletions(fleet.ProductKinds()...))
	_ = cmd.RegisterFlagCompletionFunc("ownership", staticCompletions(fleet.OwnershipConsistent, fleet.OwnershipConflicting, fleet.OwnershipUnowned, fleet.OwnershipUnidentified))
	_ = cmd.RegisterFlagCompletionFunc("status", staticCompletions(fleet.CanonicalStatuses()...))
	_ = cmd.RegisterFlagCompletionFunc("phase", staticCompletions(fleet.PhaseBuckets()...))
	_ = cmd.RegisterFlagCompletionFunc("sort", staticCompletions(fleet.SortName, fleet.SortHealth, fleet.SortServices, fleet.SortNonCompliant))
	return cmd
}

func newFleetNeighborhoodCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "neighborhood <service>",
		Short: "Show the services around one service, with their owners",
		Long: "Shows who a service depends on and who depends on it, to the requested depth, each with its owner " +
			"and status, then the edges between them: each says whether a contract declares it, telemetry observed it " +
			"or both, and how the two compare. The service is a name, or its key when the name is used in more than one domain.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			dir, _ := f.GetString("direction")
			views, _ := f.GetString("views")
			depth, _ := f.GetInt("depth")
			maxNodes, _ := f.GetInt("max-nodes")
			maxEdges, _ := f.GetInt("max-edges")
			nb, err := q.Neighborhood(fleet.NeighborhoodQuery{
				Kind: fleet.KindService, Key: args[0], Direction: fleet.Direction(dir),
				Depth: depth, Views: mcp.SplitViews(views), MaxNodes: maxNodes, MaxEdges: maxEdges,
			})
			if err != nil {
				return err
			}
			return printFleetNeighborhood(cmd, nb, v.GetString(outputFormatKey))
		},
	}
	f := cmd.Flags()
	f.String("direction", string(fleet.DirectionBoth), "which side to walk (dependencies, dependents, both)")
	f.Int("depth", 0, fmt.Sprintf("how many hops to walk (0 = %d, at most %d)", fleet.DefaultNeighborhoodDepth, fleet.MaxNeighborhoodDepth))
	f.String("views", "expected,observed", "comma-separated knowledge views (expected, observed, differences)")
	f.Int("max-nodes", 0, fmt.Sprintf("maximum services (0 = %d, at most %d)", fleet.DefaultMaxNodes, fleet.MaxNeighborhoodNodes))
	f.Int("max-edges", 0, fmt.Sprintf("maximum edges (0 = %d, at most %d)", fleet.DefaultMaxEdges, fleet.MaxNeighborhoodEdges))
	_ = cmd.RegisterFlagCompletionFunc("direction", staticCompletions(string(fleet.DirectionDependencies), string(fleet.DirectionDependents), string(fleet.DirectionBoth)))
	return cmd
}

func entityFilterFromCmd(cmd *cobra.Command, text string) fleet.EntityFilter {
	get := func(name string) string { s, _ := cmd.Flags().GetString(name); return s }
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	kinds := fleet.ProductKinds()
	if k := get("kind"); k != "" {
		kinds = []string{k}
	} else if get("template") == fleet.TemplateOnly {
		kinds = []string{string(fleet.KindService)}
	}
	var ks []fleet.EntityKind
	for _, k := range kinds {
		ks = append(ks, fleet.EntityKind(k))
	}
	return fleet.EntityFilter{
		Text: text, Kinds: ks, Owner: get("owner"), OwnerKey: get("owner-key"), BusinessDomain: get("business-domain"),
		Offering: get("offering"), Consumes: get("consumes"), Phase: get("phase"), Ownership: get("ownership"), Status: get("status"), Sort: get("sort"),
		Template: get("template"), CreatedFrom: get("created-from"), Limit: limit, Offset: offset,
	}
}

// addTemplateFlag adds --template: bare it keeps only golden paths, and
// --template=exclude leaves them out.
func addTemplateFlag(f *pflag.FlagSet) {
	f.String("template", "", "golden paths only, or exclude to leave them out")
	f.Lookup("template").NoOptDefVal = fleet.TemplateOnly
}

func newFleetEntityCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "entity <kind> <key>",
		Short: "Inspect one service, revision, target, owner, source, business domain or offering",
		Args:  cobra.ExactArgs(2),
		// The kind is a closed vocabulary; the key lives in a snapshot too
		// expensive to build at the prompt.
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) == 0 {
				return fleet.EntityKinds(), cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			res, err := q.EntityDetail(fleet.EntityKind(args[0]), args[1])
			if err != nil {
				return err
			}
			return printFleetEntity(cmd, res, v.GetString(outputFormatKey))
		},
	}
}

func newFleetVersionsCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions <offering>",
		Short: "List the versions of each provider of a platform offering",
		Long: "Lists each provider's versions of a platform offering, newest first, with whether a target runs " +
			"each one and the service lifecycle it declares. With --range, each version shows whether it " +
			"satisfies the range and each provider names its latest compatible version. The list comes from " +
			"the contract revisions the snapshot holds; the registry may publish others.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: noCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			provider, _ := cmd.Flags().GetString("provider")
			rng, _ := cmd.Flags().GetString("range")
			res, err := q.OfferingVersions(args[0], provider, rng)
			if err != nil {
				return err
			}
			return printFleetVersions(cmd, res, v.GetString(outputFormatKey))
		},
	}
	cmd.Flags().String("provider", "", "only this provider: its key, or a name only one domain uses")
	cmd.Flags().String("range", "", "semver range to check each version against, such as ^16.0.0")
	return cmd
}

func newFleetAttentionCommand(svc *app.Service, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attention",
		Short: "List what needs attention, most severe first",
		Long: "Lists what needs attention, most severe first, each item with its reason and next step. " +
			"--category undeclared lists runtime calls no contract declares and callers with no contract.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := buildQuery(cmd, svc)
			if err != nil {
				return err
			}
			res, err := q.Attention(attentionFilterFromCmd(cmd))
			if err != nil {
				return err
			}
			return printFleetAttention(cmd, res, v.GetString(outputFormatKey))
		},
	}
	f := cmd.Flags()
	f.String("category", "", "attention category")
	f.String("severity", "", "attention severity (error, warning, info)")
	f.String("owner", "", "free-text owner search over team, DRI and contacts")
	f.String("owner-key", "", "exact owner key, such as team:platform or dri:alice")
	f.String("business-domain", "", "exact business domain, "+fleet.NoBusinessDomain+" for none or "+fleet.UnknownBusinessDomain+" for unknown")
	f.String("service", "", "items of this service: its key, or a name only one domain uses")
	f.Int("limit", 0, fmt.Sprintf("maximum results (0 = %d, capped at %d)", fleet.DefaultAttentionLimit, fleet.MaxAttentionLimit))
	f.Int("offset", 0, "result offset for paging")
	_ = cmd.RegisterFlagCompletionFunc("category", staticCompletions(fleet.AttentionCategoriesList()...))
	_ = cmd.RegisterFlagCompletionFunc("severity", staticCompletions("error", "warning", "info"))
	return cmd
}

func attentionFilterFromCmd(cmd *cobra.Command) fleet.AttentionFilter {
	get := func(name string) string { s, _ := cmd.Flags().GetString(name); return s }
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	return fleet.AttentionFilter{
		Category: get("category"), Severity: get("severity"), Owner: get("owner"), OwnerKey: get("owner-key"),
		BusinessDomain: get("business-domain"), Service: get("service"), Limit: limit, Offset: offset,
	}
}

func printFleetEntities(cmd *cobra.Command, res *fleet.EntityList, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%d of %d entit(ies):\n", res.Count, res.Total)
		for _, e := range res.Entities {
			kind := string(e.Kind)
			if e.Template {
				kind = "golden path"
			}
			_, _ = fmt.Fprintf(w, "  %-11s %-28s %-32s %s\n", kind, e.Label, e.Key, orDash(e.Status))
		}
		printNonCompliantRanking(w, &res.Aggregate)
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
}

// printNonCompliantRanking renders who owns the matched non-compliant services and
// which domains hold them, the rows and counts summing to every one of them.
func printNonCompliantRanking(w io.Writer, a *fleet.EntityAggregate) {
	total := a.ServiceCompliance.NonCompliant
	if total == 0 {
		return
	}
	parts := make([]string, 0, len(a.NonCompliantByOwner)+2)
	for _, o := range a.NonCompliantByOwner {
		parts = append(parts, fmt.Sprintf("%s %d of %d", o.Key, o.NonCompliant, o.Services))
	}
	parts = appendCount(parts, a.NonCompliantBeyondRanking, "with other owners")
	parts = appendCount(parts, a.NonCompliantUnidentified, "with no identified owner")
	_, _ = fmt.Fprintf(w, "Non-compliant services by owner (%d): %s\n", total, strings.Join(parts, ", "))
	parts = parts[:0]
	for _, d := range a.NonCompliantByDomain {
		parts = append(parts, fmt.Sprintf("%s %d of %d", d.Domain, d.NonCompliant, d.Services))
	}
	parts = appendCount(parts, a.NonCompliantDomainsBeyond, "in other domains")
	parts = appendCount(parts, a.NonCompliantNoDomain, "with no domain")
	parts = appendCount(parts, a.NonCompliantDomainUnknown, "whose domain is unknown")
	_, _ = fmt.Fprintf(w, "Non-compliant services by domain (%d): %s\n", total, strings.Join(parts, ", "))
}

// appendCount adds "N words" when N is not zero.
func appendCount(parts []string, n int, words string) []string {
	if n == 0 {
		return parts
	}
	return append(parts, fmt.Sprintf("%d %s", n, words))
}

func printFleetEntity(cmd *cobra.Command, res *fleet.EntityDetail, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Kind: %s\nKey: %s\nLabel: %s\nStatus: %s\n", res.Entity.Kind, res.Entity.Key, res.Entity.Label, orDash(res.Status))
		if res.Offering != nil {
			printOfferingText(w, res.Offering)
		}
		if res.Service != nil {
			printOfferingUses(w, "Offerings used:", res.Service.ConsumedOfferings)
			printTemplateText(w, res.Entity.Summary, res.Service.Template)
			printOriginText(w, res.Service.Origin, res.Service.Creation)
			printDependents(w, res.Service)
		}
		if res.Revision != nil {
			printRevisionPlace(w, res.Revision)
		}
		_, _ = fmt.Fprintln(w, "Run with --output-format json for the full detail")
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetVersions(cmd *cobra.Command, res *fleet.OfferingVersionsResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		note := ""
		for _, p := range res.Providers {
			_, _ = fmt.Fprint(w, p.Provider.Label)
			if p.Versions.Range != "" {
				_, _ = fmt.Fprintf(w, ": latest compatible %s", latestCompatible(p.Versions))
			}
			_, _ = fmt.Fprintln(w)
			for _, it := range p.Versions.Items {
				_, _ = fmt.Fprintf(w, "  %s%s\n", orDash(it.Version), versionNotes(it))
			}
			if p.Versions.Truncated {
				_, _ = fmt.Fprintf(w, "  %d more\n", p.Versions.Total-p.Versions.Count)
			}
			note = p.Versions.Note
		}
		if res.Truncated {
			_, _ = fmt.Fprintf(w, "%d of %d providers shown\n", len(res.Providers), res.Total)
		}
		_, _ = fmt.Fprintln(w, note)
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
}

// latestCompatible is the version of v's latest compatible revision, "none"
// when no version satisfies the range.
func latestCompatible(v fleet.ProviderVersionsPreview) string {
	if v.LatestCompatible == nil {
		return "none"
	}
	for _, it := range v.Items {
		if it.Revision.Key == v.LatestCompatible.Key {
			return orDash(it.Version)
		}
	}
	return v.LatestCompatible.Label
}

// versionNotes is the bracketed suffix of one version line: whether a target
// runs it, its lifecycle and its range status.
func versionNotes(v fleet.ProviderVersion) string {
	var notes []string
	if v.InUse != nil && *v.InUse {
		notes = append(notes, "in use")
	}
	if l := lifecycleText(v.Lifecycle); l != "" {
		notes = append(notes, l)
	}
	if v.RangeStatus != "" {
		notes = append(notes, v.RangeStatus)
	}
	if len(notes) == 0 {
		return ""
	}
	return " [" + strings.Join(notes, ", ") + "]"
}

// lifecycleText renders a service lifecycle, empty when there is none.
func lifecycleText(l *contract.InterfaceLifecycle) string {
	if l == nil {
		return ""
	}
	var parts []string
	if l.Deprecated != "" {
		parts = append(parts, "deprecated since "+l.Deprecated)
	}
	if l.Sunset != "" {
		parts = append(parts, "sunset "+l.Sunset)
	}
	if l.Successor != "" {
		parts = append(parts, "successor "+l.Successor)
	}
	return strings.Join(parts, ", ")
}

// printOfferingText renders an offering's providers, its consumers and how
// many consumers the caller's permissions hide.
func printOfferingText(w io.Writer, o *fleet.OfferingDetailData) {
	_, _ = fmt.Fprintln(w, "Providers:")
	for _, p := range o.Providers.Items {
		_, _ = fmt.Fprintf(w, "  %s (%s)\n", p.Service.Label, p.OfferingProvenance)
		if p.Ownership != nil && p.Ownership.Owner != "" {
			_, _ = fmt.Fprintf(w, "    owner: %s\n", p.Ownership.Owner)
		}
		if p.CurrentRevision != nil {
			_, _ = fmt.Fprintf(w, "    current: %s\n", p.CurrentRevision.Label)
		}
		if l := lifecycleText(p.Lifecycle); l != "" {
			_, _ = fmt.Fprintf(w, "    lifecycle: %s\n", l)
		}
		versions := make([]string, 0, len(p.Versions.Items))
		for _, v := range p.Versions.Items {
			versions = append(versions, orDash(v.Version)+versionNotes(v))
		}
		_, _ = fmt.Fprintf(w, "    versions: %s\n", orDash(strings.Join(versions, "; ")))
		if p.Consume != nil {
			_, _ = fmt.Fprintf(w, "    To consume %s:\n", p.Service.Label)
			for line := range strings.Lines(p.Consume.YAML) {
				_, _ = fmt.Fprint(w, "      "+line)
			}
		}
	}
	printOfferingUses(w, "Consumers:", o.Consumers)
	if o.HiddenConsumers > 0 {
		_, _ = fmt.Fprintf(w, "%d consumers you may not see\n", o.HiddenConsumers)
	}
}

// printTemplateText renders a golden path: what it is for, its signature,
// the plugins it names, what it requires, the services created from it and
// how to create one.
func printTemplateText(w io.Writer, summary string, t *fleet.TemplateDetail) {
	if t == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Golden path: %s\n", orDash(summary))
	if t.Signature == nil {
		_, _ = fmt.Fprintln(w, "Signature: not checked: no source that read it checks signatures")
	} else {
		_, _ = fmt.Fprintf(w, "Signature: %s%s\n  %s\n", t.Signature.Status, signatureReason(*t.Signature), t.SignatureNote)
	}
	if len(t.Plugins) > 0 {
		_, _ = fmt.Fprintln(w, "Plugins:")
		for _, p := range t.Plugins {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", p.Name, orDash(p.Description))
		}
	}
	printOfferingUses(w, "Requirements:", t.Requirements)
	if t.UnresolvedRequirements.Total > 0 {
		_, _ = fmt.Fprintln(w, "Unresolved requirements:")
		for _, u := range t.UnresolvedRequirements.Items {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", u.Ref, orDash(u.Reason))
		}
	}
	if t.HiddenRequirements > 0 {
		_, _ = fmt.Fprintf(w, "Hidden requirements: %d on services you may not see\n", t.HiddenRequirements)
	}
	if t.CreatedServices.Total > 0 || t.HiddenCreatedServices > 0 {
		_, _ = fmt.Fprintln(w, "Created services:")
		for _, c := range t.CreatedServices.Items {
			_, _ = fmt.Fprintf(w, "  %s\n", c.Label)
		}
		if t.CreatedServices.Truncated {
			_, _ = fmt.Fprintf(w, "  %d more\n", t.CreatedServices.Total-t.CreatedServices.Count)
		}
		if t.HiddenCreatedServices > 0 {
			_, _ = fmt.Fprintf(w, "  %d created services you may not see\n", t.HiddenCreatedServices)
		}
	}
	if t.Instantiate != nil {
		_, _ = fmt.Fprintf(w, "Create a service: %s\n", t.Instantiate.CLI)
	} else {
		_, _ = fmt.Fprintf(w, "Create a service: %s\n", t.InstantiateReason)
	}
}

// signatureReason is what follows a signature status: the signer when it
// verified, else the reason when the check gives one.
func signatureReason(s fleet.BundleSignature) string {
	if s.Status == fleet.SignatureVerified {
		return ", signed by " + s.Signer
	}
	if s.Reason == "" {
		return ""
	}
	return ": " + s.Reason
}

// printOriginText renders a service's declared origin, what it resolves to
// and its creation checklist.
func printOriginText(w io.Writer, o *fleet.OriginDetail, steps []fleet.CreationStep) {
	if o == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "Created from: %s (%s), %s\n", o.Ref, o.Digest, o.Provenance)
	if o.GoldenPath != nil {
		label := o.GoldenPath.Label
		if o.Revision != nil {
			label = o.Revision.Label
		}
		_, _ = fmt.Fprintf(w, "  golden path: %s\n", label)
	} else {
		_, _ = fmt.Fprintf(w, "  golden path: %s\n", orDash(o.Reason))
	}
	printCreationSteps(w, "Creation:", steps)
}

// printOfferingUses renders offering uses with their range, the provider's
// lifecycle and one provisioning line per environment.
func printOfferingUses(w io.Writer, title string, uses fleet.OfferingUsesPreview) {
	if uses.Total == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, title)
	for _, u := range uses.Items {
		_, _ = fmt.Fprintf(w, "  %s -> %s (%s) via %s", u.Consumer.Label, u.Provider.Label, u.Offering, u.Via)
		if u.RangeStatus != "" {
			_, _ = fmt.Fprintf(w, ", range %s", u.RangeStatus)
		}
		if l := lifecycleText(u.ProviderLifecycle); l != "" {
			_, _ = fmt.Fprintf(w, ", provider %s", l)
		}
		_, _ = fmt.Fprintln(w)
		for _, st := range u.Provisioning {
			where := strings.TrimSpace(st.Environment + " " + st.Entry)
			if where != "" {
				where = " " + where
			}
			_, _ = fmt.Fprintf(w, "    provisioning%s: %s\n", where, provisioningText(st))
		}
	}
	if uses.Truncated {
		_, _ = fmt.Fprintf(w, "  %d more\n", uses.Total-uses.Count)
	}
	r := uses.Range
	_, _ = fmt.Fprintf(w, "  range of all %d: %d satisfied, %d unsatisfied, %d unknown, %d not checked\n", uses.Total, r.Satisfied, r.Unsatisfied, r.Unknown, r.None)
	if p := uses.Phases; p != (fleet.PhaseTally{}) {
		_, _ = fmt.Fprintf(w, "  provisioning requests: %d requested, %d provisioning, %d ready, %d failed, %d deleting, %d not reported\n",
			p.Requested, p.Provisioning, p.Ready, p.Failed, p.Deleting, p.Unknown)
		c := uses.PhaseConsumers
		_, _ = fmt.Fprintf(w, "  consumers by phase: %d requested, %d provisioning, %d ready, %d failed, %d deleting, %d not reported\n",
			c.Requested, c.Provisioning, c.Ready, c.Failed, c.Deleting, c.Unknown)
	}
}

// printRevisionPlace renders where a revision stands in its service, how many
// of the service's targets run it and which sources report about it.
func printRevisionPlace(w io.Writer, r *fleet.RevisionDetailData) {
	current := ""
	if r.Current {
		current = ", current"
	}
	_, _ = fmt.Fprintf(w, "Position: %d of %d revisions, newest first%s\n", r.Position, r.Revisions, current)
	_, _ = fmt.Fprintf(w, "Targets: %d of the service's %d run this revision (%d exact, %d inferred)\n",
		r.ExactTargets.Total+r.InferredTargets.Total, r.ServiceTargets, r.ExactTargets.Total, r.InferredTargets.Total)
	c := r.EvidenceSourceCounts
	_, _ = fmt.Fprintf(w, "Evidence sources: %d (%d available, %d partial, %d stale, %d unavailable)\n", c.Total, c.Available, c.Partial, c.Stale, c.Unavailable)
}

// printDependents renders how many services depend on this one, by status,
// and how many the caller's permissions hide.
func printDependents(w io.Writer, d *fleet.ServiceDetailData) {
	if d.Dependents.Total == 0 && d.HiddenDependents == 0 {
		return
	}
	t := d.DependentsStatus
	_, _ = fmt.Fprintf(w, "Dependents: %d (%d compliant, %d non-compliant, %d unknown, %d other)\n",
		d.Dependents.Total, t.Compliant, t.NonCompliant, t.Unknown, t.Total()-t.Compliant-t.NonCompliant-t.Unknown)
	if d.HiddenDependents > 0 {
		_, _ = fmt.Fprintf(w, "%d dependents you may not see\n", d.HiddenDependents)
	}
}

// provisioningText renders one provisioning status: its phase, (unsigned) for
// a report from an unsigned file and, for Unknown, why.
func provisioningText(st fleet.ProvisioningStatus) string {
	s := st.Phase
	if st.Source != "" && !st.Verified {
		s += " (unsigned)"
	}
	if st.Reason != "" {
		s += ": " + st.Reason
	}
	return s
}

func printFleetAttention(cmd *cobra.Command, res *fleet.AttentionList, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%d of %d attention item(s):\n", res.Count, res.Total)
		for _, it := range res.Items {
			_, _ = fmt.Fprintf(w, "  [%s] %s %s: %s (next: %s)\n", it.Severity, it.Category, it.Label, it.Summary, orDash(it.NextStep))
		}
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetNeighborhood(cmd *cobra.Command, nb *fleet.Neighborhood, format string) error {
	return formatResult(cmd, format, nb, func() error {
		w := cmd.OutOrStdout()
		for _, n := range nb.Nodes {
			_, _ = fmt.Fprintf(w, "  %s  depth=%d  owner=%s  status=%s\n", n.Ref.Label, n.Depth, orDash(n.Owner), orDash(n.Status))
		}
		for _, e := range nb.Edges {
			_, _ = fmt.Fprintf(w, "  %s -> %s  %s  %s\n", e.From.Label, e.To.Label, e.Provenance, orDash(e.Difference))
		}
		if nb.Truncated {
			_, _ = fmt.Fprintf(w, "truncated: %d of %d services shown; raise --max-nodes to see more\n", len(nb.Nodes), nb.TotalNodes)
		}
		if nb.TruncatedEdges {
			_, _ = fmt.Fprintln(w, "truncated: some edges are not shown; raise --max-edges to see more")
		}
		warnProductPartial(cmd, nb.Meta)
		return nil
	}, nil)
}

// warnProductPartial is warnPartial for a product answer's meta.
func warnProductPartial(cmd *cobra.Command, m fleet.ProductMeta) {
	warnPartial(cmd, fleet.Meta{Completeness: m.Completeness, AsOf: m.AsOf, Limitations: m.Limitations})
}
