package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default external catalog carries no provenance annotations, so its
// expected digest is the only identity a resumed or adopted publish can be
// decided by, and the signature must cover exactly that digest. Signing
// `$(crane digest "$REF")` would sign whatever the tag points at when signing
// runs.
func TestExternalCatalogSignsExpectedDigest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	block, ok := jobsOf(t, string(b))["external-catalog"]
	if !ok {
		t.Fatal("release.yml has no external-catalog job")
	}
	for _, want := range []string{
		"go run ./cmd/genexternalcatalog check --digest-only",
		`PACTO_EXPECT_DIGEST="$EXPECTED"`,
		`cosign sign --yes "${REF}@${EXPECTED}"`,
		"id-token: write",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the external-catalog job does not contain %q", want)
		}
	}
	if strings.Contains(block, "$(crane digest") {
		t.Error("the external-catalog job reads the digest to sign from the tag; sign @${EXPECTED} only")
	}
}
