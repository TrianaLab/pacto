package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/internal/ci"
)

// ciInitRepo is a repository holding files, each with its content.
func ciInitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCIInitWritesGitHubCaller(t *testing.T) {
	ciEnvOff(t)
	dir := ciInitRepo(t, map[string]string{".github/dependabot.yml": "version: 2\n"})
	out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir)
	_, content := ci.Caller("github")
	if err != nil || out != "wrote .github/workflows/pacto.yml (5 lines):\n"+content+rekorNote+"\n" {
		t.Fatalf("err %v, stdout:\n%s", err, out)
	}
	if got := readFile(t, filepath.Join(dir, ".github/workflows/pacto.yml")); got != content {
		t.Fatalf("wrote:\n%s", got)
	}
}

func TestCIInitUpToDate(t *testing.T) {
	ciEnvOff(t)
	_, content := ci.Caller("github")
	dir := ciInitRepo(t, map[string]string{".github/workflows/pacto.yml": content})
	if out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir); err != nil || out != ".github/workflows/pacto.yml is up to date\n" {
		t.Fatalf("err %v, stdout %q", err, out)
	}
}

func TestCIInitRefusesExistingWithoutForce(t *testing.T) {
	ciEnvOff(t)
	dir := ciInitRepo(t, map[string]string{".github/workflows/pacto.yml": "on: push\n"})
	_, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err %v", err)
	}
	if got := readFile(t, filepath.Join(dir, ".github/workflows/pacto.yml")); got != "on: push\n" {
		t.Fatalf("the file changed:\n%s", got)
	}
}

func TestCIInitForceOverwrites(t *testing.T) {
	ciEnvOff(t)
	dir := ciInitRepo(t, map[string]string{".github/workflows/pacto.yml": "on: push\n"})
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", "--force", dir); err != nil {
		t.Fatal(err)
	}
	if _, content := ci.Caller("github"); readFile(t, filepath.Join(dir, ".github/workflows/pacto.yml")) != content {
		t.Fatal("--force did not replace the caller")
	}
}

func TestCIInitEditsGitLab(t *testing.T) {
	ciEnvOff(t)
	dir := ciInitRepo(t, map[string]string{".gitlab-ci.yml": "# acme\nbuild:\n  script: [make]\n"})
	out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir)
	_, content := ci.Caller("gitlab")
	if err != nil || out != "added the pacto-actions include entry to .gitlab-ci.yml (2 lines):\n"+content {
		t.Fatalf("err %v, stdout:\n%s", err, out)
	}
	if got := readFile(t, filepath.Join(dir, ".gitlab-ci.yml")); got != "# acme\nbuild:\n  script: [make]\n\n"+content {
		t.Fatalf("wrote:\n%s", got)
	}
	if out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir); err != nil || out != ".gitlab-ci.yml is up to date\n" {
		t.Fatalf("second run: err %v, stdout %q", err, out)
	}
}

func TestCIInitGitLabRefusal(t *testing.T) {
	ciEnvOff(t)
	dir := ciInitRepo(t, map[string]string{".gitlab-ci.yml": "include: a.yml\n"})
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir); err == nil || !strings.Contains(err.Error(), "- remote: "+ci.GitLabIncludeURL()) {
		t.Fatalf("err %v", err)
	}
}

func TestCIInitDryRunWritesNothing(t *testing.T) {
	ciEnvOff(t)
	dir := t.TempDir()
	out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", "--platform", "github", "--dry-run", dir)
	if err != nil || !strings.HasPrefix(out, "would write .github/workflows/pacto.yml (5 lines):\n") {
		t.Fatalf("err %v, stdout:\n%s", err, out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("--dry-run wrote %v", entries)
	}
}

func TestCIInitJSON(t *testing.T) {
	ciEnvOff(t)
	dir := ciInitRepo(t, map[string]string{".gitlab-ci.yml": ""})
	out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir, "--output-format", "json")
	doc := decode(t, out)
	_, content := ci.Caller("gitlab")
	if err != nil || len(doc) != 5 || doc["platform"] != "gitlab" || doc["path"] != ".gitlab-ci.yml" || doc["lines"] != 2.0 || doc["content"] != content || doc["written"] != true {
		t.Fatalf("err %v, doc %v", err, doc)
	}
}

func TestCIInitAmbiguousNeedsPlatform(t *testing.T) {
	ciEnvOff(t)
	for _, files := range []map[string]string{{".github/x": "", ".gitlab-ci.yml": ""}, {}} {
		if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", ciInitRepo(t, files)); err == nil || !strings.Contains(err.Error(), "--platform github|gitlab") {
			t.Errorf("%v: err %v", files, err)
		}
	}
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", "--platform", "jenkins", t.TempDir()); err == nil || !strings.Contains(err.Error(), "want github or gitlab") {
		t.Errorf("--platform jenkins: err %v", err)
	}
}

func TestCIInitUnreadableCaller(t *testing.T) {
	ciEnvOff(t)
	// The caller path is a folder: reading it fails with something other than
	// not-exist.
	dir := ciInitRepo(t, map[string]string{".github/workflows/pacto.yml/x": ""})
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir); err == nil {
		t.Fatal("no error")
	}
}

func TestCIInitUnwritableDir(t *testing.T) {
	ciEnvOff(t)
	// .github is read-only, so .github/workflows cannot be made.
	dir := ciInitRepo(t, map[string]string{".github/x": ""})
	gh := filepath.Join(dir, ".github")
	if err := os.Chmod(gh, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(gh, 0o755) })
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir); err == nil {
		t.Fatal("no error")
	}
}

// TestCIInitMakesNoGitCall: pacto ci init writes into the working tree only;
// a git on PATH that records its calls sees none.
func TestCIInitMakesNoGitCall(t *testing.T) {
	ciEnvOff(t)
	bin := t.TempDir()
	marker := filepath.Join(bin, "called")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" >> "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := ciInitRepo(t, map[string]string{".github/x": ""})
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("git was called: %v", err)
	}
}

// TestCIFolderNamedInit: init is a subcommand, so a folder named init is
// planned as ./init.
func TestCIFolderNamedInit(t *testing.T) {
	ciEnvOff(t)
	statsOff(t)
	t.Chdir(ciInitRepo(t, map[string]string{"init/pacto.yaml": "service:\n  name: payments\n"}))
	stdout, _, err := runRoot(t, defaultDeps(), nil, "ci", "./init", "--registry", "127.0.0.1:1/acme")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 2 || !strings.Contains(stdout, "\npayments ") {
		t.Fatalf("err %v, stdout:\n%s", err, stdout)
	}
}

// TestCIInitGitLabJSONIsTheFile: over an existing .gitlab-ci.yml, a dry run's
// JSON content is the whole file a real run writes, never the bare entry an
// agent would write over the pipeline.
func TestCIInitGitLabJSONIsTheFile(t *testing.T) {
	ciEnvOff(t)
	src := "stages: [test]\ninclude:\n  - local: a.yml\nbuild:\n  script: make\n"
	dir := ciInitRepo(t, map[string]string{".gitlab-ci.yml": src})
	out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", "--dry-run", dir, "--output-format", "json")
	dry := decode(t, out)
	if err != nil || dry["written"] != false || dry["lines"] != 2.0 || readFile(t, filepath.Join(dir, ".gitlab-ci.yml")) != src {
		t.Fatalf("err %v, doc %v", err, dry)
	}
	out, _, err = runRoot(t, defaultDeps(), nil, "ci", "init", dir, "--output-format", "json")
	written := readFile(t, filepath.Join(dir, ".gitlab-ci.yml"))
	if doc := decode(t, out); err != nil || dry["content"] != written || doc["content"] != written || !strings.Contains(written, "- local: a.yml") {
		t.Fatalf("err %v, dry %v, wrote:\n%s", err, dry, written)
	}
}

// TestCIInitGitHubJSONNotes: the Rekor disclosure a person reads in text is
// in the JSON an agent reads.
func TestCIInitGitHubJSONNotes(t *testing.T) {
	ciEnvOff(t)
	out, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", "--platform", "github", "--dry-run", t.TempDir(), "--output-format", "json")
	doc := decode(t, out)
	notes, _ := doc["notes"].([]any)
	if err != nil || len(notes) != 1 || notes[0] != rekorNote {
		t.Fatalf("err %v, doc %v", err, doc)
	}
}

// TestCIInitRefusesMissingDirAndEscape: a dir that does not exist is an
// error, never created, and a caller path that is a symlink out of the
// repository is never followed.
func TestCIInitRefusesMissingDirAndEscape(t *testing.T) {
	ciEnvOff(t)
	missing := filepath.Join(t.TempDir(), "tpyo", "dir")
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", "--platform", "github", missing); err == nil {
		t.Fatal("a missing dir: no error")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the missing dir was created: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(outside, []byte("a: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".gitlab-ci.yml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, defaultDeps(), nil, "ci", "init", "--platform", "gitlab", dir); err == nil {
		t.Fatal("a symlink out of the repository: no error")
	}
	if got := readFile(t, outside); got != "a: 1\n" {
		t.Fatalf("the file outside changed:\n%s", got)
	}
}
