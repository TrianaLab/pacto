package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The standalone-consumer checks resolve a Go module out of the working checkout
// by pointing go at a tag, so the tag must name HEAD — the tree about to be
// published, not the tree the last release published. The names they stage are
// real published tags between releases (`v3.3.1`, `integrations/kubernetes/
// v5.4.1`), and a maintainer who has fetched tags holds them locally pointing at
// their actual release commits. Staging therefore force-moves them, and both
// callers used to clean up by DELETING: a read-only check was destroying release
// history in the maintainer's clone. CI never noticed, because CI clones fresh
// and throws the clone away.
//
// release/scripts/staging-tags.sh exists to make the staging reversible. These
// tests drive it against a real throwaway repository — the bug was in what git
// ends up holding, so asserting on anything less would not have caught it.

// gitRepo builds a throwaway repository with two commits and returns its path
// plus the two commit shas, oldest first.
func gitRepo(t *testing.T) (dir, first, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	for _, msg := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(dir, msg), []byte(msg), 0o600); err != nil {
			t.Fatal(err)
		}
		run("add", msg)
		run("commit", "-q", "-m", msg)
	}
	return dir, run("rev-parse", "HEAD~1"), run("rev-parse", "HEAD")
}

// stagingScript runs a bash snippet with staging-tags.sh sourced, in `dir`.
// wantFail asserts the snippet exits non-zero, which is how the EXIT trap is
// exercised on the path that actually leaked tags.
func stagingScript(t *testing.T, dir, body string, wantFail bool) {
	t.Helper()
	script := "set -euo pipefail\n. " + filepath.Join(repoRoot(t), "release", "scripts", "staging-tags.sh") + "\n" + body
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if wantFail && err == nil {
		t.Fatalf("snippet was expected to fail but exited 0:\n%s", out)
	}
	if !wantFail && err != nil {
		t.Fatalf("snippet failed: %v\n%s", err, out)
	}
}

// tagSha resolves a tag ref, or "" when it does not exist.
func tagSha(t *testing.T, dir, tag string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "-q", "--verify", "refs/tags/"+tag).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func TestStagingTagsRestoresAPreexistingTag(t *testing.T) {
	dir, first, head := gitRepo(t)
	const tag = "integrations/kubernetes/v5.4.1" // a real published name, slashes and all

	if out, err := exec.Command("git", "-C", dir, "tag", tag, first).CombinedOutput(); err != nil {
		t.Fatalf("seed tag: %v\n%s", err, out)
	}
	stagingScript(t, dir, `
stage_tag "$PWD" `+tag+`
[ "$(git rev-parse refs/tags/`+tag+`)" = "`+head+`" ] || { echo "stage_tag did not move the tag to HEAD"; exit 1; }
restore_staged_tags
`, false)

	if got := tagSha(t, dir, tag); got != first {
		t.Errorf("published tag %s not restored: points at %q, want the commit it named before staging (%q).\n"+
			"A read-only standalone check must leave the maintainer's clone exactly as it found it.", tag, got, first)
	}
}

func TestStagingTagsDeletesOnlyWhatItCreated(t *testing.T) {
	dir, _, head := gitRepo(t)
	const tag = "v9.9.9" // not published, not present: ours to create and remove

	stagingScript(t, dir, `
stage_tag "$PWD" `+tag+`
[ "$(git rev-parse refs/tags/`+tag+`)" = "`+head+`" ] || { echo "stage_tag did not create the tag at HEAD"; exit 1; }
restore_staged_tags
`, false)

	if got := tagSha(t, dir, tag); got != "" {
		t.Errorf("staging tag %s survived the restore at %q — a tag we created must not leak into the clone", tag, got)
	}
}

func TestStagingTagsRestoresWhenTheCheckFails(t *testing.T) {
	dir, first, _ := gitRepo(t)
	const existing, created = "v3.3.1", "v3.4.0"

	if out, err := exec.Command("git", "-C", dir, "tag", existing, first).CombinedOutput(); err != nil {
		t.Fatalf("seed tag: %v\n%s", err, out)
	}
	// The shape both callers use: trap the restore, stage, then fail part-way.
	// verify-standalone.sh's only deletion used to be its last line, so every
	// failure between staging and success leaked the tag.
	stagingScript(t, dir, `
trap restore_staged_tags EXIT
stage_tag "$PWD" `+existing+`
stage_tag "$PWD" `+created+`
echo "pretend the standalone build failed here" >&2
exit 1
`, true)

	if got := tagSha(t, dir, existing); got != first {
		t.Errorf("after a failed check, %s points at %q, want %q — the EXIT trap did not restore it", existing, got, first)
	}
	if got := tagSha(t, dir, created); got != "" {
		t.Errorf("after a failed check, staging tag %s survived at %q", created, got)
	}
}

// The two callers must trap the restore rather than call it on the happy path;
// a check that fails mid-way is exactly when the tags need putting back. And
// neither may keep a bare `git tag -d` for a name it staged — that is the
// original bug, and it reads as cleanup.
func TestStandaloneChecksTrapTheRestore(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range [][]string{
		{"release", "scripts", "verify-standalone.sh"},
		{"release", "orchestrator", "verify-k8s-standalone.sh"},
	} {
		name := filepath.Join(rel...)
		body := readFile(t, root, rel...)
		if !strings.Contains(body, "staging-tags.sh") {
			t.Errorf("%s stages tags without release/scripts/staging-tags.sh, so nothing restores them", name)
			continue
		}
		if !strings.Contains(body, "trap") || !strings.Contains(body, "restore_staged_tags") {
			t.Errorf("%s does not trap restore_staged_tags on EXIT — a failure mid-check leaks the staging tags", name)
		}
		if strings.Contains(body, "tag -d") {
			t.Errorf("%s still deletes a tag directly; the staged names are real published tags between "+
				"releases, so deletion destroys them. Let restore_staged_tags decide.", name)
		}
		if strings.Contains(body, "tag -f") {
			t.Errorf("%s force-moves a tag directly, bypassing the record stage_tag needs to restore it", name)
		}
	}
}
