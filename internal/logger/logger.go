// Package logger configures the global slog logger based on verbosity.
package logger

import (
	"io"
	"log/slog"
)

// Setup configures the default slog logger. When verbose is true, messages at
// Debug level and above are emitted to w. When verbose is false, only Warn
// level and above are emitted, which effectively silences informational output.
func Setup(w io.Writer, verbose bool) {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
