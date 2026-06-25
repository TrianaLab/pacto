package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestProgressLabel(t *testing.T) {
	if got := progressLabel("Resolving deps…", 7); got != "Resolving deps… 7" {
		t.Fatalf("got %q", got)
	}
	if got := progressLabel("X", 0); got != "X" {
		t.Fatalf("zero count should omit the number: got %q", got)
	}
}

func TestStartSpinnerCountedNoopWhenNotText(t *testing.T) {
	withTTY(t, true)
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp, count := startSpinnerCounted(cmd, "json", "Resolving")
	count.Add(1) // counter still usable
	sp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("expected no output for json, got %q", buf.String())
	}
}

func TestStartSpinnerCountedNoopWhenAnimDisabled(t *testing.T) {
	withTTY(t, true)
	t.Setenv("PACTO_NO_ANIM", "1")
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp, count := startSpinnerCounted(cmd, "text", "Resolving")
	count.Add(1) // counter still usable even when suppressed
	sp.Stop()
	if buf.Len() != 0 {
		t.Fatalf("PACTO_NO_ANIM should suppress the counted spinner, got %q", buf.String())
	}
}

func TestStartSpinnerCountedAnimatesLiveCount(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "")
	orig := spinnerInterval
	spinnerInterval = time.Millisecond
	t.Cleanup(func() { spinnerInterval = orig })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	sp, count := startSpinnerCounted(cmd, "text", "Resolving deps")
	count.Add(3)
	time.Sleep(10 * time.Millisecond) // let ticks render the bumped count
	sp.Stop()

	out := buf.String()
	if !strings.Contains(out, "Resolving deps") {
		t.Fatalf("expected base label, got %q", out)
	}
	if !strings.Contains(out, "Resolving deps 3") {
		t.Fatalf("expected live count 3 in output, got %q", out)
	}
	if !strings.Contains(out, "\r\033[K") {
		t.Fatalf("expected line-clear on stop, got %q", out)
	}
}
