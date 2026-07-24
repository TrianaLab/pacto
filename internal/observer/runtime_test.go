package observer

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v2/pkg/evidence"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func int64Ptr(v int64) *int64 { return &v }
func int32Ptr(v int32) *int32 { return &v }

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	return s
}

// --- CollectForTarget tests (v2 EvidenceSet flow) ---

func TestCollectForTarget_DeploymentWithService(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: "ghcr.io/org/app:v1"},
					},
					Volumes: []corev1.Volume{
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8080}, {Port: 9090}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep, svc).Build()
	obs := New(c)

	evidenceSet, err := obs.CollectForTarget(context.Background(), "default", "my-app", "my-app", "Deployment", "ghcr.io/org/app-pacto:1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if evidenceSet.Subject.Kind != "service" {
		t.Errorf("expected subject kind=service, got %s", evidenceSet.Subject.Kind)
	}
	if evidenceSet.Subject.Name != "default/my-app" {
		t.Errorf("expected subject name=default/my-app, got %s", evidenceSet.Subject.Name)
	}
	if evidenceSet.ContractRef != "ghcr.io/org/app-pacto:1.0.0" {
		t.Errorf("expected contractRef, got %s", evidenceSet.ContractRef)
	}
	if evidenceSet.Source != "k8s" {
		t.Errorf("expected source=k8s, got %s", evidenceSet.Source)
	}

	// Should have 2 interface observations (port-8080, port-9090) + 1 workload + 1 persistence
	if len(evidenceSet.Observations) != 4 {
		t.Errorf("expected 4 observations, got %d", len(evidenceSet.Observations))
	}

	// Check interface observations
	interfaceObs := 0
	for _, obs := range evidenceSet.Observations {
		if obs.Kind == evidence.InterfaceObserved {
			interfaceObs++
			iface, ok := obs.Value.(evidence.InterfaceObservation)
			if !ok || !iface.Present {
				t.Errorf("expected interface.present=true, got %+v", obs.Value)
			}
		}
		if obs.Kind == evidence.WorkloadObserved {
			wl, ok := obs.Value.(evidence.WorkloadObservation)
			if !ok || wl.Type != "service" {
				t.Errorf("expected workload.type=service, got %+v", obs.Value)
			}
		}
		if obs.Kind == evidence.PersistenceObserved {
			pers, ok := obs.Value.(evidence.PersistenceObservation)
			if !ok || pers.Durable {
				t.Errorf("expected persistence.durable=false (no PVC), got %+v", obs.Value)
			}
		}
	}
	if interfaceObs != 2 {
		t.Errorf("expected 2 interface observations, got %d", interfaceObs)
	}
}

func TestCollectForTarget_StatefulSetWithPVC(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "postgres:15"}}},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}},
					},
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(sts).Build()
	obs := New(c)

	evidenceSet, err := obs.CollectForTarget(context.Background(), "default", "", "db", "StatefulSet", "ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No service → subject name should be namespace/workloadName
	if evidenceSet.Subject.Name != "default/db" {
		t.Errorf("expected subject name=default/db, got %s", evidenceSet.Subject.Name)
	}

	// Should have 1 workload + 1 persistence
	if len(evidenceSet.Observations) != 2 {
		t.Errorf("expected 2 observations, got %d", len(evidenceSet.Observations))
	}

	found := false
	for _, obs := range evidenceSet.Observations {
		if obs.Kind == evidence.PersistenceObserved {
			found = true
			pers, ok := obs.Value.(evidence.PersistenceObservation)
			if !ok || !pers.Durable {
				t.Errorf("expected persistence.durable=true (PVC present), got %+v", obs.Value)
			}
		}
	}
	if !found {
		t.Error("expected persistence observation")
	}
}

func TestCollectForTarget_Job(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "migration", Namespace: "default"},
		Spec:       batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "migrate", Image: "migrate:v1"}}, RestartPolicy: corev1.RestartPolicyNever}}},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(job).Build()
	obs := New(c)

	evidenceSet, err := obs.CollectForTarget(context.Background(), "default", "", "migration", "Job", "ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Job → workload type should map to "job"
	found := false
	for _, obs := range evidenceSet.Observations {
		if obs.Kind == evidence.WorkloadObserved {
			found = true
			wl, ok := obs.Value.(evidence.WorkloadObservation)
			if !ok || wl.Type != "job" {
				t.Errorf("expected workload.type=job, got %+v", obs.Value)
			}
		}
	}
	if !found {
		t.Error("expected workload observation")
	}
}

func TestCollectForTarget_CronJob(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "cleanup", Namespace: "default"},
		Spec: batchv1.CronJobSpec{
			Schedule:    "0 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "clean", Image: "clean:v1"}}, RestartPolicy: corev1.RestartPolicyNever}}}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(cj).Build()
	obs := New(c)

	evidenceSet, err := obs.CollectForTarget(context.Background(), "default", "", "cleanup", "CronJob", "ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CronJob → workload type should map to "scheduled"
	found := false
	for _, obs := range evidenceSet.Observations {
		if obs.Kind == evidence.WorkloadObserved {
			found = true
			wl, ok := obs.Value.(evidence.WorkloadObservation)
			if !ok || wl.Type != "scheduled" {
				t.Errorf("expected workload.type=scheduled, got %+v", obs.Value)
			}
		}
	}
	if !found {
		t.Error("expected workload observation")
	}
}

func TestCollectForTarget_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	obs := New(c)

	evidenceSet, err := obs.CollectForTarget(context.Background(), "default", "nonexistent", "nonexistent", "Deployment", "ref")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No service or workload exists → no observations
	if len(evidenceSet.Observations) != 0 {
		t.Errorf("expected 0 observations when nothing exists, got %d", len(evidenceSet.Observations))
	}
}

// --- Observe tests (legacy RuntimeSnapshot flow, kept for backward compat) ---

func TestObserve_DeploymentWithDetails(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: int64Ptr(30),
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "ghcr.io/org/app:v1.0.0",
							ReadinessProbe: &corev1.Probe{
								InitialDelaySeconds: 10,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name:         "data",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8080}},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep, svc).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "my-app", "my-app", "Deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.ServiceExists {
		t.Error("expected ServiceExists=true")
	}
	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if snap.DeploymentStrategy != "RollingUpdate" {
		t.Errorf("expected RollingUpdate, got %s", snap.DeploymentStrategy)
	}
	if snap.TerminationGracePeriod == nil || *snap.TerminationGracePeriod != 30 {
		t.Errorf("expected grace period 30, got %v", snap.TerminationGracePeriod)
	}
	if len(snap.ContainerImages) != 1 || snap.ContainerImages[0] != "ghcr.io/org/app:v1.0.0" {
		t.Errorf("expected image ghcr.io/org/app:v1.0.0, got %v", snap.ContainerImages)
	}
	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 10 {
		t.Errorf("expected probe delay 10, got %v", snap.HealthProbeInitialDelay)
	}
	if !snap.HasEmptyDir {
		t.Error("expected HasEmptyDir=true")
	}
	if snap.HasPVC {
		t.Error("expected HasPVC=false")
	}
}

func TestObserve_StatefulSetWithPVC(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			PodManagementPolicy: appsv1.OrderedReadyPodManagement,
			Selector:            &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "db", Image: "postgres:15"},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("10Gi"),
							},
						},
					},
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(sts).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "", "db", "StatefulSet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if snap.PodManagementPolicy != "OrderedReady" {
		t.Errorf("expected OrderedReady, got %s", snap.PodManagementPolicy)
	}
	if !snap.HasPVC {
		t.Error("expected HasPVC=true from volumeClaimTemplates")
	}
	if len(snap.ContainerImages) != 1 || snap.ContainerImages[0] != "postgres:15" {
		t.Errorf("expected postgres:15, got %v", snap.ContainerImages)
	}
}

func TestObserve_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	obs := New(c)

	snap, err := obs.Observe(context.Background(), "default", "nonexistent", "nonexistent", "Deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap.ServiceExists {
		t.Error("expected ServiceExists=false")
	}
	if snap.WorkloadExists {
		t.Error("expected WorkloadExists=false")
	}
}

func TestObserve_ServiceGetError(t *testing.T) {
	brokenScheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "my-svc", "", "Deployment")
	if err == nil {
		t.Fatal("expected error when scheme cannot handle Service")
	}
}

func TestObserve_DeploymentGetError(t *testing.T) {
	brokenScheme := runtime.NewScheme()
	_ = corev1.AddToScheme(brokenScheme)
	_ = batchv1.AddToScheme(brokenScheme)
	fc := fake.NewClientBuilder().WithScheme(brokenScheme).Build()
	obs := New(fc)

	_, err := obs.Observe(context.Background(), "default", "", "app", "Deployment")
	if err == nil {
		t.Fatal("expected error for Deployment with broken scheme")
	}
}

func TestMapWorkloadKindToType(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{"Job", "job"},
		{"CronJob", "scheduled"},
		{"Deployment", "service"},
		{"StatefulSet", "service"},
		{"ReplicaSet", "service"},
	}
	for _, tt := range tests {
		got := mapWorkloadKindToType(tt.kind)
		if got != tt.want {
			t.Errorf("mapWorkloadKindToType(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
