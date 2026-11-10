package cli

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// newDeliveryLifecycleCommand follows one version of a service through its
// Stages.
func newDeliveryLifecycleCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lifecycle",
		Short: "Follow one version of a service through every Stage: published, arrived, verified, re-checked, rolled back or left",
		Long: "Follows one artifact of a service, named by exactly one of --digest, --version or --contract, through every Stage of the " +
			"service you can see: whether pacto publish published it (Flux verified its signature) and, on each Stage, when it arrived, " +
			"was verified, re-checked, rolled back or left, each from its signed record or the Operation's unsigned status. " +
			"Without -n it reads every namespace you can list; one you cannot makes the answer partial and is named.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("namespace") {
				ns = ""
			}
			q := delivery.LifecycleQuery{Namespace: ns}
			for _, f := range []struct {
				name string
				to   *string
			}{{"service", &q.Service}, {"domain", &q.Domain}, {"digest", &q.Digest}, {"version", &q.Version}, {"contract", &q.Contract}} {
				*f.to, _ = cmd.Flags().GetString(f.name)
			}
			l, err := c.Lifecycle(cmd.Context(), q)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), l, func() error {
				printLifecycle(cmd.OutOrStdout(), l)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("service", "", "the service the version belongs to (required)")
	cmd.Flags().String("domain", "", "the service's registry domain; a Stage that names none matches any")
	cmd.Flags().String("digest", "", "the artifact digest Stages point at")
	cmd.Flags().String("version", "", "the version its contract declares")
	cmd.Flags().String("contract", "", "its contract: a manifest digest or a digest-pinned reference")
	_ = cmd.MarkFlagRequired("service")
	return cmd
}

func printLifecycle(w io.Writer, l *delivery.Lifecycle) {
	head := strings.TrimSpace(l.Service+" "+l.Version) + " " + l.Digest
	if l.Contract != "" {
		head += ", contract " + l.Contract
	}
	if l.SourceRevision != "" {
		head += ", source revision " + l.SourceRevision[:min(len(l.SourceRevision), 7)] + " (an annotation, not signed)"
	}
	_, _ = fmt.Fprintln(w, head)
	_, _ = fmt.Fprintf(w, "signature: %s%s\n", l.Signature, signatureEvidence(l))
	unsigned := false
	for _, s := range l.Stages {
		_, _ = fmt.Fprintf(w, "%s/%s (%s): %s\n", s.Namespace, s.Stage, s.Environment, lifecycleStageWords(s))
		for _, e := range s.Events {
			result := strings.Trim(string(e.Outcome)+"/"+string(e.Verification), "/")
			_, _ = fmt.Fprintf(w, "  %s  %s  %s  %s  %s  %s  %s\n", cmp.Or(e.Applied, e.Finished, e.Since, "-"), e.Role,
				cmp.Or(strings.TrimPrefix(e.Type, "delivery.pacto.trianalab.io/"), e.Kind), cmp.Or(e.Operation, "-"),
				cmp.Or(result, string(e.Phase), "-"), actorCell(e.HistoryEntry), recordCell(e.HistoryEntry))
			unsigned = unsigned || e.Record == ""
		}
		if lc := s.LastCheck; lc != nil {
			label := string(delivery.Worst(lc.Verdicts))
			if i := slices.IndexFunc(lc.Verdicts, func(v delivery.Verdict) bool { return string(v.Result) == label }); i >= 0 {
				label = delivery.VerdictLabel(lc.Verdicts[i])
			}
			_, _ = fmt.Fprintf(w, "  re-check %s: %s\n", lc.At, label)
		}
	}
	if unsigned {
		_, _ = fmt.Fprintln(w, "lines with record - are read from the Operation's status: their cause, correlation and applied time are not signed")
	}
	printAnswerNotes(w, nil, l.Unreadable)
	_, _ = fmt.Fprintf(w, "as of %s, %s: %s\n", l.AsOf, l.Completeness, l.Note)
}

// signatureEvidence names the Stages behind a lifecycle's signature.
func signatureEvidence(l *delivery.Lifecycle) string {
	switch l.Signature {
	case delivery.SignatureSigned:
		return " (published on " + strings.Join(l.SignedOn, ", ") + ")"
	case delivery.SignatureUnsigned:
		return " (an entry Stage holds it unpublished: " + strings.Join(l.UnsignedOn, ", ") + ")"
	}
	return ""
}

// lifecycleStageWords says where the artifact stands on one Stage. A Stage
// whose history shows nothing of it never reached it only when that history
// is complete.
func lifecycleStageWords(s delivery.LifecycleStage) string {
	if len(s.Events) == 0 && !s.Running && s.Verified != "yes" && s.Failed != "yes" && !s.Offered && s.LastCheck == nil {
		if s.Completeness == "complete" {
			return "never reached"
		}
		return "not seen in the history read (partial)"
	}
	var words []string
	if s.Running {
		words = append(words, "running")
	}
	words = append(words, map[string]string{"yes": "verified", "no": "not verified", "unknown": "verified: unknown"}[s.Verified])
	if f := map[string]string{"yes": "failed", "unknown": "failed: unknown"}[s.Failed]; f != "" {
		words = append(words, f)
	}
	if s.Offered {
		words = append(words, "offered downstream")
	}
	switch {
	case s.Source == delivery.SourceRecords && s.RecordsVerified:
		words = append(words, "records verified")
	case s.Source == delivery.SourceRecords:
		words = append(words, "records not verified")
	default:
		words = append(words, "from the Operations' status")
	}
	if s.Completeness != "complete" {
		words = append(words, "history partial")
	}
	return strings.Join(words, ", ")
}
