package delivery

import (
	"cmp"
	"path"
	"strings"
)

// The forms an applier selects an environment's values in.
const (
	// FormHelmValues is an Argo CD spec.source.helm.valueFiles entry.
	FormHelmValues = "helm-values"
	// FormKustomizeOverlay is a Kustomize overlay directory,
	// overlays/<environment>, a Flux Kustomization or an Argo CD source
	// builds.
	FormKustomizeOverlay = "kustomize-overlay"
)

// ValuesSelection is the values file or Kustomize overlay an applier selects,
// as observed on the applier object. Problem says why it is not the one
// accepted form: values-<environment>.yaml alone, or overlays/<environment>.
type ValuesSelection struct{ File, Form, Problem string }

// SelectedValues is what the applier ref names selects for environment:
// applier is the object (an Argo CD Application), ks the Kustomizations of
// which those applying the ref's OCIRepository in namespace count. A Flux
// HelmRelease selects nothing: its chartRef takes no values file from inside
// the chart.
func SelectedValues(ref ApplyRef, applier map[string]any, ks []KustomizationView, namespace, environment string) ValuesSelection {
	switch refKind(ref) {
	case ApplierHelmRelease:
		return ValuesSelection{}
	case ApplierArgoCD:
		return argoValues("Application "+ref.Name, applier, environment)
	}
	return kustomizeValues(ref.Name, ks, namespace, environment)
}

// argoValues reads spec.source.helm.valueFiles, else an overlay
// spec.source.path.
func argoValues(what string, applier map[string]any, environment string) ValuesSelection {
	raw, ok := field(applier, "spec", "source", "helm", "valueFiles").([]any)
	if !ok {
		return overlayValues(what, cleanPath(str(applier, "spec", "source", "path")), environment)
	}
	files := make([]string, len(raw))
	for i, f := range raw {
		s, _ := f.(string)
		files[i] = strings.TrimPrefix(s, "./")
	}
	sel := ValuesSelection{File: strings.Join(files, ", "), Form: FormHelmValues}
	if len(files) != 1 || files[0] != "values-"+environment+".yaml" {
		sel.Problem = what + " selects " + sel.File + ", and the Stage delivers environment " + environment
	}
	return sel
}

// kustomizeValues reads the overlay the Kustomizations applying the Stage's
// OCIRepository build; any of them building another environment's overlay is
// the Problem.
func kustomizeValues(name string, ks []KustomizationView, namespace, environment string) ValuesSelection {
	var sel ValuesSelection
	for _, k := range ks {
		if !appliesStage(k, namespace, name) {
			continue
		}
		s := overlayValues("Kustomization "+k.Metadata.Name, cleanPath(k.Spec.Path), environment)
		// The first overlay counts, unless a later one is refused.
		if s.Form != "" && (sel.Form == "" || sel.Problem == "" && s.Problem != "") {
			sel = s
		}
	}
	return sel
}

// overlayValues is the overlay a built path p is: a directory whose parent
// is overlays, else nothing.
func overlayValues(what, p, environment string) ValuesSelection {
	if path.Base(path.Dir(p)) != "overlays" {
		return ValuesSelection{}
	}
	sel := ValuesSelection{File: p, Form: FormKustomizeOverlay}
	if path.Base(p) != environment {
		sel.Problem = what + " builds " + p + ", and the Stage delivers environment " + environment
	}
	return sel
}

// appliesStage says whether Kustomization k applies the OCIRepository name
// in namespace. A Kustomization in another namespace applies as that
// namespace's identity: it is that namespace's concern, and a tenant there
// cannot change this Stage's verdict.
func appliesStage(k KustomizationView, namespace, name string) bool {
	src := k.Spec.SourceRef
	return src.Kind == ApplierFlux && k.Metadata.Namespace == namespace && cmp.Or(src.Namespace, namespace) == namespace && src.Name == name
}

// helmSelectsValuesOnly says whether spec.source.helm sets valueFiles and
// nothing else.
func helmSelectsValuesOnly(applier map[string]any) bool {
	helm, _ := field(applier, "spec", "source", "helm").(map[string]any)
	return len(helm) == 1 && helm["valueFiles"] != nil
}
