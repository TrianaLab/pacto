package observer

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Test CollectForTarget with observe error (Get service fails)
func TestCollectForTarget_ServiceGetError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Service); ok {
					return fmt.Errorf("service get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	obs := New(c)

	_, err := obs.CollectForTarget(context.Background(), "default", "svc", "dep", "Deployment", "ref")
	if err == nil {
		t.Fatal("expected error from service Get failure")
	}
}

// Test CollectForTarget with workload observe error
func TestCollectForTarget_WorkloadObserveError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.Deployment); ok {
					return fmt.Errorf("deployment get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	obs := New(c)

	_, err := obs.CollectForTarget(context.Background(), "default", "", "dep", "Deployment", "ref")
	if err == nil {
		t.Fatal("expected error from deployment Get failure")
	}
}

// Test observeWorkload with unknown kind (default case)
func TestObserveWorkload_UnknownKind(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "app"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "app:v1"}},
				},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	err := obs.observeWorkload(context.Background(), "default", "app", "UnknownKind", snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true (default to Deployment)")
	}
}

// Test observeDeployment with Get error (non-NotFound)
func TestObserveDeployment_GetError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.Deployment); ok {
					return fmt.Errorf("get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	err := obs.observeDeployment(context.Background(), client.ObjectKey{Name: "app", Namespace: "default"}, snap)
	if err == nil {
		t.Fatal("expected error from Get failure")
	}
}

// Test observeStatefulSet with Get error (non-NotFound)
func TestObserveStatefulSet_GetError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.StatefulSet); ok {
					return fmt.Errorf("get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	err := obs.observeStatefulSet(context.Background(), client.ObjectKey{Name: "db", Namespace: "default"}, snap)
	if err == nil {
		t.Fatal("expected error from Get failure")
	}
}

// Test observeReplicaSet with Get error (non-NotFound)
func TestObserveReplicaSet_GetError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.ReplicaSet); ok {
					return fmt.Errorf("get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	err := obs.observeReplicaSet(context.Background(), client.ObjectKey{Name: "rs", Namespace: "default"}, snap)
	if err == nil {
		t.Fatal("expected error from Get failure")
	}
}

// Test observeJob with Get error (non-NotFound)
func TestObserveJob_GetError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*batchv1.Job); ok {
					return fmt.Errorf("get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	err := obs.observeJob(context.Background(), client.ObjectKey{Name: "job", Namespace: "default"}, snap)
	if err == nil {
		t.Fatal("expected error from Get failure")
	}
}

// Test observeCronJob with Get error (non-NotFound)
func TestObserveCronJob_GetError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*batchv1.CronJob); ok {
					return fmt.Errorf("get failed")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	err := obs.observeCronJob(context.Background(), client.ObjectKey{Name: "cj", Namespace: "default"}, snap)
	if err == nil {
		t.Fatal("expected error from Get failure")
	}
}

// Test observeStatefulSet with all optional fields populated
func TestObserveStatefulSet_AllFields(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			PodManagementPolicy: appsv1.OrderedReadyPodManagement,
			Replicas:            int32Ptr(3),
			Selector:            &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "postgres:15"}}},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(sts).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	err := obs.observeStatefulSet(context.Background(), client.ObjectKey{Name: "db", Namespace: "default"}, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.WorkloadExists {
		t.Error("expected WorkloadExists=true")
	}
	if snap.PodManagementPolicy != "OrderedReady" {
		t.Errorf("expected PodManagementPolicy=OrderedReady, got %s", snap.PodManagementPolicy)
	}
	if snap.Replicas == nil || *snap.Replicas != 3 {
		t.Errorf("expected Replicas=3, got %v", snap.Replicas)
	}
}

// Test extractPodTemplateInfo with PVC volume
func TestExtractPodTemplateInfo_PVC(t *testing.T) {
	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{
			{Name: "app", Image: "app:v1"},
		},
		Volumes: []corev1.Volume{
			{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "data-claim",
					},
				},
			},
		},
	}

	obs := &Observer{}
	snap := &RuntimeSnapshot{}
	obs.extractPodTemplateInfo(podSpec, snap)

	if !snap.HasPVC {
		t.Error("expected HasPVC=true")
	}
	if snap.HasEmptyDir {
		t.Error("expected HasEmptyDir=false")
	}
}

// Test extractPodTemplateInfo with liveness probe only
func TestExtractPodTemplateInfo_LivenessProbe(t *testing.T) {
	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "app",
				Image: "app:v1",
				LivenessProbe: &corev1.Probe{
					InitialDelaySeconds: 15,
				},
			},
		},
	}

	obs := &Observer{}
	snap := &RuntimeSnapshot{}
	obs.extractPodTemplateInfo(podSpec, snap)

	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 15 {
		t.Errorf("expected liveness probe delay=15, got %v", snap.HealthProbeInitialDelay)
	}
}

// Test extractPodTemplateInfo with readiness probe (takes precedence)
func TestExtractPodTemplateInfo_ReadinessProbe(t *testing.T) {
	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "app",
				Image: "app:v1",
				ReadinessProbe: &corev1.Probe{
					InitialDelaySeconds: 20,
				},
				LivenessProbe: &corev1.Probe{
					InitialDelaySeconds: 15,
				},
			},
		},
	}

	obs := &Observer{}
	snap := &RuntimeSnapshot{}
	obs.extractPodTemplateInfo(podSpec, snap)

	if snap.HealthProbeInitialDelay == nil || *snap.HealthProbeInitialDelay != 20 {
		t.Errorf("expected readiness probe delay=20 (precedence over liveness), got %v", snap.HealthProbeInitialDelay)
	}
}

// Test extractPodTemplateInfo with no containers (edge case)
func TestExtractPodTemplateInfo_NoContainers(t *testing.T) {
	podSpec := &corev1.PodSpec{
		Containers: []corev1.Container{},
	}

	obs := &Observer{}
	snap := &RuntimeSnapshot{}
	obs.extractPodTemplateInfo(podSpec, snap)

	if snap.HealthProbeInitialDelay != nil {
		t.Error("expected no probe delay when no containers")
	}
	if len(snap.ContainerImages) != 0 {
		t.Error("expected no container images when no containers")
	}
}
