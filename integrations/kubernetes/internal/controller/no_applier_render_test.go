/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// renderedDoc is one document of a helm template render and the template it came from.
type renderedDoc struct {
	source string
	obj    map[string]any
}

const fluxTemplate = "pacto-operator/templates/idp/flux.yaml"

// str walks obj through keys and returns the string there, or "".
func str(obj map[string]any, keys ...string) string {
	var cur any = obj
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

func list(obj map[string]any, key string) []any {
	l, _ := obj[key].([]any)
	return l
}

func applierGroup(apiVersion string) bool {
	g, _, _ := strings.Cut(apiVersion, "/")
	return strings.HasSuffix(g, "toolkit.fluxcd.io") || g == "argoproj.io"
}

// fluxOwn reports whether a document is one of Flux's own objects: upstream's
// part-of label, its namespace or a CRD of a Flux group.
func fluxOwn(obj map[string]any) bool {
	return str(obj, "metadata", "labels", "app.kubernetes.io/part-of") == "flux" ||
		str(obj, "metadata", "namespace") == "flux-system" ||
		(str(obj, "kind") == "Namespace" && str(obj, "metadata", "name") == "flux-system") ||
		(str(obj, "kind") == "CustomResourceDefinition" && strings.HasSuffix(str(obj, "spec", "group"), "toolkit.fluxcd.io"))
}

// renderApplierViolations returns why a render breaks #547's "no engine": (b) a
// Flux or Argo CD object, (c) Flux's own objects from anywhere but
// templates/idp/flux.yaml or without idp.flux.install, (d) a cluster-admin
// binding other than upstream Flux's to its kustomize and helm controllers, and
// (e) a <fullname>-deployer ClusterRole naming a Flux or Argo CD group. A
// deployer ClusterRole of namespaced kinds and the promoter's bind on it pass.
func renderApplierViolations(docs []renderedDoc, fluxInstall bool) []string {
	var out []string
	fluxDeployments := 0
	for _, d := range docs {
		kind, name := str(d.obj, "kind"), str(d.obj, "metadata", "name")
		id := fmt.Sprintf("%s %s (%s)", kind, name, d.source)
		if applierGroup(str(d.obj, "apiVersion")) {
			out = append(out, id+" is a Flux or Argo CD object")
		}
		own := fluxOwn(d.obj)
		if own && (d.source != fluxTemplate || !fluxInstall) {
			out = append(out, id+" is a Flux object outside templates/idp/flux.yaml under idp.flux.install")
		}
		if own && kind == "Deployment" {
			fluxDeployments++
		}
		out = append(out, rbacViolations(d.obj, id, own)...)
	}
	if fluxInstall && fluxDeployments != 3 {
		out = append(out, fmt.Sprintf("idp.flux.install renders %d Flux controllers, not 3", fluxDeployments))
	}
	return out
}

// rbacViolations holds (d) and (e) for one document.
func rbacViolations(obj map[string]any, id string, own bool) []string {
	var out []string
	kind := str(obj, "kind")
	if kind == "ClusterRoleBinding" && str(obj, "roleRef", "name") == "cluster-admin" {
		subjects := make([]string, 0, len(list(obj, "subjects")))
		for _, s := range list(obj, "subjects") {
			m, _ := s.(map[string]any)
			subjects = append(subjects, str(m, "namespace")+"/"+str(m, "name"))
		}
		slices.Sort(subjects)
		if !own || !slices.Equal(subjects, []string{"flux-system/helm-controller", "flux-system/kustomize-controller"}) {
			out = append(out, fmt.Sprintf("%s binds cluster-admin to %v: only upstream Flux's kustomize and helm controllers hold it", id, subjects))
		}
	}
	if kind == "ClusterRole" && strings.HasSuffix(str(obj, "metadata", "name"), "-deployer") {
		for _, r := range list(obj, "rules") {
			m, _ := r.(map[string]any)
			for _, g := range list(m, "apiGroups") {
				if s, _ := g.(string); applierGroup(s + "/") {
					out = append(out, id+" grants "+s+": a deployer applies namespaced kinds, never a delivery object")
				}
			}
		}
	}
	return out
}

// parseRender splits a helm template render into its documents.
func parseRender(t *testing.T, out []byte) []renderedDoc {
	t.Helper()
	var docs []renderedDoc
	for raw := range strings.SplitSeq(string(out), "\n---\n") {
		var d renderedDoc
		for line := range strings.SplitSeq(raw, "\n") {
			if s, ok := strings.CutPrefix(line, "# Source: "); ok {
				d.source = s
				break
			}
		}
		if err := yaml.Unmarshal([]byte(raw), &d.obj); err != nil {
			t.Fatalf("parse the render (%s): %v", d.source, err)
		}
		if d.obj != nil {
			docs = append(docs, d)
		}
	}
	return docs
}

// TestChartRendersNoAppliedObjects renders the chart through Helm in every
// profile and Flux combination and holds renderApplierViolations empty: the
// render half of tests/architecture TestNoAppliedObjectsFromPacto (#547).
func TestChartRendersNoAppliedObjects(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		// CI's chart legs install Helm; a workstation without it still runs every other test here.
		t.Skip("helm not on PATH")
	}
	chart := filepath.Join("..", "..", "charts", "pacto-operator")
	renders := map[string][]string{
		"default":                   nil,
		"delivery":                  {"--set", "delivery.enabled=true", "--set", "delivery.flux.enabled=true", "--set", "delivery.argocd.enabled=true"},
		"hardened":                  {"--set", "profile=hardened", "--set", "controller.watchNamespaces={payments}"},
		"idp":                       {"--set", "profile=idp"},
		"idp with idp.flux.install": {"--set", "profile=idp", "--set", "idp.flux.install=true"},
	}
	// 515-S2's Crossplane install joins once its value is in the chart.
	if schema, _ := os.ReadFile(filepath.Join(chart, "values.schema.json")); strings.Contains(string(schema), `"crossplane"`) { //nolint:gosec // the chart under test
		renders["idp with idp.crossplane.install"] = []string{"--set", "profile=idp", "--set", "idp.crossplane.install=true"}
	}
	for name, extra := range renders {
		t.Run(name, func(t *testing.T) {
			args := append([]string{"template", "t", chart, "-n", "pacto",
				"--api-versions", "admissionregistration.k8s.io/v1/ValidatingAdmissionPolicy"}, extra...)
			out, err := exec.Command(helm, args...).Output() //nolint:gosec // helm from PATH, fixed arguments
			if err != nil {
				t.Fatalf("helm template: %v", err)
			}
			for _, v := range renderApplierViolations(parseRender(t, out), slices.Contains(extra, "idp.flux.install=true")) {
				t.Error(v)
			}
		})
	}
}

// TestRenderApplierViolationsCatches pins one failing case per rule, and the
// 511-S5 deployer ClusterRole with the promoter's bind on it as passing.
func TestRenderApplierViolationsCatches(t *testing.T) {
	doc := func(source, y string) renderedDoc {
		d := renderedDoc{source: "pacto-operator/templates/" + source}
		if err := yaml.Unmarshal([]byte(y), &d.obj); err != nil {
			t.Fatal(err)
		}
		return d
	}
	flux := func(y string) renderedDoc { d := doc("idp/flux.yaml", y); d.source = fluxTemplate; return d }
	fluxDeploys := []renderedDoc{
		flux("kind: Deployment\nmetadata: {name: source-controller, namespace: flux-system}"),
		flux("kind: Deployment\nmetadata: {name: kustomize-controller, namespace: flux-system}"),
		flux("kind: Deployment\nmetadata: {name: helm-controller, namespace: flux-system}"),
	}
	reconciler := "kind: ClusterRoleBinding\nmetadata: {name: cluster-reconciler, labels: {app.kubernetes.io/part-of: flux}}\nroleRef: {name: cluster-admin}\n" +
		"subjects: [{kind: ServiceAccount, name: kustomize-controller, namespace: flux-system}, {kind: ServiceAccount, name: helm-controller, namespace: flux-system}]"
	deployer := doc("delivery/deployer.yaml", "kind: ClusterRole\nmetadata: {name: t-pacto-operator-deployer}\nrules: [{apiGroups: ['', apps], resources: [deployments, services], verbs: ['*']}]")
	bind := doc("role.yaml", "kind: ClusterRole\nmetadata: {name: t-pacto-operator}\nrules: [{apiGroups: [rbac.authorization.k8s.io], resources: [clusterroles], verbs: [bind], resourceNames: [t-pacto-operator-deployer]}]")

	pass := append(slices.Clone(fluxDeploys), flux(reconciler), deployer, bind)
	if out := renderApplierViolations(pass, true); len(out) != 0 {
		t.Fatalf("(e) the Flux install with the deployer ClusterRole and its bind fails: %v", out)
	}
	if out := renderApplierViolations([]renderedDoc{deployer, bind}, false); len(out) != 0 {
		t.Fatalf("(e) the deployer ClusterRole without Flux fails: %v", out)
	}
	for name, c := range map[string]struct {
		docs    []renderedDoc
		install bool
	}{
		"(b) a Kustomization":        {[]renderedDoc{doc("x.yaml", "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization")}, false},
		"(b) an Argo CD Application": {[]renderedDoc{doc("x.yaml", "apiVersion: argoproj.io/v1alpha1\nkind: Application")}, false},
		"(c) Flux without the value": {fluxDeploys, false},
		"(c) Flux from elsewhere":    {append(slices.Clone(fluxDeploys), doc("x.yaml", "kind: ServiceAccount\nmetadata: {name: x, namespace: flux-system}")), true},
		"(c) a Flux CRD elsewhere":   {[]renderedDoc{doc("crds.yaml", "kind: CustomResourceDefinition\nspec: {group: source.toolkit.fluxcd.io}")}, false},
		"(c) the value renders none": {nil, true},
		"(d) a release SA as admin":  {[]renderedDoc{doc("x.yaml", "kind: ClusterRoleBinding\nmetadata: {name: x}\nroleRef: {name: cluster-admin}\nsubjects: [{kind: ServiceAccount, name: t-pacto-operator, namespace: pacto}]")}, false},
		"(d) Flux's binding widened": {append(slices.Clone(fluxDeploys), flux(strings.Replace(reconciler, "subjects: [", "subjects: [{kind: ServiceAccount, name: source-controller, namespace: flux-system}, ", 1))), true},
		"(e) a deployer of Flux":     {[]renderedDoc{doc("x.yaml", "kind: ClusterRole\nmetadata: {name: t-pacto-operator-deployer}\nrules: [{apiGroups: [kustomize.toolkit.fluxcd.io], resources: ['*'], verbs: ['*']}]")}, false},
		"(e) a deployer of Argo CD":  {[]renderedDoc{doc("x.yaml", "kind: ClusterRole\nmetadata: {name: t-pacto-operator-deployer}\nrules: [{apiGroups: [argoproj.io], resources: ['*'], verbs: ['*']}]")}, false},
	} {
		if len(renderApplierViolations(c.docs, c.install)) == 0 {
			t.Errorf("%s: renderApplierViolations found nothing", name)
		}
	}
}
