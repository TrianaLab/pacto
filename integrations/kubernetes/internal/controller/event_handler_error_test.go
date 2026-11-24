/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"fmt"
	"testing"

	pactov1 "github.com/trianalab/pacto/integrations/kubernetes/v7/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Test mapObjectToPactos with List error
func TestMapObjectToPactos_ListError(t *testing.T) {
	s := newScheme()
	c := fake.NewClientBuilder().WithScheme(s).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*pactov1.PactoList); ok {
					return fmt.Errorf("list failed")
				}
				return c.List(ctx, list, opts...)
			},
		}).Build()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "my-svc", Namespace: "ns"},
	}

	fn := mapObjectToPactos(c)
	requests := fn(context.Background(), svc)

	if requests != nil {
		t.Errorf("expected nil result on List error, got %d requests", len(requests))
	}
}
