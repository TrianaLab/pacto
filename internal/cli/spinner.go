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
	if format != "text" || !isTerminal(w) {
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
func (s *spinner) Stop() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	<-s.done
	_, _ = fmt.Fprint(s.w, "\r\033[K")
}

func frameGlyph(i int, color bool) string {
	g := spinnerFrames[i]
	if color {
		return "\033[36m" + g + "\033[0m"
	}
	return g
}

func writeFrame(w io.Writer, glyph, label string) {
	_, _ = fmt.Fprintf(w, "\r%s %s", glyph, label)
}
