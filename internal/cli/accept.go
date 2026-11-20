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
digest. Only a record a person who owns the service signs counts: a CI or
agent signature is a proposal. This release only plans: --dry-run is required.`,
		Example: `  # Plan accepting every proposed range of reports
  pacto accept oci://ghcr.io/acme/reports --proposed --dry-run

  # Plan accepting the break ledger 3.0.0 announces, before it is published
  pacto accept ./reports --break ledger@3.0.0 --from ../ledger --dry-run`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			acceptSubject(&opts, args[0])
			opts.Fields = append(opts.Fields, args[1:]...)
			opts.Trust = publisherTrust(cmd, d)
			p, err := d.planAccept(svc, cmd.Context(), opts)
			return printAccept(cmd, v.GetString(outputFormatKey) == "json", p, err)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.Proposed, "proposed", false, "accept every proposed dependency range")
	f.StringArrayVar(&opts.Fields, "field", nil, "a proposed field to accept, as <key> or <key>=<range>; repeat it for more")
	f.StringVar(&opts.Break, "break", "", "accept the break this provider version announces, as <provider>@<version>")
	f.StringVar(&opts.From, "from", "", "the provider's checkout or oci:// ref to read an unpublished break from")
	f.StringVar(&opts.Intent, "intent", "", "the intent digest of the plan you reviewed: a different plan is refused")
	f.BoolVar(&dryRun, "dry-run", false, "print the plan and write nothing")
	_ = cmd.MarkFlagRequired("dry-run") // the flag exists, so marking cannot fail
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

// expiryText says when a break expires, or that its expiry is unknown.
func expiryText(expires string) string {
	if expires == "" {
		return "expiry unknown: run pacto ci or declare expires"
	}
	return "expires " + expires
}
