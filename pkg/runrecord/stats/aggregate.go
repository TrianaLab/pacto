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
}

// Group is the numbers of one value of the by argument.
type Group struct {
	Key     string  `json:"key" doc:"Group key"`
	Label   string  `json:"label" doc:"Group label"`
	Metrics Metrics `json:"metrics" doc:"Numbers of the records in this group"`
}

// Scope narrows what a caller may see. Every record is visible for now.
type Scope struct{}

// Stats is one answer.
type Stats struct {
	AsOf            time.Time          `json:"asOf" doc:"Time the answer was computed, UTC"`
	Window          Window             `json:"window" doc:"Time range the answer covers"`
	Registry        string             `json:"registry,omitempty" doc:"Stats repository read"`
	Provenance      string             `json:"provenance" doc:"Always unsigned statistics: run records are never evidence"`
	RecordsRead     int                `json:"recordsRead" doc:"Records read from the repository, in or out of the window; skipped records are counted apart"`
	Runs            int                `json:"runs" doc:"Records in the window after retries of one record id count once"`
	DistinctChanges int                `json:"distinctChanges" doc:"Distinct changes (service, old revision, new revision) among diff, impact and publish runs in the window"`
	Skipped         Skipped            `json:"skipped" doc:"Records that could not be counted; they are left out of every number"`
	Truncated       string             `json:"truncated" doc:"The bound that cut the read; empty when the read is whole"`
	Hidden          Hidden             `json:"hidden" doc:"Whether records outside the caller's view exist"`
	Completeness    fleet.Completeness `json:"completeness" enum:"complete,partial,empty" doc:"partial when a record was skipped, the read was cut or records are outside the caller's view; empty when the window holds no records"`
	Limitations     []fleet.Limitation `json:"limitations" doc:"Why the answer is incomplete"`
	By              string             `json:"by,omitempty" doc:"What the groups are by"`
	Attribution     *Attribution       `json:"attribution,omitempty" doc:"Where owner and domain came from, when grouping by them"`
	Totals          *Metrics           `json:"totals,omitempty" doc:"Numbers over every record in the window; absent when there are none"`
	Groups          []Group            `json:"groups,omitempty" doc:"Numbers per group"`
}

// Aggregate totals set for q at now. It does no I/O.
func Aggregate(set RecordSet, q Query, _ Scope, now time.Time) *Stats {
	st := &Stats{AsOf: now.UTC(), Window: q.Window, Provenance: Provenance, By: q.By, Truncated: set.Truncated}
	current, future := notFuture(set.Entries, now)
	for _, s := range set.Skipped {
		st.Skipped = st.Skipped.Add(s)
	}
	st.Skipped.Malformed += future
	st.RecordsRead = len(current)
	entries := dedupe(selectEntries(current, q))
	st.Runs = len(entries)
	if len(entries) > 0 {
		m, changes := ciMetrics(entries)
		m.Environments = environmentMetrics(entries, publishesOf(current))
		st.Totals, st.DistinctChanges = &m, changes
	}
	st.Limitations, st.Completeness = limitations(st, future)
	if st.Totals != nil {
		st.Limitations = append(st.Limitations, unjoinedLimitation(st.Totals.Environments)...)
	}
	return st
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

// publishesOf is every publish record, in the window or before it, so a
// promotion in the window joins the publish that preceded it.
func publishesOf(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Record.Kind == runrecord.KindPublish {
			out = append(out, e)
		}
	}
	return out
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

func isChangeKind(kind string) bool {
	return kind == runrecord.KindDiff || kind == runrecord.KindImpact || kind == runrecord.KindPublish
}

// ciMetrics computes the CI numbers and counts the distinct changes.
func ciMetrics(entries []Entry) (Metrics, int) {
	changes := map[string][]Entry{}
	for _, e := range entries {
		if r := e.Record; isChangeKind(r.Kind) {
			key := r.Service + "|" + r.OldRevision + "|" + r.NewRevision
			changes[key] = append(changes[key], e)
		}
	}
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

func breakingRuns(recs []Entry) int {
	n := 0
	for _, e := range recs {
		if e.Record.Kind != runrecord.KindPublish && e.Record.Classification == classificationBreaking {
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

// countGate counts a change's newest verdict. A refused verdict is a
// publish the plan refused, which judges no consumer, so it counts nowhere.
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
		}
	}
}

func limitations(st *Stats, future int) ([]fleet.Limitation, fleet.Completeness) {
	lims := []fleet.Limitation{}
	c := fleet.CompletenessComplete
	if s := st.Skipped; s.Total() > 0 {
		dated := ""
		if future > 0 {
			dated = fmt.Sprintf(" (%d dated in the future)", future)
		}
		lims = append(lims, fleet.Limitation{Code: LimitationSkipped, Message: fmt.Sprintf(
			"%d records could not be read: %d malformed%s, %d oversized, %d unreadable; they are left out of every number",
			s.Total(), s.Malformed, dated, s.Oversized, s.Unreadable)})
		c = fleet.CompletenessPartial
	}
	if st.Truncated != "" {
		lims = append(lims, fleet.Limitation{Code: LimitationTruncated, Message: "the read was cut at " + st.Truncated + "; records past the bound are left out of every number"})
		c = fleet.CompletenessPartial
	}
	if st.Runs == 0 {
		lims = append(lims, fleet.Limitation{Code: LimitationNoRecords, Message: "no run records in this window: recording may be off for these jobs, or the window holds none"})
		if c == fleet.CompletenessComplete {
			c = fleet.CompletenessEmpty
		}
	}
	return lims, c
}
