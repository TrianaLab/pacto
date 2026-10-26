package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeliveryNamesNoParkedDomain keeps delivery identifiers off pacto.dev, a
// domain Pacto does not own: operation types, record predicates and apiserver
// extra keys live under delivery.pacto.trianalab.io, the API group. It scans
// every authored file whose path names delivery, pactify or autonomy. The fleet
// and evidence schema URNs predate delivery and are out of its scope.
func TestDeliveryNamesNoParkedDomain(t *testing.T) {
	root := docsRoot(t)
	skip := map[string]bool{"node_modules": true, ".git": true, "ui": true, "dist": true, ".claude": true}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		low := strings.ToLower(rel)
		if !strings.Contains(low, "delivery") && !strings.Contains(low, "pactify") && !strings.Contains(low, "autonomy") {
			return nil
		}
		if rel == filepath.Join("tests", "architecture", "delivery_domain_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "pacto.dev/") {
			t.Errorf("%s names pacto.dev/: delivery identifiers live under delivery.pacto.trianalab.io", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
