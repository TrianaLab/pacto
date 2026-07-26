package validation

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePrefix = "github.com/trianalab/pacto/v3/"

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

// disallowedImport returns a non-empty reason when the import is disallowed, "" when allowed.
func disallowedImport(path string) string {
	if strings.HasPrefix(path, modulePrefix) {
		if !allowedInternal[path] {
			return "internal import not on allowlist (contract, evidence, finding, graph)"
		}
		return ""
	}
	if forbiddenExact[path] {
		return "forbidden exact import (engine must stay pure)"
	}
	for _, bad := range forbiddenPrefix {
		if strings.HasPrefix(path, bad) {
			return "forbidden prefix import (engine must stay pure)"
		}
	}
	return ""
}

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
			if reason := disallowedImport(p); reason != "" {
				t.Errorf("%s imports disallowed %q: %s", e.Name(), p, reason)
			}
		}
	}
}

func TestDisallowedImport(t *testing.T) {
	cases := []struct {
		path string
		want string // "" = allowed, non-empty = reason
	}{
		// allowed: module-internal on allowlist
		{modulePrefix + "pkg/contract", ""},
		{modulePrefix + "pkg/evidence", ""},
		{modulePrefix + "pkg/finding", ""},
		{modulePrefix + "pkg/graph", ""},
		// allowed: stdlib (not forbidden)
		{"fmt", ""},
		{"strings", ""},
		// disallowed: module-internal not on allowlist
		{modulePrefix + "pkg/oci", "internal import not on allowlist (contract, evidence, finding, graph)"},
		// disallowed: forbidden exact
		{"os", "forbidden exact import (engine must stay pure)"},
		{"net", "forbidden exact import (engine must stay pure)"},
		{"net/http", "forbidden exact import (engine must stay pure)"},
		// disallowed: forbidden prefix
		{"k8s.io/client-go", "forbidden prefix import (engine must stay pure)"},
		{"sigs.k8s.io/controller-runtime", "forbidden prefix import (engine must stay pure)"},
	}
	for _, tc := range cases {
		got := disallowedImport(tc.path)
		if tc.want == "" && got != "" {
			t.Errorf("disallowedImport(%q) = %q, want allowed (empty)", tc.path, got)
		}
		if tc.want != "" && got == "" {
			t.Errorf("disallowedImport(%q) = allowed, want %q", tc.path, tc.want)
		}
	}
}
