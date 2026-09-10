package tui

import (
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// frameInterval is how often an animating frame repaints: ~16fps. Fast enough
// that a slide reads as motion rather than as a jump, slow enough that a screen
// full of pulsing dots is not a busy loop. It is a var so a test can shrink it.
var frameInterval = 60 * time.Millisecond

// frameMsg is one animation frame. Nothing but the animation clock sends it.
type frameMsg struct{}

// tick schedules the next frame.
func tick() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameMsg{} })
}

// animator is the optional interface a screen implements to say it has motion
// on screen right now. The root Model polls it after every frame and stops the
// clock when every screen says no, so an idle TUI costs nothing. A screen that
// does not implement it never animates.
type animator interface {
	animating(c *Context) bool
}

// screenAnimating reports whether s has motion in flight.
func screenAnimating(s screen, c *Context) bool {
	a, ok := s.(animator)
	return ok && a.animating(c)
}

// phase is the animation clock as a rising count of frames. Screens derive
// everything from it rather than each holding a time.Time: one counter keeps
// every animation on the same beat, and a test can drive it by hand.
//
// pulse turns it into a 0..1 triangle wave over period frames -- a triangle
// rather than a sine because the ANSI colour cube quantises the ends of a sine
// into a visible pause at full and empty.
func pulse(frame, period int) float64 {
	if period <= 0 {
		return 0
	}
	x := float64(frame%period) / float64(period)
	if x < 0.5 {
		return x * 2
	}
	return (1 - x) * 2
}

// easeOutCubic is the curve every finite animation uses: fast at the start,
// settling at the end. A linear slide looks mechanical; this looks placed.
func easeOutCubic(t float64) float64 {
	t = clamp01(t)
	u := 1 - t
	return 1 - u*u*u
}

func clamp01(t float64) float64 {
	return math.Max(0, math.Min(1, t))
}

// progressAt returns how far a finite animation started at startFrame has run,
// as 0..1 over durFrames. It reaches exactly 1 and stays there.
func progressAt(frame, startFrame, durFrames int) float64 {
	if durFrames <= 0 {
		return 1
	}
	return clamp01(float64(frame-startFrame) / float64(durFrames))
}

// framesFor converts a wall-clock duration to whole frames, never fewer than 1
// so a duration shorter than a frame still animates for one.
func framesFor(d time.Duration) int {
	n := int(d / frameInterval)
	if n < 1 {
		return 1
	}
	return n
}

// countUp animates an integer from zero to n. The banner's headline numbers use
// it so a fleet arriving on screen reads as something being counted rather than
// something that was always there.
func countUp(n int, p float64) int {
	return int(math.Round(float64(n) * easeOutCubic(p)))
}

// spinnerFrames is the braille spinner the CLI already uses, so a reader who has
// watched `pacto pull` sees the same glyph here.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerAt returns the spinner glyph for a frame.
func spinnerAt(frame int) string { return spinnerFrames[frame%len(spinnerFrames)] }

// bar renders a proportional bar of width cells, filled cells first. It is the
// health banner's only chart, and it is deliberately a bar rather than a
// percentage: a fleet of three and a fleet of three hundred both read at a
// glance, and neither invites a false precision the numbers beside it do not
// have.
func bar(filled, width int) string {
	if width <= 0 {
		return ""
	}
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat(glyphBarFull, filled) + strings.Repeat(glyphBarEmpty, width-filled)
}

// revealLines wipes body into place from the top: at p it shows the first
// ceil(n*p) lines and blanks the rest. At p >= 1 it returns body untouched, so
// a finished transition costs nothing and a disabled one is one branch away.
//
// A wipe rather than a slide, because a slide has to move text sideways and
// this package's bodies are already exactly one terminal wide -- indenting them
// pushes the last cells off the edge and the renderer wraps the overflow onto
// the next row, so the frame grows mid-animation and the footer walks down the
// screen. Blanking whole lines keeps the line COUNT fixed, which is the only
// property the surrounding header and footer depend on.
func revealLines(body string, p float64) string {
	if p >= 1 {
		return body
	}
	lines := strings.Split(body, "\n")
	shown := int(math.Ceil(float64(len(lines)) * easeOutCubic(p)))
	for i := shown; i < len(lines); i++ {
		lines[i] = ""
	}
	return strings.Join(lines, "\n")
}

// sweep is an indeterminate progress bar: a lit band of sweepBand cells moving
// back and forth across width. Indeterminate on purpose -- a snapshot load has
// no denominator to divide by, and a bar that fills at a made-up rate is a
// worse lie than one that plainly says "still working".
func sweep(frame, width int) string {
	if width <= 0 {
		return ""
	}
	const band = 4
	travel := width - band
	if travel < 1 {
		return strings.Repeat(glyphBarFull, width)
	}
	start := int(math.Round(pulse(frame, sweepPeriod) * float64(travel)))
	cells := make([]string, width)
	for i := range cells {
		if i >= start && i < start+band {
			cells[i] = glyphBarFull
			continue
		}
		cells[i] = glyphBarEmpty
	}
	return strings.Join(cells, "")
}

// sweepPeriod is how many frames one there-and-back sweep takes: about 1.7s,
// slow enough to read as deliberate rather than frantic.
const sweepPeriod = 28

// pulsePeriod is the beat an attention marker throbs at, about 1.6s.
const pulsePeriod = 26
