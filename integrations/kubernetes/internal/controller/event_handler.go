/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"

	pactov1alpha1 "github.com/trianalab/pacto-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// enqueueForTarget returns an event handler that maps Service/Workload events
// to Pacto CRs that target them.
func enqueueForTarget(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(mapObjectToPactos(c))
}

// enqueueForPullSecret returns an event handler that maps Secret events
// to Pacto CRs that reference them via spec.contractRef.pullSecretRef.
func enqueueForPullSecret(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(mapSecretToPactos(c))
}

// mapSecretToPactos returns the MapFunc used by enqueueForPullSecret.
func mapSecretToPactos(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		pactoList := &pactov1alpha1.PactoList{}
		if err := c.List(ctx, pactoList, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var requests []reconcile.Request
		for _, p := range pactoList.Items {
			if p.Spec.ContractRef.PullSecretRef == obj.GetName() {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: p.Name, Namespace: p.Namespace},
				})
			}
		}
		return requests
	}
}

// mapObjectToPactos returns the MapFunc used by enqueueForTarget.
func mapObjectToPactos(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		pactoList := &pactov1alpha1.PactoList{}
		if err := c.List(ctx, pactoList, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}

		var requests []reconcile.Request

		for _, p := range pactoList.Items {
			nn := types.NamespacedName{Name: p.Name, Namespace: p.Namespace}

			// Match by service name
			if p.Spec.Target.ServiceName == obj.GetName() {
				requests = append(requests, reconcile.Request{NamespacedName: nn})
				continue
			}

			// Match by workload ref name
			workloadName, _ := p.ResolvedWorkload()
			if workloadName == obj.GetName() {
				requests = append(requests, reconcile.Request{NamespacedName: nn})
			}
		}
		return requests
	}
}
