package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

func printFleetSearch(cmd *cobra.Command, res *fleet.SearchResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%d of %d service(s):\n", res.Count, res.Total)
		for _, s := range res.Services {
			_, _ = fmt.Fprintf(w, "  %-28s %-12s owner=%-14s revs=%d targets=%d\n",
				s.Name, orDash(s.Status), orDash(s.Owner), s.RevisionCount, s.TargetCount)
		}
		warnPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetService(cmd *cobra.Command, sv *fleet.ServiceView, format string) error {
	return formatResult(cmd, format, sv, func() error {
		w := cmd.OutOrStdout()
		s := sv.Service
		_, _ = fmt.Fprintf(w, "Service: %s\n", s.Name)
		_, _ = fmt.Fprintf(w, "Owner: %s\n", orDash(s.Owner.DisplayString()))
		_, _ = fmt.Fprintf(w, "Status: %s\n", orDash(s.Status))
		_, _ = fmt.Fprintf(w, "Sources: %v\n", s.Sources)
		printRevisions(w, sv.Revisions)
		printServiceTargets(w, sv.Targets)
		printServiceEdges(w, sv)
		warnPartial(cmd, sv.Meta)
		return nil
	}, nil)
}

func printRevisions(w io.Writer, revs []*fleet.ContractRevision) {
	if len(revs) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "Revisions (%d):\n", len(revs))
	for _, r := range revs {
		_, _ = fmt.Fprintf(w, "  %s  version=%s valid=%t digest=%s\n",
			r.Key, orDash(r.Version), r.Valid, orDash(r.Digest))
	}
}

func printServiceTargets(w io.Writer, targets []*fleet.TargetRecord) {
	if len(targets) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "Targets (%d):\n", len(targets))
	for _, t := range targets {
		_, _ = fmt.Fprintf(w, "  %s  compliance=%s stale=%t\n", t.Key, t.Compliance, t.Stale)
	}
}

func printServiceEdges(w io.Writer, sv *fleet.ServiceView) {
	if len(sv.Dependencies) > 0 {
		_, _ = fmt.Fprintf(w, "Dependencies (%d):\n", len(sv.Dependencies))
		for _, d := range sv.Dependencies {
			_, _ = fmt.Fprintf(w, "  %s  resolved=%t ref=%s\n", d.To, d.Resolved, orDash(d.RequestedRef))
		}
	}
	if len(sv.Dependents) > 0 {
		_, _ = fmt.Fprintf(w, "Dependents (%d): %v\n", len(sv.Dependents), sv.Dependents)
	}
	for _, c := range sv.Capabilities {
		if len(c.Tools) > 0 || len(c.Skills) > 0 {
			_, _ = fmt.Fprintf(w, "Revision %s (v%s): tools=%d skills=%d\n", c.Revision, c.Version, len(c.Tools), len(c.Skills))
		}
	}
}

func printFleetTarget(cmd *cobra.Command, tv *fleet.TargetView, format string) error {
	return formatResult(cmd, format, tv, func() error {
		w := cmd.OutOrStdout()
		t := tv.Target
		_, _ = fmt.Fprintf(w, "Target: %s\n", t.Key)
		_, _ = fmt.Fprintf(w, "Service: %s\n", t.Service)
		_, _ = fmt.Fprintf(w, "Compliance: %s\n", t.Compliance)
		rev := orDash(string(t.ContractRevision))
		// Only an exact (immutable-digest) link is the revision known to be running;
		// an inferred link is a mutable correlation and is labelled as such.
		if t.ContractRevision != "" && t.RevisionMatch != "" {
			rev += " (" + t.RevisionMatch + ")"
		}
		_, _ = fmt.Fprintf(w, "Revision: %s\n", rev)
		_, _ = fmt.Fprintf(w, "Stale: %t\n", t.Stale)
		if t.Coverage != nil {
			_, _ = fmt.Fprintf(w, "Coverage: %d/%d evaluated\n", t.Coverage.Evaluated, t.Coverage.Required)
		}
		for _, f := range t.Findings {
			_, _ = fmt.Fprintf(w, "  finding [%s] %s: %s\n", f.Severity, f.Code, f.Message)
		}
		for _, l := range t.Limitations {
			_, _ = fmt.Fprintf(w, "  limitation [%s] %s\n", l.Code, l.Message)
		}
		warnPartial(cmd, tv.Meta)
		return nil
	}, nil)
}

func printFleetGraph(cmd *cobra.Command, res *fleet.GraphResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		root := string(res.Root)
		if res.Revision != "" {
			root = string(res.Revision)
		} else if res.Aggregated {
			root += " (aggregated across revisions)"
		}
		_, _ = fmt.Fprintf(w, "%s of %s (%d node(s)):\n", res.Direction, root, len(res.Nodes))
		for _, n := range res.Nodes {
			_, _ = fmt.Fprintf(w, "  %*s%s (depth %d)\n", n.Depth*2, "", n.Name, n.Depth)
		}
		for _, c := range res.Cycles {
			_, _ = fmt.Fprintf(w, "  cycle: %v\n", c)
		}
		for _, u := range res.Unresolved {
			_, _ = fmt.Fprintf(w, "  unresolved: %s -> %s (%s)\n", u.FromService, u.To, orDash(u.Reason))
		}
		warnPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetStatus(cmd *cobra.Command, res *fleet.StatusResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%d attention item(s):\n", len(res.Items))
		for _, it := range res.Items {
			_, _ = fmt.Fprintf(w, "  [%s] %s %s — %s\n", it.Code, it.Kind, it.Name, it.Reason)
		}
		warnPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetExplain(cmd *cobra.Command, res *fleet.ExplainResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%s %s: %s\n", res.Kind, res.Subject, orDash(res.Status))
		if len(res.Reasons) == 0 {
			_, _ = fmt.Fprintln(w, "  no reasons recorded")
		}
		for _, r := range res.Reasons {
			_, _ = fmt.Fprintf(w, "  [%s] %s\n", r.Code, r.Message)
		}
		warnPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetSnapshot(cmd *cobra.Command, snap *fleet.FleetSnapshot, format string) error {
	return formatResult(cmd, format, snap, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "Fleet snapshot (as of %s, %s):\n", snap.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"), snap.Completeness)
		_, _ = fmt.Fprintf(w, "  services=%d revisions=%d targets=%d relationships=%d\n",
			len(snap.Services), len(snap.Revisions), len(snap.Targets), len(snap.Relationships))
		_, _ = fmt.Fprintf(w, "Sources (%d):\n", len(snap.Sources))
		for _, s := range snap.Sources {
			_, _ = fmt.Fprintf(w, "  %s (%s): %s revs=%d targets=%d\n", s.ID, s.Kind, s.Status, s.RevisionCount, s.TargetCount)
		}
		for _, l := range snap.Limitations {
			_, _ = fmt.Fprintf(w, "  limitation [%s] %s\n", l.Code, l.Message)
		}
		return nil
	}, nil)
}

// orDash returns "-" for an empty string so tabular text output stays aligned.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
