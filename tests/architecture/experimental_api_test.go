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
		files, err := filepath.Glob(filepath.Join(root, "pkg", pkg, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		doc := ""
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly|parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if f.Doc != nil {
				doc += f.Doc.Text()
			}
		}
		if !strings.Contains(doc, "Experimental: not covered by semantic versioning") {
			t.Errorf("pkg/%s: its package doc must say \"Experimental: not covered by semantic versioning\"", pkg)
		}
	}
}
