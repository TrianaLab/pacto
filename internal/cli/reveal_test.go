package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/pactify"
)

func TestBannerStatic(t *testing.T) {
	full := bannerStatic(true)
	if !strings.Contains(full, ansiIndigo) || !strings.Contains(full, "service contracts") {
		t.Fatal("colored full banner missing parts")
	}
	if strings.Contains(bannerStatic(false), ansiIndigo) {
		t.Fatal("plain banner must not contain color codes")
	}
}

func TestBannerStaticNoDuplication(t *testing.T) {
	// The help banner is static: each row appears exactly once (guards against the
	// old cumulative-frame stacking bug).
	out := bannerStatic(true)
	if n := strings.Count(out, "██████   █████"); n != 1 {
		t.Fatalf("top banner row should appear once, got %d", n)
	}
	if !strings.Contains(out, "service contracts") {
		t.Fatal("banner missing tagline")
	}
}

// blankInit is what init reports for a new blank bundle.
var blankInit = &app.InitResult{Dir: "svc", Path: "svc/pacto.yaml", Files: []pactify.File{
	{Path: "pacto.yaml", Action: pactify.Add},
	{Path: "interfaces/openapi.yaml", Action: pactify.Add},
	{Path: "configuration/schema.json", Action: pactify.Add},
}}

func TestInitLines(t *testing.T) {
	got := initLines(blankInit)
	want := []string{"Created svc/", "  svc/pacto.yaml", "  svc/interfaces/openapi.yaml", "  svc/configuration/schema.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	got = initLines(&app.InitResult{Dir: "svc", Files: []pactify.File{{Path: ".pactoignore", Action: pactify.Append}, {Path: "pacto.yaml", Action: pactify.Keep}}})
	if !reflect.DeepEqual(got, []string{"Created svc/", "  svc/.pactoignore (lines added)"}) {
		t.Fatalf("got %#v", got)
	}
	if got := initLines(&app.InitResult{Dir: "svc"}); !reflect.DeepEqual(got, []string{"Nothing to add in svc/: every file init writes is already there"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestRevealInitOffTTY(t *testing.T) {
	var buf bytes.Buffer
	revealInit(&cobra.Command{}, &buf, initLines(blankInit))
	if buf.String() != "Created svc/\n  svc/pacto.yaml\n  svc/interfaces/openapi.yaml\n  svc/configuration/schema.json\n" {
		t.Fatalf("off-TTY init must be byte-identical: %q", buf.String())
	}
}

func TestRevealInitTTY(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("PACTO_NO_ANIM", "")
	var buf ttyBuffer
	revealInit(&cobra.Command{}, &buf, initLines(blankInit))
	if !strings.Contains(buf.String(), "✓") || !strings.Contains(buf.String(), "\033[32m") {
		t.Fatal("TTY init should show green ✓")
	}
}

func TestRevealInitEmptyLines(t *testing.T) {
	var buf bytes.Buffer
	revealInit(&cobra.Command{}, &buf, []string{})
	if buf.Len() != 0 {
		t.Fatal("empty lines should produce no output")
	}
}
