package release

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDeliveryFilterWatchesTheDemoInputs: the delivery-demo leg runs the live
// browser journeys against the real approval guard, so a change to anything
// it serves or sources must run it. The filter once missed the delivery views,
// the served UI bundle, the server that wires the guard and the shell library
// delivery-demo.sh sources, so breakage there first showed up after merge.
func TestDeliveryFilterWatchesTheDemoInputs(t *testing.T) {
	root := repoRoot(t)
	filters := pathFilters(t, root)
	if len(filters["delivery"]) == 0 || len(filters["delivery_ui"]) == 0 {
		t.Fatal("ci.yml has no `delivery` or `delivery_ui` path filter")
	}
	// delivery_ui (the frontend) runs only the shards that load the bundle,
	// so the guard and the harness must stay in delivery, which runs them all.
	patterns := slices.Concat(filters["delivery"], filters["delivery_ui"])
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
			t.Errorf("the delivery path filters do not match %s, so a change to it skips the delivery-demo leg", f)
		}
	}
}

// TestDeliveryUIFilterRunsOnlyTheBrowserShards: a frontend-only change
// (delivery_ui) runs delivery-demo with ui-only, which drops the shards that
// never load the UI bundle. Anything the other shards run on (the guard, the
// harness) must stay in delivery, which runs every shard.
func TestDeliveryUIFilterRunsOnlyTheBrowserShards(t *testing.T) {
	root := repoRoot(t)
	filters := pathFilters(t, root)
	for _, f := range []string{"pkg/dashboard/server.go", "scripts/ci/kind-run.sh", "tests/acceptance/kind/delivery-demo.sh"} {
		if matchesAny(filters["delivery_ui"], f) || !matchesAny(filters["delivery"], f) {
			t.Errorf("%s must be in the delivery filter (every shard), not delivery_ui (the browser shards only)", f)
		}
	}
	wf := readFile(t, root, ".github", "workflows", "ci.yml")
	for _, s := range []string{"needs.changes.outputs.delivery_ui == 'true'", "ui-only: ${{ needs.changes.outputs.delivery != 'true'"} {
		if !strings.Contains(wf, s) {
			t.Errorf("ci.yml's delivery-demo leg lost %q: a frontend-only change must run the browser shards, and only them", s)
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

// TestDeliveryDemoRunsDoNotCancelEachOther: ci.yml calls delivery-demo.yml
// as a required leg, and the nightly schedule runs the same file. On main both
// resolve github.ref to refs/heads/main, so a concurrency group keyed on the
// ref alone with cancel-in-progress lets the nightly cancel the required leg
// (main goes red with no defect) or a push cancel the nightly (its failure
// issue is never opened). The group must tell the caller's event apart.
func TestDeliveryDemoRunsDoNotCancelEachOther(t *testing.T) {
	var doc struct {
		Concurrency struct {
			Group string `yaml:"group"`
		} `yaml:"concurrency"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, repoRoot(t), ".github", "workflows", "delivery-demo.yml")), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Concurrency.Group, "github.event_name") {
		t.Errorf("delivery-demo.yml concurrency group %q is shared by the nightly and the CI call: key it on github.event_name too", doc.Concurrency.Group)
	}
}
