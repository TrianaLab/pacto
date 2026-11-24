package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
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

func newDeliveryCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delivery",
		Short: "Plan, propose and approve changes to what runs (needs delivery.enabled on the pacto-operator chart)",
		Long: "Pacto delivery decides which version each environment (a Stage) runs. Every change is an " +
			"Operation: plan it to see what the promoter will do and who must agree, propose it, and a person " +
			"approves only when the contracts say a consumer would break, Pacto cannot see enough or the Stage " +
			"asks for approval on every promote.\n\n" +
			"There is no approve tool for agents. `approve` is for people.",
	}
	cmd.PersistentFlags().StringP("namespace", "n", "default", "namespace of the Stage and its Operations")
	cmd.PersistentFlags().String("delivery-namespace", delivery.PolicyNamespace, "namespace Pacto delivery runs in, whose published identity policy (agent, break-glass and human groups, team prefix) says who is an agent and who may answer")
	cmd.PersistentFlags().String("trust", "", "the trust config delivery records are verified with: the recorder's PEM public key, "+
		"or a YAML file of keys and the streams each may sign (default $"+app.TrustEnv+")")
	cmd.AddCommand(
		newDeliveryTypesCommand(v),
		newDeliveryDescribeCommand(v),
		newDeliveryCandidatesCommand(v, d),
		newDeliveryStatusCommand(v, d),
		newDeliveryLifecycleCommand(v, d),
		newDeliveryPlanCommand(svc, v, d),
		newDeliveryProposeCommand(svc, v, d),
		newDeliveryGetCommand(v, d),
		newDeliveryCancelCommand(d),
		newDeliveryApproveCommand(svc, v, d),
		newDeliveryExplainCommand(v, d),
		newDeliveryHistoryCommand(v, d),
		newDeliveryBlameCommand(v, d),
		newDeliveryUndoCommand(v, d),
		newDeliveryRecordCommand(v, d),
		newDeliveryTargetsCommand(v, d),
		newDeliveryLadderCommand(v, d),
	)
	cmd.AddCommand(newDeliveryViewCommands(svc, d)...)
	return cmd
}

// deliveryClient builds the delivery client over the ambient cluster, once
// per command, with the cluster's API discovery as its scope.
func deliveryClient(cmd *cobra.Command, d deps) (*app.Delivery, string, error) {
	dyn, err := d.newDynamic()
	if err != nil {
		return nil, "", err
	}
	c, ns, err := deliveryClientFor(cmd, d, dyn)
	if err == nil && d.clusterConfig != nil {
		cfg, _ := d.clusterConfig()
		c.Scope, c.Infra = app.DiscoveryScope(cfg)
	}
	return c, ns, err
}

// trustPath is the trust config --trust, else --delivery-trust, names.
func trustPath(cmd *cobra.Command) string {
	path, _ := cmd.Flags().GetString("trust")
	if path == "" {
		path, _ = cmd.Flags().GetString("delivery-trust")
	}
	return path
}

// deliveryClientFor builds the delivery client over dyn.
func deliveryClientFor(cmd *cobra.Command, d deps, dyn dynamic.Interface) (*app.Delivery, string, error) {
	ns, _ := cmd.Flags().GetString("namespace")
	policyNS, _ := cmd.Flags().GetString("delivery-namespace")
	trust, err := app.DeliveryTrust(trustPath(cmd))
	if err != nil {
		return nil, "", err
	}
	return &app.Delivery{Dynamic: dyn, Registry: d.newDeliveryRegistry, Records: d.newRecordRepo, Trust: trust,
		Policy: delivery.DefaultClassPolicy(), PolicyNamespace: policyNS, Now: time.Now}, ns, nil
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
				t, ok = delivery.TypeByID("delivery.pacto.trianalab.io/" + args[0])
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
	cmd.Flags().String("stage", "", "the Stage to change; or name --service and --environment to request a promote on the hub's members")
	cmd.Flags().String("type", delivery.TypePromote, "operation type")
	cmd.Flags().String("to", "", "where to move: for a pointer type a version (3.0.0), a tag or an artifact digest (sha256:...), "+
		"where a version or tag must name exactly one version pacto publish published that the Stage may take (list them with pacto delivery candidates); "+
		"for "+delivery.TypeAutonomy+" a level (observe, advise, auto-nonbreaking or autonomous)")
	cmd.Flags().String("from", "", "where the Stage is now: a digest, or a level for "+delivery.TypeAutonomy+" (defaults to its pointer, or to its current level)")
	cmd.Flags().String("undoes", "", "for a rollback: the name of the operation it undoes")
	cmd.Flags().String("record", "", "for "+delivery.TypeAcknowledge+": the record id (sha256:...) of the upstream stand-in, as the Stage's Ready message names it")
	cmd.Flags().String("reason", "", "why, for the record")
	cmd.Flags().String("correlation-id", "", "an id that ties this change to an incident or another change, such as a ticket; "+
		"kept with the change and its record, filtered by pacto delivery history --correlation-id")
	cmd.Flags().String("on-behalf-of", "", "the person an agent acts for, as the Kubernetes username the cluster authenticates them as (what kubectl auth whoami prints for them, any OIDC prefix included); asserted, not verified; required when an agent proposes, never a service account or other system user, and that person may not approve the change")
	cmd.Flags().String("cause-verdict", "", "a verdict (JSON, as `pacto delivery explain --output-format json` prints it) you cite as the cause; "+
		"the promoter runs it again and refuses a cause that does not hold")
	cmd.Flags().String("service", "", "member mode: the service to promote on the members of this hub, with --environment and a sha256: --to")
	cmd.Flags().String("environment", "", "member mode: the environment to promote it in")
	cmd.Flags().StringArray("member", nil, "member mode: a member to request on, by its uid or an 8-character prefix (repeatable; default every placed member)")
	cmd.Flags().String("registry", "", "member mode: the registry the members publish to, or its pacto-targets repository")
}

func requestFrom(cmd *cobra.Command, ns string) (delivery.Request, error) {
	get := func(name string) string { s, _ := cmd.Flags().GetString(name); return s }
	typ := get("type")
	if !strings.Contains(typ, "/") {
		typ = "delivery.pacto.trianalab.io/" + typ
	}
	members, _ := cmd.Flags().GetStringArray("member")
	req := delivery.Request{Namespace: ns, Stage: get("stage"), Type: typ, To: get("to"), From: get("from"),
		Undoes: get("undoes"), Record: get("record"), Reason: get("reason"), CorrelationID: get("correlation-id"), OnBehalfOf: get("on-behalf-of"),
		Service: get("service"), Environment: get("environment"), Members: members}
	if err := delivery.CheckRequestMode(req, get("registry")); err != nil {
		return req, err
	}
	if raw := get("cause-verdict"); raw != "" {
		var v delivery.Verdict
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return req, fmt.Errorf("--cause-verdict is not a verdict: %w", err)
		}
		req.Cause = &delivery.Cause{Verdict: &v}
	}
	return req, nil
}

func newDeliveryPlanCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Predict what the promoter will do: verdicts, the approval floor, the one question for a person and what must move first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ns, _ := cmd.Flags().GetString("namespace")
			req, err := requestFrom(cmd, ns)
			if err != nil {
				return err
			}
			var plan delivery.PlanAnswer
			if req.MemberMode() {
				plan, _, err = planMembers(cmd, d, svc, req)
			} else {
				var c *app.Delivery
				if c, _, err = deliveryClient(cmd, d); err == nil {
					plan, _, err = c.Plan(cmd.Context(), req)
				}
			}
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), plan, func() error {
				if plan.Members != nil {
					printMemberPlan(cmd.OutOrStdout(), *plan.Members)
					return nil
				}
				printPlan(cmd.OutOrStdout(), plan)
				return nil
			}, nil)
		},
	}
	addRequestFlags(cmd)
	return cmd
}

// newDeliveryCandidatesCommand lists the versions a Stage may take.
func newDeliveryCandidatesCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "candidates",
		Short: "List the versions a Stage may be promoted to: what pacto publish published that its candidate source offers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			stage, _ := cmd.Flags().GetString("stage")
			list, err := c.Candidates(cmd.Context(), ns, stage)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), list, func() error {
				printCandidates(cmd.OutOrStdout(), list)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("stage", "", "the Stage (required)")
	_ = cmd.MarkFlagRequired("stage")
	return cmd
}

func printCandidates(out io.Writer, c *delivery.CandidateVersions) {
	_, _ = fmt.Fprintf(out, "stage %s/%s (%s) runs %s, candidates from the %s\n", c.Namespace, c.Stage, c.Service, orNone([]string{c.Running}), c.Source)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "VERSION\tDIGEST\tNOTE")
	for _, v := range c.Versions {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", cmp.Or(v.Version, "?"), v.Digest, v.Unreadable)
	}
	_ = w.Flush()
	if c.Note != "" {
		_, _ = fmt.Fprintf(out, "note: %s\n", c.Note)
	}
}

// printVerdict prints one verdict as a person reads it: an evidence gate's
// Pass reads Checked, as the dashboard shows it (delivery.VerdictLabel).
func printVerdict(w io.Writer, indent string, v delivery.Verdict) {
	_, _ = fmt.Fprintf(w, "%s%-17s %-8s %s\n", indent, v.Gate, delivery.VerdictLabel(v), v.Message)
}

func printPlan(w io.Writer, p delivery.PlanAnswer) {
	_, _ = fmt.Fprintf(w, "intent:    %s\n", p.IntentDigest)
	_, _ = fmt.Fprintf(w, "change:    %s %s -> %s\n", p.Type, orNone([]string{p.From}), orNone([]string{p.To}))
	_, _ = fmt.Fprintf(w, "predicted: %s\n", p.Predicted)
	printPlanApplier(w, p)
	for _, v := range p.Verdicts {
		printVerdict(w, "  ", v)
	}
	printPlanManifests(w, p)
	if p.Question != "" {
		_, _ = fmt.Fprintf(w, "question:  %s\n", p.Question)
	}
	for _, c := range p.Conflicts {
		_, _ = fmt.Fprintf(w, "conflict:  %s\n", c)
	}
	printOrder(w, p.Order)
	printPrecedent(w, p.Precedent)
	for _, n := range p.Next {
		_, _ = fmt.Fprintf(w, "next:      %s\n", n)
	}
}

// printPlanApplier names a promote's applier from the pacto.applier citation,
// keeping what the promoter observed and what the Stage declares apart.
func printPlanApplier(w io.Writer, p delivery.PlanAnswer) {
	if p.Type != delivery.TypePromote {
		return
	}
	i := slices.IndexFunc(p.Verdicts, func(v delivery.Verdict) bool { return v.Gate == delivery.GateApplier })
	if i < 0 {
		// The gate runs once pacto.published did not fail; before that, or
		// on a refused plan, the applier is not judged.
		if !slices.ContainsFunc(p.Verdicts, func(v delivery.Verdict) bool { return v.Gate == delivery.GatePublished && v.Result != delivery.Fail }) {
			_, _ = fmt.Fprintln(w, "applier:   not judged: an earlier check stopped the plan")
			return
		}
		_, _ = fmt.Fprintln(w, "applier:   not reported by the Stage status")
		return
	}
	c := p.Verdicts[i].Citation
	observed := c["observed"]
	if c["state"] != "" {
		observed += ", " + c["state"]
	}
	_, _ = fmt.Fprintf(w, "applier:   %s; declared %s\n", observed, c["declared"])
}

// printPlanManifests lists, from the pacto.manifests citation, the objects
// a promote applies, the refusal code and what the inspector refused or
// could not read;
// nothing when the gate did not run.
func printPlanManifests(w io.Writer, p delivery.PlanAnswer) {
	i := slices.IndexFunc(p.Verdicts, func(v delivery.Verdict) bool { return v.Gate == delivery.GateManifests })
	if i < 0 {
		return
	}
	c := p.Verdicts[i].Citation
	if c["objects"] != "" {
		_, _ = fmt.Fprintf(w, "objects:   %s: %s\n", c["artifact"], c["objects"])
	}
	for _, k := range []string{"code", "refused", "unread"} {
		if c[k] == "" {
			continue
		}
		for _, item := range strings.Split(c[k], "; ") {
			_, _ = fmt.Fprintf(w, "%-11s%s\n", k+":", item)
		}
	}
}

// printPrecedent says what the records attached to the version say happened
// to it before.
func printPrecedent(w io.Writer, p *delivery.Precedent) {
	if p == nil {
		return
	}
	for _, e := range p.Entries {
		result := strings.Trim(string(e.Outcome)+"/"+string(e.Verification), "/")
		_, _ = fmt.Fprintf(w, "precedent: %s seq %d %s %s %s\n", e.Stream, e.Seq, strings.TrimPrefix(e.Type, "delivery.pacto.trianalab.io/"), result, e.Reason)
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

func newDeliveryProposeCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "propose",
		Short: "Propose an operation; with --intent-digest it fails with PlanStale if anything moved since the plan or the reason or on-behalf-of differs",
		Long: "Propose an operation on a Stage of this cluster. In member mode (--service, --environment, a sha256: --to and --registry) " +
			"it plans the promote over the hub's members and, with --confirm, logs you in again in your browser and signs one request per " +
			"placed member (or per --member) keyless as you, pushed to that member's request stream; without --confirm it prints the plan and signs nothing. " +
			"Each member verifies the request, checks your grant there and raises the promote as a proposal its own gates and approvals decide.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ns, _ := cmd.Flags().GetString("namespace")
			req, err := requestFrom(cmd, ns)
			if err != nil {
				return err
			}
			if req.MemberMode() {
				return proposeMembers(cmd, v, d, svc, req)
			}
			c, _, err := deliveryClient(cmd, d)
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
				Plan      delivery.PlanAnswer       `json:"plan"`
			}{op, plan}
			return formatResult(cmd, v.GetString(outputFormatKey), out, func() error {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "proposed %s/%s\n", op.Metadata.Namespace, op.Metadata.Name)
				printPlan(cmd.OutOrStdout(), plan)
				return nil
			}, nil)
		},
	}
	addRequestFlags(cmd)
	cmd.Flags().String("intent-digest", "", "the intent digest from your plan; propose refuses if it no longer matches, so pass the same --to, --reason and --on-behalf-of")
	cmd.Flags().String("name", "", "the Operation name (generated when empty)")
	cmd.Flags().Bool("confirm", false, "member mode: sign one request per member as you, after a fresh login in your browser")
	cmd.Flags().String("issuer", "", "member mode: the approver signer's OIDC issuer, when the hub lists several")
	cmd.Flags().String("fulcio-url", "", "member mode: Fulcio to get the signing certificate from, when the hub's signer names none")
	cmd.Flags().String("timestamp-url", "", "member mode: timestamp authority to date the signature with, when the hub's signer names none")
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
			answer := delivery.AnswerOperation(op)
			return formatResult(cmd, v.GetString(outputFormatKey), answer, func() error {
				w, s := cmd.OutOrStdout(), op.Status
				_, _ = fmt.Fprintf(w, "operation: %s/%s (%s on %s)\n", op.Metadata.Namespace, op.Metadata.Name, op.Spec.Type, op.Spec.Stage)
				_, _ = fmt.Fprintf(w, "phase:     %s %s\n", s.Phase, s.Outcome)
				if s.Reason != "" || s.Message != "" {
					_, _ = fmt.Fprintf(w, "reason:    %s: %s\n", s.Reason, s.Message)
				}
				for _, v := range s.Verdicts {
					printVerdict(w, "  ", v)
				}
				if s.Question != "" {
					_, _ = fmt.Fprintf(w, "question:  %s\n", s.Question)
				}
				if answer.Next != "" {
					_, _ = fmt.Fprintf(w, "next:      %s\n", answer.Next)
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

// freshClient logs the person in again in their browser and returns a
// delivery client that acts with that login's token alone.
func freshClient(cmd *cobra.Command, v *viper.Viper, d deps, decision, ns, name string) (*app.Delivery, error) {
	issuer := v.GetString("delivery.oidc-issuer-url")
	if issuer == "" {
		return nil, errors.New("this Stage asks for a fresh login to approve: set --oidc-issuer-url (or " +
			"PACTO_DELIVERY_OIDC_ISSUER_URL) to the identity provider the cluster trusts")
	}
	cluster, err := d.clusterConfig()
	if err != nil {
		return nil, err
	}
	client, err := authn.IssuerClient(issuer, "", v.GetString("delivery.oidc-ca-file"))
	if err != nil {
		return nil, err
	}
	token, err := d.freshLogin(cmd.Context(), authn.FreshConfig{IssuerURL: issuer, HTTPClient: client,
		ClientID: v.GetString("delivery.oidc-client-id"), ClientSecret: v.GetString("delivery.oidc-client-secret"),
		ExtraScopes: v.GetStringSlice("delivery.oidc-extra-scopes"), Port: v.GetInt("delivery.oidc-redirect-port"),
		Prompt: func(u string) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "To record %s on %s/%s, log in again in your browser:\n\n  %s\n\n", decision, ns, name, u)
		}})
	if err != nil {
		return nil, err
	}
	dyn, err := d.dynamicFor(authn.RESTConfig(cluster, token))
	if err != nil {
		return nil, err
	}
	c, _, err := deliveryClientFor(cmd, d, dyn)
	return c, err
}

var deliveryDecisions = []string{string(delivery.Approve), string(delivery.Reject), string(delivery.AcceptUnknown)}

// newDeliveryApproveCommand records a person's decision with their
// kubeconfig's credentials. On a Stage that publishes a login freshness, or
// one it cannot read, it never uses them to decide, since any process in the
// person's shell (an agent included) can reuse them: it logs the person in
// again in their browser and sends the Approval with that login's token, then
// drops it.
func newDeliveryApproveCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve [<operation>]",
		Short: "Record your decision on a held operation (people only)",
		Long: "Record your decision on a held operation, as the person your kubeconfig signs you in as. " +
			"When the installation accepts signed approvals (delivery.identities.signers) and the Stage keeps an approvals " +
			"repository, the command instead logs you in again in your browser, signs the decision keyless with that login " +
			"alone and pushes the signed approval record to the registry; it never reads SIGSTORE_ID_TOKEN or a CI token. " +
			"When the promoter asks for a fresh login (delivery.promoter.authFreshness), the command instead makes " +
			"you log in again in your browser (prompt=login, max_age=0) with its own OpenID Connect client and sends " +
			"the Approval with that login's token, never your kubeconfig's: an agent running in your shell cannot " +
			"approve as you without you typing your password at that moment. The token is not stored. With --intent and " +
			"no operation name it reads no kubeconfig: it approves the operation a verified intent record names, signed keyless.",
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			decision, _ := cmd.Flags().GetString("decision")
			if !slices.Contains(deliveryDecisions, decision) {
				return fmt.Errorf("--decision must be one of %s", strings.Join(deliveryDecisions, ", "))
			}
			note, _ := cmd.Flags().GetString("note")
			intent, _ := cmd.Flags().GetString("intent")
			if (intent == "") == (len(args) == 0) {
				return errApproveOne
			}
			if intent != "" {
				return approveIntent(cmd, v, d, svc, intent, decision, note)
			}
			return approveInCluster(cmd, v, d, decision, args[0], note)
		},
	}
	cmd.Flags().String("decision", string(delivery.Approve), "approve, reject or accept-unknown")
	cmd.Flags().String("note", "", "why, for the record")
	cmd.Flags().String("oidc-issuer-url", "", "OpenID Connect issuer the cluster trusts, to log in again with (also PACTO_DELIVERY_OIDC_ISSUER_URL)")
	cmd.Flags().String("oidc-client-id", "pacto-approve", "OpenID Connect client used only for approvals, never by a kubeconfig "+
		"(a confidential client's secret comes from PACTO_DELIVERY_OIDC_CLIENT_SECRET)")
	cmd.Flags().StringSlice("oidc-extra-scopes", nil, "scopes to ask for beyond openid, profile and groups")
	cmd.Flags().Int("oidc-redirect-port", 0, "loopback port the identity provider sends the browser back to (default: a free port)")
	cmd.Flags().String("oidc-ca-file", "", "PEM bundle of the CAs that sign the issuer's certificate, trusted on top of the system roots (also PACTO_DELIVERY_OIDC_CA_FILE)")
	cmd.Flags().Bool("keyless", true, "sign the decision keyless into the Stage's approvals repository when the installation accepts signed approvals; false records it in the cluster")
	cmd.Flags().String("fulcio-url", signing.PublicFulcio, "Fulcio to get the signing certificate from, when the installation's signer names none")
	cmd.Flags().String("timestamp-url", "", "timestamp authority to date the signature with, when the installation's signer names none")
	cmd.Flags().String("intent", "", "approve the operation this intent record names (<repository>:<tag> or <repository>@<digest>) "+
		"with no kubeconfig: the intent must verify with --trust, and the decision is signed keyless under a signer --signers names")
	cmd.Flags().String("signers", "", signersFlagHelp)
	cmd.Flags().String("registry", "", "with --intent and no --trust: the registry the hub's members publish to; the intent then verifies "+
		"under the recorder key its member advertises, on that member's Stage streams only, and the hub's approver signers sign the decision unless --signers names others")
	for _, f := range []string{"oidc-issuer-url", "oidc-client-id", "oidc-extra-scopes", "oidc-redirect-port", "oidc-ca-file"} {
		_ = v.BindPFlag("delivery."+f, cmd.Flags().Lookup(f))
	}
	// A secret never goes on a command line, where any process can read it.
	_ = v.BindEnv("delivery.oidc-client-secret", "PACTO_DELIVERY_OIDC_CLIENT_SECRET")
	_ = cmd.RegisterFlagCompletionFunc("decision", staticCompletions(deliveryDecisions...))
	return cmd
}

// approveInCluster decides operation op as the person the kubeconfig signs
// in: signed keyless into the registry when the installation accepts that,
// else as an Approval in the cluster, with a fresh login when the promoter
// asks for one.
func approveInCluster(cmd *cobra.Command, v *viper.Viper, d deps, decision, op, note string) error {
	c, ns, err := deliveryClient(cmd, d)
	if err != nil {
		return err
	}
	if keyless, _ := cmd.Flags().GetBool("keyless"); keyless {
		// An operation, Stage or policy it cannot read keeps the
		// cluster path, which says what is wrong.
		_, st, policy, err := c.ApprovalTarget(cmd.Context(), ns, op)
		why := "the operation, its Stage or the identity policy could not be read"
		if err == nil {
			var signer delivery.PersonSigner
			if signer, why = approveRoute(policy, st, v.GetString("delivery.oidc-issuer-url")); why == "" {
				return approveKeyless(cmd, v, d, c, signer, st, decision, ns, op, note)
			}
		}
		if v.GetBool("verbose") {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "recording the decision in the cluster: %s\n", why)
		}
	}
	// A Stage the kubeconfig cannot read (one with no credentials at
	// all, say) gets the fresh login: the safe way when in doubt.
	freshness, err := c.Freshness(cmd.Context(), ns, op)
	if err != nil && v.GetString("delivery.oidc-issuer-url") == "" {
		return err
	}
	if err != nil || freshness > 0 {
		if c, err = freshClient(cmd, v, d, decision, ns, op); err != nil {
			return err
		}
	}
	name, err := c.Approve(cmd.Context(), ns, op, delivery.Decision(decision), note)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "recorded %s on %s/%s as Approval %s\n", decision, ns, op, name)
	return nil
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
					printExplainedVerdict(w, x)
				}
				printOrder(w, e.Order)
				printStageNow(w, e.Stage)
				return nil
			}, nil)
		},
	}
}

// printExplainedVerdict prints a verdict with its phase, its citation and
// how to run it again, as explain and record get show it.
func printExplainedVerdict(w io.Writer, x delivery.ExplainedVerdict) {
	_, _ = fmt.Fprintf(w, "  %-17s %-6s %-8s %s\n", x.Gate, x.Phase, delivery.VerdictLabel(x.Verdict), x.Message)
	printCitations(w, x.Citation, x.Rerun)
}

// printCitations prints a verdict's citation, sorted by key, then how to run
// it again. The runner's signed answer is for -o json.
func printCitations(w io.Writer, citation map[string]string, rerun string) {
	for _, k := range slices.Sorted(maps.Keys(citation)) {
		if k == "runnerBundle" {
			continue
		}
		_, _ = fmt.Fprintf(w, "      %s: %s\n", k, citation[k])
	}
	if rerun != "" {
		_, _ = fmt.Fprintf(w, "      re-run: %s\n", rerun)
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
			printVerdict(w, "  ", x)
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
			if cmd.Flags().Changed("at") {
				return historyAt(cmd, v, c, ns, stage, all)
			}
			since, until, of, err := historyFilters(cmd, all, c.Now())
			if err != nil {
				return err
			}
			f := entryFilter(cmd)
			if all {
				hs, err := c.HistoryAll(cmd.Context(), ns, of)
				if err != nil {
					return err
				}
				start, end := delivery.TimelineWindow(since, until, c.Now())
				var all []delivery.HistoryEntry
				for i := range hs.Histories {
					hs.Histories[i].History = delivery.FilterEntries(delivery.WindowEntries(hs.Histories[i].History, since, until), f)
					all = append(all, hs.Histories[i].Entries...)
				}
				hs.Timeline = new(delivery.Timeline(all, start, end))
				return formatResult(cmd, v.GetString(outputFormatKey), hs, func() error {
					out := cmd.OutOrStdout()
					for _, h := range hs.Histories {
						_, _ = fmt.Fprintf(out, "== %s/%s\n", h.Namespace, h.Stage.Name)
						printHistory(out, &h.History, hs.Revisions)
					}
					printTimeline(out, hs.Timeline)
					printAnswerNotes(out, nil, hs.Unreadable)
					_, _ = fmt.Fprintf(out, "as of %s, %s: %s\n", hs.AsOf, hs.Completeness, hs.Note)
					return nil
				}, nil)
			}
			h, err := c.HistoryWithRevisions(cmd.Context(), ns, stage)
			if err != nil {
				return err
			}
			*h.History = delivery.FilterEntries(delivery.WindowEntries(*h.History, since, until), f)
			start, end := delivery.TimelineWindow(since, until, c.Now())
			h.Timeline = new(delivery.Timeline(h.Entries, start, end))
			return formatResult(cmd, v.GetString(outputFormatKey), h, func() error {
				printHistory(cmd.OutOrStdout(), h.History, h.Revisions)
				printTimeline(cmd.OutOrStdout(), h.Timeline)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("stage", "", "the Stage (this or --all)")
	cmd.Flags().Bool("all", false, "walk every Stage in the namespace given with -n, or in every namespace you can list")
	cmd.Flags().String("since", "", "only changes at or after this RFC 3339 time, or this long before now (2h)")
	cmd.Flags().String("until", "", "only changes at or before this RFC 3339 time, or this long before now (30m)")
	cmd.Flags().String("service", "", "with --all, only the Stages of this service")
	cmd.Flags().String("domain", "", "with --service, its registry domain; a Stage that names none matches any")
	cmd.Flags().String("cause", "", "only changes whose cause (reason, operation or cited verdict) names this text, ignoring case; it narrows the timeline too")
	cmd.Flags().String("correlation-id", "", "only changes with exactly this correlation id; it narrows the timeline too")
	cmd.Flags().String("at", "", "what each Stage ran at this RFC 3339 time, or this long before now, from its signed records; with --stage or --all")
	return cmd
}

// historyAt answers history --at: which artifact each Stage ran at that
// time, from its signed records. The change filters narrow a list of
// changes, which --at does not print, so they are refused with it.
func historyAt(cmd *cobra.Command, v *viper.Viper, c *app.Delivery, ns, stage string, all bool) error {
	for _, f := range []string{"since", "until", "cause", "correlation-id"} {
		if cmd.Flags().Changed(f) {
			return fmt.Errorf("--at says what each Stage ran at one time; it takes no --%s", f)
		}
	}
	at, _ := cmd.Flags().GetString("at")
	t, err := delivery.ParseAt(at, c.Now())
	if err != nil {
		return fmt.Errorf("--at %w", err)
	}
	_, _, of, err := historyFilters(cmd, all, c.Now())
	if err != nil {
		return err
	}
	a, err := c.HistoryAt(cmd.Context(), ns, of, stage, t)
	if err != nil {
		return err
	}
	return formatResult(cmd, v.GetString(outputFormatKey), a, func() error {
		printAt(cmd.OutOrStdout(), a)
		return nil
	}, nil)
}

// printAt prints what each Stage ran at a time as a table, then why each
// Stage not known is not.
func printAt(out io.Writer, a *delivery.AtAnswer) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "STAGE\tSERVICE\tVERSION\tREVISION (DERIVED)\tOWNER\tDOMAIN\tSINCE\tSTATE")
	known, derived := 0, false
	for _, s := range a.Stages {
		_, _ = fmt.Fprintf(w, "%s/%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Namespace, s.Stage, cmp.Or(s.Service, "-"), cmp.Or(s.Version, "-"),
			cmp.Or(s.Revision, "-"), cmp.Or(s.Owner, "-"), cmp.Or(s.BusinessDomain, "-"), cmp.Or(s.Since, "-"), s.State)
		if s.State == delivery.AtKnown {
			known++
		}
		derived = derived || s.Revision != ""
	}
	_ = w.Flush()
	for _, s := range a.Stages {
		if s.Reason != "" {
			_, _ = fmt.Fprintf(out, "%s/%s is %s: %s\n", s.Namespace, s.Stage, s.State, s.Reason)
		}
	}
	if derived {
		_, _ = fmt.Fprintln(out, "revisions are derived from each artifact's contract digest and name, not checked against a fleet")
	}
	printAnswerNotes(out, nil, a.Unreadable)
	_, _ = fmt.Fprintf(out, "As of %s (from signed records; complete for %d of %d Stages); read %s, %s: %s\n",
		a.At, known, len(a.Stages), a.AsOf, a.Completeness, a.Note)
}

// entryFilter reads --cause and --correlation-id.
func entryFilter(cmd *cobra.Command) delivery.EntryFilter {
	var f delivery.EntryFilter
	f.Cause, _ = cmd.Flags().GetString("cause")
	f.CorrelationID, _ = cmd.Flags().GetString("correlation-id")
	return f
}

// historyFilters reads --since, --until, --service and --domain; a duration
// is that long before now. The service flags narrow a walk, so they need --all,
// and --domain only qualifies --service.
func historyFilters(cmd *cobra.Command, all bool, now time.Time) (since, until time.Time, of delivery.ServiceRef, err error) {
	of.Name, _ = cmd.Flags().GetString("service")
	of.Domain, _ = cmd.Flags().GetString("domain")
	switch {
	case (of.Name != "" || of.Domain != "") && !all:
		return since, until, of, errors.New("--service and --domain need --all")
	case of.Domain != "" && of.Name == "":
		return since, until, of, errors.New("--domain needs --service")
	}
	if since, err = whenFlag(cmd, "since", now); err == nil {
		if until, err = whenFlag(cmd, "until", now); err == nil {
			err = delivery.CheckWindow(since, until)
		}
	}
	return since, until, of, err
}

// whenFlag reads the time flag name: an RFC 3339 time or a duration before
// now; empty is no bound.
func whenFlag(cmd *cobra.Command, name string, now time.Time) (time.Time, error) {
	v, _ := cmd.Flags().GetString(name)
	t, err := delivery.ParseWhen(v, now)
	if err != nil {
		return t, fmt.Errorf("--%s %w", name, err)
	}
	return t, nil
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

// printHistory prints h as a table; revisions are the derived revision keys
// of the digests it names.
func printHistory(out io.Writer, h *delivery.History, revisions map[string]string) {
	printStageNow(out, h.Stage)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SEQ\tOPERATION\tTYPE\tTO\tREVISION (DERIVED)\tACTOR\tRESULT\tIMPACT\tAPPLIED\tCAUSE\tCORRELATION\tREASON\tRECORD")
	unsigned := false
	for _, e := range h.Entries {
		result := strings.Trim(string(e.Outcome)+"/"+string(e.Verification), "/")
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Seq, cmp.Or(e.Operation, "-"), cmp.Or(strings.TrimPrefix(e.Type, "delivery.pacto.trianalab.io/"), e.Kind),
			orNone([]string{e.To}), revisionCell(e, revisions), actorCell(e), cmp.Or(result, string(e.Phase)), impactCell(*h, e),
			cmp.Or(e.Applied, "-"), causeCell(e.Cause), cmp.Or(e.CorrelationID, "-"), e.Reason, recordCell(e))
		unsigned = unsigned || e.Record == ""
	}
	_ = w.Flush()
	if unsigned {
		_, _ = fmt.Fprintln(out, "lines with record - are read from the Operation's status: their cause, correlation and applied time are not signed")
	}
	if len(revisions) > 0 {
		_, _ = fmt.Fprintln(out, "revisions are derived from each artifact's contract digest and name, not checked against a fleet")
	}
	printAnswerNotes(out, h.Problems, h.Unreadable)
	_, _ = fmt.Fprintf(out, "as of %s, from %s, %s: %s\n", h.AsOf, h.Source, h.Completeness, h.Note)
}

// printTimeline says the timeline's totals in one line (B7); the slices are
// in -o json.
func printTimeline(out io.Writer, t *delivery.HistoryTimeline) {
	c := t.Totals
	_, _ = fmt.Fprintf(out, "%s to %s: %d failed, %d held, %d accepted unknown, %d released unverified, %d released, %d other, %d with the record missing",
		t.Start, t.End, c.Failed, c.Held, c.AcceptedUnknown, c.Unverified, c.Released, c.Other, c.Missing)
	if t.Untimed > 0 {
		_, _ = fmt.Fprintf(out, "; %d with no time", t.Untimed)
	}
	_, _ = fmt.Fprintln(out)
}

// shortRecord is a record id short enough for a table; "-" is a line read
// from an Operation's status.
func shortRecord(id string) string {
	if id == "" {
		return "-"
	}
	return id[:min(len(id), 19)]
}

// actorCell is who made a change, their class and how it was decided:
// user (class, source); a line with no source keeps user (class).
func actorCell(e delivery.HistoryEntry) string {
	switch {
	case e.Actor == "":
		return cmp.Or(string(e.ActorClass), "-")
	case e.ActorClass == "":
		return e.Actor
	case e.ActorClassifiedBy == "":
		return e.Actor + " (" + string(e.ActorClass) + ")"
	}
	return e.Actor + " (" + classCell(e.ActorClass, e.ActorClassifiedBy) + ")"
}

// classSourceText says how an actor was classified, in words; empty is a
// record from an earlier promoter.
var classSourceText = map[delivery.ClassSource]string{
	delivery.ClassByServiceAccount: "service account", delivery.ClassByAgentGroup: "agent group",
	delivery.ClassByOutsideHumanGroups: "outside humanGroups", delivery.ClassByBreakGlassGroup: "break-glass group",
	delivery.ClassByController: "the promoter", delivery.ClassBySystemUser: "system user", delivery.ClassByUser: "signed-in user",
}

// sourceText is classSourceText for s, or "classification not recorded".
func sourceText(s delivery.ClassSource) string {
	return cmp.Or(classSourceText[s], cmp.Or(string(s), "classification not recorded"))
}

// classCell is a class and how it was decided: "agent, service account".
func classCell(c delivery.ActorClass, s delivery.ClassSource) string {
	return string(c) + ", " + sourceText(s)
}

// approvalCell is one decision with how the approver was classified and what
// let them decide: by decision (source; may access, checked by ... at ...) note.
func approvalCell(a delivery.RecordApproval) string {
	why := sourceText(a.ClassifiedBy)
	switch {
	case a.Access != "":
		checked := map[string]string{"subject-access-review": "checked by the promoter",
			"self-subject-access-review": "checked with the caller's own credentials"}[a.AccessSource]
		why += "; may " + a.Access + ", " + cmp.Or(checked, cmp.Or(a.AccessSource, "source not recorded"))
		if a.AccessAt != "" {
			why += " at " + a.AccessAt
		}
	case a.ClassifiedBy != "":
		why += "; access not recorded"
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s (%s) %s", a.By, a.Decision, why, a.Note))
}

// causeCell is why a change was raised, in a few words: its reason, else the
// verdict it cites, else the operation that caused it.
func causeCell(c *delivery.Cause) string {
	switch {
	case c == nil:
		return "-"
	case c.Reason != "":
		return c.Reason
	case c.Verdict != nil:
		return "verdict " + c.Verdict.Gate + " " + string(c.Verdict.Result)
	case c.Operation != "":
		return "operation " + c.Operation[:min(len(c.Operation), 8)]
	}
	return "-"
}

// revisionCell is the derived revision key of the digest a line moved the
// Stage to, else of its to; "-" when none is known.
func revisionCell(e delivery.HistoryEntry, revisions map[string]string) string {
	to := e.To
	if e.Pointer != nil {
		to = e.Pointer.After
	}
	return cmp.Or(revisions[to], "-")
}

// impactCell is the pacto.impact verdict a line's record holds, marked
// unverified unless the line is a signed move; "-" when it holds none.
func impactCell(h delivery.History, e delivery.HistoryEntry) string {
	switch {
	case e.Impact == nil:
		return "-"
	case !delivery.SignedMove(h, e):
		return string(e.Impact.Result) + " (unverified)"
	}
	return string(e.Impact.Result)
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
			b, err := c.BlameWithRevision(cmd.Context(), ns, stage, digest)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), b, func() error {
				w := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(w, "version:   %s\n", orNone([]string{b.Digest}))
				if b.Revision != "" {
					_, _ = fmt.Fprintf(w, "revision (derived): %s\n", b.Revision)
				}
				if e := b.Change; e != nil {
					_, _ = fmt.Fprintf(w, "change:    seq %d %s (%s) %s\n", e.Seq, e.Operation, strings.TrimPrefix(e.Type, "delivery.pacto.trianalab.io/"), shortRecord(e.Record))
					_, _ = fmt.Fprintf(w, "asked by:  %s (%s)\n", e.Actor, classCell(e.ActorClass, e.ActorClassifiedBy))
					if b.OnBehalfOf != "" {
						_, _ = fmt.Fprintf(w, "for:       %s (asserted)\n", b.OnBehalfOf)
					}
					_, _ = fmt.Fprintf(w, "why:       %s\n", orNone([]string{b.IntentReason}))
					for _, a := range b.Approvals {
						_, _ = fmt.Fprintf(w, "decided:   %s\n", approvalCell(a))
					}
					for _, x := range b.Verdicts {
						printVerdict(w, "  ", x)
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
		Use:   "verify [<ref>]",
		Short: "Verify every record of a Stage's stream against the trust config and check the chain, or one record by its registry reference",
		Long: "Verifies every record of a Stage's stream against the trust config and checks the chain. With a record reference " +
			"(<repository>:<tag> or <repository>@<digest>) it reads no kubeconfig: it verifies that one record, an approval or " +
			"intent included, and for an approval says whether the operation record lists it (counted).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return runRecordAt(cmd, v, d, args[0], true)
			}
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
	verify.Flags().String("signers", "", signersFlagHelp)
	cmd.AddCommand(verify, newDeliveryRecordGetCommand(v, d))
	return cmd
}

func newDeliveryRecordGetCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id-or-ref>",
		Short: "Read one signed record of a Stage's stream by its id, or any record by its registry reference",
		Long: "Reads one signed record of a Stage's stream by its full id or by the short id a text history prints " +
			"(sha256: and 12 hex characters), when only one record starts with it. A record reference " +
			"(<repository>:<tag> or <repository>@<digest>) reads any record, approvals and intents included, with no kubeconfig.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isRecordRef(args[0]) {
				return runRecordAt(cmd, v, d, args[0], false)
			}
			stage, _ := cmd.Flags().GetString("stage")
			if stage == "" {
				return errors.New("pass --stage or a record reference")
			}
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			r, err := c.Record(cmd.Context(), ns, stage, args[0])
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), r, func() error {
				printRecordView(cmd.OutOrStdout(), r)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("stage", "", "the Stage whose stream holds the record (with a record id)")
	cmd.Flags().String("signers", "", signersFlagHelp)
	return cmd
}

// printRecordView prints one record: the change it records, who asked, for
// whom and why, then the verdicts as explain prints them.
func printRecordView(w io.Writer, r *delivery.RecordView) {
	e := r.Entry
	_, _ = fmt.Fprintf(w, "record %s seq %d (%s), %s\n", r.Record, e.Seq, e.Kind, r.Note)
	result := strings.TrimSuffix(string(e.Outcome)+"/"+string(e.Verification), "/")
	if e.Reason != "" {
		result += ": " + e.Reason
	}
	_, _ = fmt.Fprintf(w, "operation %s: %s %s -> %s, %s\n", e.Operation, strings.TrimPrefix(e.Type, "delivery.pacto.trianalab.io/"),
		orNone([]string{e.From}), orNone([]string{e.To}), result)
	_, _ = fmt.Fprintf(w, "asked by %s\n", actorCell(e))
	cause := ""
	if e.Cause != nil {
		cause = causeCell(e.Cause)
	}
	for _, l := range [][3]string{{"for ", r.OnBehalfOf, " (asserted)"}, {"why: ", r.IntentReason}, {"cause: ", cause}, {"correlation: ", e.CorrelationID}} {
		if l[1] != "" {
			_, _ = fmt.Fprintf(w, "%s%s%s\n", l[0], l[1], l[2])
		}
	}
	_, _ = fmt.Fprintf(w, "applied %s, finished %s\n", cmp.Or(e.Applied, "-"), cmp.Or(e.Finished, "-"))
	for _, a := range e.Approvals {
		_, _ = fmt.Fprintf(w, "decided: %s\n", approvalCell(a))
	}
	for _, x := range r.Verdicts {
		printExplainedVerdict(w, x)
	}
	_, _ = fmt.Fprintf(w, "as of %s\n", r.AsOf)
}

func printVerification(w io.Writer, res *delivery.StreamVerification) {
	_, _ = fmt.Fprintf(w, "stream %s at %s, trust config %s\n", res.Stream, res.Anchor, res.Trust)
	for _, r := range res.Records {
		unguarded := ""
		if r.Kind == delivery.RecordOperation && !r.Guarded {
			unguarded = "  (unguarded: actor and approvers not checked by admission)"
		}
		_, _ = fmt.Fprintf(w, "  seq %-4d %-13s %s %s%s\n", r.Seq, r.Kind, r.Record, r.Operation, unguarded)
		for _, rc := range r.Runners {
			if rc.OK {
				_, _ = fmt.Fprintf(w, "           %s: the exam was run and signed by %s\n", rc.Gate, rc.Runner)
			} else {
				_, _ = fmt.Fprintf(w, "           %s: the runner's signature does not verify (%s)\n", rc.Gate, rc.Problem)
			}
		}
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
