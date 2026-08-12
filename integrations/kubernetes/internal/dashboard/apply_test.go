/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"testing"

	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
	"k8s.io/apimachinery/pkg/types"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
)

func testOwnerRef() *metav1ac.OwnerReferenceApplyConfiguration {
	return metav1ac.OwnerReference().
		WithAPIVersion("apps/v1").
		WithKind("Deployment").
		WithName("pacto-operator").
		WithUID(types.UID("test-uid-1234"))
}

func TestServiceAccountAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := serviceAccountAC(cfg)
	sa, ok := ac.(*corev1ac.ServiceAccountApplyConfiguration)
	if !ok {
		t.Fatalf("expected *ServiceAccountApplyConfiguration, got %T", ac)
	}
	if len(sa.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(sa.OwnerReferences))
	}
	if *sa.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *sa.OwnerReferences[0].Name)
	}
}

func TestDeploymentAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	if len(deploy.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(deploy.OwnerReferences))
	}
	if *deploy.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *deploy.OwnerReferences[0].Name)
	}
}

func TestDeploymentAC_WithWatchNamespace(t *testing.T) {
	cfg := Config{
		Enabled:        true,
		Image:          "img:v1",
		Namespace:      "test-ns",
		WatchNamespace: "production",
	}
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	var found bool
	for _, env := range container.Env {
		if *env.Name == "PACTO_WATCH_NAMESPACE" {
			found = true
			if *env.Value != "production" {
				t.Errorf("expected PACTO_WATCH_NAMESPACE=production, got %q", *env.Value)
			}
		}
	}
	if !found {
		t.Error("expected PACTO_WATCH_NAMESPACE env var")
	}
}

func TestDeploymentAC_WithOCISecrets(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OCISecret: "my-creds",
	}
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	container := deploy.Spec.Template.Spec.Containers[0]

	var foundMount bool
	for _, vm := range container.VolumeMounts {
		if *vm.Name == "oci-creds" {
			foundMount = true
		}
	}
	if !foundMount {
		t.Error("expected oci-creds volume mount")
	}

	var foundVolume bool
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if *v.Name == "oci-creds" {
			foundVolume = true
		}
	}
	if !foundVolume {
		t.Error("expected oci-creds volume")
	}
}

func TestDeploymentAC_WithEvidenceSourceURL(t *testing.T) {
	cfg := Config{
		Enabled:           true,
		Image:             "img:v1",
		Namespace:         "test-ns",
		EvidenceSourceURL: "http://pacto-evidence.test-ns.svc:8686",
	}
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	var found bool
	for _, env := range container.Env {
		if *env.Name == "PACTO_EVIDENCE_SOURCE_URL" {
			found = true
			if *env.Value != cfg.EvidenceSourceURL {
				t.Errorf("expected PACTO_EVIDENCE_SOURCE_URL=%q, got %q", cfg.EvidenceSourceURL, *env.Value)
			}
		}
	}
	if !found {
		t.Error("expected PACTO_EVIDENCE_SOURCE_URL env var when EvidenceSourceURL is set")
	}
}

func TestDeploymentAC_WithoutEvidenceSourceURL(t *testing.T) {
	cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns"}
	ac := deploymentAC(cfg)
	deploy := ac.(*appsv1ac.DeploymentApplyConfiguration)
	container := deploy.Spec.Template.Spec.Containers[0]
	for _, env := range container.Env {
		if *env.Name == "PACTO_EVIDENCE_SOURCE_URL" {
			t.Error("unexpected PACTO_EVIDENCE_SOURCE_URL env var when EvidenceSourceURL is empty")
		}
	}
}

// The dashboard resolves contract revisions itself, so the plain-HTTP allowance
// has to reach the managed workload — the controller honouring it is not enough.
func TestDeploymentAC_InsecureRegistries(t *testing.T) {
	for _, tc := range []struct {
		name, configured, want string
	}{
		{"configured", "reg.pacto-system.svc:5000", "reg.pacto-system.svc:5000"},
		{"unset", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Enabled: true, Image: "img:v1", Namespace: "test-ns", InsecureRegistries: tc.configured}
			deploy, ok := deploymentAC(cfg).(*appsv1ac.DeploymentApplyConfiguration)
			if !ok {
				t.Fatal("expected *DeploymentApplyConfiguration")
			}
			var got string
			for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
				if *env.Name == loader.InsecureRegistriesEnvVar {
					got = *env.Value
				}
			}
			if got != tc.want {
				t.Errorf("%s=%q, want %q", loader.InsecureRegistriesEnvVar, got, tc.want)
			}
		})
	}
}

func TestServiceAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := serviceAC(cfg)
	svc, ok := ac.(*corev1ac.ServiceApplyConfiguration)
	if !ok {
		t.Fatalf("expected *ServiceApplyConfiguration, got %T", ac)
	}
	if len(svc.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(svc.OwnerReferences))
	}
	if *svc.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *svc.OwnerReferences[0].Name)
	}
}

func TestOCISecretAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "img:v1",
		Namespace: "test-ns",
		OwnerRef:  testOwnerRef(),
	}
	ac := ociSecretAC(cfg, []byte(`{"auths":{}}`))
	secret, ok := ac.(*corev1ac.SecretApplyConfiguration)
	if !ok {
		t.Fatalf("expected *SecretApplyConfiguration, got %T", ac)
	}
	if len(secret.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(secret.OwnerReferences))
	}
	if *secret.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *secret.OwnerReferences[0].Name)
	}
}
