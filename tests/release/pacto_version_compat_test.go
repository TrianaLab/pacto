package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// TestEveryPactoVersionNamesItsRelease: the installation page and the
// generated compatibility snippet name the core and operator release each
// supported pactoVersion after 2.0 needs. The generator once stopped at 2.3,
// so a new contract version must be added to both.
func TestEveryPactoVersionNamesItsRelease(t *testing.T) {
	root := repoRoot(t)
	for _, page := range []string{"docs/installation.md", "integrations/kubernetes/docs/generated/_compatibility.md"} {
		b, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(b)), " ")
		for _, v := range contract.SupportedVersions[1:] {
			if !strings.Contains(text, "`pactoVersion: \""+v+"\"` need") {
				t.Errorf("%s names no release for pactoVersion %s", page, v)
			}
		}
	}
}
