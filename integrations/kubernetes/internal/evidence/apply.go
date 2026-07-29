/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1ac "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
)

// fileBucketPath returns the on-disk path a file:// bucket URL points at (the
// mount point for the PVC), or "" for a non-file bucket.
func fileBucketPath(bucketURL string) string {
	if !isFileBucket(bucketURL) {
		return ""
	}
	return strings.TrimPrefix(bucketURL, "file://")
}

// deploymentAC returns the server-side apply configuration for the Evidence
// Server Deployment. Exactly one replica (single-writer); readiness gates on
// completed storage recovery; liveness is independent.
func deploymentAC(cfg Config) runtime.ApplyConfiguration {
	args := []string{
		"evidence", "serve",
		"--bucket-url", cfg.BucketURL,
		"--prefix", cfg.Prefix,
		"--trust", TrustMountPath,
		"--listen-address", ":8686",
	}

	volumeMounts := []*corev1ac.VolumeMountApplyConfiguration{
		corev1ac.VolumeMount().WithName(volumeTrust).WithMountPath(TrustMountPath).WithReadOnly(true),
	}
	volumes := []*corev1ac.VolumeApplyConfiguration{
		corev1ac.Volume().WithName(volumeTrust).WithSecret(
			corev1ac.SecretVolumeSource().WithSecretName(cfg.TrustSecret),
		),
	}
	if path := fileBucketPath(cfg.BucketURL); path != "" {
		volumeMounts = append(volumeMounts,
			corev1ac.VolumeMount().WithName(volumeData).WithMountPath(path),
		)
		volumes = append(volumes,
			corev1ac.Volume().WithName(volumeData).WithPersistentVolumeClaim(
				corev1ac.PersistentVolumeClaimVolumeSource().WithClaimName(cfg.Persistence.ClaimName()),
			),
		)
	}

	res := cfg.Resources.BuildResources()

	container := corev1ac.Container().
		WithName("evidence").
		WithImage(cfg.Image).
		WithCommand("pacto").
		WithArgs(args...).
		WithPorts(
			corev1ac.ContainerPort().WithName("http").WithContainerPort(EvidencePort).WithProtocol(corev1.ProtocolTCP),
		).
		WithVolumeMounts(volumeMounts...).
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

// pvcAC returns the apply configuration for the evidence PVC. It deliberately
// carries NO owner reference: the persistent evidence must never be
// garbage-collected with the operator.
func pvcAC(cfg Config) runtime.ApplyConfiguration {
	modes := cfg.Persistence.AccessModes
	if len(modes) == 0 {
		modes = []string{string(corev1.ReadWriteOnce)}
	}
	accessModes := make([]corev1.PersistentVolumeAccessMode, 0, len(modes))
	for _, m := range modes {
		accessModes = append(accessModes, corev1.PersistentVolumeAccessMode(m))
	}
	size := cfg.Persistence.Size
	if size == "" {
		size = "1Gi"
	}
	spec := corev1ac.PersistentVolumeClaimSpec().
		WithAccessModes(accessModes...).
		WithResources(corev1ac.VolumeResourceRequirements().
			WithRequests(corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)}),
		)
	if cfg.Persistence.StorageClass != "" {
		spec.WithStorageClassName(cfg.Persistence.StorageClass)
	}
	// No owner reference: the PVC outlives the operator so evidence is retained.
	return corev1ac.PersistentVolumeClaim(PVCName, cfg.Namespace).
		WithLabels(Labels()).
		WithSpec(spec)
}
