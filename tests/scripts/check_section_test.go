package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scriptPath resolves scripts/check-section-sign.sh relative to this test file so
// the test does not depend on the working directory.
func scriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // tests/scripts -> repo root
	return filepath.Join(root, "scripts", "check-section-sign.sh")
}

func TestCheckSection_CleanFilePasses(t *testing.T) {
	f := filepath.Join(t.TempDir(), "clean.md")
	if err := os.WriteFile(f, []byte("no forbidden glyph here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", scriptPath(t), f).CombinedOutput(); err != nil {
		t.Fatalf("clean file must pass, got err=%v out=%s", err, out)
	}
}

// The checker must fail on a forbidden glyph AND report the exact path:line so an
// author can locate it (review section S19). The glyph is built at runtime, not
// written literally, so this source file itself stays clean.
func TestCheckSection_ReportsForbiddenGlyphWithLine(t *testing.T) {
	f := filepath.Join(t.TempDir(), "dirty.md")
	body := "clean first line\nbad " + string(rune(0x00A7)) + " here\n" // glyph on line 2
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", scriptPath(t), f).CombinedOutput()
	if err == nil {
		t.Fatalf("a file containing U+00A7 must fail the checker; out=%s", out)
	}
	if want := f + ":2:"; !strings.Contains(string(out), want) {
		t.Errorf("checker must report %q (path:line), got:\n%s", want, out)
	}
}
