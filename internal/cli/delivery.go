package cli

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// newDeliveryRegistry is the production registry for the delivery verbs: the
// CLI's own credentials and plain-HTTP hosts, plus the hosts an OCIRepository
// marks insecure.
func newDeliveryRegistry(hosts ...string) app.DeliveryRegistry {
	return oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()),
		oci.WithInsecureRegistries(append(oci.EnvInsecureRegistries(), hosts...)...))
}

func newDeliveryCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delivery",
		Short: "Plan, propose and approve changes to what runs (needs the pacto-delivery chart)",
		Long: "Pacto delivery decides which version each environment (a Stage) runs. Every change is an " +
			"Operation: plan it to see what the promoter will do and who must agree, propose it, and a person " +
			"approves only when the contracts say a consumer would break or Pacto cannot see enough.\n\n" +
			"There is no approve tool for agents. `approve` is for people, with a fresh login.",
	}
	cmd.PersistentFlags().StringP("namespace", "n", "default", "namespace of the Stage and its Operations")
	cmd.PersistentFlags().StringSlice("agent-groups", []string{"pacto:agents"}, "groups whose members are agents")
	cmd.PersistentFlags().StringSlice("break-glass-groups", []string{"pacto:break-glass"}, "groups that may override")
	cmd.AddCommand(
		newDeliveryTypesCommand(v),
		newDeliveryDescribeCommand(v),
		newDeliveryPlanCommand(v, d),
		newDeliveryProposeCommand(v, d),
		newDeliveryGetCommand(v, d),
		newDeliveryCancelCommand(d),
		newDeliveryApproveCommand(d),
		newDeliveryExplainCommand(v, d),
		newDeliveryHistoryCommand(v, d),
	)
	return cmd
}

// deliveryClient builds the delivery client over the ambient cluster.
func deliveryClient(cmd *cobra.Command, d deps) (*app.Delivery, string, error) {
	dyn, err := d.newDynamic()
	if err != nil {
		return nil, "", err
	}
	ns, _ := cmd.Flags().GetString("namespace")
	agents, _ := cmd.Flags().GetStringSlice("agent-groups")
	breakGlass, _ := cmd.Flags().GetStringSlice("break-glass-groups")
	return &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry,
		Policy: delivery.ClassPolicy{AgentGroups: agents, BreakGlassGroups: breakGlass}, Now: time.Now}, ns, nil
}

func newDeliveryTypesCommand(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "types",
		Short: "List the operation types you may propose",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			types := delivery.Types()
			return formatResult(cmd, v.GetString(outputFormatKey), types, func() error {
				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "TYPE\tFACET\tRAISERS\tDESCRIPTION")
				for _, t := range types {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Facet, joinClasses(t.Raisers), t.Description)
				}
				return w.Flush()
			}, nil)
		},
	}
}

func joinClasses(cs []delivery.ActorClass) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c)
	}
	return strings.Join(out, ",")
}

func newDeliveryDescribeCommand(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "describe <type>",
		Short: "Show what an operation type checks, when a person is needed and how it is undone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			t, ok := delivery.TypeByID(args[0])
			if !ok {
				t, ok = delivery.TypeByID("pacto.dev/" + args[0])
			}
			if !ok {
				return fmt.Errorf("unknown operation type %q; see `pacto delivery types`", args[0])
			}
			return formatResult(cmd, v.GetString(outputFormatKey), t, func() error {
				w := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(w, "type:           %s %s (%s)\n", t.ID, t.Version, t.Digest())
				_, _ = fmt.Fprintf(w, "description:    %s\nfacet:          %s\n", t.Description, t.Facet)
				_, _ = fmt.Fprintf(w, "params:         %s\nraisers:        %s\n", strings.Join(t.Params, ", "), joinClasses(t.Raisers))
				_, _ = fmt.Fprintf(w, "pre-gates:      %s\nverify gates:   %s\n", orNone(t.PreGates), orNone(t.VerifyGates))
				_, _ = fmt.Fprintf(w, "approval floor: %s\ninverse:        %s\n", t.ApprovalFloor, orNone([]string{t.Inverse}))
				_, _ = fmt.Fprintf(w, "cancellable:    %s\ndeadlines:      approval %s, execution %s\n", t.Cancellable, t.ApprovalDeadline, t.ExecutionDeadline)
				return nil
			}, nil)
		},
	}
}

func orNone(s []string) string {
	if j := strings.Join(s, ", "); j != "" {
		return j
	}
	return "none"
}

// addRequestFlags declares the flags that describe an operation.
func addRequestFlags(cmd *cobra.Command) {
	cmd.Flags().String("stage", "", "the Stage to change (required)")
	cmd.Flags().String("type", delivery.TypePromote, "operation type")
	cmd.Flags().String("to", "", "the artifact digest to move to (sha256:...)")
	cmd.Flags().String("from", "", "the digest the Stage runs now (defaults to its pointer)")
	cmd.Flags().String("undoes", "", "for a rollback: the name of the operation it undoes")
	cmd.Flags().String("reason", "", "why, for the record")
	cmd.Flags().String("on-behalf-of", "", "the person an agent acts for (asserted, not verified)")
	cmd.Flags().String("cause-verdict", "", "a verdict (JSON, as `pacto delivery explain --output-format json` prints it) you cite as the cause; "+
		"the promoter runs it again and refuses a cause that does not hold")
	_ = cmd.MarkFlagRequired("stage")
}

func requestFrom(cmd *cobra.Command, ns string) (delivery.Request, error) {
	get := func(name string) string { s, _ := cmd.Flags().GetString(name); return s }
	typ := get("type")
	if !strings.Contains(typ, "/") {
		typ = "pacto.dev/" + typ
	}
	req := delivery.Request{Namespace: ns, Stage: get("stage"), Type: typ, To: get("to"), From: get("from"),
		Undoes: get("undoes"), Reason: get("reason"), OnBehalfOf: get("on-behalf-of")}
	if raw := get("cause-verdict"); raw != "" {
		var v delivery.Verdict
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return req, fmt.Errorf("--cause-verdict is not a verdict: %w", err)
		}
		req.Cause = &delivery.Cause{Verdict: &v}
	}
	return req, nil
}

func newDeliveryPlanCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Predict what the promoter will do: verdicts, the approval floor, the one question for a person and what must move first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			req, err := requestFrom(cmd, ns)
			if err != nil {
				return err
			}
			plan, _, err := c.Plan(cmd.Context(), req)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), plan, func() error {
				printPlan(cmd.OutOrStdout(), plan)
				return nil
			}, nil)
		},
	}
	addRequestFlags(cmd)
	return cmd
}

func printPlan(w io.Writer, p delivery.Plan) {
	_, _ = fmt.Fprintf(w, "intent:    %s\n", p.IntentDigest)
	_, _ = fmt.Fprintf(w, "change:    %s %s -> %s\n", p.Type, orNone([]string{p.From}), orNone([]string{p.To}))
	_, _ = fmt.Fprintf(w, "predicted: %s\n", p.Predicted)
	for _, v := range p.Verdicts {
		_, _ = fmt.Fprintf(w, "  %-14s %-8s %s\n", v.Gate, v.Result, v.Message)
	}
	if p.Question != "" {
		_, _ = fmt.Fprintf(w, "question:  %s\n", p.Question)
	}
	for _, c := range p.Conflicts {
		_, _ = fmt.Fprintf(w, "conflict:  %s\n", c)
	}
	printOrder(w, p.Order)
}

// printOrder says what must move first, and what Pacto could not see.
func printOrder(w io.Writer, o *delivery.Order) {
	if o == nil {
		return
	}
	for _, m := range o.MoveFirst {
		_, _ = fmt.Fprintf(w, "move first: %s\n", m.Message)
	}
	for _, u := range o.Unknown {
		_, _ = fmt.Fprintf(w, "  may be incomplete: %s\n", u)
	}
}

func newDeliveryProposeCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose",
		Short: "Propose an operation; with --intent-digest it fails with PlanStale if anything moved since the plan",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			req, err := requestFrom(cmd, ns)
			if err != nil {
				return err
			}
			req.IntentDigest, _ = cmd.Flags().GetString("intent-digest")
			req.Name, _ = cmd.Flags().GetString("name")
			op, plan, err := c.Propose(cmd.Context(), req)
			if err != nil {
				return err
			}
			out := struct {
				Operation *delivery.OperationObject `json:"operation"`
				Plan      delivery.Plan             `json:"plan"`
			}{op, plan}
			return formatResult(cmd, v.GetString(outputFormatKey), out, func() error {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "proposed %s/%s\n", op.Metadata.Namespace, op.Metadata.Name)
				printPlan(cmd.OutOrStdout(), plan)
				return nil
			}, nil)
		},
	}
	addRequestFlags(cmd)
	cmd.Flags().String("intent-digest", "", "the intent digest from your plan; propose refuses if it no longer matches")
	cmd.Flags().String("name", "", "the Operation name (generated when empty)")
	return cmd
}

func newDeliveryGetCommand(v *viper.Viper, d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "get <operation>",
		Short: "Show an operation: phase, outcome, verdicts and the question a person is asked",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			op, err := c.Get(cmd.Context(), ns, args[0])
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), op, func() error {
				w, s := cmd.OutOrStdout(), op.Status
				_, _ = fmt.Fprintf(w, "operation: %s/%s (%s on %s)\n", op.Metadata.Namespace, op.Metadata.Name, op.Spec.Type, op.Spec.Stage)
				_, _ = fmt.Fprintf(w, "phase:     %s %s\n", s.Phase, s.Outcome)
				if s.Reason != "" || s.Message != "" {
					_, _ = fmt.Fprintf(w, "reason:    %s: %s\n", s.Reason, s.Message)
				}
				for _, v := range s.Verdicts {
					_, _ = fmt.Fprintf(w, "  %-14s %-8s %s\n", v.Gate, v.Result, v.Message)
				}
				if s.Question != "" {
					_, _ = fmt.Fprintf(w, "question:  %s\n", s.Question)
				}
				return nil
			}, nil)
		},
	}
}

func newDeliveryCancelCommand(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <operation>",
		Short: "Cancel an operation that has not moved the pointer yet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			if err := c.Cancel(cmd.Context(), ns, args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cancel requested for %s/%s\n", ns, args[0])
			return nil
		},
	}
}

var deliveryDecisions = []string{string(delivery.Approve), string(delivery.Reject), string(delivery.AcceptUnknown)}

func newDeliveryApproveCommand(d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve <operation>",
		Short: "Record your decision on a held operation (people only, with a fresh login)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			decision, _ := cmd.Flags().GetString("decision")
			if !slices.Contains(deliveryDecisions, decision) {
				return fmt.Errorf("--decision must be one of %s", strings.Join(deliveryDecisions, ", "))
			}
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			note, _ := cmd.Flags().GetString("note")
			name, err := c.Approve(cmd.Context(), ns, args[0], delivery.Decision(decision), note)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "recorded %s on %s/%s as Approval %s\n", decision, ns, args[0], name)
			return nil
		},
	}
	cmd.Flags().String("decision", string(delivery.Approve), "approve, reject or accept-unknown")
	cmd.Flags().String("note", "", "why, for the record")
	_ = cmd.RegisterFlagCompletionFunc("decision", staticCompletions(deliveryDecisions...))
	return cmd
}

func newDeliveryExplainCommand(v *viper.Viper, d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "explain <operation>",
		Short: "Explain an operation: where it is, every verdict with its citation and how to run it again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			e, err := c.Explain(cmd.Context(), ns, args[0])
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), e, func() error {
				w := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(w, "%s\n%s\n", e.Operation, e.Summary)
				for _, x := range e.Verdicts {
					_, _ = fmt.Fprintf(w, "  %-14s %-6s %-8s %s\n", x.Gate, x.Phase, x.Result, x.Message)
					for _, k := range slices.Sorted(maps.Keys(x.Citation)) {
						_, _ = fmt.Fprintf(w, "      %s: %s\n", k, x.Citation[k])
					}
					_, _ = fmt.Fprintf(w, "      re-run: %s\n", x.Rerun)
				}
				printOrder(w, e.Order)
				printStageNow(w, e.Stage)
				return nil
			}, nil)
		},
	}
}

func printStageNow(w io.Writer, s delivery.StageNow) {
	_, _ = fmt.Fprintf(w, "stage %s runs %s", s.Name, orNone([]string{s.Pointer}))
	if s.Frozen {
		_, _ = fmt.Fprintf(w, ", frozen: %s", s.FrozenReason)
	}
	if s.AutoHold {
		_, _ = fmt.Fprint(w, ", on auto-hold")
	}
	_, _ = fmt.Fprintln(w)
	if lc := s.LastCheck; lc != nil {
		_, _ = fmt.Fprintf(w, "last re-check %s of %s:\n", lc.At, lc.Digest)
		for _, x := range lc.Verdicts {
			_, _ = fmt.Fprintf(w, "  %-14s %-8s %s\n", x.Gate, x.Result, x.Message)
		}
	}
}

func newDeliveryHistoryCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List what happened on a Stage, from the operations still in the cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			stage, _ := cmd.Flags().GetString("stage")
			h, err := c.History(cmd.Context(), ns, stage)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), h, func() error {
				out := cmd.OutOrStdout()
				printStageNow(out, h.Stage)
				w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "SEQ\tOPERATION\tTYPE\tTO\tACTOR\tRESULT\tREASON")
				for _, e := range h.Entries {
					result := strings.Trim(string(e.Outcome)+"/"+string(e.Verification), "/")
					_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Seq, e.Operation, strings.TrimPrefix(e.Type, "pacto.dev/"),
						orNone([]string{e.To}), e.ActorClass, cmp.Or(result, string(e.Phase)), e.Reason)
				}
				_ = w.Flush()
				_, _ = fmt.Fprintf(out, "as of %s, %s: %s\n", h.AsOf, h.Completeness, h.Note)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("stage", "", "the Stage (required)")
	_ = cmd.MarkFlagRequired("stage")
	return cmd
}
