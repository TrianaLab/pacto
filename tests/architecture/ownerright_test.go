package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ownerRightCallers are the only production files outside the defining
// package that call the owner rule: one rule for every surface, never a
// re-implemented membership check. A story that adds a caller adds its row.
var ownerRightCallers = map[string][]string{
	// The declare-time check (pacto declare, pacto_declare, the dashboard).
	"delivery.DecideOwnerRight(": {"internal/app/declare.go"},
	// A created service's first owner (pacto_create_service, the dashboard).
	"delivery.DecideFirstOwner(": {"internal/app/create_service.go", "internal/app/declare.go"},
	// The fleet's owner pass over published bundles, and the operator's
	// OwnerVerified condition over running ones.
	"publish.VerifyOwnerRight(": {"integrations/kubernetes/internal/controller/owner.go", "internal/app/ownerright.go"},
}

func TestOwnerRightCallers(t *testing.T) {
	root := repoRoot(t)
	found := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".claude", "vendor", "node_modules", ".git":
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
		rel, _ := filepath.Rel(root, path)
		for call := range ownerRightCallers {
			if strings.Contains(string(data), call) {
				found[call] = append(found[call], filepath.ToSlash(rel))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for call, want := range ownerRightCallers {
		if got := found[call]; !slices.Equal(got, want) {
			t.Errorf("%s is called from %v, want exactly %v", call, got, want)
		}
	}
}
