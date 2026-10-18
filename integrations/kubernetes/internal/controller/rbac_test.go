package controller

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
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

// rbacRule is one ClusterRole rule as both the generated role and the chart template spell it.
type rbacRule struct {
	APIGroups     []string `yaml:"apiGroups"`
	Resources     []string `yaml:"resources"`
	Verbs         []string `yaml:"verbs"`
	ResourceNames []string `yaml:"resourceNames"`
}

// chartDirective is any template line: the metadata labels and the `if`/`end` around feature-gated rules.
// Dropping only the directive lines keeps the gated rules, so the parsed template is the ClusterRole with
// every feature enabled.
var chartDirective = regexp.MustCompile(`(?m)^.*\{\{.*\}\}.*$`)

// TestChartClusterRole_GrantsGeneratedRole asserts every permission controller-gen derives from the RBAC
// markers (config/rbac/role.yaml) is granted by the Helm chart's ClusterRole, which is hand-written: a
// marker added without mirroring it in the chart breaks Helm installs. The template is parsed after
// dropping its template lines, so no helm binary is needed. Rules the generated role holds for optional
// components (dashboard, Evidence Server) sit behind the chart's feature gates, so the check reads the
// template with every feature on; the always-on rules are pinned by helm-unittest.
func TestChartClusterRole_GrantsGeneratedRole(t *testing.T) {
	read := func(path string, strip bool) []rbacRule {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strip {
			data = chartDirective.ReplaceAll(data, nil)
		}
		var role struct {
			Rules []rbacRule `yaml:"rules"`
		}
		if err := yaml.Unmarshal(data, &role); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if len(role.Rules) == 0 {
			t.Fatalf("%s has no rules", path)
		}
		return role.Rules
	}
	generated := read("../../config/rbac/role.yaml", false)
	chart := read("../../charts/pacto-operator/templates/clusterrole.yaml", true)

	granted := func(group, resource, verb string) bool {
		for _, r := range chart {
			if len(r.ResourceNames) == 0 && slices.Contains(r.APIGroups, group) &&
				slices.Contains(r.Resources, resource) && slices.Contains(r.Verbs, verb) {
				return true
			}
		}
		return false
	}
	for _, r := range generated {
		for _, g := range r.APIGroups {
			for _, res := range r.Resources {
				for _, v := range r.Verbs {
					if !granted(g, res, v) {
						t.Errorf("chart ClusterRole does not grant %q on %q in group %q", v, res, g)
					}
				}
			}
		}
	}
}
