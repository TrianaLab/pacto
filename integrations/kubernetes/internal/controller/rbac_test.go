package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/nodeagent"
)

// TestClusterRole_ConfigMapVerbs guards the RBAC needed to observe ConfigMap-backed
// configurations. The observer reads ConfigMaps through the manager's cached client,
// whose informer performs a List+Watch to sync; granting only "get" leaves the
// informer unable to start and every ConfigMap observation fails under least-privilege
// RBAC. This asserts the generated role keeps get;list;watch on configmaps (matching
// every other observed resource) so a reverted rbac marker is caught.
func TestClusterRole_ConfigMapVerbs(t *testing.T) {
	data, err := os.ReadFile("../../config/rbac/role.yaml")
	if err != nil {
		t.Fatalf("read role.yaml: %v", err)
	}

	var role struct {
		Rules []struct {
			APIGroups []string `yaml:"apiGroups"`
			Resources []string `yaml:"resources"`
			Verbs     []string `yaml:"verbs"`
		} `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &role); err != nil {
		t.Fatalf("parse role.yaml: %v", err)
	}

	var verbs []string
	for _, rule := range role.Rules {
		if slices.Contains(rule.APIGroups, "") && slices.Contains(rule.Resources, "configmaps") {
			verbs = rule.Verbs
			break
		}
	}
	if verbs == nil {
		t.Fatal("no core-group configmaps rule found in role.yaml")
	}
	for _, want := range []string{"get", "list", "watch"} {
		if !slices.Contains(verbs, want) {
			t.Errorf("configmaps rule missing verb %q (cached client informer needs get;list;watch)", want)
		}
	}
}

// rbacRule is one ClusterRole or Role rule as both the generated role and the chart templates spell it.
type rbacRule struct {
	APIGroups     []string `yaml:"apiGroups"`
	Resources     []string `yaml:"resources"`
	Verbs         []string `yaml:"verbs"`
	ResourceNames []string `yaml:"resourceNames"`
}

// rbacDoc is one ClusterRole or Role document.
type rbacDoc struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Rules []rbacRule `yaml:"rules"`
}

// chartDirective is any template line: the metadata labels and the `if`/`end` around feature-gated rules.
// Dropping only the directive lines keeps the gated rules, so the parsed template is the role with every
// feature enabled. A templated resourceNames entry is dropped with them, which reads as unscoped: the
// check is about the verbs the chart grants, and helm-unittest pins the names.
var chartDirective = regexp.MustCompile(`(?m)^.*\{\{.*\}\}.*$`)

// readRoles parses every ClusterRole and Role document of a manifest, after dropping template lines when
// strip is set, so no helm binary is needed.
func readRoles(t *testing.T, path string, strip bool) []rbacDoc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strip {
		data = chartDirective.ReplaceAll(data, nil)
	}
	return parseRoles(t, path, data)
}

// parseRoles parses every ClusterRole and Role document of data.
func parseRoles(t *testing.T, path string, data []byte) []rbacDoc {
	t.Helper()
	var docs []rbacDoc
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var d rbacDoc
		if err := dec.Decode(&d); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if d.Kind == "ClusterRole" || d.Kind == "Role" {
			if len(d.Rules) == 0 {
				t.Fatalf("%s: a %s has no rules", path, d.Kind)
			}
			docs = append(docs, d)
		}
	}
	return docs
}

// grants reports whether one of rules grants verb on resource in group for every name in names: an
// unscoped rule grants every name, a scoped one only its own.
func grants(rules []rbacRule, group, resource, verb string, names []string) bool {
	for _, r := range rules {
		if !slices.Contains(r.APIGroups, group) || !slices.Contains(r.Resources, resource) || !slices.Contains(r.Verbs, verb) {
			continue
		}
		if len(r.ResourceNames) == 0 {
			return true
		}
		if len(names) > 0 && !slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(r.ResourceNames, n) }) {
			return true
		}
	}
	return false
}

// TestChartRBAC_GrantsGeneratedRole asserts every permission controller-gen derives from the RBAC markers
// (config/rbac/role.yaml) is granted by the Helm chart, which is hand-written: a marker added without
// mirroring it in the chart breaks Helm installs. A cluster-wide marker must be granted by the chart's
// ClusterRole. A namespaced marker (`namespace=`) must be granted in that namespace: by the ClusterRole,
// or by the chart's Role there, which is role.yaml for the operator namespace (kustomize's "system"
// placeholder) and nodeagent-namespace.yaml for the node agent's. Rules the generated role holds for
// optional components sit behind the chart's feature gates, so the check reads the templates with every
// feature on; the always-on rules are pinned by helm-unittest.
func TestChartRBAC_GrantsGeneratedRole(t *testing.T) {
	const chart = "../../charts/pacto-operator/templates/"
	chartRole := func(file string) []rbacRule {
		t.Helper()
		for _, d := range readRoles(t, chart+file, true) {
			if d.Kind == "Role" {
				return d.Rules
			}
		}
		t.Fatalf("%s has no Role", file)
		return nil
	}
	clusterRole := readRoles(t, chart+"clusterrole.yaml", true)[0].Rules
	byNamespace := map[string][]rbacRule{
		"":                         nil,
		"system":                   chartRole("role.yaml"),
		nodeagent.DefaultNamespace: chartRole("nodeagent-namespace.yaml"),
	}
	generated := readRoles(t, "../../config/rbac/role.yaml", false)
	if len(generated) != len(byNamespace) {
		t.Fatalf("generated %d roles, want the ClusterRole and one Role per namespace in %v", len(generated), slices.Collect(maps.Keys(byNamespace)))
	}
	for _, doc := range generated {
		ns := doc.Metadata.Namespace
		namespaced, known := byNamespace[ns]
		if !known {
			t.Errorf("a marker names namespace %q, which no chart Role covers", ns)
			continue
		}
		for _, r := range doc.Rules {
			for _, g := range r.APIGroups {
				for _, res := range r.Resources {
					for _, v := range r.Verbs {
						if !grants(clusterRole, g, res, v, r.ResourceNames) && !grants(namespaced, g, res, v, r.ResourceNames) {
							t.Errorf("chart does not grant %q on %q in group %q (namespace %q, names %v)", v, res, g, ns, r.ResourceNames)
						}
					}
				}
			}
		}
	}
}

func TestGrants_ResourceNames(t *testing.T) {
	rules := []rbacRule{{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{"get"}, ResourceNames: []string{"a"}}}
	for _, tc := range []struct {
		names []string
		want  bool
	}{{[]string{"a"}, true}, {[]string{"a", "b"}, false}, {nil, false}} {
		if got := grants(rules, "", "namespaces", "get", tc.names); got != tc.want {
			t.Errorf("names %v: got %v, want %v", tc.names, got, tc.want)
		}
	}
}

const chartTemplates = "../../charts/pacto-operator/templates/"

// rbacTemplate matches a template that renders a Role, a ClusterRole or a binding.
var rbacTemplate = regexp.MustCompile(`kind: (Cluster)?Role|include "pacto-operator.binding"`)

// rbacTemplates lists every chart template that renders RBAC, relative to the templates directory. Helm
// partials (a base name starting with "_") render no document and are skipped.
func rbacTemplates(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(chartTemplates, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), "_") || filepath.Ext(path) != ".yaml" {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && rbacTemplate.Match(data) {
			rel, _ := filepath.Rel(chartTemplates, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", chartTemplates, err)
	}
	return out
}

// inventoryAssert is one helm-unittest assertion of tests/rbac_inventory_test.yaml.
type inventoryAssert struct {
	Equal *struct {
		Path  string `yaml:"path"`
		Value any    `yaml:"value"`
	} `yaml:"equal"`
	DocumentIndex *int `yaml:"documentIndex"`
}

// inventory is tests/rbac_inventory_test.yaml.
type inventory struct {
	Templates []string `yaml:"templates"`
	Tests     []struct {
		It       string            `yaml:"it"`
		Template string            `yaml:"template"`
		Asserts  []inventoryAssert `yaml:"asserts"`
	} `yaml:"tests"`
}

// TestRBACInventoryListsEveryRBACTemplate keeps tests/rbac_inventory_test.yaml complete: every template
// that renders a Role, a ClusterRole or a binding is in its templates, and in each render every binding's
// roleRef names a role the chart renders in that render (never a built-in role such as edit or admin).
// The one exception is upstream Flux's own cluster-admin binding, vendored under idp.flux.install
// (upstreamFluxAdmin): it is upstream's, and the inventory pins its two subjects.
func TestRBACInventoryListsEveryRBACTemplate(t *testing.T) {
	data, err := os.ReadFile("../../charts/pacto-operator/tests/rbac_inventory_test.yaml")
	if err != nil {
		t.Fatalf("read the inventory: %v", err)
	}
	var inv inventory
	if err := yaml.Unmarshal(data, &inv); err != nil {
		t.Fatalf("parse the inventory: %v", err)
	}
	found := rbacTemplates(t)
	if len(found) == 0 {
		t.Fatal("no chart template renders RBAC")
	}
	for _, tpl := range found {
		if !slices.Contains(inv.Templates, "templates/"+tpl) {
			t.Errorf("templates/%s renders RBAC and is missing from tests/rbac_inventory_test.yaml", tpl)
		}
	}
	for render, docs := range inventoryDocs(inv) {
		roles := map[string]bool{}
		for _, d := range docs {
			if d["kind"] == "Role" || d["kind"] == "ClusterRole" {
				roles[fmt.Sprint(d["kind"], "/", d["metadata.name"])] = true
			}
		}
		for _, d := range docs {
			ref, ok := d["roleRef"].(map[string]any)
			if !ok {
				continue
			}
			if !roles[fmt.Sprint(ref["kind"], "/", ref["name"])] && !upstreamFluxAdmin(render, d) {
				t.Errorf("%s: %s %v binds %s %v, which the chart does not render", render, d["kind"], d["metadata.name"], ref["kind"], ref["name"])
			}
		}
	}
}

// upstreamFluxAdmin is upstream Flux's cluster-reconciler binding in the idp flux install render: cluster-admin
// for the kustomize-controller and helm-controller ServiceAccounts in flux-system, and nobody else.
func upstreamFluxAdmin(render string, d map[string]any) bool {
	want := []any{
		map[string]any{"kind": "ServiceAccount", "name": "kustomize-controller", "namespace": "flux-system"},
		map[string]any{"kind": "ServiceAccount", "name": "helm-controller", "namespace": "flux-system"},
	}
	return render == "idp flux install" && d["kind"] == "ClusterRoleBinding" && d["metadata.name"] == "cluster-reconciler" &&
		fmt.Sprint(d["roleRef"]) == fmt.Sprint(map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "cluster-admin"}) &&
		fmt.Sprint(d["subjects"]) == fmt.Sprint(want)
}

// inventoryDocs groups the pinned documents of the per-render inventory cases ("<render>: every rule ...")
// by render: each document is the map of the paths its equal assertions pin.
func inventoryDocs(inv inventory) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for _, tc := range inv.Tests {
		render, _, ok := strings.Cut(tc.It, ": every rule")
		if !ok {
			continue
		}
		byIndex := map[string]map[string]any{}
		for _, a := range tc.Asserts {
			if a.Equal == nil || a.DocumentIndex == nil {
				continue
			}
			key := tc.Template + "#" + fmt.Sprint(*a.DocumentIndex)
			if byIndex[key] == nil {
				byIndex[key] = map[string]any{}
				out[render] = append(out[render], byIndex[key])
			}
			byIndex[key][a.Equal.Path] = a.Equal.Value
		}
	}
	return out
}

// TestNoClusterRoleGrantsSecrets fails on any ClusterRole rule naming Secrets in any template, read with
// every feature on: Secrets are read only where a namespaced Role grants them. A managed kind whose
// objects hold secret values (secretBearingKinds) is refused the same way, in a rule or a managed kind.
func TestNoClusterRoleGrantsSecrets(t *testing.T) {
	checked := 0
	for _, tpl := range rbacTemplates(t) {
		data, err := os.ReadFile(chartTemplates + tpl)
		if err != nil {
			t.Fatalf("read %s: %v", tpl, err)
		}
		// Only the ClusterRole documents are read: the others hold templating that is no YAML once stripped.
		for doc := range strings.SplitSeq(string(inlineRuleIncludes(t, data)), "\n---") {
			if !strings.Contains(doc, "kind: ClusterRole\n") {
				continue
			}
			for _, d := range parseRoles(t, tpl, chartDirective.ReplaceAll([]byte(doc), nil)) {
				checked++
				for _, r := range d.Rules {
					if slices.Contains(r.Resources, "secrets") {
						t.Errorf("%s: a ClusterRole grants %v on secrets", tpl, r.Verbs)
					}
					for _, g := range r.APIGroups {
						for _, res := range r.Resources {
							if slices.Contains(secretBearingKinds[g], res) {
								t.Errorf("%s: a ClusterRole grants %v on %s/%s, whose objects hold secret values", tpl, r.Verbs, g, res)
							}
						}
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no ClusterRole was checked")
	}
	// The managed rules render from the helper's kinds, so the kinds are checked too.
	managedKindsGrantNoSecrets(t)
}

// ruleInclude is a whole-line include of a named template, indented with nindent: how the chart pulls
// shared rules into a role.
var ruleInclude = regexp.MustCompile(`(?m)^\s*\{\{-? include "([^"]+)".*\| nindent (\d+) \}\}$`)

// chartDefine is one named template of a chart partial.
var chartDefine = regexp.MustCompile(`(?s)\{\{- define "([^"]+)" -\}\}\n(.*?)\n\{\{- end \}\}`)

// inlineRuleIncludes replaces each whole-line include in data with the named template's body, indented as
// the include asks, so the rules a role pulls from a partial are read too.
func inlineRuleIncludes(t *testing.T, data []byte) []byte {
	t.Helper()
	defines := map[string]string{}
	partials, err := filepath.Glob(chartTemplates + "*/_*.tpl")
	if err != nil {
		t.Fatal(err)
	}
	top, _ := filepath.Glob(chartTemplates + "_*.tpl")
	for _, p := range append(top, partials...) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		for _, m := range chartDefine.FindAllStringSubmatch(string(b), -1) {
			defines[m[1]] = m[2]
		}
	}
	for range 3 {
		data = ruleInclude.ReplaceAllFunc(data, func(line []byte) []byte {
			m := ruleInclude.FindSubmatch(line)
			body, ok := defines[string(m[1])]
			if !ok {
				return line
			}
			pad := strings.Repeat(" ", int(m[2][0]-'0'))
			return []byte(pad + strings.ReplaceAll(body, "\n", "\n"+pad))
		})
	}
	return data
}

// TestOnlyNotesLooksUpDetectedConfigMap keeps detection inert: the chart reads
// a ConfigMap only from NOTES.txt, so no rendered manifest can depend on what
// the operator detected, and NOTES pins the <fullname>-detected name.
func TestOnlyNotesLooksUpDetectedConfigMap(t *testing.T) {
	lookup := regexp.MustCompile(`lookup\s+"v1"\s+"ConfigMap"`)
	found := false
	err := filepath.WalkDir(chartTemplates, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !lookup.Match(data) {
			return nil
		}
		if filepath.Base(path) != "NOTES.txt" {
			t.Errorf("%s looks up a ConfigMap; only NOTES.txt may", path)
			return nil
		}
		found = true
		if !bytes.Contains(data, []byte(`printf "%s-detected"`)) {
			t.Errorf("NOTES.txt looks up a ConfigMap without naming it printf \"%%s-detected\"")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the chart templates: %v", err)
	}
	if !found {
		t.Error("NOTES.txt does not look up the detected ConfigMap")
	}
}

// verbList is one rule's verbs as every chart template and helper spells them, in flow style.
var verbList = regexp.MustCompile(`verbs:\s*\[([^\]]*)\]`)

// TestNoRenderGrantsImpersonate keeps the chart free of the verbs that let the operator act as someone else
// or grant more than it holds: a member answers the hub's reviews with its own SubjectAccessReview and
// impersonates nobody. It reads every RBAC template with template lines stripped, so every gated rule
// counts, and every verb list in the helpers, whose rules templates include.
// deployerBind reports whether r is the one bind the chart grants: under
// profile idp the promoter binds each service's deployer account to the
// <fullname>-deployer ClusterRole, and to no other role (helm-unittest pins
// the resourceName; its line is a directive, so it is dropped here).
func deployerBind(tpl string, r rbacRule) bool {
	return tpl == "delivery/rbac.yaml" && slices.Equal(r.Verbs, []string{"bind"}) &&
		slices.Equal(r.Resources, []string{"clusterroles"}) && slices.Equal(r.APIGroups, []string{"rbac.authorization.k8s.io"})
}

func TestNoRenderGrantsImpersonate(t *testing.T) {
	forbidden := []string{"impersonate", "escalate", "bind", "*"}
	check := func(where string, verbs []string) {
		for _, v := range verbs {
			if v = strings.Trim(strings.TrimSpace(v), `"'`); slices.Contains(forbidden, v) {
				t.Errorf("%s grants %q", where, v)
			}
		}
	}
	for _, tpl := range rbacTemplates(t) {
		data, err := os.ReadFile(filepath.Join(chartTemplates, tpl))
		if err != nil {
			t.Fatal(err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(chartDirective.ReplaceAll(data, nil)))
		for {
			var d rbacDoc
			if err := dec.Decode(&d); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				t.Fatalf("parse templates/%s: %v", tpl, err)
			}
			for _, r := range d.Rules {
				if deployerBind(tpl, r) {
					continue
				}
				check(fmt.Sprintf("templates/%s %s on %v", tpl, d.Kind, r.Resources), r.Verbs)
			}
		}
	}
	helpers, _ := filepath.Glob(filepath.Join(chartTemplates, "_*.tpl"))
	if len(helpers) == 0 {
		t.Fatal("no chart helper found")
	}
	for _, h := range helpers {
		data, err := os.ReadFile(h)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range verbList.FindAllSubmatch(data, -1) {
			check(filepath.Base(h), strings.Split(string(m[1]), ","))
		}
	}
}
