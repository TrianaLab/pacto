// Package semver is the single source of truth for semver tag handling shared by
// the CLI/OCI resolver and the dashboard data sources. Both must filter, sort,
// and pick "latest" identically so a service never appears or vanishes depending
// on which component or source answered.
package semver

import (
	"sort"

	"github.com/Masterminds/semver/v3"
)

// Filter returns only valid semver tags, sorted descending (latest first).
// Non-semver tags are dropped. Returns nil when no tags are valid semver.
func Filter(tags []string) []string {
	var versions []*semver.Version
	for _, t := range tags {
		if v, err := semver.NewVersion(t); err == nil {
			versions = append(versions, v)
		}
	}
	sort.Sort(sort.Reverse(semver.Collection(versions)))
	var out []string
	for _, v := range versions {
		out = append(out, v.Original())
	}
	return out
}

// Latest returns the highest valid semver tag, or "" if none are valid semver.
func Latest(tags []string) string {
	f := Filter(tags)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// LessDesc reports whether tag a should sort before tag b in descending order
// (latest first). Valid semver sorts before invalid; two invalid tags fall back
// to reverse-lexicographic order. Use as the less-func for sorting mixed lists.
func LessDesc(a, b string) bool {
	va, ea := semver.NewVersion(a)
	vb, eb := semver.NewVersion(b)
	switch {
	case ea == nil && eb == nil:
		return vb.LessThan(va) // descending: higher first
	case ea != nil && eb != nil:
		return a > b // both invalid: reverse-lexicographic
	default:
		return ea == nil // valid semver sorts before invalid
	}
}
