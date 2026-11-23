package fleet

import (
	"strings"
	"time"
)

// Line says who contributed the service, from where and how old the record
// is, as of asOf: "Contributed by my-idp from <repository>@sha256:ab12…,
// recorded 3h ago, read 2m ago; inferred, never clears a gate". The CLI and
// the TUI share it.
func (c *ServiceContribution) Line(asOf time.Time) string {
	from := c.Ref
	if repo, digest, ok := strings.Cut(c.Ref, "@"); ok {
		from = repo + "@" + shortDigestOf(digest)
	}
	line := "Contributed by " + c.Producer + " from " + from + ", "
	if c.AsOf != nil {
		line += "recorded " + longAge(asOf.Sub(*c.AsOf)) + " ago"
	} else {
		line += "recorded at an unknown time"
	}
	if c.Stale {
		line += ", stale"
	}
	if c.NewerRefused != "" {
		line += " (newer version " + c.NewerRefused + " was refused)"
	}
	if c.ReadAt != nil {
		line += ", read " + Age(asOf.Sub(*c.ReadAt)) + " ago"
	}
	return line + "; " + ProvenanceInferred + ", never clears a gate"
}

// ReportedLine says which producer reported the target through an Evidence
// Server and how old its evidence is as of now: "Reported by: my-idp
// (evidence 3m ago)". It is empty for a target any other source read.
func (t *TargetRecord) ReportedLine(now time.Time) string {
	if t.ReportedBy == "" {
		return ""
	}
	return "Reported by: " + reportedPhrase(t.ReportedBy, t.EvidenceAt, now)
}

// ReportedPhrase is [TargetRecord.ReportedLine] without its label, for a
// detail page that labels it itself; empty for a target any other source
// read.
func (t *TargetDetailData) ReportedPhrase(now time.Time) string {
	if t.ReportedBy == "" {
		return ""
	}
	return reportedPhrase(t.ReportedBy, t.EvidenceAt, now)
}

// reportedPhrase is "<producer> (evidence <age> ago)", or "(evidence age
// unknown)" when no evidence time was reported.
func reportedPhrase(by string, at *time.Time, now time.Time) string {
	if at == nil {
		return by + " (evidence age unknown)"
	}
	return by + " (evidence " + Age(now.Sub(*at)) + " ago)"
}
