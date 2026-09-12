package release

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// `make release-dry-run` READS the pending changesets: verify-standalone.sh
// computes the next core version from them, and build-release-plan.mjs turns
// them into the plan the whole rehearsal is built on. So a pull request that
// adds nothing but a changeset changes the dry run's input, and must run it.
//
// It did not. ci.yml gated release-dry-run on the `e2e` path filter, which has
// no '.changeset/**' entry — deliberately, because a changeset cannot affect
// the kind or Compose legs that share that filter. The `release` filter does
// list it, and its comment even names this job, but nothing consumed that
// output here. The result: the one pull request class that OPENS a release
// transaction was the one class the dry run never rehearsed. Observed on #370,
// which adds a single .changeset/*.md and nothing else: `release-dry-run
// skipping`, while release-version-test (gated on `release`) ran.
//
// Coverage was usually restored by accident — the code pull request that
// motivated the changeset touches a watched path and rehearses nearly the same
// tree — which is exactly why this went unnoticed. This gate does not depend on
// that accident.
func TestReleaseDryRunIsGatedOnAFilterWatchingChangesets(t *testing.T) {
	root := repoRoot(t)

	// Non-vacuity: if the dry run ever stops reading changesets, this gate is
	// obsolete and should be deleted rather than left passing over nothing.
	if !dryRunReadsChangesets(t, root) {
		t.Fatal("no file under release/ reads .changeset any more — `make release-dry-run` no longer " +
			"depends on pending changesets, so this gate proves nothing. Delete it.")
	}

	// Which path filters watch .changeset/**? Parsed from the live filter block,
	// so moving the entry between filters keeps the gate honest.
	watching := filtersWatchingChangesets(t, root)
	if len(watching) == 0 {
		t.Fatal("no path filter in ci.yml lists '.changeset/**' — a changeset-only pull request now " +
			"triggers no path-filtered job at all")
	}

	var doc struct {
		Jobs map[string]struct {
			If string `yaml:"if"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github", "workflows", "ci.yml")), &doc); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}
	job, ok := doc.Jobs["release-dry-run"]
	if !ok {
		t.Fatal("ci.yml has no release-dry-run job")
	}
	for _, f := range watching {
		if strings.Contains(job.If, "needs.changes.outputs."+f) {
			return
		}
	}
	t.Errorf("release-dry-run's `if:` consumes none of the filters that watch '.changeset/**' (%s), so a "+
		"changeset-only pull request skips it — and that is the pull request that opens the release "+
		"transaction the dry run exists to rehearse.\n  if: %s", strings.Join(watching, ", "), job.If)
}

// filtersWatchingChangesets returns the names of the dorny/paths-filter filters
// whose path list includes a '.changeset/' pattern.
func filtersWatchingChangesets(t *testing.T, root string) []string {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				With struct {
					Filters string `yaml:"filters"`
				} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github", "workflows", "ci.yml")), &doc); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}
	// The filters value is itself YAML: filter name -> list of path patterns.
	for _, job := range doc.Jobs {
		for _, step := range job.Steps {
			if step.With.Filters == "" {
				continue
			}
			var filters map[string][]string
			if err := yaml.Unmarshal([]byte(step.With.Filters), &filters); err != nil {
				t.Fatalf("parse paths-filter filters block: %v", err)
			}
			var out []string
			for name, patterns := range filters {
				for _, p := range patterns {
					if strings.Contains(p, ".changeset/") {
						out = append(out, name)
						break
					}
				}
			}
			slices.Sort(out) // map iteration order is random; the failure message is not
			return out
		}
	}
	t.Fatal("ci.yml has no dorny/paths-filter step with a `filters:` block")
	return nil
}

// changesetReadRE matches a real read of the changeset directory, not the word
// "changeset" in prose: a path segment, which is how every reader spells it.
var changesetReadRE = regexp.MustCompile(`\.changeset[/"']`)

// dryRunReadsChangesets reports whether anything `make release-dry-run` runs
// still reads the changeset directory.
func dryRunReadsChangesets(t *testing.T, root string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(filepath.Join(root, "release"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return err
		}
		switch filepath.Ext(path) {
		case ".sh", ".mjs", ".js":
		default:
			return nil
		}
		b, e := os.ReadFile(path)
		if e == nil && changesetReadRE.Match(b) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk release/: %v", err)
	}
	return found
}
