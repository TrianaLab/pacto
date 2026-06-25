package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v2/internal/app"
)

func TestBannerStatic(t *testing.T) {
	full := bannerStatic(true)
	if !strings.Contains(full, "\033[36m") || !strings.Contains(full, "service contracts") {
		t.Fatal("colored full banner missing parts")
	}
	if strings.Contains(bannerStatic(false), "\033[36m") {
		t.Fatal("plain banner must not contain color codes")
	}
}

func TestBannerStaticNoDuplication(t *testing.T) {
	// The help banner is static: each row appears exactly once (guards against the
	// old cumulative-frame stacking bug).
	out := bannerStatic(true)
	if n := strings.Count(out, "██████╗  █████╗"); n != 1 {
		t.Fatalf("top banner row should appear once, got %d", n)
	}
	if !strings.Contains(out, "service contracts") {
		t.Fatal("banner missing tagline")
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
