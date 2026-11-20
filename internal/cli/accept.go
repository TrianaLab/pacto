package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

func newAcceptCommand(svc *app.Service, v *viper.Viper, d deps) *cobra.Command {
	var opts app.AcceptOptions
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "accept <service-ref-or-dir> [field...]",
		Short: "Accept proposed dependency ranges or an announced break with one signed record",
		Long: `Plans an acceptance record for the newest published version of a service:
a bundle repository (oci://host/path) or the service's checkout, whose
publish.bundle names it.

--proposed accepts every dependency range pacto ci proposed in the inferred
record. A field, positional or with --field, accepts one proposed field:
dependencies[<name>].compatibility, optionally with =<range> to accept another
range, or dependencies[<name>].ref, which --proposed never accepts. --break
<provider>@<version> accepts a break the provider announces for this service in
breaks[], read from that published version, or from --from (the provider's
checkout or a pushed oci:// ref) before it is published.

The plan names every field with its value and source, who may accept (the
service's declared owner), the acceptances already attached and the intent
digest. Without --dry-run, the record is signed and attached to that version
in its own bundle repository with your registry credentials. Only your
interactive Sigstore login (a browser, or a device code with --device) signs
the record, and only a record a person who owns the service signs counts.
Every other signer (--sign-key, an ambient OIDC token in CI) writes a draft,
pacto-accept-draft-<n>, that a person accepts.`,
		Example: `  # Plan accepting every proposed range of reports
  pacto accept oci://ghcr.io/acme/reports --proposed --dry-run

  # Accept the plan you reviewed, signed after a Sigstore login
  pacto accept oci://ghcr.io/acme/reports --proposed --intent sha256:...

  # Accept the break ledger 3.0.0 announces, before it is published
  pacto accept ./reports --break ledger@3.0.0 --from ../ledger`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			acceptSubject(&opts, args[0])
			opts.Fields = append(opts.Fields, args[1:]...)
			opts.Trust = publisherTrust(cmd, d)
			asJSON := v.GetString(outputFormatKey) == "json"
			if dryRun {
				p, err := d.planAccept(svc, cmd.Context(), opts)
				return printAccept(cmd, asJSON, p, err)
			}
			opts.Signing.Prompt = func(s string) { _, _ = fmt.Fprintln(cmd.ErrOrStderr(), s) }
			res, err := d.accept(svc, cmd.Context(), opts)
			return printAccepted(cmd, asJSON, res, err)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Proposed, "proposed", false, "accept every proposed dependency range")
	f.StringArrayVar(&opts.Fields, "field", nil, "a proposed field to accept, as <key> or <key>=<range>; repeat it for more")
	f.StringVar(&opts.Break, "break", "", "accept the break this provider version announces, as <provider>@<version>")
	f.StringVar(&opts.From, "from", "", "the provider's checkout or oci:// ref to read an unpublished break from")
	f.StringVar(&opts.Intent, "intent", "", "the intent digest of the plan you reviewed: a different plan is refused")
	f.BoolVar(&dryRun, "dry-run", false, "print the plan and write nothing")
	f.StringVar(&opts.Signing.Key, "sign-key", "", "sign with this key file: a key names no person, so the run writes a draft")
	f.StringVar(&opts.Signing.OIDCIssuer, "oidc-issuer-url", "https://oauth2.sigstore.dev/auth", "OpenID Connect issuer of the Sigstore login")
	f.StringVar(&opts.Signing.OIDCClient, "oidc-client-id", "sigstore", "OpenID Connect client of the Sigstore login")
	f.BoolVar(&opts.Signing.Device, "device", false, "log in with a device code instead of a browser")
	f.StringVar(&opts.Signing.FulcioURL, "fulcio-url", signing.PublicFulcio, "Fulcio to get the signing certificate from")
	f.StringVar(&opts.Signing.TimestampURL, "timestamp-url", "", "timestamp authority to date the signature with")
	f.StringVar(&opts.Signing.RekorURL, "rekor-url", signing.PublicRekor, "Rekor transparency log the signature is recorded in")
	f.StringVar(&opts.Signing.TrustedRoot, "trusted-root", "", "Sigstore trusted root JSON to verify with (default: public-good Sigstore)")
	return cmd
}

// acceptSubject reads the service argument: an oci:// reference, a folder
// (the service's checkout) or a bundle repository.
func acceptSubject(opts *app.AcceptOptions, arg string) {
	if !strings.HasPrefix(arg, "oci://") {
		if info, err := os.Stat(arg); err == nil && info.IsDir() {
			opts.Dir = arg
			return
		}
	}
	opts.Ref = arg
}

// printAccept prints the plan, then returns err: a refusal prints its
// message and the command a person runs about it.
func printAccept(cmd *cobra.Command, json bool, p *app.AcceptPlan, err error) error {
	var refusal *app.DeclareError
	if errors.As(err, &refusal) && refusal.CLI != "" {
		err = &declareRefusal{refusal}
	}
	if json {
		return printJSONExit(cmd, struct {
			*app.AcceptPlan
			Refusal *app.DeclareError `json:"refusal,omitempty"`
		}{p, refusal}, err)
	}
	if p != nil {
		printAcceptPlan(cmd.OutOrStdout(), p)
		if err == nil {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "nothing was written: this is a dry run")
		}
	}
	return err
}

// printAcceptPlan prints what the record accepts, who may accept, what is
// already attached and the intent digest.
func printAcceptPlan(w io.Writer, p *app.AcceptPlan) {
	_, _ = fmt.Fprintf(w, "plan: accept for %s %s (%s)\n", p.Service, p.Version, p.Subject)
	for _, r := range p.Ranges {
		switch {
		case strings.HasSuffix(r.Field, ".ref"):
			_, _ = fmt.Fprintf(w, "  %s: %s (from %s; this bundle repository becomes declared)\n", r.Field, r.Value, r.Source)
		case r.Value != r.Proposed:
			_, _ = fmt.Fprintf(w, "  %s: %s (proposed %s from %s)\n", r.Field, r.Value, r.Proposed, r.Source)
		default:
			_, _ = fmt.Fprintf(w, "  %s: %s (from %s)\n", r.Field, r.Value, r.Source)
		}
	}
	if b := p.Break; b != nil {
		_, _ = fmt.Fprintf(w, "  break: %s %s (announcement %s, %s)\n", b.Provider, b.Version, b.Announcement, expiryText(p.Expires))
	}
	_, _ = fmt.Fprintln(w, "who may accept: "+p.WhoMayAccept)
	for _, a := range p.Existing {
		line := fmt.Sprintf("attached: %s by %s (%s)", a.State, a.By, a.Record)
		if a.Reason != "" {
			line += ": " + a.Reason
		}
		_, _ = fmt.Fprintln(w, line)
	}
	for _, n := range p.Notes {
		_, _ = fmt.Fprintln(w, "note: "+n)
	}
	_, _ = fmt.Fprintln(w, "intent: "+p.IntentDigest)
}

// printAccepted prints what pacto accept did after the plan, then returns
// err: a refusal prints its message and the command a person runs about it.
func printAccepted(cmd *cobra.Command, json bool, res *app.AcceptResult, err error) error {
	var refusal *app.DeclareError
	if errors.As(err, &refusal) && refusal.CLI != "" {
		err = &declareRefusal{refusal}
	}
	if json {
		return printJSONExit(cmd, struct {
			*app.AcceptResult
			Refusal *app.DeclareError `json:"refusal,omitempty"`
		}{res, refusal}, err)
	}
	if res == nil || res.Plan == nil {
		return err
	}
	w := cmd.OutOrStdout()
	printAcceptPlan(w, res.Plan)
	if res.Executed {
		_, _ = fmt.Fprintln(w, acceptOutcome(res))
	}
	return err
}

// acceptOutcome is the line saying what was written: the signed record and
// how readers judge it, or the draft and how a person accepts it.
func acceptOutcome(res *app.AcceptResult) string {
	p := res.Plan
	_, name := fleet.ParseServiceKey(fleet.ServiceKey(p.Service))
	if res.Draft != "" {
		return "draft " + res.Draft + " written for " + p.WhoMayAccept + "; a person accepts it with " + res.AcceptCLI
	}
	what := fmt.Sprintf("%d ranges for %s %s", len(p.Ranges), name, p.Version)
	if len(p.Ranges) == 1 {
		what = "1 range for " + name + " " + p.Version
	}
	if b := p.Break; b != nil {
		_, provider := fleet.ParseServiceKey(fleet.ServiceKey(b.Provider))
		what = "break " + provider + " " + b.Version + " for " + name
	}
	repo, dgst, _ := strings.Cut(res.Record, "@")
	record := "record " + repo + "@" + dgst[:min(len(dgst), len("sha256:")+12)] + "..."
	if res.State == impact.AcceptRouted {
		return "routed: " + res.Signer.Subject + " is not the declared owner of " + name + "; the acceptance is kept and " + res.Reason + "\n" + record
	}
	return "accepted " + what + ", signed by " + res.Signer.Subject + " (" + res.Signer.Issuer + ")\n" + record
}

// expiryText says when a break expires, or that its expiry is unknown.
func expiryText(expires string) string {
	if expires == "" {
		return "expiry unknown: run pacto ci or declare expires"
	}
	return "expires " + expires
}
