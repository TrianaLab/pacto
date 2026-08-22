package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFleetStaysRouteNeutral enforces ADR-2: pkg/fleet owns canonical identities
// and route-neutral entity references, but NEVER dashboard routes or hrefs. Route
// emission is a transport concern; MCP and other non-dashboard consumers use the
// same fleet facts and must never receive dashboard URLs. If a dashboard route
// concept returns to pkg/fleet, this test fails and forces it back to the
// transport boundary.
//
// It is an AST invariant, not a string ban, so it targets exactly the route
// vocabulary (a "/fleet" path literal, a RouteFor* helper, a Route/Href
// navigation field, the net/url escaper) and never trips on innocent words like
// "routing" in a comment or the "pacto.dev/fleet/v1" schema URN.
func TestFleetStaysRouteNeutral(t *testing.T) {
	root := docsRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "pkg", "fleet", "*.go"))
	if err != nil {
		t.Fatalf("glob pkg/fleet: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no pkg/fleet/*.go source found — moved or renamed?")
	}
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		scanned++
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", f, perr)
		}
		base := filepath.Base(f)

		// (a) No import of net/url (the route-path escaper).
		for _, imp := range file.Imports {
			if imp.Path.Value == `"net/url"` {
				t.Errorf("%s imports net/url — pkg/fleet must not build route paths; move it to the dashboard transport (ADR-2).", base)
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			// (b) No "/fleet..." path string literal (the schema URN pacto.dev/fleet/v1
			// starts with "pacto.dev", not "/fleet", so it is not matched).
			case *ast.BasicLit:
				if node.Kind == token.STRING {
					if v, uerr := stringLitValue(node.Value); uerr == nil && strings.HasPrefix(v, "/fleet") {
						t.Errorf("%s contains a %q route-path literal — routes belong to the dashboard transport (ADR-2).", base, v)
					}
				}
			// (c) No RouteFor* helper function.
			case *ast.FuncDecl:
				if strings.HasPrefix(node.Name.Name, "RouteFor") {
					t.Errorf("%s declares %s — RouteFor* route helpers belong to the dashboard transport (ADR-2).", base, node.Name.Name)
				}
			// (d) No navigation field named Route or Href on a fleet model.
			case *ast.StructType:
				for _, fld := range node.Fields.List {
					for _, name := range fld.Names {
						if name.Name == "Route" || name.Name == "Href" {
							t.Errorf("%s declares a struct field %q — fleet product models must be route-neutral; add navigation hrefs at the dashboard transport (ADR-2).", base, name.Name)
						}
					}
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no non-test pkg/fleet source scanned")
	}
}

// stringLitValue unquotes a Go string literal value. strconv.Unquote handles both
// double-quoted and backtick-quoted forms.
func stringLitValue(lit string) (string, error) {
	return strconv.Unquote(lit)
}
