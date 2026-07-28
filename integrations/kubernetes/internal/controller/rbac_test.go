package controller

import (
	"os"
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
