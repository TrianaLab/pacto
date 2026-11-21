package detect

import (
	"fmt"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// LiveState is what a live edge source read from a detected backend: its
// status, why it is not available, a hint that replaces the detected one,
// further facts, how many edges it read, which source won (see the dashboard's
// winner rule), the row id and name that replace the detected ones and when
// it read.
type LiveState struct {
	Status         fleet.SourceStatus
	Reason, Hint   string
	Details        []string
	Edges          int
	Won, Row, Name string
	ReadAt         time.Time
}

// EdgeSourceRow is the one row of a live edge source over entry e of res:
// the row [ProviderRows] makes for e alone, provenance inferred with its
// objects and cluster, used for observed edges, at the lower of the detected
// and the live status, dated by the live read (the detection time moves to
// Details) and saying how many edges it read and who won. The dashboard and
// the WASM demo both build their rows here, so the wording is one. A res with
// no AsOf is no detection (the OTLP receiver with no Collector found): the
// row is dated by the live read alone.
func EdgeSourceRow(res Result, e Entry, source string, live LiveState) fleet.Provider {
	detected := !res.AsOf.IsZero()
	if !detected {
		res.AsOf = live.ReadAt
	}
	if live.Status != fleet.SourceUnavailable {
		noun := "edges"
		if live.Edges == 1 {
			noun = "edge"
		}
		e.Evidence = joinEvidence(e.Evidence, fmt.Sprintf("%d %s in the last 15 minutes", live.Edges, noun))
	}
	if live.Won != "" {
		e.Evidence = joinEvidence(e.Evidence, "won: "+live.Won)
	}
	one := res
	one.Installation, one.Entries = nil, []Entry{e}
	p := ProviderRows(one, source, live.ReadAt)[0]
	if live.Row != "" {
		p.ID = live.Row
	}
	if live.Name != "" {
		p.Name = live.Name
	}
	p.UsedFor = []string{fleet.UseObservedEdges}
	if statusRank(live.Status) < statusRank(p.Status) {
		p.Status = live.Status
	}
	if live.Reason != "" {
		p.Limitations = append(p.Limitations, fleet.Limitation{Code: fleet.LimitationEdgeSourcePartial, Source: source, Message: live.Reason})
	}
	if live.Hint != "" {
		p.Hint = live.Hint
	}
	details := p.Details
	if detected {
		details = append(details, "detected "+res.AsOf.UTC().Format(time.RFC3339))
	}
	p.Details = capped(append(details, live.Details...), MaxDetails)
	at := live.ReadAt
	p.AsOf = &at
	return p
}

// joinEvidence is a and b joined by "; ", or b alone when a is empty.
func joinEvidence(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// statusRank orders row health, unavailable lowest; an unset status counts
// as available.
func statusRank(s fleet.SourceStatus) int {
	switch s {
	case fleet.SourceUnavailable:
		return 0
	case fleet.SourceStale:
		return 1
	case fleet.SourcePartial:
		return 2
	}
	return 3
}

// SplitEdgeKinds splits res into the entries of kinds, in res's order, and
// the rest: the detection source keeps the rest and each live edge source
// reports one taken entry, so every backend is one row.
func SplitEdgeKinds(res Result, kinds []string) (rest Result, taken []Entry) {
	rest = res
	rest.Entries = nil
	for _, e := range res.Entries {
		if slices.Contains(kinds, e.Kind) {
			taken = append(taken, e)
		} else {
			rest.Entries = append(rest.Entries, e)
		}
	}
	return rest, taken
}
