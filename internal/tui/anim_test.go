package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// nearly compares two floats with the tolerance appropriate to a 0..1 ratio.
func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestTickSendsAFrame(t *testing.T) {
	cmd := tick()
	if cmd == nil {
		t.Fatal("tick() returned nil")
	}
	if _, ok := cmd().(frameMsg); !ok {
		t.Fatalf("tick() sent %T, want frameMsg", cmd())
	}
}

// TestScreenAnimatingIsOptional covers both sides of the optional interface: a
// screen that does not implement animator must never be asked to animate, which
// is what keeps the clock off for the screens that have no motion.
func TestScreenAnimatingIsOptional(t *testing.T) {
	c := &Context{Anim: true}
	if screenAnimating(stubScreen{}, c) {
		t.Fatal("a screen with no animating method reported motion")
	}
	if !screenAnimating(loadingScreen{}, c) {
		t.Fatal("the loading screen always has motion")
	}
}

// TestPulseIsATriangle walks a whole period: up to 1 at the halfway point, back
// to 0 at the end, and symmetric about the peak.
func TestPulseIsATriangle(t *testing.T) {
	const period = 10
	if got := pulse(0, period); !nearly(got, 0) {
		t.Errorf("pulse at the start = %v, want 0", got)
	}
	if got := pulse(period/2, period); !nearly(got, 1) {
		t.Errorf("pulse at the peak = %v, want 1", got)
	}
	if got := pulse(period, period); !nearly(got, 0) {
		t.Errorf("pulse a full period on = %v, want 0 again", got)
	}
	for i := 1; i < period/2; i++ {
		up, down := pulse(i, period), pulse(period-i, period)
		if !nearly(up, down) {
			t.Errorf("pulse(%d)=%v is not the mirror of pulse(%d)=%v", i, up, period-i, down)
		}
	}
	// A zero or negative period is a caller bug, not a divide by zero.
	if got := pulse(3, 0); got != 0 {
		t.Errorf("pulse with a zero period = %v, want 0", got)
	}
	if got := pulse(3, -4); got != 0 {
		t.Errorf("pulse with a negative period = %v, want 0", got)
	}
}

// TestEaseOutCubicDecelerates pins the property the curve is chosen for: more of
// the distance is covered in the first half than the second.
func TestEaseOutCubicDecelerates(t *testing.T) {
	if got := easeOutCubic(0); !nearly(got, 0) {
		t.Errorf("easeOutCubic(0) = %v, want 0", got)
	}
	if got := easeOutCubic(1); !nearly(got, 1) {
		t.Errorf("easeOutCubic(1) = %v, want 1", got)
	}
	if got := easeOutCubic(0.5); got <= 0.5 {
		t.Errorf("easeOutCubic(0.5) = %v, want it past halfway", got)
	}
	// Out-of-range input is clamped rather than extrapolated.
	if got := easeOutCubic(-1); !nearly(got, 0) {
		t.Errorf("easeOutCubic(-1) = %v, want 0", got)
	}
	if got := easeOutCubic(2); !nearly(got, 1) {
		t.Errorf("easeOutCubic(2) = %v, want 1", got)
	}
}

func TestClamp01(t *testing.T) {
	for _, tt := range []struct{ in, want float64 }{
		{-0.5, 0}, {0, 0}, {0.25, 0.25}, {1, 1}, {1.5, 1},
	} {
		if got := clamp01(tt.in); !nearly(got, tt.want) {
			t.Errorf("clamp01(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestProgressAt covers the three answers it can give: not started, partway and
// finished, plus the degenerate duration that must read as already done rather
// than divide by zero.
func TestProgressAt(t *testing.T) {
	for _, tt := range []struct {
		name              string
		frame, start, dur int
		want              float64
	}{
		{"before the start", 4, 10, 8, 0},
		{"at the start", 10, 10, 8, 0},
		{"halfway", 14, 10, 8, 0.5},
		{"exactly done", 18, 10, 8, 1},
		{"long past done", 900, 10, 8, 1},
		{"a zero duration is already done", 0, 0, 0, 1},
		{"a negative duration is already done", 0, 0, -3, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := progressAt(tt.frame, tt.start, tt.dur); !nearly(got, tt.want) {
				t.Errorf("progressAt(%d,%d,%d) = %v, want %v", tt.frame, tt.start, tt.dur, got, tt.want)
			}
		})
	}
}

func TestFramesForNeverReturnsZero(t *testing.T) {
	if got := framesFor(10 * frameInterval); got != 10 {
		t.Errorf("framesFor(10 intervals) = %d, want 10", got)
	}
	// Anything shorter than one frame still animates for one frame rather than
	// finishing before it is drawn.
	for _, d := range []time.Duration{frameInterval - 1, 0, -time.Second} {
		if got := framesFor(d); got != 1 {
			t.Errorf("framesFor(%v) = %d, want 1", d, got)
		}
	}
}

// TestCountUpReachesTheRealNumber guards the property that matters: whatever the
// curve does in between, the final frame shows the true count.
func TestCountUpReachesTheRealNumber(t *testing.T) {
	if got := countUp(0, 0.5); got != 0 {
		t.Errorf("countUp(0, ...) = %d, want 0", got)
	}
	if got := countUp(120, 0); got != 0 {
		t.Errorf("countUp at p=0 = %d, want 0", got)
	}
	if got := countUp(120, 1); got != 120 {
		t.Errorf("countUp at p=1 = %d, want 120", got)
	}
	if got := countUp(120, 0.5); got <= 0 || got >= 120 {
		t.Errorf("countUp halfway = %d, want it strictly between 0 and 120", got)
	}
}

func TestSpinnerAtCyclesAndNeverPanics(t *testing.T) {
	if got := spinnerAt(0); got != spinnerFrames[0] {
		t.Errorf("spinnerAt(0) = %q, want %q", got, spinnerFrames[0])
	}
	if spinnerAt(len(spinnerFrames)) != spinnerAt(0) {
		t.Error("the spinner does not wrap at the end of its cycle")
	}
	// The frame counter only rises, but a large one must still index safely.
	if got := spinnerAt(1_000_003); got == "" {
		t.Error("spinnerAt returned nothing for a large frame")
	}
}

func TestBar(t *testing.T) {
	for _, tt := range []struct {
		name          string
		filled, width int
		want          string
	}{
		{"empty", 0, 3, strings.Repeat(glyphBarEmpty, 3)},
		{"partial", 1, 3, glyphBarFull + strings.Repeat(glyphBarEmpty, 2)},
		{"full", 3, 3, strings.Repeat(glyphBarFull, 3)},
		{"over-filled is clamped", 9, 3, strings.Repeat(glyphBarFull, 3)},
		{"negative fill is clamped", -9, 3, strings.Repeat(glyphBarEmpty, 3)},
		{"no width draws nothing", 2, 0, ""},
		{"negative width draws nothing", 2, -4, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := bar(tt.filled, tt.width); got != tt.want {
				t.Errorf("bar(%d,%d) = %q, want %q", tt.filled, tt.width, got, tt.want)
			}
		})
	}
}

// TestRevealLinesKeepsTheLineCount is the whole reason this is a wipe and not a
// slide: the header and footer around it are positioned by line count, so a
// half-revealed body must occupy exactly as many lines as a finished one.
func TestRevealLinesKeepsTheLineCount(t *testing.T) {
	body := "one\ntwo\nthree\nfour\nfive\nsix"
	want := strings.Count(body, "\n")
	for _, p := range []float64{-1, 0, 0.25, 0.5, 0.99, 1, 2} {
		got := revealLines(body, p)
		if n := strings.Count(got, "\n"); n != want {
			t.Fatalf("at p=%v the body has %d newlines, want %d", p, n, want)
		}
	}
	if got := revealLines(body, 1); got != body {
		t.Error("a finished reveal must return the body untouched")
	}
	if got := revealLines(body, 0); strings.Contains(got, "one") {
		t.Errorf("nothing should be revealed at p=0, got %q", got)
	}
	// Partway through, an early line is on screen and a late one is not.
	half := revealLines(body, 0.3)
	if !strings.Contains(half, "one") {
		t.Errorf("the first line should lead the reveal, got %q", half)
	}
	if strings.Contains(half, "six") {
		t.Errorf("the last line should not have arrived yet, got %q", half)
	}
}

// TestSweepMovesAndStaysWidth covers the travelling band: fixed width, a lit run
// that is somewhere different on a different frame, and the degenerate widths.
func TestSweepMovesAndStaysWidth(t *testing.T) {
	const width = 20
	first := sweep(0, width)
	if n := len([]rune(first)); n != width {
		t.Fatalf("sweep drew %d cells, want %d", n, width)
	}
	if !strings.Contains(first, glyphBarFull) {
		t.Fatalf("sweep drew no lit band: %q", first)
	}
	moved := false
	for f := 1; f < sweepPeriod; f++ {
		if sweep(f, width) != first {
			moved = true
			break
		}
	}
	if !moved {
		t.Error("the sweep never moved across a whole period")
	}
	// A width at or under the band length has nowhere to travel, so it is lit
	// solid rather than drawn with a negative offset.
	if got := sweep(3, 4); got != strings.Repeat(glyphBarFull, 4) {
		t.Errorf("sweep at band width = %q, want it solid", got)
	}
	if got := sweep(3, 0); got != "" {
		t.Errorf("sweep with no width = %q, want empty", got)
	}
	if got := sweep(3, -2); got != "" {
		t.Errorf("sweep with a negative width = %q, want empty", got)
	}
}

// stubScreen implements screen and nothing optional. It is the control for every
// "does the optional interface actually gate this" assertion.
type stubScreen struct{}

func (stubScreen) Title() string                              { return "stub" }
func (stubScreen) View(*Context) string                       { return "stub" }
func (stubScreen) Update(*Context, tea.Msg) (screen, tea.Cmd) { return stubScreen{}, nil }
