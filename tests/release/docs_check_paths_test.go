package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs_check.py builds ./cmd/pacto and validates every fenced Pacto contract example
// in the docs. If the docs-check workflow's path filter omits the validation code, a
// change to the validator/schema/CLI can invalidate a doc example without triggering
// the gate. Assert the filter includes the validation inputs.
func TestDocsCheckPathsCoverValidationCode(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "docs-check.yml"))
	if err != nil {
		t.Fatalf("read docs-check.yml: %v", err)
	}
	s := string(b)
	for _, p := range []string{"cmd/pacto/**", "pkg/validation/**", "pkg/contract/**", "schema/**"} {
		if !strings.Contains(s, p) {
			t.Errorf("docs-check.yml path filter is missing %q; a change there could break a fenced-contract example without running docs-check", p)
		}
	}
}
