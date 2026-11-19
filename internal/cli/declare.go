package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// planDeclare is app's PlanDeclare as a deps function: the CLI reads only
// the plan.
func planDeclare(svc *app.Service, ctx context.Context, req app.DeclareRequest) (*app.DeclarePlan, error) {
	p, _, err := svc.PlanDeclare(ctx, req)
	return p, err
}

func newDeclareCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	var req app.DeclareRequest
	var yes bool
	cmd := &cobra.Command{
		Use:   "declare <service>",
		Short: "Declare a running service: publish its contract as a version you sign",
		Long: `Declares a workload the cluster runs as a service with a contract. The
workload is name, namespace/name, namespace/Kind/name or k8s:namespace/Kind/name.
Its contract is drafted from the cluster with your kubeconfig credentials, as
pacto init --cluster does, or read from --dir, a folder pacto init --cluster
wrote (its cluster facts are read again).

Without --yes it prints the plan and writes nothing: the version (0.1.0 for a
first declaration, else the published baseline raised by the change), contract
only with the baseline's deploy artifact reused or none, every dependency range
and who signs. Proposed ranges stay unknown unless you name the dependency with
--accept. The plan ends with its intent digest.

--yes prints the plan, then publishes it: a contract-only bundle signed keyless
by you through an interactive Sigstore login (a browser, or a device code with
--device). Nothing is built, no deploy tag is written and no Git access is
needed. Only that login writes a declared version: a key, an ambient OIDC token
(SIGSTORE_ID_TOKEN included) or a CI identity writes the next draft-<n> tag
instead, and the output names the command a person runs to accept it with
--from-draft. --intent refuses the run when the plan differs from the one you
reviewed.

A declaration never sets an owner its published baseline does not declare.`,
		Example: `  # What declaring invoices would publish
  pacto declare billing/invoices --registry ghcr.io/acme

  # Accept the proposed range of ledger and publish, signed by you
  pacto declare billing/invoices --registry ghcr.io/acme --accept ledger --yes

  # Accept a draft an agent wrote
  pacto declare invoices --registry ghcr.io/acme --from-draft draft-3 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := declareSubject(&req, args[0]); err != nil {
				return err
			}
			dyn, err := declareDynamic(req)
			if err != nil {
				return err
			}
			req.Dynamic = dyn
			asJSON := v.GetString(outputFormatKey) == "json"
			req.Signing.Prompt = func(s string) { _, _ = fmt.Fprintln(cmd.ErrOrStderr(), s) }
			if !yes {
				p, err := d.planDeclare(svc, cmd.Context(), req)
				return printDeclare(cmd, asJSON, p, nil, err)
			}
			if !asJSON {
				req.OnPlan = func(p *app.DeclarePlan) { printDeclarePlan(cmd.OutOrStdout(), p) }
			}
			res, err := d.declare(svc, cmd.Context(), req)
			return printDeclare(cmd, asJSON, nil, res, err)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&req.Namespace, "namespace", "n", "", "the namespace of the workload (default: every namespace the name is found in)")
	f.StringVar(&req.Dir, "dir", "", "a folder pacto init --cluster wrote, declared instead of a new draft")
	f.StringVar(&req.Registry, "registry", "", "the registry the contract is published to, as host/path")
	f.StringSliceVar(&req.Accept, "accept", nil, "a dependency whose proposed range you accept as declared; repeat it for more")
	f.BoolVar(&yes, "yes", false, "publish the plan (or write a draft) after printing it")
	f.StringVar(&req.IntentDigest, "intent", "", "the intent digest of the plan you reviewed: a different plan is refused")
	f.StringVar(&req.FromDraft, "from-draft", "", "accept this draft-<n> tag: the workload is read again and you sign")
	f.IntVar(&req.MaxItems, "max-items", 0, "the most items read per kind of object; a kind cut short reads as partial (default 10000)")
	f.DurationVar(&req.CallWindow, "call-window", 0, "how far back a call counts as a dependency (at least 1m, default 24h)")
	f.StringVar(&req.Signing.Key, "sign-key", "", "sign with this key file: a key names no person, so the run writes a draft")
	f.StringVar(&req.Signing.OIDCIssuer, "oidc-issuer-url", "https://oauth2.sigstore.dev/auth", "OpenID Connect issuer of the Sigstore login")
	f.StringVar(&req.Signing.OIDCClient, "oidc-client-id", "sigstore", "OpenID Connect client of the Sigstore login")
	f.BoolVar(&req.Signing.Device, "device", false, "log in with a device code instead of a browser")
	f.StringVar(&req.Signing.FulcioURL, "fulcio-url", signing.PublicFulcio, "Fulcio to get the signing certificate from")
	f.StringVar(&req.Signing.TimestampURL, "timestamp-url", "", "timestamp authority to date the signature with")
	f.StringVar(&req.Signing.RekorURL, "rekor-url", signing.PublicRekor, "Rekor transparency log the signature is recorded in")
	f.StringVar(&req.Signing.TrustedRoot, "trusted-root", "", "Sigstore trusted root JSON to verify with (default: public-good Sigstore)")
	return cmd
}

// declareSubject reads the workload argument into req: name, ns/name,
// ns/Kind/name or k8s:ns/Kind/name. Any other <kind>: subject is refused.
func declareSubject(req *app.DeclareRequest, arg string) error {
	if kind, rest, ok := strings.Cut(arg, ":"); ok {
		if kind != "k8s" {
			return fmt.Errorf("pacto declare reads a workload, not %s: offerings and templates are declared by a later release", kind+":")
		}
		arg = rest
	}
	req.Service = arg
	return nil
}

// declareDynamic is the kubeconfig client the declaration reads the cluster
// with. A run without a registry is refused before any read, so it builds
// none; a --dir run whose client fails declares the folder with its cluster
// facts dropped.
func declareDynamic(req app.DeclareRequest) (dynamic.Interface, error) {
	if req.Registry == "" {
		return nil, nil
	}
	dyn, err := initDynamic()
	if err != nil && req.Dir == "" {
		return nil, fmt.Errorf("declaring a workload reads the cluster: %w", err)
	}
	if err != nil {
		return nil, nil
	}
	return dyn, nil
}

// printDeclare prints a plan (p) or what a run did (res), then returns err:
// a refusal prints its message and the command a person runs about it, and
// exits 1 whatever its code.
func printDeclare(cmd *cobra.Command, json bool, p *app.DeclarePlan, res *app.DeclareResult, err error) error {
	var de *app.DeclareError
	if errors.As(err, &de) && de.CLI != "" {
		err = &declareRefusal{de}
	}
	if json {
		return printDeclareJSON(cmd, p, res, err)
	}
	w := cmd.OutOrStdout()
	switch {
	case p != nil && p.Service != "":
		printDeclarePlan(w, p)
		if err == nil && p.Unchanged == "" {
			_, _ = fmt.Fprintf(w, "nothing was written: run again with --yes --intent %s to carry out this plan\n", p.IntentDigest)
		}
	case res != nil:
		printDeclareResult(w, res)
	}
	return err
}

// declareRefusal is a declare refusal as the CLI prints it.
type declareRefusal struct{ *app.DeclareError }

func (r *declareRefusal) Error() string { return r.Message + "\nrun: " + r.CLI }

func (r *declareRefusal) Unwrap() error { return r.DeclareError }

// printDeclareJSON prints the plan or the result as one document; a refusal
// adds its code, message and command, and the exit.
func printDeclareJSON(cmd *cobra.Command, p *app.DeclarePlan, res *app.DeclareResult, err error) error {
	var refusal *app.DeclareError
	errors.As(err, &refusal)
	if res != nil {
		return printJSONExit(cmd, struct {
			*app.DeclareResult
			Refusal *app.DeclareError `json:"refusal,omitempty"`
		}{res, refusal}, err)
	}
	return printJSONExit(cmd, struct {
		*app.DeclarePlan
		Refusal *app.DeclareError `json:"refusal,omitempty"`
	}{p, refusal}, err)
}

// printDeclarePlan prints the plan block: what is published where, every
// range, who signs, where it was inferred from, then what the plan skipped,
// cannot back or notes, the cluster read's coverage and the policy note.
func printDeclarePlan(w io.Writer, p *app.DeclarePlan) {
	if p.Unchanged != "" {
		_, _ = fmt.Fprintf(w, "plan: %s unchanged since %s: nothing to publish\n", p.Service, p.Unchanged)
		return
	}
	from := "first declaration"
	if p.Baseline != nil {
		from = "from " + p.Baseline.Version
	}
	_, _ = fmt.Fprintf(w, "plan: oci://%s:%s (%s, contract only, deploy: %s)\n", p.BundleRepo, p.Version, from, p.Deploy)
	for _, r := range p.Ranges {
		_, _ = fmt.Fprintf(w, "  dependencies.%s.compatibility: %s\n", r.Dependency, rangeText(r))
	}
	_, _ = fmt.Fprintf(w, "state: %s, signed by %s\n", p.State, signerLine(p.Signer))
	if p.InferredFrom != "" {
		cluster := ""
		if p.Cluster != "" {
			cluster = " (cluster " + p.Cluster + ")"
		}
		_, _ = fmt.Fprintf(w, "inferredFrom %s%s\n", p.InferredFrom, cluster)
	}
	for _, s := range p.Skipped {
		_, _ = fmt.Fprintf(w, "skipped %s, signed by %s: %s\n", s.Tag, s.Signer, s.Why)
	}
	for _, u := range p.Unbacked {
		_, _ = fmt.Fprintf(w, "not backed by the cluster read or the baseline: %s\n", u)
	}
	for _, n := range p.Notes {
		_, _ = fmt.Fprintln(w, "note: "+n)
	}
	if n := len(p.Coverage); n > 0 {
		_, _ = fmt.Fprintf(w, "Cluster read: %d of %d sources complete\n", complete(p.Coverage), n)
	}
	if p.PolicyNote != "" {
		_, _ = fmt.Fprintln(w, p.PolicyNote)
	}
	_, _ = fmt.Fprintln(w, "intent: "+p.IntentDigest)
}

// rangeText is one dependency's declared range, what proposed it and
// whether this run accepted it.
func rangeText(r app.DeclaredRange) string {
	var why []string
	if r.Accepted {
		why = append(why, "accepted by you")
	} else if r.Proposed != "" {
		why = append(why, "proposed "+r.Proposed)
	}
	if r.Observed != "" {
		why = append(why, "observed "+r.Observed)
	}
	if len(why) == 0 {
		return r.Compatibility
	}
	return r.Compatibility + " (" + strings.Join(why, "; ") + ")"
}

// signerLine names who signs a plan, and why it writes what it writes.
func signerLine(s app.SignerView) string {
	who := s.Subject
	switch {
	case s.Source == "login" && who == "":
		who = "you, after a Sigstore login at " + s.Issuer
	case s.Source == "none":
		who = "nobody (an unsigned draft)"
	case who == "":
		who = "a " + s.Source + " that names no person"
	default:
		who += " via " + s.Issuer
	}
	if s.Why != "" {
		who += " (" + s.Why + ")"
	}
	return who
}

// complete counts the coverage rows read in full.
func complete(rows []contract.InferredCoverage) int {
	n := 0
	for _, r := range rows {
		if r.State == contract.CoverageComplete {
			n++
		}
	}
	return n
}

// printDeclareResult prints what a run wrote, after the plan its OnPlan
// printed.
func printDeclareResult(w io.Writer, res *app.DeclareResult) {
	p := res.Plan
	switch {
	case p.Unchanged != "":
		printDeclarePlan(w, &p)
	case !res.Executed:
		return
	case res.Draft:
		_, _ = fmt.Fprintf(w, "wrote %s (%s): a person accepts it with %s\n", res.Tag, res.Published, p.AcceptCLI)
	default:
		_, _ = fmt.Fprintf(w, "signed as %s via %s\n", p.Signer.Subject, p.Signer.Issuer)
		_, _ = fmt.Fprintf(w, "published %s (inferredFrom %s)\n", res.Published, p.InferredFrom)
	}
}
