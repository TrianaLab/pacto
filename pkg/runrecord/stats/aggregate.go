package stats

import (
	"fmt"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// Provenance is said by every answer: run records are unsigned statistics.
const Provenance = "unsigned statistics"

// MaxClockSkew is how far past now a record may be dated before it is
// counted as malformed. Registry data is untrusted: a forged or skewed
// record must never speak for its change.
var MaxClockSkew = 5 * time.Minute

// GroupBy is the closed vocabulary of the by argument.
var GroupBy = []string{"service", "owner", "domain", "repository", "environment"}

// Limitation codes of a stats answer.
const (
	LimitationSkipped   = "RUN_RECORDS_SKIPPED"
	LimitationTruncated = "RUN_RECORDS_TRUNCATED"
	LimitationNoRecords = "NO_RUN_RECORDS"
	// LimitationRepositoryMissing replaces LimitationNoRecords when the stats
	// repository itself was not found.
	LimitationRepositoryMissing = "RUN_RECORDS_REPOSITORY_NOT_FOUND"
	// LimitationOnlyCoverage replaces LimitationNoRecords when the window
	// holds coverage records and no other run record.
	LimitationOnlyCoverage = "NO_CI_OR_DELIVERY_RUN_RECORDS"
	// LimitationConsumersPartial says a consumer count is a lower bound.
	LimitationConsumersPartial = "CONSUMERS_FROM_PARTIAL_SNAPSHOT"
)

// classificationBreaking is the diff classification that counts as caught.
const classificationBreaking = "BREAKING"

// Query is what an answer totals.
type Query struct {
	Window  Window
	By      string
	Service string
	Kinds   []string
}

// Ratio is a count out of a denominator. A zero denominator is unknown,
// never a 0 rate.
type Ratio struct {
	Count   int  `json:"count" doc:"Records that match"`
	Of      int  `json:"of" doc:"Records the rate is taken over"`
	Unknown bool `json:"unknown,omitempty" doc:"True when of is 0: there is nothing to take the rate over"`
}

func ratio(count, of int) Ratio {
	if of == 0 {
		return Ratio{Unknown: true}
	}
	return Ratio{Count: count, Of: of}
}

// Hidden says whether records outside a restricted caller's view exist. It
// never counts them.
type Hidden struct {
	Present bool `json:"present" doc:"True when run records name services outside the caller's view; they are left out of every number"`
}

// Distinct counts distinct changes and the runs behind them.
type Distinct struct {
	Distinct int `json:"distinct" doc:"Distinct changes (service, old revision, new revision)"`
	Runs     int `json:"runs" doc:"Run records behind those changes"`
}

// Protected counts consumers held back by blocked gates.
type Protected struct {
	Count   int `json:"count" doc:"Blocking consumers summed over changes whose newest gate is blocked"`
	Unknown int `json:"unknown" doc:"Unknown consumers summed over the same changes"`
	Partial int `json:"partial,omitempty" doc:"Changes among them judged on a partial fleet snapshot; with any, count is a lower bound"`
}

// Gates counts changes by their newest gate verdict.
type Gates struct {
	Clear      int `json:"clear" doc:"Changes whose newest gate verdict is clear"`
	Blocked    int `json:"blocked" doc:"Changes whose newest gate verdict is blocked"`
	Unverified int `json:"unverified" doc:"Changes whose newest gate verdict is unverified"`
}

// Metrics are the numbers of one group or of every record.
type Metrics struct {
	BreakingCaught     Distinct             `json:"breakingCaught" doc:"Changes a diff or impact run classified BREAKING"`
	ConsumersProtected Protected            `json:"consumersProtected" doc:"Consumers held back by changes whose newest gate is blocked; changes no impact run judged add nothing"`
	Gates              Gates                `json:"gates" doc:"Changes by their newest gate verdict"`
	Environments       []EnvironmentMetrics `json:"environments,omitempty" doc:"Delivery numbers per environment, alphabetical"`
}

// Attribution says where owner and domain came from.
type Attribution struct {
	Provenance string    `json:"provenance" doc:"Always inferred: resolved at read time from the current fleet snapshot"`
	AsOf       time.Time `json:"asOf" doc:"Time of the fleet snapshot"`
	SnapshotID string    `json:"snapshotId" doc:"Fleet snapshot the attribution was resolved from"`
	Partial    bool      `json:"partial,omitempty" doc:"True when the fleet snapshot is partial: a service it misses groups as unknown"`
}

// Group is the numbers of one value of the by argument.
type Group struct {
	Key     string  `json:"key" doc:"Group key"`
	Label   string  `json:"label" doc:"Group label"`
	Metrics Metrics `json:"metrics" doc:"Numbers of the records in this group"`
}

// Stats is one answer.
type Stats struct {
	AsOf            time.Time           `json:"asOf" doc:"Time the answer was computed, UTC"`
	Window          Window              `json:"window" doc:"Time range the answer covers"`
	Registry        string              `json:"registry,omitempty" doc:"Stats repository read"`
	Provenance      string              `json:"provenance" doc:"Always unsigned statistics: run records are never evidence"`
	RecordsRead     int                 `json:"recordsRead" doc:"Records read from the repository, in or out of the window; skipped records are counted apart"`
	Runs            int                 `json:"runs" doc:"Records in the window after retries of one record id count once"`
	DistinctChanges int                 `json:"distinctChanges" doc:"Distinct changes (service, old revision, new revision) among diff and impact runs in the window; publish runs name versions, so they are counted in runs only"`
	Skipped         Skipped             `json:"skipped" doc:"Records that could not be counted; they are left out of every number"`
	Truncated       string              `json:"truncated" doc:"The bound that cut the read; services listed after the cut were not read at all. Empty when the read is whole"`
	Hidden          Hidden              `json:"hidden" doc:"Whether records outside the caller's view exist"`
	Completeness    fleet.Completeness  `json:"completeness" enum:"complete,partial,empty" doc:"partial when a record was skipped, the read was cut, records are outside the caller's view or a blocked change was judged on a partial snapshot; empty when the window holds no records"`
	Limitations     []fleet.Limitation  `json:"limitations" doc:"Why the answer is incomplete"`
	By              string              `json:"by,omitempty" doc:"What the groups are by"`
	Attribution     *Attribution        `json:"attribution,omitempty" doc:"Where owner and domain came from, when grouping by them"`
	Totals          *Metrics            `json:"totals,omitempty" doc:"Numbers over every record in the window; absent when there are none"`
	Coverage        *runrecord.Coverage `json:"coverage,omitempty" doc:"The newest coverage record in the window; a level, never a sum. Absent when the window holds none"`
	Groups          []Group             `json:"groups,omitempty" doc:"Numbers per group"`
}

// Aggregate totals set for q at now, as sc lets the caller see it. It does
// no I/O.
func Aggregate(set RecordSet, q Query, sc Scope, now time.Time) *Stats {
	st := &Stats{AsOf: now.UTC(), Window: q.Window, Provenance: Provenance, By: q.By, Truncated: set.Truncated}
	sel := selectRecords(set, q, sc, now)
	st.Skipped, st.Hidden.Present = sel.skipped, sel.outside
	st.RecordsRead = len(sel.history)
	entries, coverage := splitCoverage(sel.inWindow)
	st.Runs, st.Coverage = len(entries), newestCoverage(coverage, q.Window.Until)
	if len(entries) > 0 {
		m, changes := metricsOf(entries, sel.history)
		st.Totals, st.DistinctChanges = &m, changes
		st.Groups = groups(entries, sel.history, q.By, sc)
	}
	if a := sc.Snapshot; a != nil && (q.By == "owner" || q.By == "domain") {
		attr := *a
		attr.Provenance = "inferred"
		st.Attribution = &attr
	}
	st.Limitations, st.Completeness = limitations(st, set, sc, sel, truncatedService(set.TruncatedAnchor, sel.history))
	if st.Totals != nil {
		st.Limitations = append(st.Limitations, unjoinedLimitation(st.Totals.Environments)...)
	}
	return st
}

// selection is what a query lets a caller count, shared by Aggregate and
// Behind so both apply one visibility, window and dedupe.
type selection struct {
	// history is every visible record not dated in the future, in the
	// window or before it, retries kept: lead times join against it.
	history []Entry
	// inWindow is history in the window and filters, one entry per id.
	inWindow []Entry
	skipped  Skipped
	future   int
	outside  bool
	// coverageHidden says a restricted caller was kept from coverage records.
	coverageHidden bool
}

func selectRecords(set RecordSet, q Query, sc Scope, now time.Time) selection {
	visible, skipped, outside, coverage := visibleEntries(set.Entries, set.Skipped, q.Service, sc)
	current, future := notFuture(visible, now)
	skipped.Malformed += future
	return selection{history: current, inWindow: dedupe(selectEntries(current, q)), skipped: skipped, future: future, outside: outside, coverageHidden: coverage}
}

// metricsOf is the CI and delivery numbers of entries and their distinct
// changes. history is every visible record, in the window or before it, that
// lead times join against.
func metricsOf(entries, history []Entry) (Metrics, int) {
	m, changes := ciMetrics(entries)
	m.Environments = environmentMetrics(entries, history)
	return m, changes
}

// notFuture drops the entries dated past now plus MaxClockSkew and counts them.
func notFuture(entries []Entry, now time.Time) ([]Entry, int) {
	limit := now.Add(MaxClockSkew)
	kept := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if t, ok := finishedAt(e.Record); e.Record.RecordedAt.After(limit) || ok && t.After(limit) {
			continue
		}
		kept = append(kept, e)
	}
	return kept, len(entries) - len(kept)
}

// recordTime is when a record happened: an operation's finish, else when it
// was recorded.
func recordTime(r runrecord.Record) time.Time {
	if t, ok := finishedAt(r); ok && r.Kind == runrecord.KindOperation {
		return t
	}
	return r.RecordedAt
}

func selectEntries(entries []Entry, q Query) []Entry {
	var out []Entry
	for _, e := range entries {
		t := recordTime(e.Record)
		switch {
		case t.Before(q.Window.Since) || !t.Before(q.Window.Until):
		case len(q.Kinds) > 0 && !slices.Contains(q.Kinds, e.Record.Kind):
		case q.Service != "" && e.Record.Service != q.Service:
		default:
			out = append(out, e)
		}
	}
	return out
}

// newer reports whether a speaks over b: the newest recordedAt, ties broken
// by the larger manifest digest, so listing order never decides.
func newer(a, b Entry) bool {
	if !a.Record.RecordedAt.Equal(b.Record.RecordedAt) {
		return a.Record.RecordedAt.After(b.Record.RecordedAt)
	}
	return a.Digest > b.Digest
}

// dedupe keeps one entry per record id: a retry writes the same id.
func dedupe(entries []Entry) []Entry {
	byID := map[string]int{}
	var out []Entry
	for _, e := range entries {
		i, seen := byID[e.Record.ID]
		switch {
		case !seen:
			byID[e.Record.ID] = len(out)
			out = append(out, e)
		case newer(e, out[i]):
			out[i] = e
		}
	}
	return out
}

// isChangeKind is a kind whose revisions are bundle digests. A publish record
// names versions, so it never shares a change key with the diff or impact of
// the same change and is counted in runs only.
func isChangeKind(kind string) bool {
	return kind == runrecord.KindDiff || kind == runrecord.KindImpact
}

// ciMetrics computes the CI numbers and counts the distinct changes.
func ciMetrics(entries []Entry) (Metrics, int) {
	changes := changesOf(entries)
	var m Metrics
	for _, recs := range changes {
		if n := breakingRuns(recs); n > 0 {
			m.BreakingCaught.Distinct++
			m.BreakingCaught.Runs += n
		}
		countGate(&m, newestGate(recs))
	}
	return m, len(changes)
}

// changesOf groups the diff and impact entries by change (service, old
// revision, new revision).
func changesOf(entries []Entry) map[string][]Entry {
	changes := map[string][]Entry{}
	for _, e := range entries {
		if r := e.Record; isChangeKind(r.Kind) {
			key := r.Service + "|" + r.OldRevision + "|" + r.NewRevision
			changes[key] = append(changes[key], e)
		}
	}
	return changes
}

func breakingRuns(recs []Entry) int {
	n := 0
	for _, e := range recs {
		if e.Record.Classification == classificationBreaking {
			n++
		}
	}
	return n
}

// newestGate is the newest record of a change that carries a gate verdict.
func newestGate(recs []Entry) *Entry {
	var best *Entry
	for i := range recs {
		if recs[i].Record.Gate != nil && (best == nil || newer(recs[i], *best)) {
			best = &recs[i]
		}
	}
	return best
}

// countGate counts a change's newest verdict. A verdict the aggregation does
// not name counts nowhere.
func countGate(m *Metrics, e *Entry) {
	if e == nil {
		return
	}
	switch e.Record.Gate.Verdict {
	case runrecord.VerdictClear:
		m.Gates.Clear++
	case runrecord.VerdictUnverified:
		m.Gates.Unverified++
	case runrecord.VerdictBlocked:
		m.Gates.Blocked++
		if c := e.Record.Consumers; c != nil {
			m.ConsumersProtected.Count += c.Blocking
			m.ConsumersProtected.Unknown += c.Unknown
			if e.Record.Completeness != string(fleet.CompletenessComplete) {
				m.ConsumersProtected.Partial++
			}
		}
	}
}

func limitations(st *Stats, set RecordSet, sc Scope, sel selection, truncatedBy string) ([]fleet.Limitation, fleet.Completeness) {
	lims := []fleet.Limitation{}
	for _, l := range []*fleet.Limitation{
		skippedLimitation(st.Skipped, sel.future),
		truncatedLimitation(st.Truncated, truncatedBy, set.UnreadAnchors, sc.Restricted),
		limitationIf(st.Hidden.Present, LimitationOutsideView, "some run records name services outside your view of the fleet and are left out of every number"),
		limitationIf(sel.coverageHidden, LimitationCoverageOutsideView, "coverage records count every service, so only a reader who can see every service sees them"),
		limitationIf(sc.NoDelivery != "", LimitationDeliveryUnavailable, sc.NoDelivery),
		limitationIf(st.Attribution != nil && st.Attribution.Partial, LimitationAttributionPartial, "the fleet snapshot owner and domain come from is partial; a service it misses groups as unknown"),
		limitationIf(st.Totals != nil && st.Totals.ConsumersProtected.Partial > 0, LimitationConsumersPartial, "some blocked changes were judged on a partial fleet snapshot, so consumersProtected is a lower bound"),
	} {
		if l != nil {
			lims = append(lims, *l)
		}
	}
	c := fleet.CompletenessComplete
	if len(lims) > 0 {
		c = fleet.CompletenessPartial
	}
	switch {
	case st.Runs == 0 && st.Coverage != nil:
		// The window holds coverage, so it is not empty: only the CI and
		// delivery numbers are missing.
		lims = append(lims, fleet.Limitation{Code: LimitationOnlyCoverage, Message: "the window holds coverage records and no CI or delivery run records: recording may be off for those jobs"})
	case st.Runs == 0 && set.RepositoryMissing:
		lims = append(lims, fleet.Limitation{Code: LimitationRepositoryMissing, Message: "the stats repository does not exist or holds no run records yet"})
	case st.Runs == 0:
		lims = append(lims, fleet.Limitation{Code: LimitationNoRecords, Message: "no run records in this window: recording may be off for these jobs, or the window holds none"})
	}
	if st.Runs == 0 && st.Coverage == nil && c == fleet.CompletenessComplete {
		c = fleet.CompletenessEmpty
	}
	return lims, c
}

func limitationIf(cond bool, code, message string) *fleet.Limitation {
	if !cond {
		return nil
	}
	return &fleet.Limitation{Code: code, Message: message}
}

func skippedLimitation(s Skipped, future int) *fleet.Limitation {
	dated := ""
	if future > 0 {
		dated = fmt.Sprintf(" (%d dated in the future)", future)
	}
	records := "records"
	if s.Total() == 1 {
		records = "record"
	}
	return limitationIf(s.Total() > 0, LimitationSkipped, fmt.Sprintf(
		"%d %s could not be read: %d malformed%s, %d oversized, %d unreadable; they are left out of every number",
		s.Total(), records, s.Malformed, dated, s.Oversized, s.Unreadable))
}

// truncatedLimitation names the service whose listing was cut only when the
// caller sees a record of it, and says how many services after the cut were
// not read at all; a restricted caller is never given a count.
func truncatedLimitation(bound, service string, unread int, restricted bool) *fleet.Limitation {
	listing := ""
	if service != "" {
		listing = " while listing the records of " + service
	}
	switch {
	case unread > 0 && restricted:
		listing += "; services after it were not read at all"
	case unread == 1:
		listing += "; 1 service after it was not read at all"
	case unread > 1:
		listing += fmt.Sprintf("; %d services after it were not read at all", unread)
	}
	return limitationIf(bound != "", LimitationTruncated, "the read was cut at "+bound+listing+"; records past the bound are left out of every number")
}
