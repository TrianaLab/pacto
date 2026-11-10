package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// newDeliveryStatusCommand says where each Stage of one service stands.
func newDeliveryStatusCommand(v *viper.Viper, d deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show where each Stage of a service stands: what it runs, its state, its candidate and what you may ask for",
		Long: "Lists every Stage of one service you can see, in promotion order from each Stage's spec.candidates: " +
			"the version it runs, its state as the dashboard counts it, its upstream, its candidate, its newest re-check, " +
			"its level and the actions you may ask for (promote, unfreeze, freeze, undo), each with why or why not. " +
			"Without -n it reads every namespace you can list; one you cannot makes the answer partial and is named. " +
			"With --why it also plans the promote of each candidate, proposes nothing and says what holds it back.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ns, err := deliveryClient(cmd, d)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("namespace") {
				ns = ""
			}
			q := delivery.StatusQuery{Namespace: ns}
			q.Service, _ = cmd.Flags().GetString("service")
			q.Domain, _ = cmd.Flags().GetString("domain")
			q.Why, _ = cmd.Flags().GetBool("why")
			s, err := c.Status(cmd.Context(), q)
			if err != nil {
				return err
			}
			return formatResult(cmd, v.GetString(outputFormatKey), s, func() error {
				printStatus(cmd.OutOrStdout(), s)
				return nil
			}, nil)
		},
	}
	cmd.Flags().String("service", "", "the service whose Stages to show (required)")
	cmd.Flags().String("domain", "", "the service's registry domain; a Stage that names none matches any")
	cmd.Flags().Bool("why", false, "also plan the promote of each Stage's candidate (proposing nothing) and say what holds it back")
	_ = cmd.MarkFlagRequired("service")
	return cmd
}

func printStatus(out io.Writer, s *delivery.ServiceStatus) {
	stages := "Stages"
	if len(s.Stages) == 1 {
		stages = "Stage"
	}
	_, _ = fmt.Fprintf(out, "%s: %d %s you can see, as of %s, %s\n", s.Service, len(s.Stages), stages, s.AsOf, s.Completeness)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "STAGE\tENVIRONMENT\tUPSTREAM\tRUNS\tSTATE\tCANDIDATE\tLAST CHECK\tLEVEL\tYOU MAY")
	for _, r := range s.Stages {
		_, _ = fmt.Fprintf(w, "%s/%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Namespace, r.Stage, r.Environment, upstreamCell(r.Upstream),
			versionCell(r.Version, r.Pointer), stateCell(r), versionCell(r.CandidateVersion, r.Candidate), checkCell(r.LastCheck),
			cmp.Or(r.Level, "-"), offersCell(r.Offers))
	}
	_ = w.Flush()
	printNotOffered(out, s.Stages)
	printNotReady(out, s.Stages)
	for _, r := range s.Stages {
		printProgress(out, r)
	}
	printAnswerNotes(out, nil, s.Unreadable)
	_, _ = fmt.Fprintln(out, s.Note)
}

// versionCell is a version, else a short digest, else "-".
func versionCell(version, digest string) string {
	return cmp.Or(version, digest[:min(len(digest), 19)], "-")
}

// stateCell is the row's state in words, with how many changes wait for a
// person; it only formats the state the row carries.
func stateCell(r delivery.StageRow) string {
	s := delivery.StageStateWord(r.State)
	switch {
	case r.HeldUnknown:
		s += ", ? held"
	case r.Held > 0:
		s += fmt.Sprintf(", %d held", r.Held)
	}
	return s
}

func upstreamCell(u *delivery.Upstream) string {
	if u == nil {
		return "-"
	}
	problem := ""
	if u.Problem != "" {
		problem = " (" + u.Problem + ")"
	}
	switch u.Kind {
	case "stage":
		return "stage " + u.Namespace + "/" + u.Stage + problem
	case "records":
		return "records " + u.Stream + " (" + u.Trust + ")"
	case "artifact":
		return "artifact " + u.Repository + problem
	}
	return u.Kind
}

// checkCell is the worst verdict of the newest re-check as a person reads it
// (delivery.VerdictLabel) and when it ran; stale when it is about an older
// version.
func checkCell(lc *delivery.CheckSummary) string {
	if lc == nil {
		return "-"
	}
	label := string(lc.Result)
	if i := slices.IndexFunc(lc.Verdicts, func(v delivery.Verdict) bool { return v.Result == lc.Result }); i >= 0 {
		label = delivery.VerdictLabel(lc.Verdicts[i])
	}
	s := label + " " + lc.At
	if !lc.Current {
		s += " stale"
	}
	return s
}

func offersCell(offers []delivery.Offer) string {
	var may []string
	for _, o := range offers {
		if o.Available {
			may = append(may, o.Action)
		}
	}
	return cmp.Or(strings.Join(may, ", "), "-")
}

// printNotOffered says why each action that is not offered is not.
func printNotOffered(out io.Writer, rows []delivery.StageRow) {
	head := "not offered:\n"
	for _, r := range rows {
		for _, o := range r.Offers {
			if !o.Available {
				_, _ = fmt.Fprintf(out, "%s  %s/%s %s: %s\n", head, r.Namespace, r.Stage, o.Action, o.Reason)
				head = ""
			}
		}
	}
}

// printNotReady says why each Stage that is not Ready is not, and the fix.
func printNotReady(out io.Writer, rows []delivery.StageRow) {
	head := "not ready:\n"
	for _, r := range rows {
		reason, ok := strings.CutPrefix(r.Ready, "False: ")
		if !ok {
			continue
		}
		_, _ = fmt.Fprintf(out, "%s  %s/%s %s: %s\n", head, r.Namespace, r.Stage, reason, r.ReadyMessage)
		head = ""
		if r.ReadyFix != "" {
			_, _ = fmt.Fprintf(out, "    fix: %s\n", r.ReadyFix)
		}
	}
}

// printProgress says, with --why, why the Stage can or cannot move to its
// candidate, then the plan of its promote, indented.
func printProgress(out io.Writer, r delivery.StageRow) {
	p := r.Progress
	if p == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "== %s/%s: %s\n  %s\n", r.Namespace, r.Stage, p.State, p.Summary)
	for _, x := range p.Reasons {
		_, _ = fmt.Fprintf(out, "  %s: %s\n", x.Code, x.Message)
		if x.Fix != "" {
			_, _ = fmt.Fprintf(out, "    fix: %s\n", x.Fix)
		}
	}
	_, _ = fmt.Fprintf(out, "  automatic promote: %s\n", cmp.Or(p.AutoPromote, "the promoter promotes it on its own"))
	if p.Plan != nil {
		var b bytes.Buffer
		printPlan(&b, *p.Plan)
		for sc := bufio.NewScanner(&b); sc.Scan(); {
			_, _ = fmt.Fprintf(out, "  %s\n", sc.Text())
		}
	}
}
