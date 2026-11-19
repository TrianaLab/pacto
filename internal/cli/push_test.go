package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
)

// printedPush prints result as pacto push does and returns stdout and stderr.
func printedPush(t *testing.T, result *app.PushResult) (stdout, stderr string) {
	t.Helper()
	cmd := &cobra.Command{}
	out, errw := &strings.Builder{}, &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(errw)
	if err := printPushResult(cmd, result, "text"); err != nil {
		t.Fatal(err)
	}
	return out.String(), errw.String()
}

func TestPushPrintsIndexLine(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	res := &app.PushResult{Ref: "reg/acme/svc:1.0.0", Digest: digest, Name: "svc", Version: "1.0.0",
		Index: "indexed as reg/acme/pacto-index:0123456789abcdef-1.0.0"}
	out, stderr := printedPush(t, res)
	digestAt, indexAt := strings.Index(out, "Digest: "+digest), strings.Index(out, "Index: indexed as reg/acme/pacto-index:0123456789abcdef-1.0.0")
	if digestAt < 0 || indexAt < digestAt || stderr != "" {
		t.Fatalf("stdout %q stderr %q", out, stderr)
	}

	res.Index = "not indexed in reg/acme/pacto-index: denied"
	out, stderr = printedPush(t, res)
	combined := out + stderr
	if !strings.Contains(stderr, "warning: not indexed in reg/acme/pacto-index: denied") || strings.Contains(out, "Index:") ||
		combined[strings.Index(combined, "sha256:"):][:len(digest)] != digest {
		t.Fatalf("stdout %q stderr %q", out, stderr)
	}
}
