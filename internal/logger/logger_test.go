package logger

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestSetup_Verbose(t *testing.T) {
	var buf bytes.Buffer
	Setup(&buf, true)

	slog.Debug("debug message")
	if buf.Len() == 0 {
		t.Error("expected debug message in verbose mode")
	}
}

func TestSetup_Quiet(t *testing.T) {
	var buf bytes.Buffer
	Setup(&buf, false)

	slog.Debug("debug message")
	slog.Info("info message")
	if buf.Len() != 0 {
		t.Errorf("expected no output in quiet mode, got %q", buf.String())
	}
}

func TestSetup_QuietAllowsWarn(t *testing.T) {
	var buf bytes.Buffer
	Setup(&buf, false)

	slog.Warn("warning message")
	if buf.Len() == 0 {
		t.Error("expected warn message even in quiet mode")
	}
}

func TestSetup_VerboseAllowsInfo(t *testing.T) {
	var buf bytes.Buffer
	Setup(&buf, true)

	slog.Info("info message")
	if buf.Len() == 0 {
		t.Error("expected info message in verbose mode")
	}
}
