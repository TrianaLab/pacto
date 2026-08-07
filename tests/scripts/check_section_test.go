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

// glyph builds the forbidden character from its code point so this source file
// (and any commit message it creates) never contains it literally.
func glyph() string { return string(rune(0x00A7)) }

// A committed generated documentation file is scanned like any other authored
// file: it is NOT excluded, so a stray glyph in generated docs still fails.
func TestCheckSection_GeneratedDocScanned(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cli-reference.md") // stands in for a generated doc
	if err := os.WriteFile(f, []byte("# Reference\nflag "+glyph()+" here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", scriptPath(t), f).CombinedOutput()
	if err == nil {
		t.Fatalf("a generated doc containing U+00A7 must fail; out=%s", out)
	}
}

func TestCheckSection_CommitMessages(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	revParse := func(ref string) string {
		cmd := exec.Command("git", "rev-parse", ref)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rev-parse %s: %v %s", ref, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "clean base commit")
	base := revParse("HEAD")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "bad "+glyph()+" in message")
	head := revParse("HEAD")

	// The dirty range fails and reports the offending commit sha.
	cmd := exec.Command("sh", scriptPath(t), "--commits", base+"..HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a commit message containing U+00A7 must fail; out=%s", out)
	}
	if !strings.Contains(string(out), head[:12]) {
		t.Errorf("checker must report the offending commit %q, got:\n%s", head, out)
	}

	// A clean range passes.
	clean := exec.Command("sh", scriptPath(t), "--commits", base)
	clean.Dir = dir
	if out, err := clean.CombinedOutput(); err != nil {
		t.Fatalf("a clean commit range must pass, got err=%v out=%s", err, out)
	}
}

func TestCheckSection_PRTitleFromStdin(t *testing.T) {
	cmd := exec.Command("sh", scriptPath(t), "--text", "pr-title", "-")
	cmd.Stdin = strings.NewReader("a bad " + glyph() + " title")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a PR title containing U+00A7 must fail; out=%s", out)
	}
	if !strings.Contains(string(out), "pr-title:") {
		t.Errorf("checker must label the PR-title source, got:\n%s", out)
	}
	// A clean title passes.
	clean := exec.Command("sh", scriptPath(t), "--text", "pr-title", "-")
	clean.Stdin = strings.NewReader("a clean title")
	if out, err := clean.CombinedOutput(); err != nil {
		t.Fatalf("a clean PR title must pass, got err=%v out=%s", err, out)
	}
}

func TestCheckSection_PRBodyFromFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(f, []byte("line one\nline two with "+glyph()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", scriptPath(t), "--text", "pr-body", f).CombinedOutput()
	if err == nil {
		t.Fatalf("a PR body containing U+00A7 must fail; out=%s", out)
	}
	if !strings.Contains(string(out), "pr-body:2:") {
		t.Errorf("checker must report pr-body:line, got:\n%s", out)
	}
}
