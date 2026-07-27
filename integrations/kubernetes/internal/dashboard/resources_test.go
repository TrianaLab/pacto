/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"slices"
	"testing"
)

var testConfig = Config{
	Enabled:   true,
	Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
	Namespace: "pacto-system",
}

func TestBuildServiceAccount(t *testing.T) {
	sa := BuildServiceAccount(testConfig)

	if sa.Name != Name {
		t.Errorf("expected name %q, got %q", Name, sa.Name)
	}
	if sa.Namespace != testConfig.Namespace {
		t.Errorf("expected namespace %q, got %q", testConfig.Namespace, sa.Namespace)
	}
	assertLabels(t, sa.Labels)
}

func TestBuildClusterRole(t *testing.T) {
	cr := BuildClusterRole()

	if cr.Name != Name {
		t.Errorf("expected name %q, got %q", Name, cr.Name)
	}
	assertLabels(t, cr.Labels)

	if len(cr.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cr.Rules))
	}

	// Pacto CRs rule
	pactoRule := cr.Rules[0]
	if pactoRule.APIGroups[0] != "pacto.trianalab.io" {
		t.Errorf("expected API group pacto.trianalab.io, got %q", pactoRule.APIGroups[0])
	}
	assertContains(t, pactoRule.Resources, "pactos")
	assertContains(t, pactoRule.Resources, "pactorevisions")
	assertContains(t, pactoRule.Verbs, "get")
	assertContains(t, pactoRule.Verbs, "list")
	assertContains(t, pactoRule.Verbs, "watch")
	// Ensure no write verbs
	assertNotContains(t, pactoRule.Verbs, "create")
	assertNotContains(t, pactoRule.Verbs, "update")
	assertNotContains(t, pactoRule.Verbs, "delete")

	// Services rule
	svcRule := cr.Rules[1]
	assertContains(t, svcRule.Resources, "services")
	assertContains(t, svcRule.Verbs, "get")
	assertNotContains(t, svcRule.Verbs, "create")
}

func TestBuildClusterRoleBinding(t *testing.T) {
	crb := BuildClusterRoleBinding(testConfig)

	if crb.Name != Name {
		t.Errorf("expected name %q, got %q", Name, crb.Name)
	}
	assertLabels(t, crb.Labels)

	if len(crb.Subjects) != 1 {
		t.Fatalf("expected 1 subject, got %d", len(crb.Subjects))
	}
	if crb.Subjects[0].Name != Name {
		t.Errorf("expected subject name %q, got %q", Name, crb.Subjects[0].Name)
	}
	if crb.Subjects[0].Namespace != testConfig.Namespace {
		t.Errorf("expected subject namespace %q, got %q", testConfig.Namespace, crb.Subjects[0].Namespace)
	}
	if crb.RoleRef.Name != Name {
		t.Errorf("expected role ref name %q, got %q", Name, crb.RoleRef.Name)
	}
}

func TestBuildDeployment(t *testing.T) {
	deploy := BuildDeployment(testConfig)

	if deploy.Name != Name {
		t.Errorf("expected name %q, got %q", Name, deploy.Name)
	}
	if deploy.Namespace != testConfig.Namespace {
		t.Errorf("expected namespace %q, got %q", testConfig.Namespace, deploy.Namespace)
	}
	assertLabels(t, deploy.Labels)

	// Check replicas
	if *deploy.Spec.Replicas != 1 {
		t.Errorf("expected 1 replica, got %d", *deploy.Spec.Replicas)
	}

	// Check container
	containers := deploy.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	container := containers[0]

	if container.Image != testConfig.Image {
		t.Errorf("expected image %q, got %q", testConfig.Image, container.Image)
	}

	// Check port
	if len(container.Ports) != 1 || container.Ports[0].ContainerPort != DashboardPort {
		t.Errorf("expected port %d, got %v", DashboardPort, container.Ports)
	}

	// Check liveness probe
	if container.LivenessProbe == nil {
		t.Fatal("expected liveness probe")
	}
	if container.LivenessProbe.HTTPGet.Path != HealthPath {
		t.Errorf("expected liveness path %q, got %q", HealthPath, container.LivenessProbe.HTTPGet.Path)
	}

	// Check readiness probe
	if container.ReadinessProbe == nil {
		t.Fatal("expected readiness probe")
	}
	if container.ReadinessProbe.HTTPGet.Path != HealthPath {
		t.Errorf("expected readiness path %q, got %q", HealthPath, container.ReadinessProbe.HTTPGet.Path)
	}

	// Check security context
	if container.SecurityContext == nil {
		t.Fatal("expected security context")
	}
	if !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("expected read-only root filesystem")
	}
	if *container.SecurityContext.AllowPrivilegeEscalation {
		t.Error("expected no privilege escalation")
	}

	// Check pod security context
	podSec := deploy.Spec.Template.Spec.SecurityContext
	if podSec == nil {
		t.Fatal("expected pod security context")
	}
	if !*podSec.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true")
	}

	// Check service account
	if deploy.Spec.Template.Spec.ServiceAccountName != Name {
		t.Errorf("expected service account %q, got %q", Name, deploy.Spec.Template.Spec.ServiceAccountName)
	}

	// Check cache volume mount
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("expected 1 volume mount, got %d", len(container.VolumeMounts))
	}
	if container.VolumeMounts[0].Name != "cache" {
		t.Errorf("expected volume mount name %q, got %q", "cache", container.VolumeMounts[0].Name)
	}
	if container.VolumeMounts[0].MountPath != "/home/pacto/.cache/pacto" {
		t.Errorf("expected mount path %q, got %q", "/home/pacto/.cache/pacto", container.VolumeMounts[0].MountPath)
	}

	// Check cache volume
	volumes := deploy.Spec.Template.Spec.Volumes
	if len(volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(volumes))
	}
	if volumes[0].Name != "cache" {
		t.Errorf("expected volume name %q, got %q", "cache", volumes[0].Name)
	}
	if volumes[0].EmptyDir == nil {
		t.Error("expected emptyDir volume source")
	}

	// Check env vars
	envMap := make(map[string]string)
	for _, e := range container.Env {
		envMap[e.Name] = e.Value
	}
	if envMap["PACTO_DASHBOARD_PORT"] != "3000" {
		t.Error("expected PACTO_DASHBOARD_PORT=3000")
	}
	if envMap["PACTO_NO_UPDATE_CHECK"] != "1" {
		t.Error("expected PACTO_NO_UPDATE_CHECK=1")
	}
}

func TestBuildDeploymentWithWatchNamespace(t *testing.T) {
	cfg := Config{
		Enabled:        true,
		Image:          "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace:      "pacto-system",
		WatchNamespace: "production",
	}
	deploy := BuildDeployment(cfg)
	container := deploy.Spec.Template.Spec.Containers[0]

	envMap := make(map[string]string)
	for _, e := range container.Env {
		if e.Value != "" {
			envMap[e.Name] = e.Value
		}
	}
	if envMap["PACTO_WATCH_NAMESPACE"] != "production" {
		t.Errorf("expected PACTO_WATCH_NAMESPACE=production, got %q", envMap["PACTO_WATCH_NAMESPACE"])
	}
}

func TestBuildDeploymentWithoutWatchNamespace(t *testing.T) {
	deploy := BuildDeployment(testConfig) // No WatchNamespace
	container := deploy.Spec.Template.Spec.Containers[0]

	for _, e := range container.Env {
		if e.Name == "PACTO_WATCH_NAMESPACE" {
			t.Error("unexpected PACTO_WATCH_NAMESPACE when no watch namespace configured")
		}
	}
}

func TestBuildDeploymentWithOCISecret(t *testing.T) {
	cfg := Config{
		Enabled:   true,
		Image:     "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace: "pacto-system",
		OCISecret: "registry-creds",
	}
	deploy := BuildDeployment(cfg)
	container := deploy.Spec.Template.Spec.Containers[0]
	volumes := deploy.Spec.Template.Spec.Volumes

	// Should have oci-creds volume mount
	var foundMount bool
	for _, vm := range container.VolumeMounts {
		if vm.Name == "oci-creds" {
			foundMount = true
			if vm.MountPath != "/home/pacto/.docker" {
				t.Errorf("expected mount path /home/pacto/.docker, got %q", vm.MountPath)
			}
			if !vm.ReadOnly {
				t.Error("expected read-only mount")
			}
		}
	}
	if !foundMount {
		t.Error("expected oci-creds volume mount")
	}

	// Should have oci-creds volume
	var foundVolume bool
	for _, v := range volumes {
		if v.Name == "oci-creds" {
			foundVolume = true
			if v.Secret == nil {
				t.Fatal("expected secret volume source")
			}
			if v.Secret.SecretName != ManagedSecretName {
				t.Errorf("expected secret name %q, got %q", ManagedSecretName, v.Secret.SecretName)
			}
			if !*v.Secret.Optional {
				t.Error("expected optional=true")
			}
			if len(v.Secret.Items) != 1 || v.Secret.Items[0].Key != ".dockerconfigjson" {
				t.Errorf("expected item key .dockerconfigjson, got %v", v.Secret.Items)
			}
			if v.Secret.Items[0].Path != "config.json" {
				t.Errorf("expected item path config.json, got %q", v.Secret.Items[0].Path)
			}
		}
	}
	if !foundVolume {
		t.Error("expected oci-creds volume")
	}
}

func TestBuildDeploymentWithOCISecrets(t *testing.T) {
	cfg := Config{
		Enabled:    true,
		Image:      "ghcr.io/trianalab/pacto-dashboard:0.24.2",
		Namespace:  "pacto-system",
		OCISecrets: []string{"ghcr-creds", "ecr-creds"},
	}
	deploy := BuildDeployment(cfg)
	container := deploy.Spec.Template.Spec.Containers[0]

	// Should have oci-creds volume mount (same as single secret)
	var foundMount bool
	for _, vm := range container.VolumeMounts {
		if vm.Name == "oci-creds" {
			foundMount = true
		}
	}
	if !foundMount {
		t.Error("expected oci-creds volume mount when OCISecrets is set")
	}
}

func TestBuildDeploymentWithoutOCISecret(t *testing.T) {
	deploy := BuildDeployment(testConfig) // No OCISecret or OCISecrets
	container := deploy.Spec.Template.Spec.Containers[0]

	for _, vm := range container.VolumeMounts {
		if vm.Name == "oci-creds" {
			t.Error("unexpected oci-creds volume mount when no OCI secret configured")
		}
	}
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.Name == "oci-creds" {
			t.Error("unexpected oci-creds volume when no OCI secret configured")
		}
	}
}

func TestBuildService(t *testing.T) {
	svc := BuildService(testConfig)

	if svc.Name != Name {
		t.Errorf("expected name %q, got %q", Name, svc.Name)
	}
	if svc.Namespace != testConfig.Namespace {
		t.Errorf("expected namespace %q, got %q", testConfig.Namespace, svc.Namespace)
	}
	assertLabels(t, svc.Labels)

	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(svc.Spec.Ports))
	}
	if svc.Spec.Ports[0].Port != DashboardPort {
		t.Errorf("expected port %d, got %d", DashboardPort, svc.Spec.Ports[0].Port)
	}

	// Selector should match pod selector labels
	selectorLabels := SelectorLabels()
	for k, v := range selectorLabels {
		if svc.Spec.Selector[k] != v {
			t.Errorf("expected selector %q=%q, got %q", k, v, svc.Spec.Selector[k])
		}
	}
}

func TestLabelsConsistency(t *testing.T) {
	labels := Labels()
	selectorLabels := SelectorLabels()

	// All selector labels must be present in the full labels
	for k, v := range selectorLabels {
		if labels[k] != v {
			t.Errorf("selector label %q=%q not found in full labels", k, v)
		}
	}
}

// --- helpers ---

func assertLabels(t *testing.T, labels map[string]string) {
	t.Helper()
	if labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("expected label %q=%q, got %q", LabelManagedBy, ManagedByValue, labels[LabelManagedBy])
	}
	if labels[LabelComponent] != ComponentValue {
		t.Errorf("expected label %q=%q, got %q", LabelComponent, ComponentValue, labels[LabelComponent])
	}
	if labels[LabelName] != Name {
		t.Errorf("expected label %q=%q, got %q", LabelName, Name, labels[LabelName])
	}
}

func assertContains(t *testing.T, slice []string, val string) {
	t.Helper()
	if !slices.Contains(slice, val) {
		t.Errorf("expected slice to contain %q, got %v", val, slice)
	}
}

func assertNotContains(t *testing.T, slice []string, val string) {
	t.Helper()
	if slices.Contains(slice, val) {
		t.Errorf("expected slice to not contain %q", val)
	}
}
