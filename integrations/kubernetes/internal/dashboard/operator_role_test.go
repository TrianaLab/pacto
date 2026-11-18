/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
	"sigs.k8s.io/yaml"
)

// TestDashboardClusterRoleWithinOperatorRole: the operator applies the
// dashboard ClusterRole, and the apiserver refuses a grant its applier does not
// hold, so every verb of every dashboard rule must be held by the operator's
// own ClusterRole for that group and resource.
func TestDashboardClusterRoleWithinOperatorRole(t *testing.T) {
	raw, err := os.ReadFile("../../config/rbac/role.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var operator *rbacv1.ClusterRole
	for doc := range strings.SplitSeq(string(raw), "\n---\n") {
		var r rbacv1.ClusterRole
		if err := yaml.Unmarshal([]byte(doc), &r); err != nil {
			t.Fatal(err)
		}
		if r.Kind == "ClusterRole" {
			operator = &r
		}
	}
	if operator == nil {
		t.Fatal("no ClusterRole in config/rbac/role.yaml")
	}
	held := func(group, resource, verb string, names []string) bool {
		return slices.ContainsFunc(operator.Rules, func(op rbacv1.PolicyRule) bool {
			return slices.Contains(op.APIGroups, group) && slices.Contains(op.Resources, resource) && slices.Contains(op.Verbs, verb) &&
				(len(op.ResourceNames) == 0 || len(names) > 0 && !slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(op.ResourceNames, n) }))
		})
	}
	role := clusterRoleAC(false).(*rbacv1ac.ClusterRoleApplyConfiguration)
	for _, r := range role.Rules {
		for _, g := range r.APIGroups {
			for _, res := range r.Resources {
				for _, v := range r.Verbs {
					if !held(g, res, v, r.ResourceNames) {
						t.Errorf("the operator cannot grant %s %s.%s %v: add it to its own role", v, res, g, r.ResourceNames)
					}
				}
			}
		}
	}
}

// TestClusterRoleListsGitOpsObjects: the unscoped dashboard lists the four
// GitOps kinds that group inferred services, with list only (no get or watch),
// and the scoped half grants none of them.
func TestClusterRoleListsGitOpsObjects(t *testing.T) {
	want := map[string]string{
		"argoproj.io":                 "applications",
		"kustomize.toolkit.fluxcd.io": "kustomizations",
		"helm.toolkit.fluxcd.io":      "helmreleases",
		"source.toolkit.fluxcd.io":    "gitrepositories",
	}
	for _, scoped := range []bool{false, true} {
		got := map[string]string{}
		for _, r := range clusterRoleAC(scoped).(*rbacv1ac.ClusterRoleApplyConfiguration).Rules {
			for _, g := range r.APIGroups {
				if _, ok := want[g]; !ok {
					continue
				}
				if !slices.Equal(r.Verbs, []string{"list"}) {
					t.Errorf("scoped=%v: %s verbs = %v, want [list]", scoped, g, r.Verbs)
				}
				got[g] = strings.Join(r.Resources, ",")
			}
		}
		if scoped && len(got) != 0 {
			t.Errorf("the scoped half grants GitOps objects: %v", got)
		}
		if !scoped && !maps.Equal(got, want) {
			t.Errorf("GitOps rules = %v, want %v", got, want)
		}
	}
}
