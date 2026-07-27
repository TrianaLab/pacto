package cli

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

// progressLabel renders the live count line, e.g. "Resolving deps 7". Before any
// dependency has resolved (done == 0) it returns just the base, so a fast/cached
// resolve that finishes between ticks never flashes a misleading "0". Pure.
func progressLabel(base string, done int64) string {
	if done == 0 {
		return base
	}
	return fmt.Sprintf("%s %d", base, done)
}

// startSpinnerCounted is like startSpinner but renders a live "<base> <n>" count
// that the caller bumps via the returned counter. The counter is safe to call
// from multiple goroutines (the resolver fires OnResolved concurrently). The
// spinner reads it atomically each tick. Same no-op contract as startSpinner:
// off-TTY / non-text returns a no-op spinner and a usable (but unread) counter.
func startSpinnerCounted(cmd *cobra.Command, format, base string) (*spinner, *atomic.Int64) {
	var n atomic.Int64
	w := cmd.ErrOrStderr()
	if format != "text" || !isTerminal(w) || !animationsEnabled(cmd) {
		return &spinner{}, &n
	}
	s := &spinner{
		w:     w,
		color: useColor(w),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	writeFrame(w, frameGlyph(0, s.color), progressLabel(base, n.Load())) // first frame
	go s.runCounted(base, &n)
	return s, &n
}

// runCounted ticks the spinner, re-rendering progressLabel(base, n.Load()) each
// tick so the live count tracks the counter. Shares stop/done with Stop/StopOK.
func (s *spinner) runCounted(base string, n *atomic.Int64) {
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
			writeFrame(s.w, frameGlyph(i, s.color), progressLabel(base, n.Load()))
		}
	}
}
