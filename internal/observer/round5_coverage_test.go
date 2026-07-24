/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package observer

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// observeDeployment records the declared replica count when Spec.Replicas is set.
func TestObserveDeployment_WithReplicas(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(4),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "app"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "app"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1"}}},
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(dep).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	if err := obs.observeDeployment(context.Background(), client.ObjectKey{Name: "app", Namespace: "default"}, snap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Replicas == nil || *snap.Replicas != 4 {
		t.Errorf("expected Replicas=4, got %v", snap.Replicas)
	}
}

// observeStatefulSet returns nil (no error) when the StatefulSet does not exist.
func TestObserveStatefulSet_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	obs := New(c)

	snap := &RuntimeSnapshot{}
	if err := obs.observeStatefulSet(context.Background(), client.ObjectKey{Name: "missing", Namespace: "default"}, snap); err != nil {
		t.Fatalf("unexpected error for missing StatefulSet: %v", err)
	}
	if snap.WorkloadExists {
		t.Error("expected WorkloadExists=false for missing StatefulSet")
	}
}
