package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/trianalab/pacto/v4/pkg/finding"
)

// TestDemoFindingCodesAreRegistered pins every finding the demo fleet records to
// the engine's registry: the code exists and the stamped severity and category
// are the ones a real producer would stamp. source_fleet.go builds only for
// js/wasm, so it is read as source here rather than run. A demo showing a code
// the engine never emits teaches the reader a vocabulary that does not exist.
func TestDemoFindingCodesAreRegistered(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "source_fleet.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isFindingSlice(lit.Type) {
			return true
		}
		for _, e := range lit.Elts {
			fields := map[string]ast.Expr{}
			for _, kv := range e.(*ast.CompositeLit).Elts {
				kv := kv.(*ast.KeyValueExpr)
				fields[kv.Key.(*ast.Ident).Name] = kv.Value
			}
			code := finding.Code(value(t, fields["Code"], "Code"))
			seen++
			if finding.CategoryOf(code) == "" {
				t.Errorf("demo finding code %q is not in the finding registry", code)
				continue
			}
			sev, cat := value(t, fields["Severity"], "Severity"), value(t, fields["Category"], "Category")
			if want := string(finding.SeverityFor(code, true)); sev != strings.ToUpper(want) {
				t.Errorf("%s: severity %s, registry says %s", code, sev, want)
			}
			if want := string(finding.CategoryOf(code)); cat != strings.ToUpper(want) {
				t.Errorf("%s: category %s, registry says %s", code, cat, want)
			}
		}
		return true
	})
	if seen == 0 {
		t.Fatal("found no []finding.Finding literals in source_fleet.go")
	}
}

func isFindingSlice(e ast.Expr) bool {
	arr, ok := e.(*ast.ArrayType)
	if !ok {
		return false
	}
	sel, ok := arr.Elt.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Finding"
}

// value upper-cases a field for comparison: a constant drops its prefix, and a
// code constant is split at capitals (finding.CodeInterfaceAbsent ->
// INTERFACE_ABSENT); a string literal is upper-cased as is.
func value(t *testing.T, e ast.Expr, prefix string) string {
	t.Helper()
	switch v := e.(type) {
	case *ast.SelectorExpr:
		var b strings.Builder
		for i, r := range strings.TrimPrefix(v.Sel.Name, prefix) {
			if i > 0 && unicode.IsUpper(r) && prefix == "Code" {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToUpper(r))
		}
		return b.String()
	case *ast.BasicLit:
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			t.Fatal(err)
		}
		return strings.ToUpper(s)
	}
	t.Fatalf("unexpected finding field expression %T", e)
	return ""
}
