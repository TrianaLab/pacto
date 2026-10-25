package release

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDeliveryFilterWatchesTheDemoInputs: the delivery-demo leg runs the live
// browser journeys against the real approval guard, so a change to anything
// it serves or sources must run it. The filter once missed the delivery views,
// the served UI bundle, the server that wires the guard and the shell library
// delivery-demo.sh sources, so breakage there first showed up after merge.
func TestDeliveryFilterWatchesTheDemoInputs(t *testing.T) {
	root := repoRoot(t)
	patterns := pathFilters(t, root)["delivery"]
	if len(patterns) == 0 {
		t.Fatal("ci.yml has no `delivery` path filter")
	}
	want := []string{"pkg/dashboard/server.go", "pkg/dashboard/ui/index.html", "scripts/ci/kind-run.sh"}
	src := filepath.Join(root, "pkg", "dashboard", "frontend", "src")
	_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(strings.ToLower(d.Name()), "delivery") {
			rel, _ := filepath.Rel(root, p)
			want = append(want, filepath.ToSlash(rel))
		}
		return nil
	})
	demo, err := os.ReadFile(filepath.Join(root, "tests", "acceptance", "kind", "delivery-demo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?m)^source "\$HERE/([^"]+)"`).FindAllStringSubmatch(string(demo), -1) {
		want = append(want, "tests/acceptance/kind/"+m[1])
	}
	if len(want) < 8 {
		t.Fatalf("found too few demo inputs to check (%v): the walk or the source lines moved", want)
	}
	for _, f := range want {
		if !matchesAny(patterns, f) {
			t.Errorf("the delivery path filter does not match %s, so a change to it skips the delivery-demo leg", f)
		}
	}
}

// matchesAny reports whether f matches one of the picomatch-style patterns
// dorny/paths-filter takes: ** spans directories, * and [..] stay within one.
func matchesAny(patterns []string, f string) bool {
	for _, p := range patterns {
		var re strings.Builder
		for i := 0; i < len(p); i++ {
			switch {
			case strings.HasPrefix(p[i:], "**/"):
				re.WriteString("(.*/)?")
				i += 2
			case strings.HasPrefix(p[i:], "**"):
				re.WriteString(".*")
				i++
			case p[i] == '*':
				re.WriteString("[^/]*")
			case p[i] == '[':
				j := strings.IndexByte(p[i:], ']')
				re.WriteString(p[i : i+j+1])
				i += j
			default:
				re.WriteString(regexp.QuoteMeta(p[i : i+1]))
			}
		}
		if regexp.MustCompile("^" + re.String() + "$").MatchString(f) {
			return true
		}
	}
	return false
}
