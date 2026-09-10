package cli

import (
	"fmt"
	"sync/atomic"

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
	n := new(atomic.Int64)
	return startSpinnerLabel(cmd, format, func() string { return progressLabel(base, n.Load()) }), n
}
