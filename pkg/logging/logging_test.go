package logging

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
)

func TestNew_Verbose(t *testing.T) {
	var buf bytes.Buffer
	lg := New(&buf, true)

	lg.Debug("debug message")
	if buf.Len() == 0 {
		t.Error("expected debug message in verbose mode")
	}
}

func TestNew_VerboseAllowsInfo(t *testing.T) {
	var buf bytes.Buffer
	lg := New(&buf, true)

	lg.Info("info message")
	if buf.Len() == 0 {
		t.Error("expected info message in verbose mode")
	}
}

func TestNew_Quiet(t *testing.T) {
	var buf bytes.Buffer
	lg := New(&buf, false)

	lg.Debug("debug message")
	lg.Info("info message")
	if buf.Len() != 0 {
		t.Errorf("expected no output in quiet mode, got %q", buf.String())
	}
}

func TestNew_QuietAllowsWarn(t *testing.T) {
	var buf bytes.Buffer
	lg := New(&buf, false)

	lg.Warn("warning message")
	if buf.Len() == 0 {
		t.Error("expected warn message even in quiet mode")
	}
}

func TestWithLogger_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	lg := New(&buf, true)

	ctx := WithLogger(context.Background(), lg)
	if got := LoggerFromContext(ctx); got != lg {
		t.Errorf("expected the logger stored on the context, got a different one")
	}
}

func TestLoggerFromContext_DefaultWhenAbsent(t *testing.T) {
	if got := LoggerFromContext(context.Background()); got != slog.Default() {
		t.Error("expected slog.Default() when no logger is on the context")
	}
}

//nolint:staticcheck // deliberately passing a nil context to exercise the guard.
func TestLoggerFromContext_DefaultWhenNilContext(t *testing.T) {
	if got := LoggerFromContext(nil); got != slog.Default() {
		t.Error("expected slog.Default() for a nil context")
	}
}

func TestLoggerFromContext_DefaultWhenNilLogger(t *testing.T) {
	ctx := WithLogger(context.Background(), nil)
	if got := LoggerFromContext(ctx); got != slog.Default() {
		t.Error("expected slog.Default() when a nil logger is stored")
	}
}
