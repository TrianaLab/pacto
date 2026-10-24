package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	commitA = "1111111111111111111111111111111111111111"
	commitB = "2222222222222222222222222222222222222222"
)

// writeGit writes files (path relative to dir/.git) into a fake git directory.
func writeGit(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	writeFiles(t, filepath.Join(dir, ".git"), files)
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGitRevision(t *testing.T) {
	sha256ID := strings.Repeat("ab", 32)
	packed := "# pack-refs with: peeled fully-peeled sorted\n" + commitB + " refs/heads/other\n" + commitA + " refs/heads/main\n^" + commitB + "\n"
	for name, files := range map[string]map[string]string{
		"loose ref":        {"HEAD": "ref: refs/heads/main\n", "refs/heads/main": commitA + "\n"},
		"detached":         {"HEAD": commitA + "\n"},
		"packed ref":       {"HEAD": "ref: refs/heads/main\n", "packed-refs": packed},
		"symbolic chain":   {"HEAD": "ref: refs/heads/alias\n", "refs/heads/alias": "ref: refs/heads/main\n", "refs/heads/main": commitA},
		"sha-256 checkout": {"HEAD": sha256ID},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeGit(t, dir, files)
			want := commitA
			if name == "sha-256 checkout" {
				want = sha256ID
			}
			if got := gitRevision(dir); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
	for name, files := range map[string]map[string]string{
		"unborn branch":   {"HEAD": "ref: refs/heads/main\n"},
		"garbage":         {"HEAD": "garbage"},
		"not hex":         {"HEAD": strings.Repeat("z", 40)},
		"ref loop":        {"HEAD": "ref: refs/heads/a", "refs/heads/a": "ref: refs/heads/b", "refs/heads/b": "ref: refs/heads/a"},
		"outside refs":    {"HEAD": "ref: config", "config": commitA},
		"escaping refs/":  {"HEAD": "ref: refs/../../secret", "refs/x": ""},
		"no HEAD at all":  {"config": ""},
		"packed no match": {"HEAD": "ref: refs/heads/gone", "packed-refs": packed},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeGit(t, dir, files)
			if got := gitRevision(dir); got != "" {
				t.Fatalf("got %q, want none", got)
			}
		})
	}
}

// TestGitRevisionFindsTheCheckout: a contract in a subdirectory, a linked
// worktree (its own HEAD, refs shared through commondir) and a submodule.
func TestGitRevisionFindsTheCheckout(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"main/.git/HEAD":                   "ref: refs/heads/main\n",
		"main/.git/packed-refs":            commitA + " refs/heads/main\n" + commitB + " refs/heads/feature\n",
		"main/.git/worktrees/wt/HEAD":      "ref: refs/heads/feature\n",
		"main/.git/worktrees/wt/commondir": "../..\n",
		"main/svc/pacto.yaml":              "",
		"wt/.git":                          "gitdir: ../main/.git/worktrees/wt\n",
		"wt/svc/pacto.yaml":                "",
		"abs/.git":                         "gitdir: " + filepath.Join(root, "main/.git/worktrees/wt") + "\n",
		"plain/.git":                       "not a gitdir line",
	})
	for dir, want := range map[string]string{"main/svc": commitA, "wt/svc": commitB, "abs": commitB, "plain": ""} {
		if got := gitRevision(filepath.Join(root, dir)); got != want {
			t.Errorf("%s: got %q, want %q", dir, got, want)
		}
	}
	// A commondir given as an absolute path.
	writeFiles(t, root, map[string]string{"main/.git/worktrees/wt/commondir": filepath.Join(root, "main/.git")})
	if got := gitRevision(filepath.Join(root, "wt")); got != commitB {
		t.Errorf("absolute commondir: got %q", got)
	}
}

// TestGitRevisionMatchesGit: on this very checkout (a linked worktree in
// development) the reader agrees with git when git is installed.
func TestGitRevisionMatchesGit(t *testing.T) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skip("no git checkout here")
	}
	wd, _ := os.Getwd()
	if got, want := gitRevision(wd), strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got %q, git says %q", got, want)
	}
}
