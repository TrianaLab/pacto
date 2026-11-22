package stats

import (
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/runrecord"
)

// CoverageOf is the coverage of q's snapshot: the one derivation the coverage
// writer and the dashboard's current value share. The knowledge tally is
// copied verbatim, never re-derived, and an empty snapshot is partial. The
// snapshot time is whole seconds, like the recordedAt it must not follow.
func CoverageOf(q *fleet.Query) runrecord.Coverage {
	ov := q.Overview()
	s, k, m := ov.Summary, ov.Summary.Knowledge, ov.Summary.Match
	cc := k.ContractCompleteness
	c := runrecord.Coverage{
		Services: s.Services, Uncontracted: s.UncontractedCallers,
		Knowledge: &runrecord.CoverageKnowledge{
			Services: k.Services, Described: k.Described, WithGaps: k.WithGaps, Unknown: k.Unknown, NotDeployed: k.Gaps.NotDeployed,
		},
		Match: &runrecord.CoverageMatch{
			ServicesMatched: m.Services.Matched, ServicesExpectedOnly: m.Services.ExpectedOnly,
			ServicesObservedOnly: m.Services.ObservedOnly, ServicesInsufficient: m.Services.Insufficient,
			DependenciesMatched: m.Dependencies.Matched, DependenciesExpectedOnly: m.Dependencies.ExpectedOnly,
			DependenciesObservedOnly: m.Dependencies.ObservedOnly, DependenciesInsufficient: m.Dependencies.Insufficient,
		},
		Contracts: &runrecord.CoverageContracts{
			Services: cc.Judged + cc.Unknown, Judged: cc.Judged, Unknown: cc.Unknown, FullyDescribed: cc.FullyDescribed, PercentComplete: cc.PercentComplete,
		},
		Links: runrecord.CoverageLinks{
			Exact: s.ExactTargetLinks, Inferred: s.InferredTargetLinks, Ambiguous: s.AmbiguousTargetLinks, Unresolved: s.UnresolvedTargetLinks,
		},
		SnapshotAsOf: ov.Meta.AsOf.UTC().Truncate(time.Second), Completeness: string(ov.Meta.Completeness),
	}
	if ov.Meta.Completeness == fleet.CompletenessEmpty {
		c.Completeness = string(fleet.CompletenessPartial)
	}
	return c
}

// newestCoverage is the coverage of the newest coverage record by snapshot
// time, ties broken by id, ignoring a snapshot after until. Coverage is a
// level: it is never summed.
func newestCoverage(entries []Entry, until time.Time) *runrecord.Coverage {
	var best *runrecord.Record
	for i := range entries {
		r := &entries[i].Record
		switch {
		case r.Coverage == nil || r.Coverage.SnapshotAsOf.After(until):
		case best == nil || r.Coverage.SnapshotAsOf.After(best.Coverage.SnapshotAsOf),
			r.Coverage.SnapshotAsOf.Equal(best.Coverage.SnapshotAsOf) && r.ID > best.ID:
			best = r
		}
	}
	if best == nil {
		return nil
	}
	c := *best.Coverage
	return &c
}

// splitCoverage parts the coverage records from the runs.
func splitCoverage(entries []Entry) (runs, coverage []Entry) {
	for _, e := range entries {
		if e.Record.Kind == runrecord.KindCoverage {
			coverage = append(coverage, e)
		} else {
			runs = append(runs, e)
		}
	}
	return runs, coverage
}
