package oci_test

import (
	"strings"
	"testing"

	msemver "github.com/Masterminds/semver/v3"
	"github.com/trianalab/pacto/v2/pkg/oci"
)

// FuzzHasExplicitTag proves OCI-reference tag detection is total (never panics)
// and matches an independent definition: a ref has an explicit pin iff it contains
// a digest ("@") or a colon that follows the final "/" (a tag, not a host port).
func FuzzHasExplicitTag(f *testing.F) {
	for _, s := range []string{
		"ghcr.io/org/svc:1.0.0", "ghcr.io/org/svc", "ghcr.io/org/svc@sha256:abc",
		"localhost:5000/repo", "localhost:5000/repo:v1", "", ":", "a:b/c", "oci://x/y:1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ref string) {
		got := oci.HasExplicitTag(ref)
		want := strings.Contains(ref, "@") || strings.LastIndex(ref, ":") > strings.LastIndex(ref, "/")
		if got != want {
			t.Fatalf("HasExplicitTag(%q) = %v, want %v", ref, got, want)
		}
	})
}

// FuzzBestTag proves version selection is total and correct: any tag list is safe,
// and a returned tag is always a valid semver present in the input that satisfies
// the constraint (when one is supplied and parses).
func FuzzBestTag(f *testing.F) {
	f.Add("1.0.0,2.0.0,latest", "^1.0.0")
	f.Add("v1.2.3,1.2.4,1.3.0-rc.1", ">=1.2.0")
	f.Add("", "")
	f.Add("abc,def", "not-a-constraint")
	f.Fuzz(func(t *testing.T, joined, constraint string) {
		tags := strings.Split(joined, ",")
		got, err := oci.BestTag(tags, constraint)
		if err != nil {
			return // no matching / no semver / bad constraint are all valid outcomes
		}
		v, verr := msemver.NewVersion(got)
		if verr != nil {
			t.Fatalf("BestTag returned non-semver %q", got)
		}
		found := false
		for _, tag := range tags {
			if tag == got {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("BestTag returned %q which is not in the input %v", got, tags)
		}
		if constraint != "" {
			c, cerr := msemver.NewConstraint(constraint)
			if cerr == nil && !c.Check(v) {
				t.Fatalf("BestTag returned %q which violates constraint %q", got, constraint)
			}
		}
	})
}
