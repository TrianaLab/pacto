package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// newDeliveryRegistry is the production registry for the delivery verbs: the
// CLI's own credentials and plain-HTTP hosts, plus the hosts an OCIRepository
// marks insecure.
func newDeliveryRegistry(hosts ...string) app.DeliveryRegistry {
	return oci.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()),
		oci.WithInsecureRegistries(append(oci.EnvInsecureRegistries(), hosts...)...))
}

// newRecordRepo opens a repository to read delivery records from, with the
// CLI's own credentials and plain-HTTP hosts, plus the hosts an OCIRepository
// marks insecure.
func newRecordRepo(path string, hosts ...string) (*remote.Repository, error) {
	return ocireferrer.Open(path, ocireferrer.Options{Keychain: oci.NewKeychain(oci.EnvCredentialOptions()),
		Insecure: append(oci.EnvInsecureRegistries(), hosts...)})
}

// trustEnv names the trust config file when no flag does.
const trustEnv = "PACTO_DELIVERY_TRUST"

// deliveryTrust reads the trust config delivery records are verified with:
// the file the flag names, or $PACTO_DELIVERY_TRUST. None is nil: records are
// read unverified, and every answer says so.
func deliveryTrust(path string) (*delivery.TrustConfig, error) {
	path = cmp.Or(path, os.Getenv(trustEnv))
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the delivery trust config: %w", err)
	}
	t, err := delivery.ParseTrust(raw)
	if err == nil {
		_, err = signing.TrustVerifier(t)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
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
	cmd.PersistentFlags().String("trust", "", "the trust config delivery records are verified with: the recorder's PEM public key, "+
		"or a YAML file of keys and the streams each may sign (default $"+trustEnv+")")
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
		newDeliveryBlameCommand(v, d),
		newDeliveryUndoCommand(v, d),
		newDeliveryRecordCommand(v, d),
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
	path, _ := cmd.Flags().GetString("trust")
	if path == "" {
		path, _ = cmd.Flags().GetString("delivery-trust")
	}
	trust, err := deliveryTrust(path)
	if err != nil {
		return nil, "", err
	}
	return &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry, Records: d.newRecordRepo, Trust: trust,
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
	printPrecedent(w, p.Precedent)
}

// printPrecedent says what the records attached to the version say happened
// to it before.
func printPrecedent(w io.Writer, p *delivery.Precedent) {
	if p == nil {
		return
	}
	for _, e := range p.Entries {
		result := strings.Trim(string(e.Outcome)+"/"+string(e.Verification), "/")
		_, _ = fmt.Fprintf(w, "precedent: %s seq %d %s %s %s\n", e.Stream, e.Seq, strings.TrimPrefix(e.Type, "pacto.dev/"), result, e.Reason)
	}
	_, _ = fmt.Fprintf(w, "precedent: %s: %s\n", p.Completeness, p.Note)
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
		Short: "List what happened on a Stage, from its signed records when the recorder runs, otherwise from the cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			stage, all, err := stageOrAll(cmd, &ns)
			if err != nil {
				return err
			}
			if all {
				hs, err := c.HistoryAll(cmd.Context(), ns)
				if err != nil {
					return err
				}
				return formatResult(cmd, v.GetString(outputFormatKey), hs, func() error {
					out := cmd.OutOrStdout()
					for _, h := range hs.Histories {
						_, _ = fmt.Fprintf(out, "== %s/%s\n", h.Namespace, h.Stage.Name)
						printHistory(out, &h.History)
					}
					printAnswerNotes(out, nil, hs.Unreadable)
					_, _ = fmt.Fprintf(out, "as of %s, %s: %s\n", hs.AsOf, hs.Completeness, hs.Note)
					return nil
				}, nil)
			}
			h, err := c.History(cmd.Context(), ns, stage)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), h, func() error {
				printHistory(cmd.OutOrStdout(), h)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("stage", "", "the Stage (this or --all)")
	cmd.Flags().Bool("all", false, "walk every Stage in the namespace given with -n, or in every namespace you can list")
	return cmd
}

// stageOrAll reads --stage and --all: exactly one must be set. With --all and
// no -n, *ns becomes "", every namespace the caller can list.
func stageOrAll(cmd *cobra.Command, ns *string) (string, bool, error) {
	stage, _ := cmd.Flags().GetString("stage")
	all, _ := cmd.Flags().GetBool("all")
	if (stage == "") == !all {
		return "", false, errors.New("name one Stage with --stage, or pass --all to walk every Stage")
	}
	if all && !cmd.Flags().Changed("namespace") {
		*ns = ""
	}
	return stage, all, nil
}

func printHistory(out io.Writer, h *delivery.History) {
	printStageNow(out, h.Stage)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SEQ\tOPERATION\tTYPE\tTO\tACTOR\tRESULT\tREASON\tRECORD")
	for _, e := range h.Entries {
		result := strings.Trim(string(e.Outcome)+"/"+string(e.Verification), "/")
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Seq, cmp.Or(e.Operation, "-"), cmp.Or(strings.TrimPrefix(e.Type, "pacto.dev/"), e.Kind),
			orNone([]string{e.To}), e.ActorClass, cmp.Or(result, string(e.Phase)), e.Reason, recordCell(e))
	}
	_ = w.Flush()
	printAnswerNotes(out, h.Problems, h.Unreadable)
	_, _ = fmt.Fprintf(out, "as of %s, from %s, %s: %s\n", h.AsOf, h.Source, h.Completeness, h.Note)
}

// shortRecord is a record id short enough for a table; "-" is a line read
// from an Operation's status.
func shortRecord(id string) string {
	if id == "" {
		return "-"
	}
	return id[:min(len(id), 19)]
}

// recordCell is a history line's record: its id, "unverified" for a record
// the recorder wrote that could not be verified, or "-".
func recordCell(e delivery.HistoryEntry) string {
	if e.Record == "" && e.UnverifiedRecord != "" {
		return "unverified"
	}
	return shortRecord(e.Record)
}

func printAnswerNotes(w io.Writer, problems, unreadable []string) {
	for _, p := range problems {
		_, _ = fmt.Fprintf(w, "problem: %s\n", p)
	}
	for _, u := range unreadable {
		_, _ = fmt.Fprintf(w, "unreadable: %s\n", u)
	}
}

func newDeliveryBlameCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blame",
		Short: "Say who put a version in place on a Stage: who asked, for whom, who approved and what every gate said",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			stage, _ := cmd.Flags().GetString("stage")
			digest, _ := cmd.Flags().GetString("digest")
			b, err := c.Blame(cmd.Context(), ns, stage, digest)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), b, func() error {
				w := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(w, "version:   %s\n", orNone([]string{b.Digest}))
				if e := b.Change; e != nil {
					_, _ = fmt.Fprintf(w, "change:    seq %d %s (%s) %s\n", e.Seq, e.Operation, strings.TrimPrefix(e.Type, "pacto.dev/"), shortRecord(e.Record))
					_, _ = fmt.Fprintf(w, "asked by:  %s (%s)\n", e.Actor, e.ActorClass)
					if b.OnBehalfOf != "" {
						_, _ = fmt.Fprintf(w, "for:       %s (asserted)\n", b.OnBehalfOf)
					}
					_, _ = fmt.Fprintf(w, "why:       %s\n", orNone([]string{b.IntentReason}))
					for _, a := range b.Approvals {
						_, _ = fmt.Fprintf(w, "decided:   %s %s %s\n", a.By, a.Decision, a.Note)
					}
					for _, x := range b.Verdicts {
						_, _ = fmt.Fprintf(w, "  %-14s %-8s %s\n", x.Gate, x.Result, x.Message)
					}
				}
				printAnswerNotes(w, nil, b.Unreadable)
				_, _ = fmt.Fprintf(w, "as of %s, from %s, %s: %s\n", b.AsOf, b.Source, b.Completeness, b.Note)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("stage", "", "the Stage (required)")
	cmd.Flags().String("digest", "", "the version to blame (defaults to the one the Stage runs)")
	_ = cmd.MarkFlagRequired("stage")
	return cmd
}

func newDeliveryUndoCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "undo <operation>",
		Short: "Plan how to undo an operation; it proposes nothing and prints the command that would",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			stage, _ := cmd.Flags().GetString("stage")
			u, err := c.Undo(cmd.Context(), ns, args[0], stage)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), u, func() error {
				w := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(w, "undo %s: %s: %s\n", u.Operation, u.Kind, u.Note)
				if u.Plan != nil {
					printPlan(w, *u.Plan)
					_, _ = fmt.Fprintf(w, "nothing was proposed; to propose it:\n  %s\n", u.Command)
				}
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("stage", "", "the operation's Stage, needed only when the Operation was deleted and must be found in the records")
	return cmd
}

func newDeliveryRecordCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{Use: "record", Short: "Work with the signed records of operations"}
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Verify every record of a Stage's stream against the trust config and check the chain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			stage, all, err := stageOrAll(cmd, &ns)
			if err != nil {
				return err
			}
			if all {
				return verifyAll(cmd, v, c, ns)
			}
			res, err := c.VerifyRecords(cmd.Context(), ns, stage)
			if err != nil {
				return err
			}
			err = formatResult(cmd, v.GetString(outputFormatKey), res, func() error {
				printVerification(cmd.OutOrStdout(), res)
				return nil
			}, nil)
			if err == nil && !res.OK {
				err = fmt.Errorf("the records of %s do not verify: the problems and the records that did not verify are listed above", res.Stage)
			}
			if err == nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%d records verified, chain intact to %s\n", len(res.Records), res.Head)
			}
			return err
		},
	}
	verify.Flags().String("stage", "", "the Stage (this or --all)")
	verify.Flags().Bool("all", false, "verify every Stage's stream in the namespace given with -n, or in every namespace you can list")
	cmd.AddCommand(verify)
	return cmd
}

func printVerification(w io.Writer, res *delivery.StreamVerification) {
	_, _ = fmt.Fprintf(w, "stream %s at %s, trust config %s\n", res.Stream, res.Anchor, res.Trust)
	for _, r := range res.Records {
		_, _ = fmt.Fprintf(w, "  seq %-4d %-13s %s %s\n", r.Seq, r.Kind, r.Record, r.Operation)
	}
	printAnswerNotes(w, res.Problems, res.Unreadable)
	_, _ = fmt.Fprintf(w, "check one record without Pacto:\n  %s\n", res.Cosign)
}

// verifyAll is `record verify --all`: it fails unless every Stage was read
// and every stream verified.
func verifyAll(cmd *cobra.Command, v *viper.Viper, c *app.Delivery, ns string) error {
	res, err := c.VerifyAll(cmd.Context(), ns)
	if err != nil {
		return err
	}
	err = formatResult(cmd, v.GetString(outputFormatKey), res, func() error {
		w := cmd.OutOrStdout()
		for i := range res.Streams {
			_, _ = fmt.Fprintf(w, "== %s\n", res.Streams[i].Stage)
			printVerification(w, &res.Streams[i])
		}
		printAnswerNotes(w, nil, res.Unreadable)
		_, _ = fmt.Fprintf(w, "as of %s, %s\n", res.AsOf, res.Completeness)
		return nil
	}, nil)
	if err == nil && !res.OK {
		err = errors.New("not every Stage's records verify: the problems and what could not be read are listed above")
	}
	if err == nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%d streams verified\n", len(res.Streams))
	}
	return err
}
