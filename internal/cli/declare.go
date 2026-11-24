package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/gitexport"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

Setting, changing or removing service.owner (in a --dir folder or an
accepted draft) takes a person who belongs to the current owner, declared or
attested, or a default approver of the installation's owner policy; anyone
else is refused before anything is pushed. The owner policy is read from
the signed target snapshots in <registry>/pacto-targets, verified with
--delivery-trust. An owner read from the workload's team label stays in
pacto.inferred.yaml as inferred.`,
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
			if req.Draft && req.FromDraft != "" {
				return errDraftAndFromDraft
			}
			dyn, err := declareDynamic(req)
			if err != nil {
				return err
			}
			req.Dynamic = dyn
			return declareRun(cmd, svc, v, d, req, yes)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&req.Namespace, "namespace", "n", "", "the namespace of the workload (default: every namespace the name is found in)")
	f.StringVar(&req.Dir, "dir", "", "a folder pacto init --cluster wrote, declared instead of a new draft")
	f.StringSliceVar(&req.Accept, "accept", nil, "a dependency whose proposed range you accept as declared; repeat it for more")
	f.IntVar(&req.MaxItems, "max-items", 0, "the most items read per kind of object; a kind cut short reads as partial (default 10000)")
	f.DurationVar(&req.CallWindow, "call-window", 0, "how far back a call counts as a dependency (at least 1m, default 24h)")
	f.BoolVar(&req.PR, "pr", false, "open a pull request with your own Git token after the bundle is written; needs --yes to write")
	f.StringVar(&req.Repository, "repository", "", "the service's source repository URL, used over every source Pacto reads; confirms an image label for --pr")
	f.StringVar(&req.Folder, "folder", "", "the folder of the repository the contract lives in (default: the folder a source names, else the root)")
	f.StringVar(&req.RepoMap, "repo-map", "", "a YAML file mapping service names to {repository, folder}; read only with --pr")
	f.StringVar(&req.GitHost, "git-host", "", "the Git host API that serves the repository, when its URL does not say: github, gitlab, bitbucket (Cloud) or gitea (Forgejo too)")
	declareFlags(f, &req, &yes)
	return cmd
}

// errDraftAndFromDraft refuses a run that would both write and accept a
// draft.
var errDraftAndFromDraft = errors.New("--draft writes a new draft and --from-draft accepts one: pass only one of them")

// declareRun plans req, or with yes carries it out, and prints the answer.
func declareRun(cmd *cobra.Command, svc *app.Service, v *viper.Viper, d deps, req app.DeclareRequest, yes bool) error {
	asJSON := v.GetString(outputFormatKey) == "json"
	req.Signing.Prompt = func(s string) { _, _ = fmt.Fprintln(cmd.ErrOrStderr(), s) }
	req.Signing.FreshLogin = d.freshLogin
	pr := &exportPR{d: d, req: req, gitHost: req.GitHost}
	if !yes {
		p, err := d.planDeclare(svc, cmd.Context(), req)
		if err == nil && req.PR {
			err = pr.resolve(cmd.Context(), p)
		}
		return printDeclare(cmd, asJSON, p, nil, err)
	}
	if !asJSON || req.PR {
		req.OnPlan = func(p *app.DeclarePlan) error {
			if req.PR {
				if err := pr.resolve(cmd.Context(), p); err != nil {
					return err
				}
			}
			if !asJSON {
				printDeclarePlan(cmd.OutOrStdout(), p)
			}
			return nil
		}
	}
	res, err := d.declare(svc, cmd.Context(), req)
	if err == nil && req.PR {
		err = pr.export(cmd.Context(), res)
	}
	return printDeclare(cmd, asJSON, nil, res, err)
}

// declareFlags registers the flags pacto declare and pacto claim share:
// where the contract is published, who signs it and how a draft is written
// or accepted.
func declareFlags(f *pflag.FlagSet, req *app.DeclareRequest, yes *bool) {
	f.StringVar(&req.Registry, "registry", "", "the registry the contract is published to, as host/path")
	f.BoolVar(yes, "yes", false, "publish the plan (or write a draft) after printing it")
	f.StringVar(&req.IntentDigest, "intent", "", "the intent digest of the plan you reviewed: a different plan is refused")
	f.BoolVar(&req.Draft, "draft", false, "write a draft-<n> a person accepts later, without a person's login: signed only by --sign-key or an ambient OIDC token when one is set")
	f.StringVar(&req.FromDraft, "from-draft", "", "accept this draft-<n> tag, signed by you (a workload draft reads the workload again)")
	f.StringVar(&req.Signing.Key, "sign-key", "", "sign with this key file: a key names no person, so the run writes a draft")
	f.StringVar(&req.Signing.OIDCIssuer, "oidc-issuer-url", "https://oauth2.sigstore.dev/auth", "OpenID Connect issuer of the Sigstore login")
	f.StringVar(&req.Signing.OIDCClient, "oidc-client-id", "sigstore", "OpenID Connect client of the Sigstore login")
	f.BoolVar(&req.Signing.Device, "device", false, "log in with a device code instead of a browser")
	f.StringVar(&req.Signing.FulcioURL, "fulcio-url", signing.PublicFulcio, "Fulcio to get the signing certificate from")
	f.StringVar(&req.Signing.TimestampURL, "timestamp-url", "", "timestamp authority to date the signature with")
	f.StringVar(&req.Signing.RekorURL, "rekor-url", signing.PublicRekor, "Rekor transparency log the signature is recorded in")
	f.StringVar(&req.Signing.TrustedRoot, "trusted-root", "", "Sigstore trusted root JSON to verify with (default: public-good Sigstore)")
	f.StringVar(&req.DeliveryTrust, "delivery-trust", "", "the trust config the target snapshots that carry the owner policy are verified with: "+
		"a YAML file with a key scoped to each cluster (default $"+app.TrustEnv+")")
}

// declareSubject reads the workload argument into req: name, ns/name,
// ns/Kind/name or k8s:ns/Kind/name. An external system is claimed with
// pacto claim; any other <kind>: subject is refused.
func declareSubject(req *app.DeclareRequest, arg string) error {
	if kind, rest, ok := strings.Cut(arg, ":"); ok {
		if kind+":" == fleet.ExternalSubjectPrefix {
			return errors.New("claim an external system with pacto claim " + rest + " --owner <team>")
		}
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
// none; a --dir run or a claim whose client fails declares with its cluster
// facts dropped (a claim reads only the installation signer policy there).
func declareDynamic(req app.DeclareRequest) (dynamic.Interface, error) {
	if req.Registry == "" {
		return nil, nil
	}
	dyn, err := initDynamic()
	if err != nil && req.Dir == "" && !strings.HasPrefix(req.Service, fleet.ExternalSubjectPrefix) {
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

func (r *declareRefusal) Error() string {
	msg := r.Message
	if slices.Contains(delivery.OwnerRightCodes, r.Code) {
		msg = "refused: " + msg
	}
	return msg + "\nrun: " + r.CLI
}

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
	var pr *app.PRPlan
	if p != nil {
		pr = p.PullRequest
	}
	return printJSONExit(cmd, struct {
		*app.DeclarePlan
		PullRequest *app.PRPlan       `json:"pullRequest,omitempty"`
		Refusal     *app.DeclareError `json:"refusal,omitempty"`
	}{p, pr, refusal}, err)
}

// printDeclarePlan prints the plan block: what is published where, every
// range, who signs, where it was inferred from, then what the plan skipped,
// cannot back or notes, the cluster read's coverage and the policy note.
func printDeclarePlan(w io.Writer, p *app.DeclarePlan) {
	if p.Unchanged != "" {
		_, _ = fmt.Fprintf(w, "plan: %s unchanged since %s: nothing to publish\n", p.Service, p.Unchanged)
		printPullRequestPlan(w, p.PullRequest)
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
	printDeclareRepository(w, p)
	printPullRequestPlan(w, p.PullRequest)
	for _, s := range p.Skipped {
		_, _ = fmt.Fprintf(w, "skipped %s, signed by %s: %s\n", s.Tag, s.Signer, s.Why)
	}
	for _, u := range p.Unbacked {
		_, _ = fmt.Fprintf(w, "not backed by the cluster read or the baseline: %s\n", u)
	}
	if p.UnbackedUnchecked != "" {
		_, _ = fmt.Fprintf(w, "unbacked changes unknown: nothing was checked against the cluster and the baseline (%s)\n", p.UnbackedUnchecked)
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
	printDeclareDrafts(w, p)
	_, _ = fmt.Fprintln(w, "intent: "+p.IntentDigest)
}

// printPullRequestPlan prints the pull request --pr would open.
func printPullRequestPlan(w io.Writer, r *app.PRPlan) {
	if r == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "pull request: %s %s, branch %s, files %s and %s, with %s\n", r.Kind, r.URL, r.Branch,
		path.Join(r.Folder, app.DefaultContractPath), path.Join(r.Folder, contract.InferredRecordFile), r.TokenSource)
}

// printDeclareRepository prints where the service's source lives and how
// it was found, or that it is unknown and why each rung gave nothing.
func printDeclareRepository(w io.Writer, p *app.DeclarePlan) {
	if p.Repository != nil {
		_, _ = fmt.Fprintln(w, "repository: "+app.RepositoryText(p.Repository))
		return
	}
	if len(p.RepositoryTried) > 0 {
		_, _ = fmt.Fprintln(w, "repository: unknown")
	}
	for _, n := range p.RepositoryTried {
		_, _ = fmt.Fprintf(w, "  tried %s: %s\n", n.Rung, n.Reason)
	}
}

// printDeclareDrafts prints the drafts waiting in the bundle repository, one
// line each: who asked, for whom and what it requests.
func printDeclareDrafts(w io.Writer, p *app.DeclarePlan) {
	if p.DraftsTotal == 0 {
		return
	}
	shown := ""
	if p.DraftsTotal > len(p.Drafts) {
		shown = fmt.Sprintf(" (newest %d of %d)", len(p.Drafts), p.DraftsTotal)
	}
	_, _ = fmt.Fprintf(w, "Drafts%s:\n", shown)
	for _, d := range p.Drafts {
		_, _ = fmt.Fprintln(w, "  "+draftLine(d))
	}
}

// draftLine is one draft as the plan lists it.
func draftLine(d app.DraftView) string {
	if d.State == "unreadable" {
		return d.Tag + " unreadable: " + d.Reason
	}
	// Only a signature proves who asked: an unsigned record's asker is a claim.
	by := "an agent"
	switch {
	case d.Signed && d.Asker.Subject != "":
		by = d.Asker.Subject
	case d.Asker.Class == contract.SignerClassHuman:
		by = "a person"
	}
	line := d.Tag + " by " + by
	if d.OnBehalfOf != "" {
		line += " for " + d.OnBehalfOf
	}
	if len(d.Requested) > 0 {
		line += ", requests " + strings.Join(d.Requested, ", ")
	}
	if !d.Signed {
		line += " (unsigned)"
	}
	if d.Reason != "" {
		line += " (" + d.Reason + ")"
	}
	return line
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
	id, claim := contract.InferredFromExternal(p.InferredFrom)
	switch {
	case p.Unchanged != "":
		printDeclarePlan(w, &p)
	case !res.Executed:
		return
	case claim:
		printClaimResult(w, res, id)
	case res.Draft:
		_, _ = fmt.Fprintf(w, "wrote %s (%s): a person accepts it with %s\n", res.Tag, res.Published, p.AcceptCLI)
	default:
		_, _ = fmt.Fprintf(w, "signed as %s via %s\n", p.Signer.Subject, p.Signer.Issuer)
		_, _ = fmt.Fprintf(w, "published %s (inferredFrom %s)\n", res.Published, p.InferredFrom)
	}
	if r := res.PullRequest; r != nil {
		_, _ = fmt.Fprintf(w, "opened %s as %s (%s)\n", r.URL, r.Login, r.TokenSource)
	}
}

// exportPR opens the pull request of a declare --pr run with the person's
// own Git token: the host kind and the token are resolved before anything
// is written, and the files committed are the exact ones written.
type exportPR struct {
	d       deps
	req     app.DeclareRequest
	gitHost string
	kind    gitexport.Kind
	token   string
}

// resolve detects p's Git host and finds the person's token for it,
// refusing the run when either is unknown. It makes no request to the host.
func (x *exportPR) resolve(ctx context.Context, p *app.DeclarePlan) error {
	r := p.Repository // ponytail: a --pr plan without a repository is refused (RepositoryUnknown)
	kind, e := gitexport.Detect(r.URL, x.gitHost, os.Getenv)
	if e != nil {
		return &app.DeclareError{Code: e.Code, Message: e.Message}
	}
	fill := x.d.gitCredentialFill
	if fill == nil {
		fill = gitexport.CredentialFill
	}
	token, source, e := gitexport.Token(ctx, kind, r.URL, os.Getenv, fill)
	if e != nil {
		return &app.DeclareError{Code: e.Code, Message: e.Message}
	}
	tag := p.Version
	if p.State == contract.DeclarationDraft {
		tag = "<next draft tag>"
	}
	x.kind, x.token = kind, token
	p.PullRequest = &app.PRPlan{Kind: string(kind), URL: r.URL, Folder: r.Folder, Branch: gitexport.Branch(p.Service, tag), TokenSource: source}
	return nil
}

// export opens the pull request after res was written, or for an unchanged
// plan with the baseline bundle's own files. A failure keeps what was
// published and says how to retry.
func (x *exportPR) export(ctx context.Context, res *app.DeclareResult) error {
	p := &res.Plan
	if p.PullRequest == nil {
		// An unchanged plan wrote nothing and never reached OnPlan.
		if err := x.resolve(ctx, p); err != nil {
			return err
		}
	}
	dec := gitexport.Declaration{Service: p.Service, Tag: res.Tag, Bundle: res.Published, State: contract.DeclarationDeclared, Signer: p.Signer.Subject,
		Rung: p.Repository.Rung}
	what := "published " + res.Published
	switch {
	case !res.Executed:
		// The baseline's own state, and the identity it counted by.
		dec.Tag, dec.Bundle, dec.Signer = p.Unchanged, p.Baseline.Bundle, p.Baseline.Signer
		dec.State = cmp.Or(p.Baseline.State, "published")
		what = p.Service + " is unchanged since " + p.Unchanged
	case res.Draft:
		dec.State, dec.OnBehalfOf = contract.DeclarationDraft, x.req.OnBehalfOf
		if !res.Signed {
			dec.Signer = ""
		}
	}
	r := p.Repository
	opened, e := gitexport.Open(ctx, gitexport.Request{Kind: x.kind, URL: r.URL, Folder: r.Folder, Files: res.Files, Token: x.token,
		Confirmed: r.Rung == contract.RepositoryRungGiven && !x.req.Agent, Declaration: dec, Transport: x.d.gitTransport})
	if e != nil {
		res.PullRequestError = &app.DeclareError{Code: e.Code, Message: e.Message}
		return fmt.Errorf("%s; the pull request failed: %s; rerun with --pr --yes to retry", what, e.Message)
	}
	res.PullRequest = &app.PullRequestResult{URL: opened.URL, Login: opened.Login, Branch: opened.Branch, TokenSource: p.PullRequest.TokenSource}
	return nil
}

// printClaimResult prints what a claim of external system id wrote: the
// claimed version and who signed it, or the draft and the command a person
// accepts it with.
func printClaimResult(w io.Writer, res *app.DeclareResult, id string) {
	p := res.Plan
	if res.Draft {
		_, _ = fmt.Fprintf(w, "wrote %s of %s%s (%s): a person accepts it with %s --yes\n", res.Tag, fleet.ExternalSubjectPrefix, id, res.Published, p.AcceptCLI)
		return
	}
	_, _ = fmt.Fprintf(w, "claimed %s%s -> oci://%s:%s\n", fleet.ExternalSubjectPrefix, id, p.BundleRepo, p.Version)
	_, _ = fmt.Fprintf(w, "  signed by %s (person), inferredFrom: %s, no Git write\n", p.Signer.Subject, id)
}
