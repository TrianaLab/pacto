package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// scopeExempt are the cluster-side repository opens that may leave Scope
// unset, keyed "<file>:<func>", each with the reason.
var scopeExempt = map[string]string{
	"integrations/kubernetes/cmd/delivery/main.go:upstreamReader": "reads another cluster's signed records and never writes",
	"integrations/kubernetes/cmd/delivery/main.go:publishers":     "the registry watch reads candidate signatures and never writes",
}

// TestEveryClusterSideOpenSetsAScope keeps every cluster-side registry writer
// behind the one push guard (decision 11): every ocireferrer.Options or
// evidenceoci.RepositoryOptions literal in the operator module, the evidence
// store and the Evidence Server sets Scope, every operator call of the
// run-record registry.Open passes a scope that is not nil, and every operator
// publish client routes its writes through oci.WithTransport. A new writer added
// without a scope fails here, so the chart's push-prefix test still proves
// where the cluster pushes.
func TestEveryClusterSideOpenSetsAScope(t *testing.T) {
	root := repoRoot(t)
	files := []string{filepath.Join(root, "internal/app/evidence.go")}
	for _, dir := range []string{"integrations/kubernetes", "internal/evidenceoci"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "vendor" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	used := map[string]bool{}
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range unscopedOpens(t, rel, string(src), used) {
			t.Error(v)
		}
	}
	for k := range scopeExempt {
		if !used[k] {
			t.Errorf("scopeExempt entry %q matches no repository open: remove it", k)
		}
	}
}

// TestUnscopedOpensFindsAMissingScope is the walk's self-test.
func TestUnscopedOpensFindsAMissingScope(t *testing.T) {
	src := `package x

import (
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	rr "github.com/trianalab/pacto/v4/pkg/runrecord/registry"
)

func open() {
	_ = ocireferrer.Options{Insecure: nil}
	_ = ocireferrer.Options{Scope: s}
	_, _ = rr.Open("p", nil, nil, nil)
	_, _ = rr.Open("p", nil, nil, s)
}
`
	got := unscopedOpens(t, "integrations/kubernetes/x.go", src, map[string]bool{})
	if len(got) != 2 || !strings.Contains(got[0], "sets no Scope") || !strings.Contains(got[1], "registry.Open") {
		t.Fatalf("violations = %q, want one literal and one registry.Open", got)
	}
	pub := `package x

import (
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

func f() {
	_ = publish.NewClient(nil)
	_ = publish.NewScopedClient(nil, nil, nil)
	_ = publish.NewScopedClient(nil, oci.NewPushScope(""), nil)
}
`
	if got := unscopedOpens(t, "integrations/kubernetes/x.go", pub, map[string]bool{}); len(got) != 2 || !strings.Contains(got[0], "publish client") {
		t.Fatalf("publish client violations = %q, want two", got)
	}
	alias := "package evidenceoci\n\nfunc f() { _ = RepositoryOptions{} }\n"
	if got := unscopedOpens(t, "internal/evidenceoci/x.go", alias, map[string]bool{}); len(got) != 1 {
		t.Fatalf("alias literal violations = %q, want one", got)
	}
}

// unscopedOpens parses src (the file rel) and returns every options literal
// without Scope and every operator registry.Open whose scope is nil, outside
// scopeExempt; it marks the exempt entries it used.
func unscopedOpens(t *testing.T, rel, src string, used map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := importNames(f)
	var out []string
	for _, decl := range f.Decls {
		fn := ""
		if d, ok := decl.(*ast.FuncDecl); ok {
			fn = d.Name.Name
		}
		key := rel + ":" + fn
		ast.Inspect(decl, func(n ast.Node) bool {
			var bad string
			switch n := n.(type) {
			case *ast.CompositeLit:
				if isOptionsType(n.Type, f.Name.Name, names) && !hasKey(n, "Scope") {
					bad = "an options literal sets no Scope"
				}
			case *ast.CallExpr:
				if strings.HasPrefix(rel, "integrations/kubernetes/") {
					bad = unscopedCall(n, names)
				}
			}
			if bad == "" {
				return true
			}
			if _, ok := scopeExempt[key]; ok {
				used[key] = true
				return true
			}
			out = append(out, fset.Position(n.Pos()).String()+": "+bad+
				" (cluster-side writes go through oci.PushScope; exempt a read-only open in scopeExempt with the reason)")
			return true
		})
	}
	return out
}

// unscopedCall is why an operator call writes outside the push guard: a
// run-record registry.Open with no scope or a publish client with no
// guarded transport; empty when it does not.
func unscopedCall(c *ast.CallExpr, names map[string]string) string {
	if isCall(c, names["github.com/trianalab/pacto/v4/pkg/runrecord/registry"], "Open") && (len(c.Args) != 4 || isNil(c.Args[3])) {
		return "registry.Open passes no scope"
	}
	if unguardedPublishClient(c, names) {
		return "a publish client sets no push scope"
	}
	return ""
}

// unguardedPublishClient reports a publish.NewClient call, which takes no
// scope, or a publish.NewScopedClient call with a nil scope.
func unguardedPublishClient(c *ast.CallExpr, names map[string]string) bool {
	pub := names["github.com/trianalab/pacto/v4/pkg/publish"]
	return isCall(c, pub, "NewClient") || (isCall(c, pub, "NewScopedClient") && len(c.Args) > 1 && isNil(c.Args[1]))
}

// importNames maps each import path of f to the name it is used under.
func importNames(f *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[p] = name
	}
	return names
}

func isOptionsType(e ast.Expr, pkg string, names map[string]string) bool {
	if id, ok := e.(*ast.Ident); ok {
		return pkg == "evidenceoci" && id.Name == "RepositoryOptions"
	}
	return isSelector(e, names["github.com/trianalab/pacto/v4/pkg/ocireferrer"], "Options") ||
		isSelector(e, names["github.com/trianalab/pacto/v4/internal/evidenceoci"], "RepositoryOptions")
}

func isSelector(e ast.Expr, pkg, sel string) bool {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || pkg == "" {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == pkg && s.Sel.Name == sel
}

func isCall(c *ast.CallExpr, pkg, fn string) bool { return isSelector(c.Fun, pkg, fn) }

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func hasKey(c *ast.CompositeLit, key string) bool {
	for _, el := range c.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == key {
				return true
			}
		}
	}
	return false
}
