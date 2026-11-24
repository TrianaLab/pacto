/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// stageSpecRules reads the generated Stage CRD's spec-level CEL rules that
// target .environment. The envtest specs in delivery_level_crd_test.go
// ("Stage environment") exercise the same rules against an API server.
func stageSpecRules(t *testing.T) apiextensionsv1.ValidationRules {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "bases", "delivery.pacto.trianalab.io_stages.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(b, &crd); err != nil {
		t.Fatal(err)
	}
	var out apiextensionsv1.ValidationRules
	for _, r := range crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].XValidations {
		if r.FieldPath == ".environment" {
			out = append(out, r)
		}
	}
	return out
}

func TestStageEnvironmentPattern(t *testing.T) {
	var re *regexp.Regexp
	for _, r := range stageSpecRules(t) {
		if m := regexp.MustCompile(`matches\('([^']+)'\)`).FindStringSubmatch(r.Rule); m != nil {
			if !strings.HasPrefix(r.Rule, "oldSelf.hasValue() ||") {
				t.Fatalf("pattern rule must spare a Stage created before it: %q", r.Rule)
			}
			re = regexp.MustCompile(m[1])
		}
	}
	if re == nil {
		t.Fatal("no DNS-label rule on spec.environment")
	}
	for _, env := range []string{"../prod", "Prod", "prod/x", "-prod", ""} {
		if re.MatchString(env) {
			t.Errorf("%q accepted", env)
		}
	}
	if !re.MatchString("pre-production") {
		t.Error("pre-production refused")
	}
}

func TestStageEnvironmentImmutable(t *testing.T) {
	for _, r := range stageSpecRules(t) {
		if r.Rule == "self.environment == oldSelf.environment" {
			if r.Message != "environment is fixed at creation; create a new Stage" {
				t.Fatalf("message %q", r.Message)
			}
			return
		}
	}
	t.Fatal("no immutability rule on spec.environment")
}
