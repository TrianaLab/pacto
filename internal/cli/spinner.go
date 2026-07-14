package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerInterval is a var (not const) so tests can shrink it for deterministic ticks.
var spinnerInterval = 100 * time.Millisecond

// isTerminal reports whether w is a terminal. Overridable in tests.
var isTerminal = defaultIsTerminal

func defaultIsTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

// useColor reports whether ANSI color should be emitted to w.
func useColor(w io.Writer) bool {
	return os.Getenv("NO_COLOR") == "" && isTerminal(w)
}

// Animation gate. Set once in PersistentPreRunE; read directly on the help path.
var animDisabled bool

// animationsEnabled reports whether motion is allowed at all (--no-anim flag and
// PACTO_NO_ANIM env both off). It does NOT consider color or TTY — callers add
// those. This governs the spinner (motion regardless of color); animate adds
// color on top for color-only reveals.
func animationsEnabled() bool {
	return !animDisabled && os.Getenv("PACTO_NO_ANIM") == ""
}

// animate reports whether a colored animation should play on w: motion enabled
// AND color enabled (TTY + NO_COLOR unset).
func animate(w io.Writer) bool {
	return animationsEnabled() && useColor(w)
}

const (
	glyphCheck = "✓" // U+2713
	glyphCross = "✗" // U+2717

	// ansiIndigo is the brand accent (#6366F1) as a truecolor SGR; ansiReset clears it.
	ansiIndigo = "\033[38;2;99;102;241m"
	ansiReset  = "\033[0m"
)

// checkGlyph returns a green ✓ when color, else a plain ✓. Pure.
func checkGlyph(color bool) string {
	if color {
		return "\033[32m" + glyphCheck + "\033[0m"
	}
	return glyphCheck
}

// crossGlyph returns a red ✗ when color, else a plain ✗. Pure.
func crossGlyph(color bool) string {
	if color {
		return "\033[31m" + glyphCross + "\033[0m"
	}
	return glyphCross
}

// okGlyph returns "✓ " (green when color enabled for w) or "" off-TTY/no-color.
func okGlyph(w io.Writer) string {
	if !useColor(w) {
		return ""
	}
	return checkGlyph(true) + " "
}

// checkLine renders the spinner-success line, e.g. "✓ Pulled in 1.2s". Pure.
func checkLine(label string, d time.Duration, color bool) string {
	return fmt.Sprintf("%s %s in %s", checkGlyph(color), label, d.Round(100*time.Millisecond))
}

type spinner struct {
	w     io.Writer
	color bool
	stop  chan struct{}
	done  chan struct{}
}

// startSpinner animates on stderr until Stop is called. It is a no-op when
// format is not "text" or stderr is not a terminal, so JSON output, pipes and
// CI logs stay clean.
func startSpinner(cmd *cobra.Command, format, label string) *spinner {
	w := cmd.ErrOrStderr()
	if format != "text" || !isTerminal(w) || !animationsEnabled() {
		return &spinner{}
	}
	s := &spinner{
		w:     w,
		color: useColor(w),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	writeFrame(w, frameGlyph(0, s.color), label) // first frame synchronously
	go s.run(label)
	return s
}

func (s *spinner) run(label string) {
	defer close(s.done)
	t := time.NewTicker(spinnerInterval)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			i = (i + 1) % len(spinnerFrames)
			writeFrame(s.w, frameGlyph(i, s.color), label)
		}
	}
}

// Stop halts the spinner and clears its line. No-op on a no-op spinner.
// Must be called exactly once per startSpinner: a second call on a live
// spinner closes an already-closed channel and panics.
func (s *spinner) Stop() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	<-s.done
	_, _ = fmt.Fprint(s.w, "\r\033[K")
}

// StopOK halts the spinner, clears its line, then prints checkLine on success.
// No-op clear on a no-op spinner; the caller still gets the line printed.
func (s *spinner) StopOK(label string, start time.Time) {
	s.Stop() // existing clear of "\r\033[K"
	w := s.w
	if w == nil {
		return
	}
	_, _ = fmt.Fprintln(w, checkLine(label, time.Since(start), s.color))
}

func frameGlyph(i int, color bool) string {
	g := spinnerFrames[i]
	if color {
		return ansiIndigo + g + ansiReset
	}
	return g
}

func writeFrame(w io.Writer, glyph, label string) {
	_, _ = fmt.Fprintf(w, "\r%s %s", glyph, label)
}
