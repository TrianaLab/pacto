/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"testing"

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

func TestFileBucketPath(t *testing.T) {
	if got := fileBucketPath("file:///data/evidence"); got != "/data/evidence" {
		t.Errorf("expected /data/evidence, got %q", got)
	}
	if got := fileBucketPath("s3://bucket"); got != "" {
		t.Errorf("expected empty path for cloud bucket, got %q", got)
	}
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

func fileBucketCfg() Config {
	return Config{
		Enabled:     true,
		Image:       "ghcr.io/trianalab/pacto:0.1.0",
		Namespace:   "pacto-system",
		BucketURL:   "file:///var/evidence",
		Prefix:      "team-a",
		TrustSecret: "trusted-keys",
		Persistence: PersistenceConfig{Enabled: true},
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

func TestDeploymentAC_FileBucket_ArgsAndReplica(t *testing.T) {
	deploy := deploymentACFor(t, fileBucketCfg())

	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 1 {
		t.Fatalf("expected 1 replica, got %v", deploy.Spec.Replicas)
	}
	argSet := map[string]bool{}
	for _, a := range deploy.Spec.Template.Spec.Containers[0].Args {
		argSet[a] = true
	}
	for _, want := range []string{"evidence", "serve", "--bucket-url", "--prefix", "--trust", "--listen-address"} {
		if !argSet[want] {
			t.Errorf("expected args to include %q", want)
		}
	}
	if len(deploy.OwnerReferences) != 0 {
		t.Errorf("expected no owner references, got %d", len(deploy.OwnerReferences))
	}
}

func TestDeploymentAC_FileBucket_ProbesAndSecurity(t *testing.T) {
	container := deploymentACFor(t, fileBucketCfg()).Spec.Template.Spec.Containers[0]

	if container.ReadinessProbe == nil || *container.ReadinessProbe.HTTPGet.Path != ReadyPath {
		t.Errorf("expected readiness path %q", ReadyPath)
	}
	if container.LivenessProbe == nil || *container.LivenessProbe.HTTPGet.Path != HealthPath {
		t.Errorf("expected liveness path %q", HealthPath)
	}
	if container.SecurityContext == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Error("expected read-only root filesystem")
	}
	if *container.SecurityContext.AllowPrivilegeEscalation {
		t.Error("expected no privilege escalation")
	}
}

func TestDeploymentAC_FileBucket_Volumes(t *testing.T) {
	deploy := deploymentACFor(t, fileBucketCfg())
	mounts := volumeMountPaths(deploy.Spec.Template.Spec.Containers[0])

	if mounts[volumeTrust] != TrustMountPath {
		t.Errorf("expected trust mount at %q, got %q", TrustMountPath, mounts[volumeTrust])
	}
	if _, ok := mounts[volumeData]; !ok {
		t.Error("expected data volume mount for file bucket")
	}

	dataVol := findVolume(deploy, volumeData)
	if dataVol == nil {
		t.Fatal("expected data volume for file bucket")
	}
	if dataVol.PersistentVolumeClaim == nil || *dataVol.PersistentVolumeClaim.ClaimName != PVCName {
		t.Errorf("expected data PVC claim %q, got %v", PVCName, dataVol.PersistentVolumeClaim)
	}
}

func TestDeploymentAC_CloudBucket_NoDataVolume(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		Image:       "ghcr.io/trianalab/pacto:0.1.0",
		Namespace:   "pacto-system",
		BucketURL:   "s3://bucket",
		TrustSecret: "trusted-keys",
	}
	deploy := deploymentACFor(t, cfg)
	for _, v := range deploy.Spec.Template.Spec.Volumes {
		if *v.Name == volumeData {
			t.Error("cloud bucket should not have a data volume")
		}
	}
	container := deploy.Spec.Template.Spec.Containers[0]
	for _, m := range container.VolumeMounts {
		if *m.Name == volumeData {
			t.Error("cloud bucket should not have a data volume mount")
		}
	}
}

func TestDeploymentAC_WithOwnerRef(t *testing.T) {
	cfg := Config{
		Enabled:     true,
		Image:       "ghcr.io/trianalab/pacto:0.1.0",
		Namespace:   "pacto-system",
		BucketURL:   "s3://bucket",
		TrustSecret: "trusted-keys",
		OwnerRef:    testOwnerRef(),
	}
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

func pvcACFor(t *testing.T, cfg Config) *corev1ac.PersistentVolumeClaimApplyConfiguration {
	t.Helper()
	ac := pvcAC(cfg)
	pvc, ok := ac.(*corev1ac.PersistentVolumeClaimApplyConfiguration)
	if !ok {
		t.Fatalf("expected *PersistentVolumeClaimApplyConfiguration, got %T", ac)
	}
	return pvc
}

func TestPvcAC_Defaults(t *testing.T) {
	// Even with an OwnerRef set on the Config, the PVC must NOT carry it: the
	// persistent evidence outlives the operator.
	cfg := Config{Namespace: "pacto-system", OwnerRef: testOwnerRef()}
	pvc := pvcACFor(t, cfg)

	if len(pvc.OwnerReferences) != 0 {
		t.Errorf("PVC must have NO owner references (retention), got %d", len(pvc.OwnerReferences))
	}
	if pvc.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("expected managed-by label on PVC")
	}
	if len(pvc.Spec.AccessModes) != 1 || string(pvc.Spec.AccessModes[0]) != "ReadWriteOnce" {
		t.Errorf("expected default ReadWriteOnce, got %v", pvc.Spec.AccessModes)
	}
	got := pvc.Spec.Resources.Requests.Storage().String()
	if got != "1Gi" {
		t.Errorf("expected default size 1Gi, got %q", got)
	}
	if pvc.Spec.StorageClassName != nil {
		t.Errorf("expected no storage class, got %q", *pvc.Spec.StorageClassName)
	}
}

func TestPvcAC_CustomAccessModesSizeAndClass(t *testing.T) {
	cfg := Config{
		Namespace: "pacto-system",
		Persistence: PersistenceConfig{
			AccessModes:  []string{"ReadWriteMany", "ReadOnlyMany"},
			Size:         "10Gi",
			StorageClass: "fast",
		},
	}
	pvc := pvcACFor(t, cfg)

	if len(pvc.Spec.AccessModes) != 2 {
		t.Fatalf("expected 2 access modes, got %v", pvc.Spec.AccessModes)
	}
	if string(pvc.Spec.AccessModes[0]) != "ReadWriteMany" {
		t.Errorf("expected ReadWriteMany, got %q", pvc.Spec.AccessModes[0])
	}
	if got := pvc.Spec.Resources.Requests.Storage().String(); got != "10Gi" {
		t.Errorf("expected size 10Gi, got %q", got)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast" {
		t.Errorf("expected storage class fast, got %v", pvc.Spec.StorageClassName)
	}
}
