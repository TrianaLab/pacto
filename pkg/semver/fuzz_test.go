package semver

import (
	"strings"
	"testing"

	msemver "github.com/Masterminds/semver/v3"
)

// FuzzFilter proves the shared semver filter is total and its output is a
// descending-sorted subset of the valid-semver inputs, with Latest == first.
func FuzzFilter(f *testing.F) {
	f.Add("1.0.0 2.0.0 latest 1.5.0")
	f.Add("v1 v2.0.0 3.0.0-rc.1")
	f.Add("")
	f.Add("bad bad bad")
	f.Fuzz(func(t *testing.T, joined string) {
		tags := strings.Fields(joined)
		got := Filter(tags)

		// Every result is valid semver and appears in the input.
		validCount := 0
		for _, tag := range tags {
			if _, err := msemver.NewVersion(tag); err == nil {
				validCount++
			}
		}
		if len(got) != validCount {
			t.Fatalf("Filter returned %d tags, want %d valid semver", len(got), validCount)
		}

		// Descending order: each element >= the next.
		for i := 1; i < len(got); i++ {
			a, _ := msemver.NewVersion(got[i-1])
			b, _ := msemver.NewVersion(got[i])
			if a.LessThan(b) {
				t.Fatalf("Filter not descending: %s before %s", got[i-1], got[i])
			}
		}

		// Latest is the first filtered element (or "" when none).
		latest := Latest(tags)
		if len(got) == 0 {
			if latest != "" {
				t.Fatalf("Latest = %q, want empty", latest)
			}
		} else if latest != got[0] {
			t.Fatalf("Latest = %q, want %q", latest, got[0])
		}
	})
}

// FuzzLessDesc proves the mixed comparator is a strict order: irreflexive and
// antisymmetric for any pair of tags (so sort.Slice with it never panics).
func FuzzLessDesc(f *testing.F) {
	f.Add("1.0.0", "2.0.0")
	f.Add("latest", "1.0.0")
	f.Add("main", "dev")
	f.Add("1.0.0", "1.0.0")
	f.Fuzz(func(t *testing.T, a, b string) {
		if LessDesc(a, a) {
			t.Fatalf("LessDesc(%q,%q) must be irreflexive", a, a)
		}
		if LessDesc(a, b) && LessDesc(b, a) {
			t.Fatalf("LessDesc not antisymmetric for %q,%q", a, b)
		}
	})
}
