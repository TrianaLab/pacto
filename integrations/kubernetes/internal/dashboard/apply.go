/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	rbacv1ac "k8s.io/client-go/applyconfigurations/rbac/v1"
)

// serviceAccountAC returns a server-side apply configuration for the dashboard ServiceAccount.
func serviceAccountAC(cfg Config) runtime.ApplyConfiguration {
	sa := corev1ac.ServiceAccount(Name, cfg.Namespace).
		WithLabels(Labels())
	if cfg.OwnerRef != nil {
		sa.WithOwnerReferences(cfg.OwnerRef)
	}
	return sa
}

// clusterRoleAC returns a server-side apply configuration for the dashboard ClusterRole.
func clusterRoleAC() runtime.ApplyConfiguration {
	return rbacv1ac.ClusterRole(Name).
		WithLabels(Labels()).
		WithRules(
			rbacv1ac.PolicyRule().
				WithAPIGroups("pacto.trianalab.io").
				WithResources("pactos", "pactorevisions").
				WithVerbs("get", "list", "watch"),
			rbacv1ac.PolicyRule().
				WithAPIGroups("").
				WithResources("services").
				WithVerbs("get", "list", "watch"),
		)
}

// clusterRoleBindingAC returns a server-side apply configuration for the dashboard ClusterRoleBinding.
func clusterRoleBindingAC(cfg Config) runtime.ApplyConfiguration {
	return rbacv1ac.ClusterRoleBinding(Name).
		WithLabels(Labels()).
		WithSubjects(
			rbacv1ac.Subject().
				WithKind("ServiceAccount").
				WithName(Name).
				WithNamespace(cfg.Namespace),
		).
		WithRoleRef(
			rbacv1ac.RoleRef().
				WithAPIGroup("rbac.authorization.k8s.io").
				WithKind("ClusterRole").
				WithName(Name),
		)
}

// deploymentAC returns a server-side apply configuration for the dashboard Deployment.
func deploymentAC(cfg Config) runtime.ApplyConfiguration {
	env := []*corev1ac.EnvVarApplyConfiguration{
		corev1ac.EnvVar().WithName("PACTO_DASHBOARD_PORT").WithValue("3000"),
		corev1ac.EnvVar().WithName("PACTO_NO_UPDATE_CHECK").WithValue("1"),
	}
	if cfg.WatchNamespace != "" {
		env = append(env, corev1ac.EnvVar().WithName("PACTO_WATCH_NAMESPACE").WithValue(cfg.WatchNamespace))
	}

	volumeMounts := []*corev1ac.VolumeMountApplyConfiguration{
		corev1ac.VolumeMount().WithName("cache").WithMountPath("/home/pacto/.cache/pacto"),
	}
	volumes := []*corev1ac.VolumeApplyConfiguration{
		corev1ac.Volume().WithName("cache").WithEmptyDir(corev1ac.EmptyDirVolumeSource()),
	}

	if len(cfg.EffectiveOCISecrets()) > 0 {
		volumeMounts = append(volumeMounts,
			corev1ac.VolumeMount().WithName("oci-creds").WithMountPath("/home/pacto/.docker").WithReadOnly(true),
		)
		volumes = append(volumes,
			corev1ac.Volume().WithName("oci-creds").WithSecret(
				corev1ac.SecretVolumeSource().
					WithSecretName(ManagedSecretName).
					WithItems(corev1ac.KeyToPath().WithKey(string(corev1.DockerConfigJsonKey)).WithPath("config.json")).
					WithOptional(true),
			),
		)
	}

	res := cfg.Resources.BuildResources()

	container := corev1ac.Container().
		WithName("dashboard").
		WithImage(cfg.Image).
		WithPorts(
			corev1ac.ContainerPort().WithName("http").WithContainerPort(DashboardPort).WithProtocol(corev1.ProtocolTCP),
		).
		WithEnv(env...).
		WithVolumeMounts(volumeMounts...).
		WithLivenessProbe(
			corev1ac.Probe().
				WithHTTPGet(corev1ac.HTTPGetAction().WithPath(HealthPath).WithPort(intstr.FromInt32(DashboardPort))).
				WithInitialDelaySeconds(10).
				WithPeriodSeconds(30),
		).
		WithReadinessProbe(
			corev1ac.Probe().
				WithHTTPGet(corev1ac.HTTPGetAction().WithPath(HealthPath).WithPort(intstr.FromInt32(DashboardPort))).
				WithInitialDelaySeconds(5).
				WithPeriodSeconds(10),
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
			WithSelector(metav1ac.LabelSelector().WithMatchLabels(SelectorLabels())).
			WithTemplate(corev1ac.PodTemplateSpec().
				WithLabels(Labels()).
				WithSpec(corev1ac.PodSpec().
					WithServiceAccountName(Name).
					WithSecurityContext(
						corev1ac.PodSecurityContext().
							WithRunAsNonRoot(true).
							WithRunAsUser(65532).
							WithSeccompProfile(corev1ac.SeccompProfile().WithType(corev1.SeccompProfileTypeRuntimeDefault)),
					).
					WithContainers(container).
					WithVolumes(volumes...).
					WithTerminationGracePeriodSeconds(10),
				),
			),
		)
	if cfg.OwnerRef != nil {
		deploy.WithOwnerReferences(cfg.OwnerRef)
	}
	return deploy
}

// serviceAC returns a server-side apply configuration for the dashboard Service.
func serviceAC(cfg Config) runtime.ApplyConfiguration {
	svc := corev1ac.Service(Name, cfg.Namespace).
		WithLabels(Labels()).
		WithSpec(corev1ac.ServiceSpec().
			WithSelector(SelectorLabels()).
			WithPorts(
				corev1ac.ServicePort().
					WithName("http").
					WithPort(DashboardPort).
					WithTargetPort(intstr.FromInt32(DashboardPort)).
					WithProtocol(corev1.ProtocolTCP),
			),
		)
	if cfg.OwnerRef != nil {
		svc.WithOwnerReferences(cfg.OwnerRef)
	}
	return svc
}

// ociSecretAC returns a server-side apply configuration for the managed OCI credentials Secret.
func ociSecretAC(cfg Config, data []byte) runtime.ApplyConfiguration {
	secret := corev1ac.Secret(ManagedSecretName, cfg.Namespace).
		WithLabels(Labels()).
		WithType(corev1.SecretTypeDockerConfigJson).
		WithData(map[string][]byte{
			string(corev1.DockerConfigJsonKey): data,
		})
	if cfg.OwnerRef != nil {
		secret.WithOwnerReferences(cfg.OwnerRef)
	}
	return secret
}
