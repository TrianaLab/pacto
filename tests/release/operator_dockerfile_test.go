package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The operator image release build (RELEASE_BUILD=1) resolves the freshly-published,
// pinned core module. proxy.golang.org may not have indexed a just-tagged version
// yet, so the release build must fetch direct from VCS (GOPROXY=direct) and skip the
// checksum database for the in-flight module — mirroring the standalone verify
// scripts. Without it the operator-image job can fail to resolve the core right after
// a core tag is published.
func TestOperatorDockerfileReleaseBuildFetchesDirect(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "integrations", "kubernetes", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	df := string(b)

	if !strings.Contains(df, "GOPROXY=direct") {
		t.Error("operator Dockerfile release build must set GOPROXY=direct so a freshly-published core tag resolves before the module proxy indexes it")
	}
	if !strings.Contains(df, "GONOSUMDB='github.com/trianalab/*'") {
		t.Error("operator Dockerfile release build must set GONOSUMDB for github.com/trianalab/* so the checksum database is not consulted for the in-flight module")
	}
}
