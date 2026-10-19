package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// ttyBuffer is a buffer that says it is a terminal.
type ttyBuffer struct{ bytes.Buffer }

func (*ttyBuffer) IsTerminal() bool { return true }

func TestIsTerminal(t *testing.T) {
	// no Fd() and no IsTerminal -> false
	if isTerminal(&bytes.Buffer{}) || !isTerminal(&ttyBuffer{}) {
		t.Fatal("a buffer is a terminal only when it says so")
	}
	// os.Stdout has Fd(); under `go test` it is not a TTY, so this returns
	// false, but it executes the term.IsTerminal line for coverage.
	_ = isTerminal(os.Stdout)
}

func TestStartSpinnerNoopWhenNotText(t *testing.T) {
	var buf ttyBuffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "json", "Pulling")
	sp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("expected no output for json format, got %q", buf.String())
	}
}

func TestStartSpinnerNoopWhenNotTTY(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "Pulling")
	sp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("expected no output without TTY, got %q", buf.String())
	}
}

func TestStartSpinnerNoopWhenAnimDisabled(t *testing.T) {
	var buf ttyBuffer
	cmd := &cobra.Command{}
	cmd.SetContext(withAnimDisabled(context.Background(), true))
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "Pulling")
	sp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("--no-anim should suppress the spinner, got %q", buf.String())
	}
}

func TestStartSpinnerAnimatesAndClears(t *testing.T) {
	t.Setenv("NO_COLOR", "") // ensure color path

	var buf ttyBuffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "Pulling")
	time.Sleep(2*spinnerInterval + spinnerInterval/2) // let several ticks fire
	sp.Stop()

	out := buf.String()
	if !strings.Contains(out, "Pulling") {
		t.Fatalf("expected label in output, got %q", out)
	}
	if !strings.Contains(out, ansiIndigo) {
		t.Fatalf("expected cyan color in output, got %q", out)
	}
	if !strings.Contains(out, "\r\033[K") {
		t.Fatalf("expected line-clear on stop, got %q", out)
	}
}

func TestUseColorRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if useColor(&ttyBuffer{}) {
		t.Fatal("NO_COLOR set should disable color")
	}
	t.Setenv("NO_COLOR", "")
	if !useColor(&ttyBuffer{}) {
		t.Fatal("TTY + no NO_COLOR should enable color")
	}
}

func TestStartSpinnerNoColorFrame(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf ttyBuffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "Pushing")
	sp.Stop()
	out := buf.String()
	if strings.Contains(out, ansiIndigo) {
		t.Fatalf("expected no color with NO_COLOR set, got %q", out)
	}
	if !strings.Contains(out, "Pushing") {
		t.Fatalf("expected label, got %q", out)
	}
}

func TestGlyphs(t *testing.T) {
	if checkGlyph(false) != "✓" || crossGlyph(false) != "✗" {
		t.Fatal("plain glyphs wrong")
	}
	if !strings.Contains(checkGlyph(true), "\033[32m") || !strings.Contains(crossGlyph(true), "\033[31m") {
		t.Fatal("colored glyphs missing ANSI")
	}
}

func TestOkGlyph(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if !strings.Contains(okGlyph(&ttyBuffer{}), "✓") {
		t.Fatal("TTY okGlyph should have check")
	}
	if okGlyph(&bytes.Buffer{}) != "" {
		t.Fatal("non-TTY okGlyph must be empty")
	}
}

func TestCheckLine(t *testing.T) {
	got := checkLine("Pulled", 1234*time.Millisecond, false)
	if got != "✓ Pulled in 1.2s" {
		t.Fatalf("got %q", got)
	}
}

func TestAnimate(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("PACTO_NO_ANIM", "")
	cmd := &cobra.Command{} // no context set -> not disabled (nil-ctx path)
	if !animate(cmd, &ttyBuffer{}) {
		t.Fatal("should animate on TTY")
	}
	cmd.SetContext(withAnimDisabled(context.Background(), true))
	if animate(cmd, &ttyBuffer{}) {
		t.Fatal("--no-anim must disable")
	}
	cmd.SetContext(withAnimDisabled(context.Background(), false))
	t.Setenv("PACTO_NO_ANIM", "1")
	if animate(cmd, &ttyBuffer{}) {
		t.Fatal("PACTO_NO_ANIM must disable")
	}
}

func TestStopOK(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	var buf ttyBuffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "x")
	sp.StopOK("Pulled", time.Now().Add(-time.Second))
	out := buf.String()
	if !strings.Contains(out, "✓") || !strings.Contains(out, "Pulled in") {
		t.Fatalf("StopOK line missing: %q", out)
	}
	// no-op spinner: no panic, no success line
	noop := startSpinner(&cobra.Command{}, "json", "x") // non-text -> no-op
	noop.StopOK("X", time.Now())
}
