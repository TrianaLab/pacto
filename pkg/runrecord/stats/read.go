// Package stats totals unsigned run records. It does no I/O: the records
// arrive as a [RecordSet] read by pkg/runrecord/registry, so the CLI, the MCP
// server and the dashboard share one aggregation. Run records are statistics,
// never evidence.
//
// Experimental: not covered by semantic versioning.
package stats

import "github.com/trianalab/pacto/v4/pkg/runrecord"

// Skipped counts the listed records a read could not count.
type Skipped struct {
	Malformed  int `json:"malformed" doc:"Records that are not a valid run record, are attached to another service's anchor or are dated in the future"`
	Oversized  int `json:"oversized" doc:"Records whose manifest or layer is larger than the run-record size bound"`
	Unreadable int `json:"unreadable" doc:"Records the registry listed but would not serve"`
}

// Total is the number of skipped records.
func (s Skipped) Total() int { return s.Malformed + s.Oversized + s.Unreadable }

// Add sums two counts.
func (s Skipped) Add(o Skipped) Skipped {
	return Skipped{Malformed: s.Malformed + o.Malformed, Oversized: s.Oversized + o.Oversized, Unreadable: s.Unreadable + o.Unreadable}
}

// Entry is one record read, with the manifest digest it was read from and
// the anchor tag it was listed under.
type Entry struct {
	Record runrecord.Record
	Digest string
	Anchor string
}

// RecordSet is what one read of a stats repository found.
type RecordSet struct {
	// Entries are the records read, in listing order.
	Entries []Entry
	// Skipped counts the records that could not be read, keyed by anchor tag.
	Skipped map[string]Skipped
	// Truncated names the bound that cut the read, without any tag; empty
	// when the read is whole.
	Truncated string
	// TruncatedAnchor is the anchor tag whose listing was cut, when one was.
	TruncatedAnchor string
}
