/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// SetupWithManager registers the operation reconciler. An Approval wakes the
// operation it names.
func (r *OperationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("delivery-operation").For(&dv1.Operation{}).
		Watches(&dv1.Approval{}, handler.EnqueueRequestsFromMapFunc(approvalToOperation)).
		Complete(r)
}

// SetupWithManager registers the stage reconciler. An Operation wakes its Stage.
func (r *StageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("delivery-stage").For(&dv1.Stage{}).
		Watches(&dv1.Operation{}, handler.EnqueueRequestsFromMapFunc(operationToStage)).
		Complete(r)
}

func approvalToOperation(_ context.Context, obj client.Object) []reconcile.Request {
	a := obj.(*dv1.Approval)
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: a.Namespace, Name: a.Spec.Operation}}}
}

func operationToStage(_ context.Context, obj client.Object) []reconcile.Request {
	o := obj.(*dv1.Operation)
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: o.Namespace, Name: o.Spec.Stage}}}
}

// registryFor returns a registry client for the apply repository, reaching it
// over plain HTTP when the OCIRepository says so.
func (c Cluster) registryFor(a *applyRef) delivery.Registry {
	if a.insecure {
		return c.Registry(a.host())
	}
	return c.Registry()
}

// ServiceAccountUser is the apiserver username of a service account.
func ServiceAccountUser(namespace, name string) string {
	return "system:serviceaccount:" + namespace + ":" + name
}

// SplitList splits a comma-separated flag value, dropping blanks.
func SplitList(v string) []string {
	var out []string
	for s := range strings.SplitSeq(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LoadClassPolicy reads the ClassPolicy the delivery chart publishes, mounted
// from its ConfigMap, and makes controllerUser its own; an empty path keeps
// the defaults. The promoter runs with exactly what clients read.
func LoadClassPolicy(path, controllerUser string) (delivery.ClassPolicy, error) {
	p := delivery.DefaultClassPolicy()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return delivery.ClassPolicy{}, err
		}
		if p, err = delivery.ParseClassPolicy(b); err != nil {
			return delivery.ClassPolicy{}, err
		}
	}
	p.ControllerUser = controllerUser
	return p, nil
}
