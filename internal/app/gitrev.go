package app

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// gitRevision is the commit HEAD points at in the git checkout that holds
// the absolute directory dir, or "" when there is none. It
// reads .git itself (HEAD, loose refs, packed-refs and a worktree's or
// submodule's gitdir file), so publishing needs no git program.
//
// ponytail: no reftable support (git 2.45+ opt-in ref storage); such a
// checkout reads as "" and --revision covers it.
func gitRevision(dir string) string {
	for {
		if gitDir := gitDirOf(filepath.Join(dir, ".git")); gitDir != "" {
			return resolveHEAD(gitDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// gitDirOf is the git directory dotGit names: dotGit itself when it is a
// directory, or the target of its "gitdir:" line when it is a file (a linked
// worktree or a submodule).
func gitDirOf(dotGit string) string {
	fi, err := os.Stat(dotGit)
	switch {
	case err != nil:
		return ""
	case fi.IsDir():
		return dotGit
	}
	b, _ := os.ReadFile(dotGit)
	target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dotGit), target)
	}
	return target
}

// resolveHEAD follows HEAD in gitDir to a commit. A linked worktree keeps its
// own HEAD in gitDir and shares refs with the main repository its commondir
// file names.
func resolveHEAD(gitDir string) string {
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
	}
	ref := "HEAD"
	// A symbolic ref chain longer than a few hops is a loop.
	for range 5 {
		v := readRef(gitDir, common, ref)
		target, symbolic := strings.CutPrefix(v, "ref:")
		if !symbolic {
			if isCommitID(v) {
				return v
			}
			return ""
		}
		ref = strings.TrimSpace(target)
	}
	return ""
}

// readRef is ref's value: its loose file in gitDir, then in common, then its
// line in common's packed-refs.
func readRef(gitDir, common, ref string) string {
	if (!strings.HasPrefix(ref, "refs/") && ref != "HEAD") || strings.Contains(ref, "..") {
		return ""
	}
	for _, d := range []string{gitDir, common} {
		if b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	b, _ := os.ReadFile(filepath.Join(common, "packed-refs"))
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		id, name, ok := strings.Cut(sc.Text(), " ")
		if ok && name == ref {
			return id
		}
	}
	return ""
}

// isCommitID reports whether s is a SHA-1 or SHA-256 object id.
func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
