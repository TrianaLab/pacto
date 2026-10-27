/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"cmp"
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// SetupWithManager registers the operation reconciler. An Approval wakes the
// operation it names, and Approvals are indexed by that operation's uid.
func (r *OperationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.heldChecks = &sync.Map{}
	err := mgr.GetFieldIndexer().IndexField(context.Background(), &dv1.Approval{}, approvalOperationUID, approvalUID)
	return errors.Join(err, ctrl.NewControllerManagedBy(mgr).Named("delivery-operation").For(&dv1.Operation{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Watches(&dv1.Approval{}, handler.EnqueueRequestsFromMapFunc(approvalToOperation)).
		Complete(r))
}

// approvalOperationUID indexes Approvals by the uid of the operation they answer.
const approvalOperationUID = "spec.operationUID"

func approvalUID(obj client.Object) []string { return []string{obj.(*dv1.Approval).Spec.OperationUID} }

// SetupWithManager registers the stage reconciler. A Stage reconciles when
// something it reads changes: its operations, its upstream Stage, its apply
// and candidate OCIRepositories, the Kustomizations that apply them and, when
// Cluster.CacheApplications, its Application. A Flux kind whose CRD is not
// installed when the promoter starts is not watched. Resync reconciles every
// Stage anyway, for what no watch covers.
func (r *StageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.seen = &sync.Map{}
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &dv1.Stage{}, stageRefs, stageRefKeys); err != nil {
		return err
	}
	b := ctrl.NewControllerManagedBy(mgr).Named("delivery-stage").For(&dv1.Stage{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Watches(&dv1.Operation{}, handler.EnqueueRequestsFromMapFunc(operationToStage)).
		Watches(&dv1.Stage{}, r.mapRefs(func(o client.Object) []string { return []string{refKey(o.GetNamespace(), "Stage", o.GetName())} }))
	watch := func(gvk schema.GroupVersionKind, keys func(*unstructured.Unstructured) []string) {
		if _, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
			// ponytail: a Flux installed later is read on Resync until the promoter restarts.
			mgr.GetLogger().Info("not watching a kind the cluster does not serve", "kind", gvk.Kind, "reason", err.Error())
			return
		}
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		b.Watches(u, r.mapRefs(func(o client.Object) []string { return keys(o.(*unstructured.Unstructured)) }))
	}
	watch(gvkOCIRepository, func(u *unstructured.Unstructured) []string {
		return []string{refKey(u.GetNamespace(), kindOCIRepository, u.GetName())}
	})
	watch(schema.GroupVersionKind{Group: gvkKustomization.Group, Version: gvkKustomization.Version, Kind: "Kustomization"},
		func(u *unstructured.Unstructured) []string {
			kind, _, _ := unstructured.NestedString(u.Object, "spec", "sourceRef", "kind")
			name, _, _ := unstructured.NestedString(u.Object, "spec", "sourceRef", "name")
			srcNS, _, _ := unstructured.NestedString(u.Object, "spec", "sourceRef", "namespace")
			if kind != kindOCIRepository {
				return nil
			}
			return []string{refKey(cmp.Or(srcNS, u.GetNamespace()), kindOCIRepository, name)}
		})
	if r.Cluster.CacheApplications {
		watch(gvkApplication, func(u *unstructured.Unstructured) []string {
			return []string{refKey(u.GetNamespace(), delivery.ApplierArgoCD, u.GetName())}
		})
	}
	return b.Complete(r)
}

const kindOCIRepository = "OCIRepository"

// stageRefs indexes Stages by the objects they read (refKey).
const stageRefs = "delivery.refs"

func refKey(ns, kind, name string) string { return ns + "/" + kind + "/" + name }

func stageRefKeys(obj client.Object) []string {
	s := obj.(*dv1.Stage)
	kind := delivery.ApplierArgoCD
	if s.Spec.ApplyRef.Kind != delivery.ApplierArgoCD {
		kind = kindOCIRepository
	}
	keys := []string{refKey(s.Namespace, kind, s.Spec.ApplyRef.Name)}
	if c := s.Spec.Candidates.OCIRepositoryRef; c != nil {
		keys = append(keys, refKey(s.Namespace, kindOCIRepository, c.Name))
	}
	if up := s.Spec.Candidates.UpstreamStage; up != nil {
		keys = append(keys, refKey(cmp.Or(up.Namespace, s.Namespace), "Stage", up.Name))
	}
	return keys
}

// mapRefs wakes every Stage that reads one of the objects keys names.
func (r *StageReconciler) mapRefs(keys func(client.Object) []string) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var out []reconcile.Request
		for _, k := range keys(obj) {
			list := &dv1.StageList{}
			if err := r.List(ctx, list, client.MatchingFields{stageRefs: k}); err != nil {
				continue // ponytail: a failed cache read waits for Resync
			}
			for _, s := range list.Items {
				out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: s.Namespace, Name: s.Name}})
			}
		}
		return out
	})
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

// LoadClassPolicy reads the ClassPolicy the chart's delivery section publishes, mounted
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
