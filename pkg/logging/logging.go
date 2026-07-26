// Package logging builds slog loggers and carries one on a context, so app and
// library code log through a per-invocation logger instead of the process-global
// slog default. The CLI configures one logger per command Execute (respecting
// --verbose/--quiet and the command's stderr) and threads it through the context
// the app and pkg layers already receive; no code mutates slog's default.
package logging

import (
	"context"
	"io"
	"log/slog"
)

// New returns a text logger writing to w. When verbose is true it emits Debug
// and above; otherwise Warn and above, which silences informational output.
func New(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}

type ctxKey struct{}

// WithLogger returns ctx carrying l for retrieval by LoggerFromContext.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// LoggerFromContext returns the logger carried by ctx, or slog.Default() when
// none is present (library callers that never set one, tests, the operator). It
// never returns nil. slog.Default() is only ever read here, never replaced, so
// concurrent callers do not race on a process global.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return slog.Default()
}
