package architecture

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// TestCatalogDigestPinsMatchDefault keeps the hand-written copies of the
// default catalog digest in step with externalcatalog.DefaultDigest(): no
// generator writes them, so a catalog change must fail here until they move.
// The published newsletter-service demo bundle keeps its old digest on purpose
// (immutable; TestDemoNewsletterCatalogMismatch pins it).
func TestCatalogDigestPinsMatchDefault(t *testing.T) {
	digest := externalcatalog.DefaultDigest()
	short := digest[:len("sha256:")+8] + "..."
	for _, pin := range []struct {
		want string
		path []string
	}{
		{digest, []string{"tests", "acceptance", "local", "declare-fixture", "pacto.inferred.yaml"}},
		{short, []string{"docs", "pactify.md"}},
		{short, []string{"docs", "contract-reference", "inferred-record.md"}},
	} {
		if !strings.Contains(readRepoFile(t, pin.path...), "default "+pin.want) {
			t.Errorf("%s does not name the default catalog digest %s", strings.Join(pin.path, "/"), pin.want)
		}
	}
}
