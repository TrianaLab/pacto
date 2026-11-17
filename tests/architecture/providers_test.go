package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// providerSurfaces are the only packages that may read provider rows: the
// fleet model, the detection format and mapper, the sources that produce
// rows, and the surfaces that show them (CLI, MCP, dashboard, WASM demo). No
// gate, impact, readiness, compliance or operator path may: a detected row
// is inferred and never clears a gate.
var providerSurfaces = []string{
	"pkg/fleet", "pkg/detect", "internal/fleetsrc", "internal/app",
	"internal/cli", "internal/mcp", "pkg/dashboard", "examples/demo",
}

// providerSymbols are the pkg/fleet names that carry provider rows.
var providerSymbols = []string{"Provider", "ProviderObject", "ProviderCount", "ProvidersResult", "ScopeCluster", "LimitationDetectionClusterMismatch"}

// TestOnlySurfacesReadProviders fails when a package outside providerSurfaces,
// in either module, names a provider row type, calls Query.Providers or maps
// a detection into rows. It matches by name (see deadcode_test.go for why), so
// a red run is always real.
func TestOnlySurfacesReadProviders(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "vendor" || d.Name() == "testdata" || rel == "tests" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || allowedProviderReader(filepath.ToSlash(filepath.Dir(rel))) {
			return nil
		}
		offenders = append(offenders, providerReads(t, path, rel)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range offenders {
		t.Errorf("%s reads provider rows outside the surfaces that may (%v)", o, providerSurfaces)
	}
}

func allowedProviderReader(dir string) bool {
	return slices.ContainsFunc(providerSurfaces, func(s string) bool { return dir == s || strings.HasPrefix(dir, s+"/") })
}

// providerReads lists, as rel:line, every provider read in the file at path.
func providerReads(t *testing.T, path, rel string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing %s: %v", rel, err)
	}
	var out []string
	at := func(n ast.Node) { out = append(out, rel+":"+strconv.Itoa(fset.Position(n.Pos()).Line)) }
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && ((x.Name == "fleet" && slices.Contains(providerSymbols, n.Sel.Name)) || (x.Name == "detect" && n.Sel.Name == "ProviderRows")) {
				at(n)
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Providers" && len(n.Args) == 0 {
				at(n)
			}
		}
		return true
	})
	return out
}
