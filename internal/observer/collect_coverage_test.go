package observer

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v2/pkg/evidence"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestCollect_Workload_CronJob verifies CronJob workload type maps to "scheduled".
func TestCollect_Workload_CronJob(t *testing.T) {
	c := makeContract("my-service", "scheduled")
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cron", Namespace: "default"},
		Spec: batchv1.CronJobSpec{
			Schedule: "*/5 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers:    []corev1.Container{{Name: "app", Image: "img"}},
							RestartPolicy: corev1.RestartPolicyNever,
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(cj).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:       "default",
		WorkloadName:    "my-cron",
		WorkloadKind:    "CronJob",
		Contract:        c,
		ContractRef:     "ghcr.io/org/my-service:1.0.0",
		WorkloadExplicit: true,
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := es.Observations[0]
	wl, err := o.GetWorkloadObservation()
	if err != nil {
		t.Fatalf("GetWorkloadObservation failed: %v", err)
	}
	if wl.Type != "scheduled" {
		t.Errorf("expected type=scheduled, got %s", wl.Type)
	}
}

// TestCollect_Workload_ReplicaSet verifies ReplicaSet workload type maps to "service".
func TestCollect_Workload_ReplicaSet(t *testing.T) {
	c := makeContract("my-service", "service")
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rs", Namespace: "default"},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-rs"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-rs"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "img"}},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(rs).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:       "default",
		WorkloadName:    "my-rs",
		WorkloadKind:    "ReplicaSet",
		Contract:        c,
		ContractRef:     "ghcr.io/org/my-service:1.0.0",
		WorkloadExplicit: true,
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := es.Observations[0]
	wl, err := o.GetWorkloadObservation()
	if err != nil {
		t.Fatalf("GetWorkloadObservation failed: %v", err)
	}
	if wl.Type != "service" {
		t.Errorf("expected type=service, got %s", wl.Type)
	}
}

// TestCollect_Persistence_MixedVolumes verifies mixed persistent+ephemeral yields durable (persistent wins).
func TestCollect_Persistence_MixedVolumes(t *testing.T) {
	c := makeContractWithPersistence("my-service", "persistent")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "img"}},
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc"},
							},
						},
						{
							Name:         "tmp",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:    "default",
		WorkloadName: "my-app",
		WorkloadKind: "Deployment",
		Contract:     c,
		ContractRef:  "ghcr.io/org/my-service:1.0.0",
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := findObservation(es.Observations, evidence.PersistenceObserved)
	if o == nil {
		t.Fatal("PersistenceObserved not found")
	}

	p, err := o.GetPersistenceObservation()
	if err != nil {
		t.Fatalf("GetPersistenceObservation failed: %v", err)
	}
	if !p.Durable {
		t.Errorf("expected durable=true (persistent wins), got false")
	}
}

// TestCollect_Persistence_ConfigMapVolume verifies configMap is in the ephemeral set.
func TestCollect_Persistence_ConfigMapVolume(t *testing.T) {
	c := makeContractWithPersistence("my-service", "persistent")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "img"}},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:    "default",
		WorkloadName: "my-app",
		WorkloadKind: "Deployment",
		Contract:     c,
		ContractRef:  "ghcr.io/org/my-service:1.0.0",
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := findObservation(es.Observations, evidence.PersistenceObserved)
	if o == nil {
		t.Fatal("PersistenceObserved not found")
	}

	p, err := o.GetPersistenceObservation()
	if err != nil {
		t.Fatalf("GetPersistenceObservation failed: %v", err)
	}
	if p.Durable {
		t.Errorf("expected durable=false for configMap, got true")
	}
}

// TestCollect_Persistence_SecretVolume verifies secret is in the ephemeral set.
func TestCollect_Persistence_SecretVolume(t *testing.T) {
	c := makeContractWithPersistence("my-service", "persistent")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "img"}},
					Volumes: []corev1.Volume{
						{
							Name: "secret",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:    "default",
		WorkloadName: "my-app",
		WorkloadKind: "Deployment",
		Contract:     c,
		ContractRef:  "ghcr.io/org/my-service:1.0.0",
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := findObservation(es.Observations, evidence.PersistenceObserved)
	if o == nil {
		t.Fatal("PersistenceObserved not found")
	}

	p, err := o.GetPersistenceObservation()
	if err != nil {
		t.Fatalf("GetPersistenceObservation failed: %v", err)
	}
	if p.Durable {
		t.Errorf("expected durable=false for secret, got true")
	}
}

// TestCollect_Persistence_DownwardAPIVolume verifies downwardAPI is in the ephemeral set.
func TestCollect_Persistence_DownwardAPIVolume(t *testing.T) {
	c := makeContractWithPersistence("my-service", "persistent")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "img"}},
					Volumes: []corev1.Volume{
						{
							Name: "downward",
							VolumeSource: corev1.VolumeSource{
								DownwardAPI: &corev1.DownwardAPIVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:    "default",
		WorkloadName: "my-app",
		WorkloadKind: "Deployment",
		Contract:     c,
		ContractRef:  "ghcr.io/org/my-service:1.0.0",
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := findObservation(es.Observations, evidence.PersistenceObserved)
	if o == nil {
		t.Fatal("PersistenceObserved not found")
	}

	p, err := o.GetPersistenceObservation()
	if err != nil {
		t.Fatalf("GetPersistenceObservation failed: %v", err)
	}
	if p.Durable {
		t.Errorf("expected durable=false for downwardAPI, got true")
	}
}

// TestCollect_Persistence_ProjectedVolume verifies projected is in the ephemeral set.
func TestCollect_Persistence_ProjectedVolume(t *testing.T) {
	c := makeContractWithPersistence("my-service", "persistent")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "img"}},
					Volumes: []corev1.Volume{
						{
							Name: "projected",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:    "default",
		WorkloadName: "my-app",
		WorkloadKind: "Deployment",
		Contract:     c,
		ContractRef:  "ghcr.io/org/my-service:1.0.0",
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := findObservation(es.Observations, evidence.PersistenceObserved)
	if o == nil {
		t.Fatal("PersistenceObserved not found")
	}

	p, err := o.GetPersistenceObservation()
	if err != nil {
		t.Fatalf("GetPersistenceObservation failed: %v", err)
	}
	if p.Durable {
		t.Errorf("expected durable=false for projected, got true")
	}
}

// TestCollect_Persistence_AmbiguousMixed verifies ambiguous + ephemeral yields ambiguous (ambiguous wins over ephemeral).
func TestCollect_Persistence_AmbiguousMixed(t *testing.T) {
	c := makeContractWithPersistence("my-service", "persistent")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "my-app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "my-app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "img"}},
					Volumes: []corev1.Volume{
						{
							Name: "host",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{Path: "/data"},
							},
						},
						{
							Name:         "tmp",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
				},
			},
		},
	}

	fc := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(fc)

	input := CollectInput{
		Namespace:    "default",
		WorkloadName: "my-app",
		WorkloadKind: "Deployment",
		Contract:     c,
		ContractRef:  "ghcr.io/org/my-service:1.0.0",
	}

	es, err := obs.Collect(context.Background(), input)
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	o := findObservation(es.Observations, evidence.PersistenceObserved)
	if o == nil {
		t.Fatal("PersistenceObserved not found")
	}
	if o.Outcome != evidence.Insufficient {
		t.Errorf("expected Insufficient (ambiguous wins), got %s", o.Outcome)
	}
}
