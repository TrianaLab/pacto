package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// applierImports are the module prefixes of an engine that applies objects a
// version carries: Pacto delivers through the Flux or Argo CD the cluster runs
// and never links one (#547).
var applierImports = []string{"helm.sh/helm", "sigs.k8s.io/kustomize"}

// applySelectors are the identifiers that make a call a server-side apply,
// by import path.
var applySelectors = map[string]map[string]bool{
	"sigs.k8s.io/controller-runtime/pkg/client": {"Apply": true, "FieldOwner": true, "ForceOwnership": true},
	"k8s.io/apimachinery/pkg/apis/meta/v1":      {"ApplyOptions": true},
	"k8s.io/apimachinery/pkg/types":             {"ApplyPatchType": true, "ApplyYAMLPatchType": true, "ApplyCBORPatchType": true},
}

// ownObjectAppliers are the files that server-side apply objects Pacto itself
// owns, each under its own field manager. None applies an object a version
// carries.
var ownObjectAppliers = map[string]string{
	"integrations/kubernetes/internal/dashboard/reconciler.go": "the dashboard Deployment, Service and RBAC the operator manages",
	"integrations/kubernetes/internal/evidence/reconciler.go":  "the Evidence Server Deployment",
	"integrations/kubernetes/internal/nodeagent/reconciler.go": "the node agent DaemonSet and its RBAC",
	"integrations/kubernetes/internal/detection/detector.go":   "the detected-providers ConfigMap",
	"integrations/kubernetes/internal/linker/linker.go":        "the linker's ConfigMap",
	"internal/runtimewatch/kubesink/kubesink.go":               "the node's NodeRuntimeObservation and its Lease",
}

// isApplierScanPath reports whether a repository-relative file is production
// Go the no-applier rule covers.
func isApplierScanPath(rel string) bool {
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	for _, p := range []string{"pkg/", "internal/", "integrations/kubernetes/internal/", "integrations/kubernetes/cmd/"} {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// applierCode returns why a production Go source could apply what a version
// carries: a Helm or Kustomize SDK import, the name pacto-applier, or a
// server-side apply outside ownObjectAppliers. applies reports whether the
// file server-side applies at all, so a stale ownObjectAppliers entry fails.
func applierCode(rel string, src []byte) (out []string, applies bool) {
	f, err := parser.ParseFile(token.NewFileSet(), rel, src, parser.SkipObjectResolution)
	if err != nil {
		return []string{rel + ": " + err.Error()}, false
	}
	alias := map[string]string{}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		for _, m := range applierImports {
			if strings.HasPrefix(path, m) {
				out = append(out, rel+" imports "+path)
			}
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		alias[name] = path
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if v, _ := strconv.Unquote(n.Value); n.Kind == token.STRING && strings.Contains(v, "pacto-applier") {
				out = append(out, rel+" names pacto-applier")
			}
		case *ast.SelectorExpr:
			if x, ok := n.X.(*ast.Ident); ok && applySelectors[alias[x.Name]][n.Sel.Name] {
				applies = true
			}
		}
		return true
	})
	if applies && ownObjectAppliers[rel] == "" {
		out = append(out, rel+" server-side applies: only the files in ownObjectAppliers apply Pacto's own objects")
	}
	return out, applies
}

// applierAPIVersion matches a document rendering a Flux or Argo CD object.
var applierAPIVersion = regexp.MustCompile(`(?m)^apiVersion:\s*["']?[a-z.]*(toolkit\.fluxcd\.io|argoproj\.io)/`)

// fluxOutside matches what marks a Flux object of its own (its CRD group, its
// namespace or upstream's part-of label) in a chart file outside files/flux/.
var fluxOutside = regexp.MustCompile(`(?m)^\s*group:\s*\S*toolkit\.fluxcd\.io|^\s+namespace:\s*flux-system\s*$|app\.kubernetes\.io/part-of:\s*flux\s*$`)

// fluxFilesRef matches a template reading a file under files/flux/.
var fluxFilesRef = regexp.MustCompile(`"files/flux/[^"]*"`)

// idpFluxDefine is the helper that gates the vendored Flux.
var idpFluxDefine = regexp.MustCompile(`(?s)define "pacto-operator\.idpFlux".*?\{\{-? end`)

// chartApplierFiles returns why chart files (by path relative to the chart)
// could render a Flux or Argo CD applier or source object, or Flux's own
// objects outside templates/idp/flux.yaml under idp.flux.install.
func chartApplierFiles(files map[string]string) []string {
	var out []string
	for rel, body := range files {
		if applierAPIVersion.MatchString(body) {
			out = append(out, rel+" renders a Flux or Argo CD object: Pacto applies none, the cluster's own applier does")
		}
		if strings.HasPrefix(rel, "files/flux/") {
			continue
		}
		if fluxOutside.MatchString(body) {
			out = append(out, rel+" holds a Flux object: Flux's own objects live only in files/flux/")
		}
		for _, ref := range fluxFilesRef.FindAllString(body, -1) {
			if ref != `"files/flux/VERSION"` && rel != "templates/idp/flux.yaml" {
				out = append(out, rel+" reads "+ref+": only templates/idp/flux.yaml renders files/flux/")
			}
		}
	}
	flux := files["templates/idp/flux.yaml"]
	guard, glob := strings.Index(flux, `{{- if include "pacto-operator.idpFlux" . }}`), strings.Index(flux, ".Files.Glob")
	if glob >= 0 && (guard < 0 || guard > glob) {
		out = append(out, `templates/idp/flux.yaml reads files/flux/ outside {{- if include "pacto-operator.idpFlux" . }}`)
	}
	if !strings.Contains(idpFluxDefine.FindString(files["templates/_helpers.tpl"]), `dig "flux" "install"`) {
		out = append(out, "pacto-operator.idpFlux no longer requires idp.flux.install")
	}
	sort.Strings(out)
	return out
}

// chartFiles reads the chart's templates, files and crds by path relative to the chart.
func chartFiles(t *testing.T, chart string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, dir := range []string{"templates", "files", "crds"} {
		_ = filepath.WalkDir(filepath.Join(chart, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // crds/ may not exist
			}
			rel, _ := filepath.Rel(chart, path)
			raw, rerr := os.ReadFile(path) //nolint:gosec // a path under the repository root
			if rerr != nil {
				t.Fatal(rerr)
			}
			files[filepath.ToSlash(rel)] = string(raw)
			return nil
		})
	}
	return files
}

// TestNoAppliedObjectsFromPacto guards #547's "no engine": no production Go
// links a Helm or Kustomize SDK, names pacto-applier or server-side applies
// beyond Pacto's own objects, and no chart file renders a Flux or Argo CD
// applier or source object; Flux's own objects live only in files/flux/,
// rendered by templates/idp/flux.yaml under idp.flux.install. The render half
// is integrations/kubernetes/internal/controller TestChartRendersNoAppliedObjects.
func TestNoAppliedObjectsFromPacto(t *testing.T) {
	root := repoRoot(t)
	applied := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".claude", "vendor", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !isApplierScanPath(rel) {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
		if err != nil {
			return err
		}
		out, applies := applierCode(rel, src)
		for _, v := range out {
			t.Error(v)
		}
		applied[rel] = applies
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel := range ownObjectAppliers {
		if !applied[rel] {
			t.Errorf("ownObjectAppliers names %s, which no longer server-side applies: drop the entry", rel)
		}
	}

	files := chartFiles(t, filepath.Join(root, "integrations", "kubernetes", "charts", "pacto-operator"))
	if files["templates/idp/flux.yaml"] == "" {
		t.Fatal("templates/idp/flux.yaml is missing: the scan reads the wrong chart")
	}
	for _, v := range chartApplierFiles(files) {
		t.Error(v)
	}
}

// TestNoAppliedObjectsFromPactoCatches pins one failing case per rule.
func TestNoAppliedObjectsFromPactoCatches(t *testing.T) {
	for name, src := range map[string]string{
		"helm sdk":       `package p; import _ "helm.sh/helm/v3/pkg/action"`,
		"kustomize sdk":  `package p; import _ "sigs.k8s.io/kustomize/api/krusty"`,
		"applier name":   `package p; const m = "pacto-applier"`,
		"apply patch":    `package p; import "sigs.k8s.io/controller-runtime/pkg/client"; var _ = client.Apply`,
		"field owner":    `package p; import c "sigs.k8s.io/controller-runtime/pkg/client"; var _ = c.FieldOwner("x")`,
		"apply options":  `package p; import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"; var _ = metav1.ApplyOptions{}`,
		"apply patch ty": `package p; import "k8s.io/apimachinery/pkg/types"; var _ = types.ApplyPatchType`,
	} {
		if out, _ := applierCode("pkg/delivery/x.go", []byte(src)); len(out) == 0 {
			t.Errorf("%s: applierCode found nothing", name)
		}
	}
	if out, applies := applierCode("internal/runtimewatch/kubesink/kubesink.go",
		[]byte(`package p; import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"; var _ = metav1.ApplyOptions{}`)); len(out) != 0 || !applies {
		t.Errorf("an own-object applier: got %v, applies %v", out, applies)
	}

	ok := map[string]string{
		"templates/idp/flux.yaml":               "{{- if include \"pacto-operator.idpFlux\" . }}\n{{- range $path, $_ := .Files.Glob \"files/flux/*.yaml\" }}\n{{- end }}\n{{- end }}\n",
		"templates/_helpers.tpl":                "{{- define \"pacto-operator.idpFlux\" -}}\n{{- if and (dig \"flux\" \"install\" false .Values.idp) }}true{{ end }}\n{{- end }}\n{{- define \"pacto-operator.fluxVersion\" -}}{{ .Files.Get \"files/flux/VERSION\" }}{{- end }}\n",
		"templates/clusterrole.yaml":            "kind: ClusterRole\nrules:\n- apiGroups: [kustomize.toolkit.fluxcd.io]\n  resources: [kustomizations]\n  verbs: [get]\n",
		"files/flux/namespace-flux-system.yaml": "apiVersion: v1\nkind: Namespace\nmetadata:\n  labels:\n    app.kubernetes.io/part-of: flux\n  name: flux-system\n",
	}
	if out := chartApplierFiles(ok); len(out) != 0 {
		t.Fatalf("the chart as it stands fails: %v", out)
	}
	for name, edit := range map[string][2]string{
		"(b) a Kustomization":        {"templates/delivery/ks.yaml", "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\n"},
		"(b) an Argo CD Application": {"templates/delivery/app.yaml", "---\napiVersion: argoproj.io/v1alpha1\nkind: Application\n"},
		"(b) a vendored source":      {"files/flux/ocirepository.yaml", "apiVersion: source.toolkit.fluxcd.io/v1\nkind: OCIRepository\n"},
		"(c) files/flux elsewhere":   {"templates/other.yaml", "{{ range $p, $_ := .Files.Glob \"files/flux/*.yaml\" }}{{ end }}\n"},
		"(c) a Flux namespace":       {"templates/other.yaml", "kind: Deployment\nmetadata:\n  namespace: flux-system\n"},
		"(c) a Flux CRD":             {"files/crds/x.yaml", "kind: CustomResourceDefinition\nspec:\n  group: source.toolkit.fluxcd.io\n"},
		"(c) the guard dropped":      {"templates/idp/flux.yaml", "{{- range $path, $_ := .Files.Glob \"files/flux/*.yaml\" }}\n{{- end }}\n"},
		"(c) the value dropped":      {"templates/_helpers.tpl", "{{- define \"pacto-operator.idpFlux\" -}}true{{- end }}\n"},
	} {
		files := map[string]string{}
		for k, v := range ok {
			files[k] = v
		}
		files[edit[0]] = edit[1]
		if len(chartApplierFiles(files)) == 0 {
			t.Errorf("%s: chartApplierFiles found nothing", name)
		}
	}
}
