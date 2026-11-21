package ci

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// IndexText says where an impact answer's consumers came from in the
// organization index, the text after "Consumers:" in pacto impact and
// "Index:" in pacto ci; "" when no index was involved (a pair of local
// folders). It says complete only when the listing finished and a target
// source checked it.
func IndexText(res *impact.Result) string {
	ix := res.ConsumerIndex
	if ix == nil {
		return ""
	}
	switch ix.Status {
	case impact.IndexNotFound:
		return ix.Reason + " (partial)"
	case impact.IndexUnavailable:
		return "the organization index " + ix.Repository + " could not be listed: " + ix.Reason + " (partial)"
	case impact.IndexSkipped:
		return "organization index not read (" + ix.Reason + ")"
	}
	how := "complete"
	switch {
	case !ix.Checked:
		how = strconv.Itoa(ix.Services) + " " + plural(ix.Services, "service", "services") + " listed, unchecked"
	case ix.Completeness != fleet.CompletenessComplete:
		how = "partial: " + indexReason(res.Limitations)
	}
	if ix.Reason != "" {
		how += ", " + ix.Reason
	}
	return strconv.Itoa(ix.Consumers) + " from " + ix.Repository + " (listed by tag, " + how + ")"
}

// NoteIndex keeps the Index line of the least complete answer: the first
// that read an index, replaced by a later one only when it says complete and
// the later one does not, so one row's complete read never hides another's
// partial one.
func (p *Plan) NoteIndex(res *impact.Result) {
	if p.Index == "" || p.indexComplete && !indexComplete(res) {
		p.Index, p.indexComplete = IndexText(res), indexComplete(res)
	}
}

// indexComplete reports whether IndexText says complete for res.
func indexComplete(res *impact.Result) bool {
	ix := res.ConsumerIndex
	return ix != nil && ix.Status == impact.IndexRead && ix.Checked && ix.Completeness == fleet.CompletenessComplete
}

// indexReason is the first limitation the index read or its cross-check
// added.
func indexReason(ls []fleet.Limitation) string {
	for _, l := range ls {
		if l.Source == "index" || l.Code == impact.LimitationConsumerNotIndexed {
			return strings.TrimSuffix(l.Message, ".")
		}
	}
	return "see the limitations"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// RangesLines is the "Ranges:" line of a coverage and one "unknown:" line
// per listed unknown range, as pacto ci and pacto impact print them; none
// when no coverage was read.
func RangesLines(c *impact.RangeCoverage) []string {
	switch {
	case c == nil:
		return nil
	case c.Edges == 0:
		return []string{"Ranges: no dependency edges"}
	}
	out := []string{fmt.Sprintf("Ranges: %d declared and %d accepted of %d dependency edges (%d%%)", c.Declared, c.Accepted, c.Edges, (c.Declared+c.Accepted)*100/c.Edges)}
	for _, u := range c.Unknown {
		line := "  unknown: " + u.From + " -> " + u.To
		if u.Proposed != "" {
			line += " (proposed " + u.Proposed + ")"
		}
		out = append(out, line)
	}
	if more := c.UnknownTotal - len(c.Unknown); more > 0 {
		out = append(out, "  and "+strconv.Itoa(more)+" more")
	}
	return out
}
