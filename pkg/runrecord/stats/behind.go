package stats

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// MetricNames is the closed list of numbers Behind can list the records of:
//   - breaking: one record per change a diff or impact run classified BREAKING
//   - protected: the blocked gate records whose consumers were held back
//   - clear, blocked, unverified: the newest gate record of each change with
//     that verdict
//   - held, failed, rolledBack, verificationFailed: the operation records
//     counted in that rate's numerator
//   - leadTime: the first verified promotions that joined a publish record,
//     with those publish records
//   - coverage: the coverage records in the window
var MetricNames = []string{"breaking", "protected", "clear", "blocked", "unverified", "held", "failed", "rolledBack", "verificationFailed", "leadTime", "coverage"}

// behindMetrics lists, per metric, the entries behind it among the window's
// entries; history is every visible record lead times join against.
var behindMetrics = map[string]func(entries, history []Entry) []Entry{
	"breaking":           breakingRecords,
	"protected":          gateRecords(runrecord.VerdictBlocked),
	"clear":              gateRecords(runrecord.VerdictClear),
	"blocked":            gateRecords(runrecord.VerdictBlocked),
	"unverified":         gateRecords(runrecord.VerdictUnverified),
	"held":               operationRecords(isHeld),
	"failed":             operationRecords(isFailed),
	"rolledBack":         operationRecords(isRolledBack),
	"verificationFailed": operationRecords(isVerificationFailed),
	"leadTime":           leadTimeRecords,
	"coverage":           coverageRecords,
}

// Behind is the visible records behind one metric in q's window, by the same
// visibility, dedupe and definitions as Aggregate, newest first. A non-empty
// environment keeps the records that count under it, as the environment
// group does; coverage is fleet-wide and ignores it. It does no I/O.
func Behind(set RecordSet, q Query, sc Scope, now time.Time, metric, environment string) ([]runrecord.Record, error) {
	pick, ok := behindMetrics[metric]
	if !ok {
		return nil, fmt.Errorf("unknown metric %q: use one of %s", metric, strings.Join(MetricNames, ", "))
	}
	sel := selectRecords(set, q, sc, now)
	entries := sel.inWindow
	if environment != "" && metric != "coverage" {
		entries = slices.DeleteFunc(slices.Clone(entries), func(e Entry) bool {
			env, _, ok := environmentOf(e.Record)
			return !ok || env != environment
		})
	}
	var out []runrecord.Record
	seen := map[string]bool{}
	for _, e := range pick(entries, sel.history) {
		if !seen[e.Record.ID] {
			seen[e.Record.ID] = true
			out = append(out, e.Record)
		}
	}
	slices.SortFunc(out, func(a, b runrecord.Record) int {
		return cmp.Or(b.RecordedAt.Compare(a.RecordedAt), strings.Compare(a.ID, b.ID))
	})
	return out, nil
}

// breakingRecords is the newest BREAKING record of each change.
func breakingRecords(entries, _ []Entry) []Entry {
	var out []Entry
	for _, recs := range changesOf(entries) {
		var best *Entry
		for i := range recs {
			if recs[i].Record.Classification == classificationBreaking && (best == nil || newer(recs[i], *best)) {
				best = &recs[i]
			}
		}
		if best != nil {
			out = append(out, *best)
		}
	}
	return out
}

// gateRecords is the newest gate record of each change whose verdict is
// verdict.
func gateRecords(verdict string) func(entries, _ []Entry) []Entry {
	return func(entries, _ []Entry) []Entry {
		var out []Entry
		for _, recs := range changesOf(entries) {
			if g := newestGate(recs); g != nil && g.Record.Gate.Verdict == verdict {
				out = append(out, *g)
			}
		}
		return out
	}
}

// operations is the operation entries that count under an environment, as
// environmentMetrics counts them.
func operations(entries []Entry) []Entry {
	var out []Entry
	for _, e := range entries {
		if _, _, ok := environmentOf(e.Record); ok && e.Record.Kind == runrecord.KindOperation {
			out = append(out, e)
		}
	}
	return out
}

// operationRecords is the operations a rate's numerator counts.
func operationRecords(counts func(runrecord.Record) bool) func(entries, _ []Entry) []Entry {
	return func(entries, _ []Entry) []Entry {
		return slices.DeleteFunc(operations(entries), func(e Entry) bool { return !counts(e.Record) })
	}
}

// leadTimeRecords is each joined first promotion and its publish record.
func leadTimeRecords(entries, history []Entry) []Entry {
	var out []Entry
	for _, j := range joinPromotes(operations(entries), history) {
		if j.pub != nil {
			out = append(out, j.op, *j.pub)
		}
	}
	return out
}

func coverageRecords(entries, _ []Entry) []Entry {
	_, coverage := splitCoverage(entries)
	return coverage
}
