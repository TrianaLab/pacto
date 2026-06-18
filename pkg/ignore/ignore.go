// Package ignore implements .pactoignore filtering for bundle packaging.
// Pattern syntax is a gitignore-style subset: '#' comments, blank lines,
// '*'/'?'/'[]' globs within a path segment, '**' across segments, a leading
// '/' to anchor at the bundle root, a trailing '/' to match directories only
// and a leading '!' to negate. Last matching rule wins.
package ignore

import (
	"errors"
	"io/fs"
	"path"
	"strings"
)

// IgnoreFileName is the name of the per-bundle ignore file.
const IgnoreFileName = ".pactoignore"

// DefaultPatterns are always ignored when building a bundle.
var DefaultPatterns = []string{
	".git/",
	".pactoignore",
	"pacto.lock",
	".DS_Store",
}

// alwaysKeep can never be ignored regardless of user patterns.
var alwaysKeep = map[string]bool{"pacto.yaml": true}

type rule struct {
	segs     []string // pattern split on '/'
	negate   bool
	dirOnly  bool
	anchored bool
}

// Matcher decides whether a bundle-relative path is ignored.
type Matcher struct{ rules []rule }

// New builds a Matcher from DefaultPatterns plus extra patterns (e.g. the
// lines of a .pactoignore file). Later patterns take precedence.
func New(extra []string) *Matcher {
	m := &Matcher{}
	for _, line := range append(append([]string{}, DefaultPatterns...), extra...) {
		if r, ok := parseRule(line); ok {
			m.rules = append(m.rules, r)
		}
	}
	return m
}

func parseRule(line string) (rule, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return rule{}, false
	}
	var r rule
	if strings.HasPrefix(s, "!") {
		r.negate = true
		s = s[1:]
	}
	if strings.HasSuffix(s, "/") {
		r.dirOnly = true
		s = strings.TrimSuffix(s, "/")
	}
	if strings.HasPrefix(s, "/") {
		r.anchored = true
		s = strings.TrimPrefix(s, "/")
	}
	// A pattern with no interior slash matches a basename at any depth (unless anchored).
	if !r.anchored && !strings.Contains(s, "/") {
		r.segs = []string{"**", s}
	} else {
		r.segs = strings.Split(s, "/")
	}
	return r, s != ""
}

// Ignored reports whether slashPath (bundle-relative, '/'-separated, never
// "." or leading "/") is excluded from the bundle.
func (m *Matcher) Ignored(slashPath string, isDir bool) bool {
	if alwaysKeep[slashPath] {
		return false
	}
	nameSegs := strings.Split(slashPath, "/")
	ignored := false
	for _, r := range m.rules {
		if r.dirOnly && !isDir {
			continue
		}
		if matchSegs(r.segs, nameSegs) {
			ignored = !r.negate
		}
	}
	return ignored
}

func matchSegs(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchSegs(pat[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	if ok, _ := path.Match(pat[0], name[0]); !ok {
		return false
	}
	return matchSegs(pat[1:], name[1:])
}

// Load reads .pactoignore from the root of fsys (if present) and returns a
// Matcher combining DefaultPatterns with the file's lines.
func Load(fsys fs.FS) (*Matcher, error) {
	data, err := fs.ReadFile(fsys, IgnoreFileName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return New(nil), nil
		}
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		lines = append(lines, strings.TrimRight(l, "\r"))
	}
	return New(lines), nil
}

// FS wraps base so ignored paths are hidden from Open and ReadDir.
func FS(base fs.FS, m *Matcher) fs.FS { return &filteredFS{base: base, m: m} }

type filteredFS struct {
	base fs.FS
	m    *Matcher
}

// ignored reports whether name is filtered out, considering ancestor directories
// so a file inside an ignored directory is itself ignored (Open/Stat must agree
// with ReadDir's subtree skipping).
func (f *filteredFS) ignored(name string, isDir bool) bool {
	if f.m.Ignored(name, isDir) {
		return true
	}
	for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if f.m.Ignored(dir, true) {
			return true
		}
	}
	return false
}

func (f *filteredFS) Open(name string) (fs.File, error) {
	if name != "." {
		if info, err := fs.Stat(f.base, name); err == nil {
			if f.ignored(name, info.IsDir()) {
				return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
			}
		}
	}
	return f.base.Open(name)
}

func (f *filteredFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(f.base, name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		p := e.Name()
		if name != "." {
			p = path.Join(name, e.Name())
		}
		if f.ignored(p, e.IsDir()) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
