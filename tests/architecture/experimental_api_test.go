package architecture

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestAlphaPackagesSayTheyAreExperimental: delivery is Alpha, so the packages
// it added to pkg/ must tell an importer their API is outside semver before a
// rename after the release forces a major bump.
func TestAlphaPackagesSayTheyAreExperimental(t *testing.T) {
	root := docsRoot(t)
	for _, pkg := range []string{"delivery", "publish", "signing", "signing/testutil", "deliveryrecord", "ocireferrer"} {
		pkgs, err := parser.ParseDir(token.NewFileSet(), filepath.Join(root, "pkg", pkg), nil, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		doc := ""
		for name, p := range pkgs {
			if strings.HasSuffix(name, "_test") {
				continue
			}
			for _, f := range p.Files {
				if f.Doc != nil {
					doc += f.Doc.Text()
				}
			}
		}
		if !strings.Contains(doc, "Experimental: not covered by semantic versioning") {
			t.Errorf("pkg/%s: its package doc must say \"Experimental: not covered by semantic versioning\"", pkg)
		}
	}
}
