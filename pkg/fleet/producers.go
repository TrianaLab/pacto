package fleet

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// ProducerRowPrefix prefixes the id of the row of one producer an Evidence
// Server lists: producer/my-idp.
const ProducerRowPrefix = "producer/"

// LimitationContributionUnread: the contributions of a producer could not
// be listed or read, so the inferred services they would add are unknown.
// It makes its source partial.
const LimitationContributionUnread = "CONTRIBUTION_UNREAD"

// ProducerFacts is what one evidence source knows of one producer, the input
// of [ProducerRow].
type ProducerFacts struct {
	Producer string
	// AdapterKinds are the provider-scoped kinds its trust config allows it
	// to report: a reporting adapter.
	AdapterKinds []string
	// NewestReport is the evidence time of its newest accepted target; nil
	// when it reported none.
	NewestReport *time.Time
	// Repository is its contribution repository; empty when the trust config
	// grants it none.
	Repository string
	// Read counts the records read from Repository and NewestContribution
	// is the newest creation time among them, nil when none says when.
	Read               int
	NewestContribution *time.Time
	// Unread is why a repository could not be listed, "<repository>: <why>"
	// sanitized and joined by "; "; empty when every one was.
	Unread string
	// Problems are why some records were not read; each makes the row partial.
	Problems []string
	// Ignored counts the tags that name no <service>_<version>.
	Ignored int
}

// ProducerRow is the one row of p: what it reports and contributes, its
// own status and the newest time it spoke. A producer that reports no
// target, is no adapter and is granted no repository gets no row (false):
// nothing it said can be shown. window is the freshness window, 0 for none.
// The evidence counts nothing a restricted view could hide.
func ProducerRow(p ProducerFacts, now time.Time, window time.Duration) (Provider, bool) {
	row := Provider{ID: ProducerRowPrefix + p.Producer, Name: p.Producer, Status: SourceAvailable,
		producer: &producerInput{p, now, window}}
	var evidence []string
	if p.NewestReport != nil {
		row.UsedFor = append(row.UsedFor, UseReportedTargets)
		evidence = append(evidence, "reports targets through the Evidence Server; newest report "+longAge(now.Sub(*p.NewestReport))+" ago")
	}
	if len(p.AdapterKinds) > 0 {
		row.UsedFor = append(row.UsedFor, UseReporting)
		evidence = append(evidence, "the trust config allows it to report "+strings.Join(p.AdapterKinds, " and "))
	}
	if p.Repository != "" {
		row.UsedFor = append(row.UsedFor, UseContributedRecords)
		evidence = append(evidence, "contributes inferred services from "+p.Repository)
	}
	if len(row.UsedFor) == 0 {
		return Provider{}, false
	}
	row.Evidence = strings.Join(evidence, "; ")
	row.AsOf = newestOf(p.NewestReport, p.NewestContribution)
	for _, c := range producerConcerns(p, now, window) {
		if sourceHealthRank(c.status) < sourceHealthRank(row.Status) {
			row.Status = c.status
		}
		row.Limitations = append(row.Limitations, Limitation{Code: LimitationProviderSourceDegraded, Message: c.why})
	}
	if p.Ignored > 0 {
		row.Details = []string{strconv.Itoa(p.Ignored) + " tags name no <service>_<version> and were ignored"}
	}
	return row, true
}

// producerInput is what [ProducerRow] built a row from.
type producerInput struct {
	facts  ProducerFacts
	now    time.Time
	window time.Duration
}

// rebuiltProducerRow is p, a producer row of a source whose state is in
// state, built again from its facts as edit changes them; false when the
// facts no longer make a row.
func rebuiltProducerRow(p Provider, state map[string]SourceStatus, edit func(*ProducerFacts)) (Provider, bool) {
	in := *p.producer
	in.facts.Problems = slices.Clone(in.facts.Problems)
	edit(&in.facts)
	row, ok := ProducerRow(in.facts, in.now, in.window)
	if !ok {
		return Provider{}, false
	}
	row.Source, row.Cluster = p.Source, p.Cluster
	if state == nil {
		return row, true
	}
	rows := finalizeRows([]Provider{row}, state)
	if len(rows) == 0 {
		return Provider{}, false
	}
	return rows[0], true
}

// restrictProducerRows builds every producer row that reports targets again
// from the targets a restricted view keeps: the newest report a hidden target
// made is never shown, and a producer whose every target is hidden says
// nothing about reports.
func restrictProducerRows(rows []Provider, targets map[TargetKey]*TargetRecord, state map[string]SourceStatus) []Provider {
	out := make([]Provider, 0, len(rows))
	for _, p := range rows {
		if p.producer == nil || p.producer.facts.NewestReport == nil {
			out = append(out, p)
			continue
		}
		row, ok := rebuiltProducerRow(p, state, func(f *ProducerFacts) {
			f.NewestReport = nil
			for _, t := range targets {
				if t.ReportedBy == f.Producer && (t.Source == p.Source || slices.Contains(t.Sources, p.Source)) {
					f.NewestReport = newestOf(f.NewestReport, t.EvidenceAt)
				}
			}
		})
		if ok {
			out = append(out, row)
		}
	}
	return out
}

// producerConcern is one reason a producer row is not available.
type producerConcern struct {
	status SourceStatus
	why    string
}

// producerConcerns are the reasons p's row is not available: its repository
// unread, records it holds unread or none at all, and a newest report or
// record older than window.
func producerConcerns(p ProducerFacts, now time.Time, window time.Duration) []producerConcern {
	var out []producerConcern
	if p.Repository != "" {
		switch {
		case p.Unread != "" && p.Read == 0:
			out = append(out, producerConcern{SourceUnavailable, "could not read " + p.Unread})
		case p.Unread != "":
			out = append(out, producerConcern{SourcePartial, "could not read " + p.Unread})
		case p.Read == 0 && len(p.Problems) == 0:
			out = append(out, producerConcern{SourcePartial, "no contribution found at " + p.Repository})
		}
	}
	for _, why := range p.Problems {
		out = append(out, producerConcern{SourcePartial, why})
	}
	if window <= 0 {
		return out
	}
	if p.NewestReport != nil && now.Sub(*p.NewestReport) > window {
		out = append(out, producerConcern{SourceStale, "last reported " + longAge(now.Sub(*p.NewestReport)) + " ago"})
	}
	switch {
	case p.Read == 0:
	case p.NewestContribution == nil:
		out = append(out, producerConcern{SourceStale, "no record says when it was made"})
	case now.Sub(*p.NewestContribution) > window:
		out = append(out, producerConcern{SourceStale, "recorded " + longAge(now.Sub(*p.NewestContribution)) + " ago"})
	}
	return out
}

// newestOf is the later of the times set, nil when neither is.
func newestOf(times ...*time.Time) *time.Time {
	var out *time.Time
	for _, t := range times {
		if t != nil && (out == nil || t.After(*out)) {
			at := *t
			out = &at
		}
	}
	return out
}

// longAge is [Age], in whole days from two days on: 40d.
func longAge(d time.Duration) string {
	if d >= 48*time.Hour {
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return Age(d)
}

// SanitizedReason is the fixed, generic reason of err, never its text: the
// message an unavailable source carries.
func SanitizedReason(err error) string { return sanitizeError(err).Message }
