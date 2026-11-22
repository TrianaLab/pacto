/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package linker

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/orgindex"
	"github.com/trianalab/pacto/v4/pkg/publish"
	"github.com/trianalab/pacto/v4/pkg/semver"
)

const (
	// linkRecentVersions is how many of each service's newest index tags are
	// read. ponytail: K fixed; a wider window if rollouts lag more.
	linkRecentVersions = 5
	// maxIndexReadsPerCycle bounds the uncached reads one cycle makes.
	maxIndexReadsPerCycle = 200
	// maxCached bounds each cache. ponytail: cleared on overflow.
	maxCached = 20000
)

// Reader reads the organization index: the validated entry under a tag and
// the images records of a bundle (partial when the referrer listing was cut).
type Reader interface {
	Entry(ctx context.Context, tag string) (orgindex.Entry, error)
	Images(ctx context.Context, repo, digest string) ([]publish.ImagesRecord, bool, error)
}

// Index caches index entries (write-once tags) and images records (immutable
// by digest) forever, within maxCached each.
type Index struct {
	entries map[string]orgindex.Entry
	images  map[string][]publish.ImagesRecord
}

// Candidates maps each digest an images record names to its candidates,
// reading the linkRecentVersions newest tags of every service plus each tag
// whose version is a running image tag. unread counts the reads past
// maxIndexReadsPerCycle; partial says something was left unread or failed.
func (x *Index) Candidates(ctx context.Context, tags, imageTags []string, read Reader) (byDigest map[string][]Candidate, unread int, partial bool) {
	if x.entries == nil {
		x.entries, x.images = map[string]orgindex.Entry{}, map[string][]publish.ImagesRecord{}
	}
	byDigest, reads := map[string][]Candidate{}, 0
	budget := func() bool {
		if reads == maxIndexReadsPerCycle {
			unread++
			return false
		}
		reads++
		return true
	}
	for _, tag := range pick(tags, imageTags) {
		e, ok := x.entries[tag]
		if !ok {
			if !budget() {
				continue
			}
			var err error
			if e, err = read.Entry(ctx, tag); err != nil {
				partial = true
				continue
			}
			keep(x.entries, tag, e)
		}
		recs, ok := x.images[e.Bundle]
		if !ok {
			if !budget() {
				continue
			}
			repo, digest, _ := strings.Cut(e.Bundle, "@")
			got, cut, err := read.Images(ctx, repo, digest)
			partial = partial || cut || err != nil
			if err != nil {
				continue
			}
			if recs = got; !cut {
				keep(x.images, e.Bundle, recs)
			}
		}
		addCandidates(byDigest, e, recs)
	}
	return byDigest, unread, partial || unread > 0
}

// Indexed says a cached entry's bundle lives in repo: the repository was
// published to the organization.
func (x *Index) Indexed(repo string) bool {
	for _, e := range x.entries {
		if r, _, _ := strings.Cut(e.Bundle, "@"); r == repo {
			return true
		}
	}
	return false
}

func keep[V any](m map[string]V, k string, v V) {
	if len(m) >= maxCached {
		clear(m)
	}
	m[k] = v
}

func addCandidates(byDigest map[string][]Candidate, e orgindex.Entry, recs []publish.ImagesRecord) {
	repo, _, _ := strings.Cut(e.Bundle, "@")
	for _, r := range recs {
		for _, img := range r.Records {
			byDigest[img.Digest] = append(byDigest[img.Digest], Candidate{
				Service: repo, Version: e.Version, Bundle: e.Bundle, Repository: img.Repository, Signed: r.Signed,
			})
		}
	}
}

// pick is the sorted tags to read: per service prefix the linkRecentVersions
// newest (releases before prereleases), plus every tag whose version is one
// of imageTags. A tag outside the scheme is never read.
func pick(tags, imageTags []string) []string {
	type tv struct{ tag, version string }
	byPrefix := map[string][]tv{}
	out := []string{}
	for _, t := range tags {
		prefix, version, ok := orgindex.ParseTag(t)
		if !ok || semver.Latest([]string{version}) == "" {
			continue
		}
		byPrefix[prefix] = append(byPrefix[prefix], tv{t, version})
		if slices.Contains(imageTags, version) {
			out = append(out, t)
		}
	}
	for _, vs := range byPrefix {
		slices.SortFunc(vs, func(a, b tv) int {
			if pa, pb := prerelease(a.version), prerelease(b.version); pa != pb {
				return cmp.Compare(btoi(pa), btoi(pb))
			}
			return semver.Compare(b.version, a.version)
		})
		for _, v := range vs[:min(len(vs), linkRecentVersions)] {
			out = append(out, v.tag)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
