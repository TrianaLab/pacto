package cli

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/tui"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

func printFleetSearch(cmd *cobra.Command, res *fleet.SearchResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%d of %d service(s):\n", res.Count, res.Total)
		for _, s := range res.Services {
			mark := ""
			if s.Template {
				mark = " golden path"
			}
			mark += nameInferredMark(s.NameInferred)
			if s.Declaration == fleet.DeclarationInferred {
				mark += " " + fleet.DeclarationInferred
			}
			_, _ = fmt.Fprintf(w, "  %-28s %-12s owner=%-14s domain=%-12s offering=%-12s revs=%d targets=%d%s\n",
				s.Name, orDash(s.Status), orDash(s.Owner.DisplayString()), orDash(s.BusinessDomain), orDash(s.Offering), s.RevisionCount, s.TargetCount, mark)
		}
		if len(res.Externals) > 0 {
			_, _ = fmt.Fprintf(w, "EXTERNALS (%d of %d):\n", len(res.Externals), res.ExternalsTotal)
			for _, e := range res.Externals {
				_, _ = fmt.Fprintf(w, "  external:%-19s %-28s %s\n", e.Key, e.Label, e.Secondary)
			}
		}
		warnPartial(cmd, res.Meta)
		return nil
	}, nil)
}

// nameInferredMark keeps an inferred name's provenance in text output.
func nameInferredMark(inf *contract.Inferred) string {
	if inf == nil {
		return ""
	}
	return " (name inferred from " + inf.Source + ")"
}

func printFleetService(cmd *cobra.Command, sv *fleet.ServiceView, format string) error {
	return formatResult(cmd, format, sv, func() error {
		w := cmd.OutOrStdout()
		s := sv.Service
		printReplacedFrom(w, sv.ReplacedFrom, sv.Meta.AsOf)
		_, _ = fmt.Fprintf(w, "Service: %s%s\n", s.Name, nameInferredMark(s.NameInferred))
		for _, l := range fleet.ReplacesLines(sv.Replaces, sv.Meta.AsOf) {
			_, _ = fmt.Fprintln(w, l)
		}
		if more := sv.ReplacesTotal - len(sv.Replaces); more > 0 {
			_, _ = fmt.Fprintf(w, "  %d more\n", more)
		}
		_, _ = fmt.Fprintf(w, "Owner: %s%s\n", orDash(s.Owner.DisplayString()), tui.OwnerProvenanceMark(s.OwnerProvenance, s.OwnerSource))
		_, _ = fmt.Fprintf(w, "Status: %s\n", orDash(s.Status))
		_, _ = fmt.Fprintf(w, "Sources: %v\n", s.Sources)
		if s.Contribution != nil {
			_, _ = fmt.Fprintln(w, s.Contribution.Line(sv.Meta.AsOf))
		}
		if s.Workload != nil {
			_, _ = fmt.Fprintln(w, strings.Join(s.Workload.Lines(), "\n"))
		}
		printFields(w, sv.Fields, sv.ReplacesHidden)
		printRevisions(w, sv.Revisions)
		printServiceTargets(w, sv.Targets, sv.Meta.AsOf)
		printExtensionChecks(w, sv.Revisions)
		printServiceEdges(w, sv)
		warnPartial(cmd, sv.Meta)
		return nil
	}, nil)
}

// printReplacedFrom says the answer is about the service that replaced the
// inferred service asked for, when it is.
func printReplacedFrom(w io.Writer, r *fleet.Replacement, asOf time.Time) {
	if r == nil {
		return
	}
	_, _ = fmt.Fprintln(w, r.ReplacedLine(asOf))
}

// printFields prints where each field of a service comes from and how many
// of its replacements the viewer may not see.
func printFields(w io.Writer, f *fleet.FieldsPreview, replacesHidden int) {
	if f != nil {
		_, _ = fmt.Fprintln(w, "Fields:")
		for _, l := range f.Lines() {
			_, _ = fmt.Fprintln(w, l)
		}
	}
	if replacesHidden > 0 {
		_, _ = fmt.Fprintf(w, "Replaces hidden: %d\n", replacesHidden)
	}
}

// printInferredEdges prints the graph's runtime-inferred edges: calls one of
// whose ends no contract describes, with the sources that saw them.
func printInferredEdges(w io.Writer, edges []fleet.Relationship) {
	for _, e := range edges {
		if e.Provenance != fleet.ProvenanceInferred || len(e.ObservedSources) == 0 {
			continue
		}
		_, from := fleet.ParseServiceKey(e.FromService)
		_, to := fleet.ParseServiceKey(e.ToService)
		if e.ToExternal != "" {
			to = fleet.ExternalSubjectPrefix + string(e.ToExternal)
		}
		sources := make([]string, len(e.ObservedSources))
		for i, s := range e.ObservedSources {
			sources[i] = s.Source
		}
		_, _ = fmt.Fprintf(w, "  %s -> %s  inferred (observed by %s)\n", from, to, strings.Join(sources, ", "))
	}
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

// printExtensionChecks prints one line per extension check that counts
// toward the service status: its state, the producer, when it observed and
// why the check is not covered.
func printExtensionChecks(w io.Writer, revs []*fleet.ContractRevision) {
	for _, r := range revs {
		for _, c := range r.Extensions {
			if !c.CountsTowardStatus {
				continue
			}
			line := fmt.Sprintf("Extension %s (v%s): %s", c.Ref, r.Version, c.State)
			if c.Producer != "" {
				line += " by " + c.Producer
			}
			if c.AsOf != nil {
				line += " (as of " + c.AsOf.UTC().Format(time.RFC3339) + ")"
			}
			if c.Reason != "" {
				line += ": " + c.Reason
			}
			_, _ = fmt.Fprintln(w, line)
		}
	}
}

func printServiceTargets(w io.Writer, targets []*fleet.TargetRecord, asOf time.Time) {
	if len(targets) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "Targets (%d):\n", len(targets))
	for _, t := range targets {
		_, _ = fmt.Fprintf(w, "  %s  compliance=%s stale=%t%s%s\n", t.Key, t.Compliance, t.Stale, reportedMark(t, asOf), linkedMark(t.Link))
	}
}

// reportedMark names the producer that reported a target through an
// Evidence Server and its evidence age, or nothing.
func reportedMark(t *fleet.TargetRecord, asOf time.Time) string {
	if t.ReportedBy == "" {
		return ""
	}
	age := "unknown"
	if t.EvidenceAt != nil {
		age = fleet.Age(asOf.Sub(*t.EvidenceAt))
	}
	return " reportedBy=" + t.ReportedBy + " evidence=" + age
}

// linkedMark names how a target was linked with no Pacto, or nothing.
func linkedMark(l *fleet.ObservedLink) string {
	if l == nil {
		return ""
	}
	return " (observed: " + strings.ReplaceAll(l.Signal, "-", " ") + ")"
}

func printServiceEdges(w io.Writer, sv *fleet.ServiceView) {
	if len(sv.Dependencies) > 0 {
		// The edges are the union ACROSS revisions, so one dependency legitimately
		// appears once per revision that declares it. Each row names its declaring
		// revision: without that, three revisions declaring the same dependency render
		// as three identical lines, which reads as one revision declaring it three
		// times -- a different claim about the service.
		_, _ = fmt.Fprintf(w, "Dependencies (%d):\n", len(sv.Dependencies))
		for _, d := range sv.Dependencies {
			_, _ = fmt.Fprintf(w, "  %s  declaredBy=%s %s ref=%s\n",
				d.To, orDash(string(d.FromRevision)), edgeResolution(d), orDash(d.RequestedRef))
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

// edgeResolution is how a dependency row says where it leads: a joined
// external system by its subject, anything else by whether it resolved.
func edgeResolution(d fleet.Relationship) string {
	if d.ToExternal != "" {
		return "-> " + fleet.ExternalSubjectPrefix + string(d.ToExternal)
	}
	return fmt.Sprintf("resolved=%t", d.Resolved)
}

// supersededNote marks a source whose consumer's newest revision dropped it.
func supersededNote(src fleet.ExternalSource) string {
	if src.Superseded {
		return ", not in the newest revision"
	}
	return ""
}

// inferredSourceLine is one inferred source of an external system: the
// consumer, the kind, where it was found and the confidence, then the
// revision whose inferred record names it, for a repository source.
func inferredSourceLine(s fleet.ExternalInferredSource) string {
	out := fmt.Sprintf("%s %s %s (inferred, %s)", s.Service, s.Kind, s.Where, s.Confidence)
	if s.Revision != "" {
		out += " from " + string(s.Revision)
	}
	return out
}

// printExternal is the text of one external system: its identity line, the
// sources that declare, call or use it (an inferred one with its kind, where
// it was found and the normalized host, never a value) and the catalog that
// classified it, then its inferred contract, when a source read one.
func printExternal(w io.Writer, rec fleet.ExternalRecord, into fleet.ExternalKey, ev fleet.ExternalEvidence) {
	consumers := "consumers"
	if rec.ConsumersTotal == 1 {
		consumers = "consumer"
	}
	_, _ = fmt.Fprintf(w, "%s  external, %s  (used by %d %s)\n", rec.Key, rec.Class, rec.ConsumersTotal, consumers)
	label := "sources"
	line := func(format string, a ...any) {
		_, _ = fmt.Fprintf(w, "  %-7s  "+format+"\n", append([]any{label}, a...)...)
		label = ""
	}
	for _, src := range rec.Sources {
		// Observed and inferred sources print through their evidence below.
		if src.Kind == fleet.ExternalSourceContract {
			line("%s pacto.yaml dependencies[%d] (name: %s%s)", src.Service, src.Index, src.Dependency, supersededNote(src))
		}
	}
	for _, s := range ev.InferredSources {
		line("%s", inferredSourceLine(s))
		if s.Host != "" {
			line("  -> %s (host and port only)", s.Host)
		}
	}
	for _, s := range ev.ObservedSources {
		line("%s %s (observed, %d calls)", s.Service, s.Source, s.Count)
	}
	if more := ev.InferredSourcesTotal - len(ev.InferredSources); more > 0 {
		line("%d more inferred sources", more)
	}
	if more := ev.ObservedSourcesTotal - len(ev.ObservedSources); more > 0 {
		line("%d more observed sources", more)
	}
	if rec.SourcesTotal > len(rec.Sources) {
		_, _ = fmt.Fprintf(w, "           %d sources in all, across every consumer revision\n", rec.SourcesTotal)
	}
	if c := rec.Catalog; c != nil {
		_, _ = fmt.Fprintf(w, "  catalog  %s %s entry %s\n", c.Origin, shortDigest(c.Digest), c.Entry)
	} else if rec.Folded == 0 {
		_, _ = fmt.Fprintf(w, "  catalog  none (%s)\n", rec.JoinWords())
	}
	if rec.Folded > 0 {
		_, _ = fmt.Fprintf(w, "  folds    %d external systems\n", rec.Folded)
	}
	if into != "" {
		_, _ = fmt.Fprintf(w, "  folded into %s in this answer\n", into)
	}
	if rec.HiddenConsumers > 0 {
		_, _ = fmt.Fprintf(w, "  hidden consumers %d\n", rec.HiddenConsumers)
	}
	printExternalProvider(w, rec.Provider)
}

// printExternalProvider is the inferred contract block of an external
// system; nothing when no source read one.
func printExternalProvider(w io.Writer, p *fleet.ExternalProvider) {
	if p == nil {
		return
	}
	_, _ = fmt.Fprintln(w, "  inferred contract")
	for _, l := range p.Lines() {
		_, _ = fmt.Fprintln(w, "    "+l)
	}
}

// shortDigest is a digest cut to its algorithm and first 12 hex characters.
func shortDigest(d string) string {
	if algo, hex, ok := strings.Cut(d, ":"); ok && len(hex) > 12 {
		return algo + ":" + hex[:12] + "..."
	}
	return d
}

func printFleetExternal(cmd *cobra.Command, ev *fleet.ExternalView, format string) error {
	return formatResult(cmd, format, ev, func() error {
		printExternal(cmd.OutOrStdout(), ev.External, ev.FoldedInto, ev.ExternalEvidence)
		warnPartial(cmd, ev.Meta)
		return nil
	}, nil)
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
		if t.Link != nil {
			_, _ = fmt.Fprintf(w, "Linked: %s\n", t.Link.Line(tv.Meta.AsOf))
		}
		if l := t.ReportedLine(tv.Meta.AsOf); l != "" {
			_, _ = fmt.Fprintln(w, l)
		}
		_, _ = fmt.Fprintf(w, "Stale: %t\n", t.Stale)
		if t.Coverage != nil {
			_, _ = fmt.Fprintf(w, "Coverage: %d/%d evaluated\n", t.Coverage.Evaluated, t.Coverage.Required)
		}
		for _, f := range t.Findings {
			_, _ = fmt.Fprintf(w, "  finding [%s] %s: %s%s\n", f.Severity, f.Code, f.Message, seenBy(f.EvidenceRefs))
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
		printReplacedFrom(w, res.ReplacedFrom, res.Meta.AsOf)
		root := string(res.Root)
		if res.External != "" {
			root = fleet.ExternalSubjectPrefix + string(res.External)
		} else if res.Revision != "" {
			root = string(res.Revision)
		} else if res.Aggregated {
			root += " (aggregated across revisions)"
		}
		_, _ = fmt.Fprintf(w, "%s of %s (%d node(s)):\n", res.Direction, root, len(res.Nodes))
		// The answer is breadth-first; path order prints each node under the
		// parent its shortest path runs through, so the indentation reads as a tree.
		nodes := slices.Clone(res.Nodes)
		slices.SortStableFunc(nodes, func(a, b fleet.GraphNode) int { return slices.Compare(a.Path, b.Path) })
		for _, n := range nodes {
			_, _ = fmt.Fprintf(w, "  %*s%s (depth %d, %s)\n", n.Depth*2, "", n.Name, n.Depth, orDash(n.Provenance))
		}
		printInferredEdges(w, res.Edges)
		for _, c := range res.Cycles {
			_, _ = fmt.Fprintf(w, "  cycle: %v\n", c)
		}
		for _, u := range res.Unresolved {
			_, _ = fmt.Fprintf(w, "  unresolved: %s -> %s (%s)\n", u.FromService, u.To, orDash(u.Reason))
		}
		if res.Truncated {
			_, _ = fmt.Fprintf(w, "  truncated: the answer stops at %d nodes\n", len(res.Nodes))
		}
		warnPartial(cmd, res.Meta)
		return nil
	}, nil)
}

func printFleetStatus(cmd *cobra.Command, res *fleet.StatusResult, format string) error {
	return formatResult(cmd, format, res, func() error {
		w := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(w, "%d of %d attention item(s):\n", len(res.Items), res.Total)
		for _, it := range res.Items {
			_, _ = fmt.Fprintf(w, "  [%s] %s %s — %s\n", it.Code, it.Kind, it.Name, it.Reason)
		}
		printContractCompleteness(w, res.ContractCompleteness)
		printDetected(w, res.Providers, res.Meta.Sources)
		printPinnedSources(w, res.Meta.Sources, res.Meta.Limitations)
		printClusters(w, res.Meta.Clusters, res.Meta.Limitations)
		warnPartial(cmd, res.Meta)
		return nil
	}, nil)
}

// clusterWords are the status words of a cluster: why a restricted view hides
// it, else how its sources answered.
var clusterWords = map[string]string{
	fleet.ClusterNoAccess: "no access", fleet.ClusterReviewFailed: "review failed", fleet.ClusterNotReviewable: "not reviewable",
	string(fleet.SourceAvailable): "complete", string(fleet.SourcePartial): "partial", string(fleet.SourceStale): "stale", string(fleet.SourceUnavailable): "unreachable",
}

// refusedWords word a cluster Pacto refused to read by why: its limitation
// code. Its server may have answered, so it never reads unreachable.
var refusedWords = map[string]string{fleet.LimitationClusterConflict: "conflicting id", fleet.LimitationClusterUnknown: "unidentified"}

// printClusters prints one Clusters line, "eu-prod (uid) complete, us-prod
// (uid) no access", when the answer knows two or more clusters or hides one.
// The UID is what --cluster takes; a context Pacto refused (no UID) is worded
// by its limitation.
func printClusters(w io.Writer, clusters []fleet.ClusterSummary, lims []fleet.Limitation) {
	words := make([]string, 0, len(clusters))
	hidden := false
	for _, c := range clusters {
		hidden = hidden || c.Access != ""
		words = append(words, clusterLabelOf(c)+" "+clusterWordOf(c, lims)+linkWordOf(c))
	}
	if len(clusters) >= 2 || hidden {
		_, _ = fmt.Fprintln(w, "Clusters: "+strings.Join(words, ", "))
	}
}

// clusterLabelOf is a cluster's name and, when known, its UID.
func clusterLabelOf(c fleet.ClusterSummary) string {
	if c.ID == "" || c.ID == c.Name {
		return c.Name
	}
	return c.Name + " (" + c.ID + ")"
}

// clusterWordOf is a cluster's status word: why a context with no UID was
// refused, else clusterWords.
func clusterWordOf(c fleet.ClusterSummary, lims []fleet.Limitation) string {
	if c.ID == "" {
		for _, l := range lims {
			if word := refusedWords[l.Code]; word != "" && l.Source == c.Name {
				return word
			}
		}
	}
	return clusterWords[cmp.Or(c.Access, string(c.Status))]
}

// modelWords word how a member reviews the people of one issuer.
var modelWords = map[string]string{fleet.IssuerShared: "shared issuer", fleet.IssuerGroupMapping: "group mapping", fleet.IssuerNotAccepted: "issuer not accepted"}

// linkWordOf is a member's channel word and, while it is live, the model of
// each issuer it reviewed: " live shared issuer", " no live channel" for a
// channel that dropped. Only the hub knows its members' channels, so a
// cluster with no link fact, as every cluster read outside the hub is,
// prints no word: an unknown channel is never "no live channel".
func linkWordOf(c fleet.ClusterSummary) string {
	switch {
	case c.Link != nil && c.Link.Channel == fleet.LinkLive:
		var models []string
		for _, m := range c.Link.Issuers {
			if w := modelWords[m.Model]; !slices.Contains(models, w) {
				models = append(models, w)
			}
		}
		return strings.TrimRight(" live "+strings.Join(models, " and "), " ")
	case c.Link != nil:
		return " no live channel"
	}
	return ""
}

// printPinnedSources prints one line per source that read a pinned ref (each
// section of a target snapshot): its status, the ref, who signed it, as of
// when and, when it is not available, its first limitation.
func printPinnedSources(w io.Writer, sources []fleet.SourceState, lims []fleet.Limitation) {
	for _, s := range sources {
		if s.Ref == "" {
			continue
		}
		line := fmt.Sprintf("Source %s (%s): %s%s", s.ID, s.Kind, s.Status, sourceRef(s))
		if s.LastSuccessfulSync != nil {
			line += ", asOf " + s.LastSuccessfulSync.UTC().Format(time.RFC3339)
		}
		if why := sourceReason(s.ID, lims); why != "" && s.Status != fleet.SourceAvailable {
			line += ": " + why
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

// sourceReason is the first limitation of source id that says why, past the
// line Build adds for every degraded source ("source <id> returned a partial
// result"), which it falls back to.
func sourceReason(id string, lims []fleet.Limitation) string {
	why := ""
	for _, l := range lims {
		switch {
		case l.Source != id:
		case !strings.HasPrefix(l.Message, "source "+id+" "):
			return l.Message
		case why == "":
			why = l.Message
		}
	}
	return why
}

// printDetected prints the declared installation row with its asOf and
// details, then the inferred
// provider rows, what the operator detected, with each status and first reason and each hint for a person to
// review, then one line per advisory source that is not available, so a
// detection Pacto could not read never reads as nothing detected. Every row
// of one detection shares its asOf, so the first one is named.
func printDetected(w io.Writer, rows []fleet.Provider, sources []fleet.SourceState) {
	defer printAdvisorySources(w, sources)
	for _, p := range rows {
		if p.Provenance == fleet.ProvenanceDeclared {
			_, _ = fmt.Fprintf(w, "%s: %s (declared, as of %s) [%s]\n", p.Name, p.Evidence, asOfText(p.AsOf), p.Status)
			for _, d := range p.Details {
				_, _ = fmt.Fprintf(w, "    %s\n", d)
			}
		}
	}
	header := false
	for _, p := range rows {
		if p.Provenance != fleet.ProvenanceInferred {
			continue
		}
		if !header {
			_, _ = fmt.Fprintf(w, "Detected (inferred, as of %s, nothing turned on):\n", asOfText(p.AsOf))
			header = true
		}
		status := string(p.Status)
		if p.Status != fleet.SourceAvailable && len(p.Limitations) > 0 {
			status += ": " + p.Limitations[0].Message
		}
		_, _ = fmt.Fprintf(w, "  %s: %s [%s]\n", p.Name, p.Evidence, status)
		if p.Hint != "" {
			_, _ = fmt.Fprintf(w, "    %s\n", p.Hint)
		}
	}
}

// asOfText is a detected row's asOf in RFC 3339, or unknown.
func asOfText(at *time.Time) string {
	if at == nil {
		return "unknown"
	}
	return at.UTC().Format(time.RFC3339)
}

// printAdvisorySources prints one line per advisory detection source that is
// not available, with its reason, and reports whether it printed any: the
// line names a detection Pacto could not read. Other advisory sources, such
// as a mesh source, already say partial and why on their rows.
func printAdvisorySources(w io.Writer, sources []fleet.SourceState) bool {
	printed := false
	for _, s := range sources {
		if !s.Advisory || s.Kind != fleet.SourceKindDetection || s.Status == fleet.SourceAvailable {
			continue
		}
		state, reason := string(s.Status), ""
		if s.Status == fleet.SourceUnavailable {
			state = "not read"
		}
		if s.Error != nil {
			reason = " (" + s.Error.Message + ")"
		}
		_, _ = fmt.Fprintf(w, "Detected: %s %s%s\n", s.ID, state, reason)
		printed = true
	}
	return printed
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
		printFields(w, res.Fields, res.ReplacesHidden)
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
			_, _ = fmt.Fprintf(w, "  %s (%s): %s revs=%d targets=%d%s\n", s.ID, s.Kind, s.Status, s.RevisionCount, s.TargetCount, sourceRef(s))
		}
		// The snapshot carries its completeness inline rather than as a fleet.Meta,
		// so wrap it to reuse the shared partial-answer warning: every other fleet
		// printer puts that on STDERR, and a CI step redirecting stdout must not
		// read a partial snapshot as a clean run.
		warnPartial(cmd, fleet.Meta{
			Completeness: snap.Completeness,
			AsOf:         snap.GeneratedAt,
			Limitations:  snap.Limitations,
		})
		return nil
	}, nil)
}

// sourceRef says what a source with a pinned ref read and who signed it:
// ", ref <ref>, signed by <signer>", with "(not scoped to this cluster)" when
// the key is not scoped to it, or ", ref <ref>, unverified"; empty with no ref.
func sourceRef(s fleet.SourceState) string {
	switch {
	case s.Ref == "":
		return ""
	case s.Signer == "":
		return ", ref " + s.Ref + ", unverified"
	case s.SignerUnscoped:
		return ", ref " + s.Ref + ", signed by " + s.Signer + " (not scoped to this cluster)"
	}
	return ", ref " + s.Ref + ", signed by " + s.Signer
}

// orDash returns "-" for an empty string so tabular text output stays aligned.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// seenBy names the distinct collectors behind a finding's evidence, so a reader can tell what the kernel
// saw (collector k8s-observer+ebpf) from what Kubernetes reported.
func seenBy(refs []finding.EvidenceRef) string {
	var cs []string
	for _, r := range refs {
		if r.Collector != "" && !slices.Contains(cs, r.Collector) {
			cs = append(cs, r.Collector)
		}
	}
	if len(cs) == 0 {
		return ""
	}
	return " (seen by " + strings.Join(cs, ", ") + ")"
}
