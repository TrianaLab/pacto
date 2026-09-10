package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/trianalab/pacto/v3/pkg/fleet"
)

// TestNewHealthBannerTalliesTheSnapshot checks the banner counts the fixture
// rather than inventing numbers: two services, two targets, one of each
// compliance state, one source, and one service the fleet says needs looking at.
func TestNewHealthBannerTalliesTheSnapshot(t *testing.T) {
	c := newLoadedContext(t)
	h := newHealthBanner(c)

	if h.services != 2 {
		t.Errorf("services = %d, want 2", h.services)
	}
	if h.targets != 2 {
		t.Errorf("targets = %d, want 2", h.targets)
	}
	if h.revisions != 2 {
		t.Errorf("revisions = %d, want 2", h.revisions)
	}
	if h.sources != 1 {
		t.Errorf("sources = %d, want 1", h.sources)
	}
	if h.attention == 0 {
		t.Error("the fixture has a non-compliant target, so something needs attention")
	}
	if sum := total(h.counts); sum != h.targets {
		t.Errorf("the compliance buckets sum to %d, want the %d targets", sum, h.targets)
	}
	if h.snapID != c.Query.SnapshotID() {
		t.Error("the banner did not record the snapshot it describes")
	}
}

// TestHealthBannerStaleFollowsTheSnapshot is what makes the banner refresh after
// a reload: it is keyed on the snapshot id, not on a time or a dirty flag.
func TestHealthBannerStaleFollowsTheSnapshot(t *testing.T) {
	c := newLoadedContext(t)
	h := newHealthBanner(c)
	if h.stale(c) {
		t.Fatal("a freshly built banner is not stale")
	}
	snap := emptySnapshot(t)
	c.Snapshot, c.Query = snap, fleet.NewQuery(snap)
	if !h.stale(c) {
		t.Fatal("the banner did not notice a different snapshot")
	}
}

// TestHealthBannerAnimatesOnlyWhenItHasCause covers both gates: motion off, and
// a fleet with nothing wrong. Either one holds the clock still.
func TestHealthBannerAnimatesOnlyWhenItHasCause(t *testing.T) {
	c := newLoadedContext(t)
	h := newHealthBanner(c)
	if h.animating(c) {
		t.Error("motion is off, so nothing animates")
	}
	c.Anim = true
	if !h.animating(c) {
		t.Error("a fleet needing attention should animate")
	}
	h.attention = 0
	if h.animating(c) {
		t.Error("a healthy fleet must hold still")
	}
}

func TestHealthBannerViewIsTwoLines(t *testing.T) {
	c := newLoadedContext(t)
	v := newHealthBanner(c).view(c)
	if n := strings.Count(v, "\n"); n != 1 {
		t.Fatalf("the banner drew %d newlines, want exactly 1:\n%s", n+1, v)
	}
}

// TestHealthBannerHeadlineCountsUp covers the animated path and the still one.
// The still one is the assertion that matters: with motion off the figures are
// the real ones on the very first frame, so a reader who disabled animation
// never sees a wrong number.
func TestHealthBannerHeadlineCountsUp(t *testing.T) {
	c := newLoadedContext(t)
	h := newHealthBanner(c)

	still := h.headline(c)
	if !strings.Contains(still, "2 services") {
		t.Fatalf("headline = %q, want the real service count immediately", still)
	}

	c.Anim = true
	if first := h.headline(c); strings.Contains(first, "2 services") {
		t.Fatalf("the first animated frame = %q, want it to start below the real count", first)
	}
	c.Frame = h.startFrame + framesFor(countUpDuration)
	if last := h.headline(c); !strings.Contains(last, "2 services") {
		t.Fatalf("the last animated frame = %q, want the real count", last)
	}
}

// TestHealthBannerHeadlineWhenNothingIsWrong covers the other side of the
// attention branch, including the singular form.
func TestHealthBannerHeadlineWhenNothingIsWrong(t *testing.T) {
	c := newLoadedContext(t)
	h := newHealthBanner(c)

	h.attention = 0
	if got := h.headline(c); !strings.Contains(got, "nothing needs attention") {
		t.Errorf("headline = %q, want the all-clear", got)
	}
	h.attention = 1
	if got := h.headline(c); !strings.Contains(got, "1 service need") {
		t.Errorf("headline = %q, want the singular noun", got)
	}
	h.attention = 2
	if got := h.headline(c); !strings.Contains(got, "2 services need") {
		t.Errorf("headline = %q, want the plural noun", got)
	}
}

// TestHealthBannerStripe covers the three shapes the second line takes: a bar
// with its legend, the legend alone when the terminal is too narrow for a bar
// that would have to hide a bucket, and the ungraded sentence.
func TestHealthBannerStripe(t *testing.T) {
	c := newLoadedContext(t)
	h := newHealthBanner(c)

	wide := h.stripe(c)
	if !strings.Contains(wide, glyphBarFull) {
		t.Errorf("stripe = %q, want a bar", wide)
	}
	if !strings.Contains(wide, "Compliant") {
		t.Errorf("stripe = %q, want the legend beside it", wide)
	}

	c.Width = 20
	narrow := h.stripe(c)
	if strings.Contains(narrow, glyphBarFull) || strings.Contains(narrow, glyphBarEmpty) {
		t.Errorf("stripe at 20 columns = %q, want the legend without a bar", narrow)
	}

	// The bar grows in with the rest of the banner when motion is on.
	c.Width, c.Anim = 100, true
	if strings.Count(h.stripe(c), glyphBarFull) >= strings.Count(wide, glyphBarFull) {
		t.Error("the first animated frame should have fewer lit cells than the finished bar")
	}
}

// TestHealthBannerUngraded covers both halves of the sentence a fleet with no
// targets gets. The degraded half exists so "no targets" is not read as "all
// clear" when the reason is that a source could not be reached.
func TestHealthBannerUngraded(t *testing.T) {
	c := newContextOver(t, emptySnapshot(t))
	h := newHealthBanner(c)
	if h.targets != 0 {
		t.Fatalf("the empty fixture has %d targets, want 0", h.targets)
	}
	plainly := h.stripe(c)
	if !strings.Contains(plainly, "compliance is ungraded") {
		t.Errorf("stripe = %q, want the ungraded sentence", plainly)
	}
	if strings.Contains(plainly, "degraded") {
		t.Errorf("stripe = %q, want no degradation claim for healthy sources", plainly)
	}

	h.degraded, h.sources = 1, 1
	if got := h.ungraded(); !strings.Contains(got, "1 of 1 source is degraded") {
		t.Errorf("ungraded = %q, want the singular degraded clause", got)
	}
	h.degraded, h.sources = 2, 3
	if got := h.ungraded(); !strings.Contains(got, "2 of 3 sources are degraded") {
		t.Errorf("ungraded = %q, want the plural degraded clause", got)
	}
}

// TestHealthBannerBarFillsToProgress checks the growth is in lit cells and not
// in width: the stripe occupies the same columns from the first frame, so the
// legend beside it does not slide.
func TestHealthBannerBarFillsToProgress(t *testing.T) {
	h := healthBanner{targets: 4, counts: []int{0, 1, 0, 0, 3, 0, 0, 0}}
	const width = 16
	prev := -1
	for _, p := range []float64{0, 0.5, 1} {
		got := h.bar(width, p)
		if n := lipgloss.Width(got); n != width {
			t.Fatalf("at p=%v the bar is %d cells wide, want %d", p, n, width)
		}
		lit := strings.Count(got, glyphBarFull)
		if lit < prev {
			t.Fatalf("at p=%v the bar lost lit cells: %d after %d", p, lit, prev)
		}
		prev = lit
	}
	if prev != width {
		t.Fatalf("a finished bar has %d lit cells, want all %d", prev, width)
	}
}

// TestHealthBannerLegendStopsAtThree keeps the legend from turning into a table.
func TestHealthBannerLegendStopsAtThree(t *testing.T) {
	h := healthBanner{targets: 8, counts: []int{1, 1, 1, 1, 1, 1, 1, 1}}
	if got := strings.Count(h.legend(), glyphDot); got != 3 {
		t.Fatalf("the legend named %d buckets, want 3", got)
	}
	// Worst first: the first bucket named is the most severe non-empty one.
	if !strings.Contains(h.legend(), "Invalid") {
		t.Errorf("legend = %q, want the worst bucket first", h.legend())
	}
	if got := (healthBanner{}).legend(); got != "" {
		t.Errorf("an empty legend = %q, want nothing", got)
	}
}

// TestAllocateSegments is the arithmetic the bar rests on: the segments always
// sum to exactly the width, and no non-empty bucket is ever rounded away.
func TestAllocateSegments(t *testing.T) {
	for _, tt := range []struct {
		name   string
		counts []int
		total  int
		width  int
		want   []int
	}{
		{"an even split needs no correction", []int{2, 2}, 4, 8, []int{4, 4}},
		{"a lone bucket takes the whole bar", []int{5}, 5, 6, []int{6}},
		{"empty buckets take nothing", []int{0, 3, 0}, 3, 6, []int{0, 6, 0}},
		{
			// The case the floor exists for: two invalid out of four hundred is
			// 0.16 of a cell, and rounding it away hides the only bucket that
			// matters. The overflow comes back off the largest segment.
			"a tiny bucket keeps a cell", []int{2, 398}, 400, 32, []int{1, 31},
		},
		{
			// Every bucket floored at once overshoots by more than one, so the
			// trim loop has to run several times.
			"many tiny buckets are all trimmed back", []int{1, 1, 1, 1, 1, 1, 1, 1}, 8, 8,
			[]int{1, 1, 1, 1, 1, 1, 1, 1},
		},
		{"rounding down leaves cells for the largest", []int{1, 1, 1}, 3, 8, []int{4, 2, 2}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := allocateSegments(tt.counts, tt.total, tt.width)
			if total(got) != tt.width {
				t.Fatalf("allocateSegments(%v,%d,%d) = %v, sums to %d not %d",
					tt.counts, tt.total, tt.width, got, total(got), tt.width)
			}
			for i, n := range tt.counts {
				if n > 0 && got[i] < 1 {
					t.Fatalf("bucket %d holds %d but was allocated no cells: %v", i, n, got)
				}
			}
			if tt.want != nil && !equalInts(got, tt.want) {
				t.Fatalf("allocateSegments(%v,%d,%d) = %v, want %v",
					tt.counts, tt.total, tt.width, got, tt.want)
			}
		})
	}
}

// TestAllocateSegmentsCoversEveryRealDistribution runs the allocator over every
// bar width the stripe can ask for against a fleet holding one of everything,
// because the sum-to-width invariant is the one a wrong trim breaks silently.
func TestAllocateSegmentsCoversEveryRealDistribution(t *testing.T) {
	counts := []int{2, 5, 1, 9, 300, 1, 40, 3}
	population := total(counts)
	for w := bannerMinBarWidth; w <= 32; w++ {
		got := allocateSegments(counts, population, w)
		sum := 0
		for i, n := range got {
			if counts[i] > 0 && n < 1 {
				t.Fatalf("width %d: bucket %d vanished: %v", w, i, got)
			}
			sum += n
		}
		if sum != w {
			t.Fatalf("width %d: segments sum to %d: %v", w, sum, got)
		}
	}
}

func TestArgmax(t *testing.T) {
	if got := argmax([]int{1, 9, 3}); got != 1 {
		t.Errorf("argmax = %d, want 1", got)
	}
	// A tie takes the first, which is what keeps the trim loop deterministic.
	if got := argmax([]int{4, 4, 4}); got != 0 {
		t.Errorf("argmax on a tie = %d, want 0", got)
	}
	if got := argmax([]int{7}); got != 0 {
		t.Errorf("argmax of one element = %d, want 0", got)
	}
}

// TestSpread covers the padding and the case where there is none to give: the
// content survives and the line wraps, rather than a count being cut in half.
func TestSpread(t *testing.T) {
	got := spread("left", "right", 20)
	if lipgloss.Width(got) != 20 {
		t.Errorf("spread = %q, %d cells wide, want 20", got, lipgloss.Width(got))
	}
	if !strings.HasPrefix(got, "left") || !strings.HasSuffix(got, "right") {
		t.Errorf("spread = %q, want left flush and right flush", got)
	}
	tight := spread("left", "right", 4)
	if !strings.Contains(tight, "left") || !strings.Contains(tight, "right") {
		t.Errorf("spread with no room = %q, want both parts kept whole", tight)
	}
	if strings.Contains(tight, "  ") {
		t.Errorf("spread with no room = %q, want a single space", tight)
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "service", "services"); got != "service" {
		t.Errorf("plural(1) = %q, want the singular", got)
	}
	for _, n := range []int{0, 2, 17} {
		if got := plural(n, "service", "services"); got != "services" {
			t.Errorf("plural(%d) = %q, want the plural", n, got)
		}
	}
}

func total(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
