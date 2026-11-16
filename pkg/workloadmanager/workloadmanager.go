// Package workloadmanager names the manager that owns a workload (Argo CD,
// Flux, Helm or a manager Pacto does not support) from the workload's own
// metadata: labels, annotations, managedFields managers and the controller
// ownerReference. It is pure and read-only: it holds no client, imports no
// Kubernetes package and never moves ownership. Callers read the object and
// pass its metadata in.
//
// Every label, annotation or manager value that reaches Manager passes
// sanitize: anyone who can patch a workload chooses these strings, and they
// are printed to terminals and signed into delivery records.
package workloadmanager

import (
	"slices"
	"strings"
)

// Manager names.
const (
	ArgoCD            = "argocd"
	FluxKustomization = "flux-kustomization"
	FluxHelmRelease   = "flux-helmrelease"
	Helm              = "helm"
	Unsupported       = "unsupported"
	Ambiguous         = "ambiguous"
	None              = "none"
)

// Object is the metadata of one workload.
type Object struct {
	Group, Kind, Namespace, Name string
	Labels, Annotations          map[string]string
	// Managers are the managedFields entries. Entries of a subresource (the
	// deployment controller's status writes) are ignored.
	Managers []FieldManager
	// Controller is the ownerReference with controller: true, if any.
	Controller *OwnerRef
}

// FieldManager is one managedFields entry.
type FieldManager struct{ Manager, Subresource string }

// OwnerRef is a controller ownerReference.
type OwnerRef struct{ Kind, Name string }

// Manager is the detected manager of a workload. OwnerName is empty when only
// a managedFields entry names the manager. For Argo CD an empty
// OwnerNamespace means the Argo CD control-plane namespace.
type Manager struct {
	Name, Product, OwnerKind, OwnerNamespace, OwnerName, Evidence string
}

const (
	argoTrackingID       = "argocd.argoproj.io/tracking-id"
	helmReleaseName      = "helm.toolkit.fluxcd.io/name"
	helmReleaseNamespace = "helm.toolkit.fluxcd.io/namespace"
	kustomizeName        = "kustomize.toolkit.fluxcd.io/name"
	kustomizeNamespace   = "kustomize.toolkit.fluxcd.io/namespace"
	helmName             = "meta.helm.sh/release-name"
	helmNamespace        = "meta.helm.sh/release-namespace"
	instanceLabel        = "app.kubernetes.io/instance"

	maxValue    = 253
	unprintable = "<unprintable>"
)

type fingerprint func(Object) (Manager, bool)

// strong signals each name one manager on their own; two of them naming
// different managers read ambiguous.
var strong = []fingerprint{argoTracking, fluxHelmRelease, fluxKustomization, helmRelease, controller}

// ponytail: fingerprint table; a manager outside it reads none, and none under
// a Stage applier reads Unknown. Add a row when a team reports one.
var weak = []fingerprint{helmManager, argoInstance, unsupportedMark, unsupportedManager, managerFallback}

// Detect names the manager of o.
func Detect(o Object) Manager {
	var hits []Manager
	for _, f := range strong {
		if m, ok := f(o); ok {
			hits = append(hits, m)
		}
	}
	// Flux's helm-controller installs through Helm: the release annotations
	// under its labels are the same chain.
	if slices.ContainsFunc(hits, func(m Manager) bool { return m.Name == FluxHelmRelease }) {
		hits = slices.DeleteFunc(hits, func(m Manager) bool { return m.Name == Helm })
	}
	switch {
	case len(hits) == 1:
		return hits[0]
	case len(hits) > 1:
		var ev []string
		for _, h := range hits {
			ev = append(ev, h.Product+" "+h.Evidence)
		}
		return Manager{Name: Ambiguous, Evidence: strings.Join(ev, " and ")}
	}
	for _, f := range weak {
		if m, ok := f(o); ok {
			return m
		}
	}
	return Manager{Name: None}
}

// Describe is the one display line every surface prints for m.
func Describe(m Manager) string {
	switch m.Name {
	case "", None:
		return "unmanaged"
	case Ambiguous:
		return "ambiguous (" + m.Evidence + ")"
	}
	owner := m.OwnerKind
	if m.OwnerName != "" {
		name := m.OwnerName
		if m.OwnerNamespace != "" && m.Name != Helm {
			name = m.OwnerNamespace + "/" + name
		}
		owner += " " + name
	}
	if owner != "" {
		owner += ", "
	}
	s := m.Product + " (" + owner + m.Evidence + ")"
	if m.Name == Unsupported {
		s += ", not supported"
	}
	return s
}

func argoTracking(o Object) (Manager, bool) {
	id, ok := o.Annotations[argoTrackingID]
	if !ok {
		return Manager{}, false
	}
	m := Manager{Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application"}
	app, rest, ok1 := strings.Cut(id, ":")
	groupKind, nsName, ok2 := strings.Cut(rest, ":")
	group, kind, ok3 := strings.Cut(groupKind, "/")
	ns, name, ok4 := strings.Cut(nsName, "/")
	if !ok1 || !ok2 || !ok3 || !ok4 || app == "" {
		m.Evidence = "malformed tracking annotation " + sanitize(id)
		return m, true
	}
	if group != o.Group || kind != o.Kind || ns != o.Namespace || name != o.Name {
		return Manager{Name: Unsupported, Product: "Argo CD", Evidence: "tracking id of another object"}, true
	}
	if appNs, appName, found := strings.Cut(app, "_"); found {
		m.OwnerNamespace, app = sanitize(appNs), appName
	}
	m.OwnerName, m.Evidence = sanitize(app), "tracking annotation"
	return m, true
}

func fluxHelmRelease(o Object) (Manager, bool) {
	return labelPair(o, helmReleaseName, helmReleaseNamespace,
		Manager{Name: FluxHelmRelease, Product: "Flux", OwnerKind: "HelmRelease", Evidence: "helm-controller labels"})
}

func fluxKustomization(o Object) (Manager, bool) {
	return labelPair(o, kustomizeName, kustomizeNamespace,
		Manager{Name: FluxKustomization, Product: "Flux", OwnerKind: "Kustomization", Evidence: "kustomize-controller labels"})
}

func labelPair(o Object, nameKey, nsKey string, m Manager) (Manager, bool) {
	name, ok1 := o.Labels[nameKey]
	ns, ok2 := o.Labels[nsKey]
	m.OwnerName, m.OwnerNamespace = sanitize(name), sanitize(ns)
	return m, ok1 && ok2
}

func helmRelease(o Object) (Manager, bool) {
	name, ok1 := o.Annotations[helmName]
	ns, ok2 := o.Annotations[helmNamespace]
	return Manager{Name: Helm, Product: "Helm", OwnerKind: "release", OwnerNamespace: sanitize(ns), OwnerName: sanitize(name), Evidence: "release annotations"}, ok1 && ok2
}

func controller(o Object) (Manager, bool) {
	if o.Controller == nil {
		return Manager{}, false
	}
	return Manager{Name: Unsupported, Product: "Operator", OwnerKind: sanitize(o.Controller.Kind), OwnerName: sanitize(o.Controller.Name), Evidence: "controller ownerReference"}, true
}

func helmManager(o Object) (Manager, bool) {
	return Manager{Name: Helm, Product: "Helm", OwnerKind: "release", Evidence: "field manager helm"}, manager(o, func(n string) bool { return n == "helm" }) != ""
}

// argoInstance needs an argocd field manager too: every Helm chart sets the
// instance label to its release name.
func argoInstance(o Object) (Manager, bool) {
	app, ok := o.Labels[instanceLabel]
	if !ok || manager(o, isArgo) == "" {
		return Manager{}, false
	}
	return Manager{Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application", OwnerName: sanitize(app), Evidence: "instance label"}, true
}

var marks = []struct{ key, product, evidence string }{
	{"kapp.k14s.io/app", "kapp", "app label"},
	{"objectset.rio.cattle.io/id", "Rancher Fleet", "bundle annotation"},
	{"moniker.spinnaker.io/application", "Spinnaker", "moniker annotation"},
	{"skaffold.dev/run-id", "Skaffold", "run-id label"},
}

func unsupportedMark(o Object) (Manager, bool) {
	for _, mk := range marks {
		_, inLabels := o.Labels[mk.key]
		_, inAnnotations := o.Annotations[mk.key]
		if inLabels || inAnnotations {
			return Manager{Name: Unsupported, Product: mk.product, Evidence: mk.evidence}, true
		}
	}
	return Manager{}, false
}

func unsupportedManager(o Object) (Manager, bool) {
	if n := manager(o, func(n string) bool { return n == "Terraform" }); n != "" {
		return Manager{Name: Unsupported, Product: "Terraform", Evidence: "field manager " + n}, true
	}
	if n := manager(o, func(n string) bool { return strings.HasPrefix(n, "pulumi-kubernetes") }); n != "" {
		return Manager{Name: Unsupported, Product: "Pulumi", Evidence: "field manager " + sanitize(n)}, true
	}
	return Manager{}, false
}

func managerFallback(o Object) (Manager, bool) {
	for _, f := range []struct {
		match func(string) bool
		m     Manager
	}{
		{func(n string) bool { return n == "kustomize-controller" }, Manager{Name: FluxKustomization, Product: "Flux", OwnerKind: "Kustomization"}},
		{func(n string) bool { return n == "helm-controller" }, Manager{Name: FluxHelmRelease, Product: "Flux", OwnerKind: "HelmRelease"}},
		{isArgo, Manager{Name: ArgoCD, Product: "Argo CD", OwnerKind: "Application"}},
	} {
		if n := manager(o, f.match); n != "" {
			f.m.Evidence = "field manager " + sanitize(n)
			return f.m, true
		}
	}
	return Manager{}, false
}

func isArgo(n string) bool { return strings.HasPrefix(n, "argocd") }

// manager returns the first main-resource field manager that matches.
func manager(o Object, match func(string) bool) string {
	for _, f := range o.Managers {
		if f.Subresource == "" && match(f.Manager) {
			return f.Manager
		}
	}
	return ""
}

// sanitize keeps printable ASCII only (the delivery.CheckCorrelationID rule;
// this package must not import pkg/delivery), caps v at maxValue bytes and
// replaces a refused value with <unprintable>.
func sanitize(v string) string {
	for i := range len(v) {
		if v[i] < 0x20 || v[i] > 0x7e {
			return unprintable
		}
	}
	return v[:min(len(v), maxValue)]
}
