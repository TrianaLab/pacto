/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"strings"
	"testing"

	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
	appsv1 "k8s.io/api/apps/v1"
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

func deploymentACFor(t *testing.T, cfg Config) *appsv1ac.DeploymentApplyConfiguration {
	t.Helper()
	ac := deploymentAC(cfg)
	deploy, ok := ac.(*appsv1ac.DeploymentApplyConfiguration)
	if !ok {
		t.Fatalf("expected *DeploymentApplyConfiguration, got %T", ac)
	}
	return deploy
}

func serverCfg() Config {
	return Config{
		Enabled:     true,
		Image:       "ghcr.io/trianalab/pacto:0.1.0",
		Namespace:   "pacto-system",
		Subjects:    []string{testSubject},
		TrustSecret: "trusted-keys",
	}
}

func volumeMountPaths(container corev1ac.ContainerApplyConfiguration) map[string]string {
	mounts := map[string]string{}
	for _, m := range container.VolumeMounts {
		mounts[*m.Name] = *m.MountPath
	}
	return mounts
}

func findVolume(deploy *appsv1ac.DeploymentApplyConfiguration, name string) *corev1ac.VolumeApplyConfiguration {
	for i := range deploy.Spec.Template.Spec.Volumes {
		if *deploy.Spec.Template.Spec.Volumes[i].Name == name {
			return &deploy.Spec.Template.Spec.Volumes[i]
		}
	}
	return nil
}

func TestDeploymentAC_ArgsAndSingleWriter(t *testing.T) {
	cfg := serverCfg()
	second := "oci://ghcr.io/acme/orders@sha256:" + strings.Repeat("2", 64)
	cfg.Subjects = append(cfg.Subjects, second)
	deploy := deploymentACFor(t, cfg)

	// Exactly one writer: one replica AND Recreate, so a rolling update never runs
	// two servers that could both pass the same replay check.
	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 1 {
		t.Fatalf("expected 1 replica, got %v", deploy.Spec.Replicas)
	}
	if deploy.Spec.Strategy == nil || *deploy.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("expected the Recreate strategy, got %v", deploy.Spec.Strategy)
	}

	args := deploy.Spec.Template.Spec.Containers[0].Args
	argSet := map[string]bool{}
	for _, a := range args {
		argSet[a] = true
	}
	for _, want := range []string{"evidence", "serve", "--trust", "--listen-address", testSubject, second} {
		if !argSet[want] {
			t.Errorf("expected args to include %q, got %v", want, args)
		}
	}
	// Every subject is passed as its own --subject: the flag is repeatable, so a
	// second revision is configured, not concatenated into the first.
	subjectFlags := 0
	for _, a := range args {
		if a == "--subject" {
			subjectFlags++
		}
	}
	if subjectFlags != 2 {
		t.Errorf("expected 2 --subject flags, got %d in %v", subjectFlags, args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--bucket-url") || strings.HasPrefix(a, "--prefix") {
			t.Errorf("the bucket store is gone; unexpected arg %q", a)
		}
	}
	if len(deploy.OwnerReferences) != 0 {
		t.Errorf("expected no owner references, got %d", len(deploy.OwnerReferences))
	}
}

func TestDeploymentAC_ProbesAndSecurity(t *testing.T) {
	container := deploymentACFor(t, serverCfg()).Spec.Template.Spec.Containers[0]

	if container.ReadinessProbe == nil || *container.ReadinessProbe.HTTPGet.Path != ReadyPath {
		t.Errorf("expected readiness path %q", ReadyPath)
	}
	if container.LivenessProbe == nil || *container.LivenessProbe.HTTPGet.Path != HealthPath {
		t.Errorf("expected liveness path %q", HealthPath)
	}
	// A startupProbe on /ready gives a slow or briefly unreachable registry a
	// budget before liveness engages, so a slow start never trips the liveness loop.
	if container.StartupProbe == nil || *container.StartupProbe.HTTPGet.Path != ReadyPath || *container.StartupProbe.FailureThreshold != 60 {
		t.Errorf("expected a startup probe on %q with failureThreshold 60", ReadyPath)
	}
	if container.SecurityContext == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("expected read-only root filesystem")
	}
	if *container.SecurityContext.AllowPrivilegeEscalation {
		t.Error("expected no privilege escalation")
	}
}

// The server is stateless: the ONLY volume it mounts by default is the read-only
// trust store. A data volume or a writable temp dir would be a place for evidence
// to hide outside the registry.
func TestDeploymentAC_MountsNothingWritable(t *testing.T) {
	deploy := deploymentACFor(t, serverCfg())
	mounts := volumeMountPaths(deploy.Spec.Template.Spec.Containers[0])

	if mounts[volumeTrust] != TrustMountPath {
		t.Errorf("expected trust mount at %q, got %q", TrustMountPath, mounts[volumeTrust])
	}
	if len(mounts) != 1 {
		t.Errorf("expected only the trust mount, got %v", mounts)
	}
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if v.PersistentVolumeClaim != nil || v.EmptyDir != nil {
			t.Errorf("stateless server must not mount storage: %+v", v)
		}
	}
}

// Registry credentials are an EXISTING Docker config Secret, mounted read-only
// and pointed at by DOCKER_CONFIG, so the server reads the registry under exactly
// the credential policy `pacto pull` uses. No second auth model, no secret value
// in the generated Deployment.
func TestDeploymentAC_RegistryCredentials(t *testing.T) {
	cfg := serverCfg()
	cfg.CredentialsSecret = "registry-creds"
	deploy := deploymentACFor(t, cfg)
	container := deploy.Spec.Template.Spec.Containers[0]

	var mount *corev1ac.VolumeMountApplyConfiguration
	for i := range container.VolumeMounts {
		if *container.VolumeMounts[i].Name == volumeRegistry {
			mount = &container.VolumeMounts[i]
		}
	}
	if mount == nil {
		t.Fatal("expected a registry-credentials mount")
	}
	if mount.ReadOnly == nil || !*mount.ReadOnly {
		t.Error("registry credentials must be mounted read-only")
	}
	vol := findVolume(deploy, volumeRegistry)
	if vol == nil || vol.Secret == nil || *vol.Secret.SecretName != "registry-creds" {
		t.Fatalf("expected the existing Secret to be referenced, got %+v", vol)
	}
	// Projected as config.json so the mount point IS a DOCKER_CONFIG directory.
	if len(vol.Secret.Items) != 1 || *vol.Secret.Items[0].Path != "config.json" {
		t.Errorf("expected the Secret projected as config.json, got %+v", vol.Secret.Items)
	}
	var dockerConfig string
	for _, env := range container.Env {
		if *env.Name == DockerConfigEnvVar {
			dockerConfig = *env.Value
		}
	}
	if dockerConfig != RegistryMountPath {
		t.Errorf("%s=%q, want %q", DockerConfigEnvVar, dockerConfig, RegistryMountPath)
	}
}

// Unconfigured credentials mean no mount and no env var at all — an anonymous or
// in-cluster registry must not get an empty Docker config pointed at it.
func TestDeploymentAC_NoRegistryCredentials(t *testing.T) {
	deploy := deploymentACFor(t, serverCfg())
	if findVolume(deploy, volumeRegistry) != nil {
		t.Error("expected no registry-credentials volume when unconfigured")
	}
	for _, env := range deploy.Spec.Template.Spec.Containers[0].Env {
		if *env.Name == DockerConfigEnvVar {
			t.Errorf("expected no %s when no credentials Secret is configured", DockerConfigEnvVar)
		}
	}
}

func TestDeploymentAC_WithOwnerRef(t *testing.T) {
	cfg := serverCfg()
	cfg.OwnerRef = testOwnerRef()
	deploy := deploymentACFor(t, cfg)
	if len(deploy.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(deploy.OwnerReferences))
	}
	if *deploy.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *deploy.OwnerReferences[0].Name)
	}
}

func TestServiceAC(t *testing.T) {
	cfg := Config{Namespace: "pacto-system"}
	ac := serviceAC(cfg)
	svc, ok := ac.(*corev1ac.ServiceApplyConfiguration)
	if !ok {
		t.Fatalf("expected *ServiceApplyConfiguration, got %T", ac)
	}
	if len(svc.Spec.Ports) != 1 || *svc.Spec.Ports[0].Port != EvidencePort {
		t.Errorf("expected single port %d, got %v", EvidencePort, svc.Spec.Ports)
	}
	if len(svc.OwnerReferences) != 0 {
		t.Errorf("expected no owner references, got %d", len(svc.OwnerReferences))
	}
}

func TestServiceAC_WithOwnerRef(t *testing.T) {
	cfg := Config{Namespace: "pacto-system", OwnerRef: testOwnerRef()}
	ac := serviceAC(cfg)
	svc := ac.(*corev1ac.ServiceApplyConfiguration)
	if len(svc.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(svc.OwnerReferences))
	}
	if *svc.OwnerReferences[0].Name != "pacto-operator" {
		t.Errorf("expected owner name pacto-operator, got %q", *svc.OwnerReferences[0].Name)
	}
}

// The Evidence Server resolves the contract ref an envelope names, so the
// plain-HTTP allowance has to reach the managed workload too.
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
