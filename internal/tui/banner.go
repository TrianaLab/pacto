package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// bannerMinBarWidth is the narrowest bar worth drawing: one cell per compliance
// bucket. Below it the allocator would have to hide a bucket to fit, and a
// health bar that silently drops the invalid targets is worse than no bar.
var bannerMinBarWidth = len(complianceOrder)

// complianceOrder is the bar's bucket order, worst first. Worst first because
// the bar is read left to right and the left edge is where the eye lands: a
// fleet with two invalid targets shows red at the start of the stripe rather
// than buried between the compliant run and the not-evaluated tail.
//
// The empty status is the "other" bucket -- a compliance value the fleet layer
// counted but does not name. It is carried rather than folded into one of the
// seven, so the segments always sum to the target population and the bar cannot
// quietly under-report.
var complianceOrder = []string{
	fleet.StatusInvalid,
	fleet.StatusNonCompliant,
	fleet.StatusUnknown,
	fleet.StatusWarning,
	fleet.StatusCompliant,
	fleet.StatusReference,
	fleet.StatusNotEvaluated,
	"",
}

// healthBanner is the fleet's state in two lines, above the list: a population
// count with an attention figure, and a compliance stripe over the targets.
//
// It is computed once per snapshot, not once per frame. Query.Overview walks
// every service, revision, target and relationship, and the banner is redrawn
// sixteen times a second while anything is animating.
type healthBanner struct {
	snapID    string
	services  int
	revisions int
	targets   int
	attention int
	counts    []int // parallel to complianceOrder
	sources   int
	degraded  int // sources that are partial, stale or flatly unavailable
	// startFrame is the frame the count-up began on, so the figures animate in
	// with the screen rather than on some clock of their own.
	startFrame int
}

// newHealthBanner tallies the snapshot behind c.
func newHealthBanner(c *Context) healthBanner {
	ov := c.Query.Overview()
	s := ov.Summary
	return healthBanner{
		snapID:    c.Query.SnapshotID(),
		services:  s.Services,
		revisions: s.Revisions,
		targets:   s.Targets,
		attention: s.ServicesNeedingAttention,
		counts: []int{
			s.InvalidTargets, s.NonCompliantTargets, s.UnknownTargets, s.WarningTargets,
			s.CompliantTargets, s.ReferenceTargets, s.NotEvaluatedTargets, s.OtherComplianceTargets,
		},
		sources:    len(c.Snapshot.Sources),
		degraded:   s.DegradedSources + s.StaleSources + s.UnavailableSources,
		startFrame: c.Frame,
	}
}

// stale reports whether the banner describes a snapshot other than the current
// one, which is the signal to recompute it.
func (h healthBanner) stale(c *Context) bool { return h.snapID != c.Query.SnapshotID() }

// animating is true while the attention figure has something to throb about.
// A fleet with nothing wrong holds still, which is what makes the throb mean
// anything on a fleet that does.
func (h healthBanner) animating(c *Context) bool { return c.Anim && h.attention > 0 }

// view renders the two lines.
func (h healthBanner) view(c *Context) string {
	return h.headline(c) + "\n" + h.stripe(c)
}

// headline is the population count, with the attention figure pushed to the
// right edge so it always lands in the same place.
func (h healthBanner) headline(c *Context) string {
	p := 1.0
	if c.Anim {
		p = progressAt(c.Frame, h.startFrame, framesFor(countUpDuration))
	}
	left := dimStyle.Render(fmt.Sprintf("%d services   %d revisions   %d targets   %d sources",
		countUp(h.services, p), countUp(h.revisions, p), countUp(h.targets, p), countUp(h.sources, p)))

	right := okStyle.Render(glyphDot + " nothing needs attention")
	if h.attention > 0 {
		n := countUp(h.attention, p)
		k := 1.0
		if c.Anim {
			const floor = 0.45
			k = floor + (1-floor)*pulse(c.Frame, pulsePeriod)
		}
		st := style{lipgloss.NewStyle().Foreground(dimColor(colAmber, k)).Bold(true)}
		right = st.Render(fmt.Sprintf("%s %d %s need attention", glyphDot, n, plural(n, "service", "services")))
	}
	return spread(left, right, c.Width)
}

// countUpDuration is how long the headline figures take to reach their real
// values. Short: it is a flourish on arrival, not a progress bar.
const countUpDuration = 450 * time.Millisecond

// stripe is the compliance bar plus its legend. Targets are the only population
// the fleet grades, so a fleet with none says so rather than drawing an empty
// bar that reads as "nothing is compliant".
func (h healthBanner) stripe(c *Context) string {
	if h.targets == 0 {
		return h.ungraded()
	}
	legend := h.legend()
	width := c.Width - lipgloss.Width(legend) - 3
	if width > 32 {
		width = 32
	}
	if width < bannerMinBarWidth {
		return legend
	}
	p := 1.0
	if c.Anim {
		p = easeOutCubic(progressAt(c.Frame, h.startFrame, framesFor(countUpDuration)))
	}
	return h.bar(width, p) + "   " + legend
}

// ungraded is the stripe's replacement when there is nothing to grade. It names
// the reason rather than the absence: no targets means no runtime source has
// reported, and that is a different fact from a fleet that is failing.
func (h healthBanner) ungraded() string {
	if h.degraded > 0 {
		return warnStyle.Render(fmt.Sprintf("no deployment targets — %d of %d %s degraded, so compliance is ungraded",
			h.degraded, h.sources, plural(h.sources, "source is", "sources are")))
	}
	return faintStyle.Render("no deployment targets — compliance is ungraded")
}

// bar renders the compliance stripe, filled to p of its width so it grows into
// place. Each segment keeps its own colour, so the stripe is the distribution
// rather than a single ratio.
func (h healthBanner) bar(width int, p float64) string {
	lit := int(float64(width) * p)
	var b strings.Builder
	cell := 0
	for i, n := range allocateSegments(h.counts, h.targets, width) {
		for j := 0; j < n; j++ {
			glyph := glyphBarFull
			if cell >= lit {
				glyph = glyphBarEmpty
			}
			b.WriteString(statusStyle(complianceOrder[i]).Render(glyph))
			cell++
		}
	}
	return b.String()
}

// legend names the buckets that have anything in them, worst first, and stops
// at three. A legend that lists every bucket is a table, and the bar beside it
// already carries the shape.
func (h healthBanner) legend() string {
	parts := make([]string, 0, 3)
	for i, n := range h.counts {
		if n == 0 || len(parts) == 3 {
			continue
		}
		s := complianceOrder[i]
		parts = append(parts, statusStyle(s).Render(fmt.Sprintf("%s %d %s", glyphDot, n, statusPresentationFor(s).Label)))
	}
	return strings.Join(parts, "  ")
}

// allocateSegments divides width between the buckets in proportion to counts,
// summing to exactly width.
//
// The floor of one cell per non-empty bucket is the point: two invalid targets
// out of four hundred round to zero cells, and the one bucket a reader needs to
// see is the one proportional rounding erases. Applying the floor can push the
// total over width, so the overflow is taken back off the largest segments --
// which is safe without a guard, because width is never below the bucket count
// (bannerMinBarWidth), so at least one segment is always above one cell for as
// long as the total exceeds width.
func allocateSegments(counts []int, total, width int) []int {
	out := make([]int, len(counts))
	used := 0
	for i, n := range counts {
		if n == 0 {
			continue
		}
		w := n * width / total
		if w < 1 {
			w = 1
		}
		out[i] = w
		used += w
	}
	for used > width {
		out[argmax(out)]--
		used--
	}
	if used < width && used > 0 {
		out[argmax(out)] += width - used
	}
	return out
}

// argmax is the index of the largest element. Called only on a non-empty slice.
func argmax(xs []int) int {
	best := 0
	for i, x := range xs {
		if x > xs[best] {
			best = i
		}
	}
	return best
}

// spread pushes right to the right-hand edge of width. When the two do not fit
// it drops the padding rather than the content: a truncated count is a wrong
// count, and the line wrapping is the lesser harm.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// plural picks the form matching n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
