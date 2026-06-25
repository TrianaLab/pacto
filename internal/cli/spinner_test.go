package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// withTTY forces isTerminal to report v and restores it after the test.
func withTTY(t *testing.T, v bool) {
	t.Helper()
	orig := isTerminal
	isTerminal = func(io.Writer) bool { return v }
	t.Cleanup(func() { isTerminal = orig })
}

func TestDefaultIsTerminal(t *testing.T) {
	// no Fd() -> short-circuits to false
	if defaultIsTerminal(&bytes.Buffer{}) {
		t.Fatal("buffer is not a terminal")
	}
	// os.Stdout has Fd(); under `go test` it is not a TTY, so this returns
	// false, but it executes the term.IsTerminal line for coverage.
	_ = defaultIsTerminal(os.Stdout)
}

func TestStartSpinnerNoopWhenNotText(t *testing.T) {
	withTTY(t, true)
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "json", "Pulling")
	sp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("expected no output for json format, got %q", buf.String())
	}
}

func TestStartSpinnerNoopWhenNotTTY(t *testing.T) {
	withTTY(t, false)
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "Pulling")
	sp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("expected no output without TTY, got %q", buf.String())
	}
}

func TestStartSpinnerAnimatesAndClears(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "") // ensure color path
	orig := spinnerInterval
	spinnerInterval = time.Millisecond
	t.Cleanup(func() { spinnerInterval = orig })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "Pulling")
	time.Sleep(10 * time.Millisecond) // let several ticks fire
	sp.Stop()

	out := buf.String()
	if !strings.Contains(out, "Pulling") {
		t.Fatalf("expected label in output, got %q", out)
	}
	if !strings.Contains(out, "\033[36m") {
		t.Fatalf("expected cyan color in output, got %q", out)
	}
	if !strings.Contains(out, "\r\033[K") {
		t.Fatalf("expected line-clear on stop, got %q", out)
	}
}

func TestUseColorRespectsNoColor(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "1")
	if useColor(&bytes.Buffer{}) {
		t.Fatal("NO_COLOR set should disable color")
	}
	t.Setenv("NO_COLOR", "")
	if !useColor(&bytes.Buffer{}) {
		t.Fatal("TTY + no NO_COLOR should enable color")
	}
}

func TestStartSpinnerNoColorFrame(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp := startSpinner(cmd, "text", "Pushing")
	sp.Stop()
	out := buf.String()
	if strings.Contains(out, "\033[36m") {
		t.Fatalf("expected no color with NO_COLOR set, got %q", out)
	}
	if !strings.Contains(out, "Pushing") {
		t.Fatalf("expected label, got %q", out)
	}
}
