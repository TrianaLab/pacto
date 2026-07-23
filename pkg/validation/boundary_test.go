package validation

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePrefix = "github.com/trianalab/pacto/v2/"

// allowedInternal is the engine's internal-import ALLOWLIST. Any import under the
// module prefix that is not listed here fails the test. pkg/graph is allowed
// because crossfield.go uses the pure graph.ParseDependencyRef and pkg/graph is
// itself dependency-pure (verified).
var allowedInternal = map[string]bool{
	modulePrefix + "pkg/contract": true,
	modulePrefix + "pkg/evidence": true,
	modulePrefix + "pkg/finding":  true,
	modulePrefix + "pkg/graph":    true,
}

// Impure imports the pure engine must never pull in, even from stdlib or outside
// the module. (The test file itself uses os, but _test.go files are skipped.)
var forbiddenExact = map[string]bool{"net": true, "net/http": true, "os": true}
var forbiddenPrefix = []string{"k8s.io/", "sigs.k8s.io/"}

func TestEngineImportBoundary(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, modulePrefix) {
				if !allowedInternal[p] {
					t.Errorf("%s imports disallowed internal %q (engine allowlist: contract, evidence, finding, graph)", e.Name(), p)
				}
				continue
			}
			if forbiddenExact[p] {
				t.Errorf("%s imports forbidden %q (engine must stay pure)", e.Name(), p)
			}
			for _, bad := range forbiddenPrefix {
				if strings.HasPrefix(p, bad) {
					t.Errorf("%s imports forbidden %q (engine must stay pure)", e.Name(), p)
				}
			}
		}
	}
}
