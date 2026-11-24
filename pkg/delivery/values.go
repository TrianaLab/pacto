package delivery

import (
	"cmp"
	"fmt"
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
// spec.source.path. An empty valueFiles selects nothing, as Argo CD reads it.
func argoValues(what string, applier map[string]any, environment string) ValuesSelection {
	raw, _ := field(applier, "spec", "source", "helm", "valueFiles").([]any)
	if len(raw) == 0 {
		return overlayValues(what, cleanPath(str(applier, "spec", "source", "path")), environment)
	}
	files := make([]string, len(raw))
	for i, f := range raw {
		files[i] = shownValuesFile(f)
	}
	if len(files) > maxShownValuesFiles {
		files = append(files[:maxShownValuesFiles:maxShownValuesFiles], fmt.Sprintf("%d more", len(raw)-maxShownValuesFiles))
	}
	sel := ValuesSelection{File: strings.Join(files, ", "), Form: FormHelmValues}
	if len(files) != 1 || environment == "" || files[0] != "values-"+environment+".yaml" {
		sel.Problem = what + " selects " + sel.File + ", and " + delivers(environment)
	}
	return sel
}

// maxShownValuesFiles is how many valueFiles entries a selection names; it
// counts the rest, so a long list cannot bloat Stage status.
const maxShownValuesFiles = 3

// otherValuesFile names a valueFiles entry Pacto does not show.
const otherValuesFile = "a values file outside the chart"

// shownValuesFile is a valueFiles entry as Pacto shows it: a path inside the
// repository as written, without a leading "./". Any other entry (a URL, an
// absolute path, one that is no string or is overly long) is otherValuesFile:
// a URL may carry credentials or an internal host, and a reader of the Stage
// may have no grant on the Application it comes from.
func shownValuesFile(f any) string {
	s, ok := f.(string)
	s = strings.TrimPrefix(s, "./")
	if !ok || s == "" || len(s) > 253 || path.IsAbs(s) || strings.ContainsAny(s, ":@?#\\") {
		return otherValuesFile
	}
	return s
}

// kustomizeValues reads the overlay the Kustomizations applying the Stage's
// OCIRepository build, from any namespace, the set Mediated reads; any of
// them building another environment's overlay is the Problem.
func kustomizeValues(name string, ks []KustomizationView, namespace, environment string) ValuesSelection {
	var sel ValuesSelection
	for _, k := range ks {
		if src := k.Spec.SourceRef; src.Kind != ApplierFlux || cmp.Or(src.Namespace, k.Metadata.Namespace) != namespace || src.Name != name {
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
		sel.Problem = what + " builds " + p + ", and " + delivers(environment)
	}
	return sel
}

// delivers says which environment the Stage delivers, for a Problem.
func delivers(environment string) string {
	if environment == "" {
		return "the Stage names no environment"
	}
	return "the Stage delivers environment " + environment
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
