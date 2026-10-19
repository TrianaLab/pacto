package controller

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
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
