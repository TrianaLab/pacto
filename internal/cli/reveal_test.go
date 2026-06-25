package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v2/internal/app"
)

func TestBannerFrameStatic(t *testing.T) {
	full := bannerFrame(1<<30, true)
	if !strings.Contains(full, "\033[36m") || !strings.Contains(full, "service contracts") {
		t.Fatal("full banner missing parts")
	}
	if bannerFrame(0, true) == full {
		t.Fatal("step 0 should be partial")
	}
}

func TestRevealEmitsFirstAndLast(t *testing.T) {
	orig := revealStagger
	revealStagger = time.Millisecond
	t.Cleanup(func() { revealStagger = orig })
	var buf bytes.Buffer
	reveal(&buf, bannerFrame, len(bannerLines(true)), true)
	if !strings.Contains(buf.String(), "service contracts") {
		t.Fatal("reveal must end on full frame")
	}
}

func TestInitLines(t *testing.T) {
	got := initLines(&app.InitResult{Dir: "svc", Path: "svc/pacto.yaml"})
	want := []string{"Created svc/", "  svc/pacto.yaml", "  svc/interfaces/", "  svc/configuration/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestRevealInitOffTTY(t *testing.T) {
	withTTY(t, false)
	var buf bytes.Buffer
	revealInit(&buf, initLines(&app.InitResult{Dir: "svc", Path: "svc/pacto.yaml"}))
	if buf.String() != "Created svc/\n  svc/pacto.yaml\n  svc/interfaces/\n  svc/configuration/\n" {
		t.Fatalf("off-TTY init must be byte-identical: %q", buf.String())
	}
}

func TestRevealInitTTY(t *testing.T) {
	withTTY(t, true)
	t.Setenv("NO_COLOR", "")
	t.Setenv("PACTO_NO_ANIM", "")
	animDisabled = false
	t.Cleanup(func() { animDisabled = false })
	orig := revealStagger
	revealStagger = time.Millisecond
	t.Cleanup(func() { revealStagger = orig })
	var buf bytes.Buffer
	revealInit(&buf, initLines(&app.InitResult{Dir: "svc", Path: "svc/pacto.yaml"}))
	if !strings.Contains(buf.String(), "✓") || !strings.Contains(buf.String(), "\033[32m") {
		t.Fatal("TTY init should show green ✓")
	}
}

func TestRevealInitEmptyLines(t *testing.T) {
	var buf bytes.Buffer
	revealInit(&buf, []string{})
	if buf.Len() != 0 {
		t.Fatal("empty lines should produce no output")
	}
}
