package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
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
	f.String("ownership", "", "how a service's revisions declare ownership (consistent, conflicting, unowned)")
	f.String("status", "", "canonical compliance status")
	f.String("sort", "", "order: name, health (least healthy first) or services (owners with the most services first)")
	f.Int("limit", 0, fmt.Sprintf("maximum results (0 = %d, capped at %d)", fleet.DefaultEntityLimit, fleet.MaxEntityLimit))
	f.Int("offset", 0, "result offset for paging")
	_ = cmd.RegisterFlagCompletionFunc("kind", staticCompletions(fleet.ProductKinds()...))
	_ = cmd.RegisterFlagCompletionFunc("ownership", staticCompletions(fleet.OwnershipConsistent, fleet.OwnershipConflicting, fleet.OwnershipUnowned))
	_ = cmd.RegisterFlagCompletionFunc("status", staticCompletions(fleet.CanonicalStatuses()...))
	_ = cmd.RegisterFlagCompletionFunc("sort", staticCompletions(fleet.SortName, fleet.SortHealth, fleet.SortServices))
	return cmd
}

func entityFilterFromCmd(cmd *cobra.Command, text string) fleet.EntityFilter {
	get := func(name string) string { s, _ := cmd.Flags().GetString(name); return s }
	limit, _ := cmd.Flags().GetInt("limit")
	offset, _ := cmd.Flags().GetInt("offset")
	kinds := fleet.ProductKinds()
	if k := get("kind"); k != "" {
		kinds = []string{k}
	}
	var ks []fleet.EntityKind
	for _, k := range kinds {
		ks = append(ks, fleet.EntityKind(k))
	}
	return fleet.EntityFilter{
		Text: text, Kinds: ks, Owner: get("owner"), OwnerKey: get("owner-key"), BusinessDomain: get("business-domain"),
		Offering: get("offering"), Ownership: get("ownership"), Status: get("status"), Sort: get("sort"),
		Limit: limit, Offset: offset,
	}
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
			_, _ = fmt.Fprintf(w, "  %-9s %-28s %-32s %s\n", e.Kind, e.Label, e.Key, orDash(e.Status))
		}
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetEntity(cmd *cobra.Command, res *fleet.EntityDetail, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Kind: %s\nKey: %s\nLabel: %s\nStatus: %s\n", res.Entity.Kind, res.Entity.Key, res.Entity.Label, orDash(res.Status))
		_, _ = fmt.Fprintln(w, "Run with --output-format json for the full detail")
		warnProductPartial(cmd, res.Meta)
		return nil
	}, nil)
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

// warnProductPartial is warnPartial for a product answer's meta.
func warnProductPartial(cmd *cobra.Command, m fleet.ProductMeta) {
	warnPartial(cmd, fleet.Meta{Completeness: m.Completeness, AsOf: m.AsOf, Limitations: m.Limitations})
}
