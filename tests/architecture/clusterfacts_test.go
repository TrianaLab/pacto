package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestOnlyPactifyCompletesWithClusterFacts keeps cluster facts out of every
// completion but a cluster draft's: only internal/pactify (and
// internal/discover, which defines them) names CompleteCluster or
// ClusterFacts, so pacto ci and every other caller complete from the folder
// alone.
// clusterFactsRE is either identifier, alone: refuseClusterFacts in
// pkg/contract names no cluster input.
var clusterFactsRE = regexp.MustCompile(`\b(CompleteCluster|ClusterFacts)\b`)

func TestOnlyPactifyCompletesWithClusterFacts(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case d.Name() == ".claude", d.Name() == ".git", d.Name() == "vendor", d.Name() == "node_modules",
				rel == "integrations", rel == "internal/discover", rel == "internal/pactify":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
		if err != nil {
			return err
		}
		if clusterFactsRE.Match(data) {
			t.Errorf("%s names CompleteCluster or ClusterFacts; only internal/pactify completes with cluster facts", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
