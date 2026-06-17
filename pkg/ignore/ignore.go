// Package ignore implements .pactoignore filtering for bundle packaging.
// Pattern syntax is a gitignore-style subset: '#' comments, blank lines,
// '*'/'?'/'[]' globs within a path segment, '**' across segments, a leading
// '/' to anchor at the bundle root, a trailing '/' to match directories only
// and a leading '!' to negate. Last matching rule wins.
package ignore

import (
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
