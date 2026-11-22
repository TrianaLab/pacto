package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// clusterLinkRE names a member link fact: the type or a field only it has. A
// target's Link (a linked workload, ObservedLink) shares the name and is no
// member link fact; Since is on both, so it is left out.
var clusterLinkRE = regexp.MustCompile(`\bClusterLink\b|\.Link\.(Channel|Issuers)\b`)

// gatePaths are the packages that decide what ships: impact, delivery and its
// records, and the operator's delivery gate.
var gatePaths = []string{"pkg/impact", "pkg/delivery", "pkg/deliveryrecord", "integrations/kubernetes/internal/delivery"}

// TestNoGateReadsClusterLink keeps member link facts for readers only: a
// link is inferred from a live channel and never clears a gate.
func TestNoGateReadsClusterLink(t *testing.T) {
	root := repoRoot(t)
	for _, dir := range gatePaths {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			data, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
			if err != nil {
				return err
			}
			if loc := clusterLinkRE.Find(data); loc != nil {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s reads a member link fact (%s): no gate may", rel, loc)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
