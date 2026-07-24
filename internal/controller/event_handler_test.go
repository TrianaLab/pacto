/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"testing"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// ---------- mapSecretToPactos ----------

func TestMapSecretToPactos(t *testing.T) {
	s := newScheme()

	p1 := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc", PullSecretRef: "oci-creds"},
		},
	}
	p2 := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc2"},
		},
	}
	p3 := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p3", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc3", PullSecretRef: "oci-creds"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p1, p2, p3).Build()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-creds", Namespace: "ns"},
	}

	fn := mapSecretToPactos(c)
	requests := fn(context.Background(), secret)

	if len(requests) != 2 {
		t.Fatalf("expected 2 requests (p1, p3), got %d", len(requests))
	}

	found := map[string]bool{}
	for _, req := range requests {
		found[req.Name] = true
	}
	if !found["p1"] || !found["p3"] {
		t.Errorf("expected p1 and p3, got %+v", found)
	}
	if found["p2"] {
		t.Errorf("p2 should not be included (no pullSecretRef)")
	}
}

func TestMapSecretToPactos_NoPactos(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-creds", Namespace: "ns"},
	}

	fn := mapSecretToPactos(c)
	requests := fn(context.Background(), secret)

	if len(requests) != 0 {
		t.Errorf("expected 0 requests, got %d", len(requests))
	}
}

func TestMapSecretToPactos_NoMatch(t *testing.T) {
	s := newScheme()
	p := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			ContractRef: pactov1alpha1.ContractRef{OCI: "ghcr.io/org/svc", PullSecretRef: "other-secret"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p).Build()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "oci-creds", Namespace: "ns"},
	}

	fn := mapSecretToPactos(c)
	requests := fn(context.Background(), secret)

	if len(requests) != 0 {
		t.Errorf("expected 0 requests (no matching pullSecretRef), got %d", len(requests))
	}
}

// ---------- mapObjectToPactos ----------

func TestMapObjectToPactos_MatchByServiceName(t *testing.T) {
	s := newScheme()
	p := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			Target: pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p).Build()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "ns"},
	}

	fn := mapObjectToPactos(c)
	requests := fn(context.Background(), svc)

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Name != "p" {
		t.Errorf("expected request for p, got %s", requests[0].Name)
	}
}

func TestMapObjectToPactos_MatchByWorkloadName(t *testing.T) {
	s := newScheme()
	p := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			Target: pactov1alpha1.TargetRef{
				WorkloadRef: &pactov1alpha1.WorkloadRef{
					Name: "my-deploy",
					Kind: "Deployment",
				},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p).Build()

	obj := &metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "my-deploy", Namespace: "ns"},
	}

	fn := mapObjectToPactos(c)
	requests := fn(context.Background(), obj)

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Name != "p" {
		t.Errorf("expected request for p, got %s", requests[0].Name)
	}
}

func TestMapObjectToPactos_NoMatch(t *testing.T) {
	s := newScheme()
	p := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			Target: pactov1alpha1.TargetRef{ServiceName: "other-svc"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p).Build()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "ns"},
	}

	fn := mapObjectToPactos(c)
	requests := fn(context.Background(), svc)

	if len(requests) != 0 {
		t.Errorf("expected 0 requests, got %d", len(requests))
	}
}

func TestMapObjectToPactos_MultipleMatches(t *testing.T) {
	s := newScheme()
	p1 := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			Target: pactov1alpha1.TargetRef{ServiceName: "shared-svc"},
		},
	}
	p2 := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			Target: pactov1alpha1.TargetRef{ServiceName: "shared-svc"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p1, p2).Build()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-svc", Namespace: "ns"},
	}

	fn := mapObjectToPactos(c)
	requests := fn(context.Background(), svc)

	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	found := map[string]bool{}
	for _, req := range requests {
		found[req.Name] = true
	}
	if !found["p1"] || !found["p2"] {
		t.Errorf("expected p1 and p2, got %+v", found)
	}
}

func TestMapObjectToPactos_DifferentNamespace(t *testing.T) {
	s := newScheme()
	p := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns1"},
		Spec: pactov1alpha1.PactoSpec{
			Target: pactov1alpha1.TargetRef{ServiceName: "my-svc"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p).Build()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "ns2"},
	}

	fn := mapObjectToPactos(c)
	requests := fn(context.Background(), svc)

	if len(requests) != 0 {
		t.Errorf("expected 0 requests (different namespace), got %d", len(requests))
	}
}

func TestMapObjectToPactos_BothServiceAndWorkload(t *testing.T) {
	s := newScheme()
	p := &pactov1alpha1.Pacto{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Spec: pactov1alpha1.PactoSpec{
			Target: pactov1alpha1.TargetRef{
				ServiceName: "my-svc",
				WorkloadRef: &pactov1alpha1.WorkloadRef{
					Name: "my-deploy",
					Kind: "Deployment",
				},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(p).Build()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "ns"},
	}

	fn := mapObjectToPactos(c)
	requests := fn(context.Background(), svc)

	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
}
