/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/loader"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
)

// deploymentAC returns the server-side apply configuration for the Evidence
// Server Deployment. Exactly one replica with a Recreate strategy: the registry
// offers no compare-and-set, so exactly one writer may exist and a rolling update
// (which briefly runs two) could let a replay through the gap. Readiness gates on
// the registry being readable; liveness is independent, so a registry outage
// makes the server unready without restarting it.
func deploymentAC(cfg Config) runtime.ApplyConfiguration {
	args := make([]string, 0, 6+2*len(cfg.Subjects))
	args = append(args, "evidence", "serve", "--trust", TrustMountPath, "--listen-address", ":8686")
	for _, subject := range cfg.Subjects {
		args = append(args, "--subject", subject)
	}

	// The container is stateless: a read-only root filesystem, a read-only trust
	// store and — when configured — read-only registry credentials. Nothing here
	// is writable, because nothing durable lives in the pod.
	volumeMounts := []*corev1ac.VolumeMountApplyConfiguration{
		corev1ac.VolumeMount().WithName(volumeTrust).WithMountPath(TrustMountPath).WithReadOnly(true),
	}
	volumes := []*corev1ac.VolumeApplyConfiguration{
		corev1ac.Volume().WithName(volumeTrust).WithSecret(
			corev1ac.SecretVolumeSource().WithSecretName(cfg.TrustSecret),
		),
	}

	// Only set when configured: an empty list leaves the container env unset
	// rather than declaring an empty one.
	var env []*corev1ac.EnvVarApplyConfiguration
	if cfg.InsecureRegistries != "" {
		env = append(env, corev1ac.EnvVar().
			WithName(loader.InsecureRegistriesEnvVar).WithValue(cfg.InsecureRegistries))
	}
	if cfg.CredentialsSecret != "" {
		volumeMounts = append(volumeMounts,
			corev1ac.VolumeMount().WithName(volumeRegistry).WithMountPath(RegistryMountPath).WithReadOnly(true),
		)
		// Projected as config.json so the directory IS a Docker config dir; the
		// Secret itself keeps its standard .dockerconfigjson key.
		volumes = append(volumes,
			corev1ac.Volume().WithName(volumeRegistry).WithSecret(
				corev1ac.SecretVolumeSource().
					WithSecretName(cfg.CredentialsSecret).
					WithItems(corev1ac.KeyToPath().
						WithKey(corev1.DockerConfigJsonKey).WithPath("config.json")),
			),
		)
		env = append(env, corev1ac.EnvVar().WithName(DockerConfigEnvVar).WithValue(RegistryMountPath))
	}

	res := cfg.Resources.BuildResources()

	container := corev1ac.Container().
		WithName("evidence").
		WithImage(cfg.Image).
		WithCommand("pacto").
		WithArgs(args...).
		WithEnv(env...).
		WithPorts(
			corev1ac.ContainerPort().WithName("http").WithContainerPort(EvidencePort).WithProtocol(corev1.ProtocolTCP),
		).
		WithVolumeMounts(volumeMounts...).
		WithStartupProbe(
			// A generous startup budget (up to ~5 min) before liveness/readiness
			// engage, so a registry that is slow or briefly unreachable at rollout
			// time can never trip the liveness loop.
			corev1ac.Probe().
				WithHTTPGet(corev1ac.HTTPGetAction().WithPath(ReadyPath).WithPort(intstr.FromInt32(EvidencePort))).
				WithPeriodSeconds(5).
				WithFailureThreshold(60),
		).
		WithReadinessProbe(
			corev1ac.Probe().
				WithHTTPGet(corev1ac.HTTPGetAction().WithPath(ReadyPath).WithPort(intstr.FromInt32(EvidencePort))).
				WithInitialDelaySeconds(2).
				WithPeriodSeconds(5),
		).
		WithLivenessProbe(
			corev1ac.Probe().
				WithHTTPGet(corev1ac.HTTPGetAction().WithPath(HealthPath).WithPort(intstr.FromInt32(EvidencePort))).
				WithInitialDelaySeconds(10).
				WithPeriodSeconds(20),
		).
		WithSecurityContext(
			corev1ac.SecurityContext().
				WithReadOnlyRootFilesystem(true).
				WithAllowPrivilegeEscalation(false).
				WithCapabilities(corev1ac.Capabilities().WithDrop("ALL")),
		).
		WithResources(corev1ac.ResourceRequirements().WithRequests(res.Requests).WithLimits(res.Limits))

	deploy := appsv1ac.Deployment(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithSpec(appsv1ac.DeploymentSpec().
			WithReplicas(1).
			WithStrategy(appsv1ac.DeploymentStrategy().WithType(appsv1.RecreateDeploymentStrategyType)).
			WithSelector(metav1ac.LabelSelector().WithMatchLabels(SelectorLabels())).
			WithTemplate(corev1ac.PodTemplateSpec().
				WithLabels(Labels()).
				WithSpec(corev1ac.PodSpec().
					WithSecurityContext(
						corev1ac.PodSecurityContext().
							WithRunAsNonRoot(true).
							WithRunAsUser(65532).
							WithFSGroup(65532).
							WithSeccompProfile(corev1ac.SeccompProfile().WithType(corev1.SeccompProfileTypeRuntimeDefault)),
					).
					WithContainers(container).
					WithVolumes(volumes...).
					WithTerminationGracePeriodSeconds(30),
				),
			),
		)
	if cfg.OwnerRef != nil {
		deploy.WithOwnerReferences(cfg.OwnerRef)
	}
	return deploy
}

// serviceAC returns the apply configuration for the internal ClusterIP Service.
func serviceAC(cfg Config) runtime.ApplyConfiguration {
	svc := corev1ac.Service(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithSpec(corev1ac.ServiceSpec().
			WithSelector(SelectorLabels()).
			WithPorts(
				corev1ac.ServicePort().
					WithName("http").
					WithPort(EvidencePort).
					WithTargetPort(intstr.FromInt32(EvidencePort)).
					WithProtocol(corev1.ProtocolTCP),
			),
		)
	if cfg.OwnerRef != nil {
		svc.WithOwnerReferences(cfg.OwnerRef)
	}
	return svc
}
