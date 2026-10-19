package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/runtimefacts"
)

// constValue is one typed string constant and the trailing comment that says
// what it means.
type constValue struct {
	Value   string `json:"value"`
	Meaning string `json:"meaning"`
}

// printRegistry writes, as JSON, the engine vocabularies the operator reference
// documents (`gendocs registry`): the finding registry from its accessor, and
// the evidence kinds, outcomes and fact values read from pkg/evidence's typed
// constants, and the Kubernetes collector names from pkg/runtimefacts. release/scripts/gen_integration_docs.py consumes it instead of
// scraping Go source with regular expressions.
func printRegistry(w io.Writer, evidenceDir string) error {
	consts, err := typedConsts(evidenceDir)
	if err != nil {
		return err
	}
	values := func(typ string) []string {
		var out []string
		for _, c := range consts[typ] {
			out = append(out, c.Value)
		}
		return out
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{
		"findings":         finding.Registry(),
		"observationKinds": values("ObservationKind"),
		"outcomes":         values("Outcome"),
		"facts":            map[string][]constValue{"Protocol": consts["Protocol"], "Exposure": consts["Exposure"]},
		"collectors": map[string]string{
			"kubernetes":       runtimefacts.CollectorKubernetes,
			"kubernetesKernel": runtimefacts.CollectorKubernetesKernel,
			"kernel":           runtimefacts.CollectorKernel,
		},
	})
}

// typedConsts reads every `Name Type = "literal"` constant in dir's non-test Go
// files, grouped by type in source order, with its trailing comment.
func typedConsts(dir string) (map[string][]constValue, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	out := map[string][]constValue{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				typ, ok := vs.Type.(*ast.Ident)
				if !ok || len(vs.Values) != 1 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, _ := strconv.Unquote(lit.Value)
				out[typ.Name] = append(out[typ.Name], constValue{Value: v, Meaning: strings.TrimSpace(vs.Comment.Text())})
			}
		}
	}
	return out, nil
}
