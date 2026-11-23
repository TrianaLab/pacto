/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// ladderSource is where a declared installation ladder rung comes from.
const ladderSource = "the chart value delivery.environments.ladder"

// EnvironmentReconciler publishes the installation ladder: the declared chain
// (Ladder) and the rungs Pacto proposes from the cluster's namespaces, the
// services its Pactos describe and the environments the cluster advertises.
// It writes only EnvironmentsConfigMap in Namespace and touches no Stage.
// One pass runs whenever a Namespace or a Pacto changes, and every Resync.
type EnvironmentReconciler struct {
	Client    client.Client
	APIReader client.Reader
	// Namespace is the delivery namespace, where the view is written.
	Namespace string
	ClusterID string
	// ClusterName and NameSource are the cluster's display name and how it
	// was found (kubecluster.Identify).
	ClusterName string
	NameSource  string
	// Ladder is the declared installation ladder, in promotion order.
	Ladder []string
	// Off publishes nothing (--environments=false).
	Off    bool
	Now    func() time.Time
	Resync time.Duration
}

// SetupWithManager registers the pass on Namespace and, when the cluster
// serves it, Pacto changes. Every event requeues the one installation-wide
// pass. Off registers nothing.
func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Off {
		return nil
	}
	b := ctrl.NewControllerManagedBy(mgr).Named("delivery-environments").
		Watches(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(onePass))
	pacto := schema.GroupVersionKind{Group: gvkPactoList.Group, Version: gvkPactoList.Version, Kind: "Pacto"}
	if _, err := mgr.GetRESTMapper().RESTMapping(pacto.GroupKind(), pacto.Version); err == nil {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(pacto)
		b.Watches(u, handler.EnqueueRequestsFromMapFunc(onePass))
	}
	return b.Complete(r)
}

// onePass maps every event to the single installation-wide request.
func onePass(context.Context, client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: delivery.EnvironmentsConfigMap}}}
}

// Reconcile runs one pass: it reads the namespaces and the Pactos, proposes
// rungs and writes the view. A read that fails leaves the view partial with
// the reason, and the proposal is built from what was read.
func (r *EnvironmentReconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	if r.Off {
		return reconcile.Result{}, nil
	}
	view := delivery.EnvironmentsView{AsOf: r.Now().UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete}
	var in delivery.LadderInput
	var unread []string
	namespaces := &corev1.NamespaceList{}
	if err := r.Client.List(ctx, namespaces); err != nil {
		unread = append(unread, "the namespaces: "+err.Error())
	}
	for _, n := range namespaces.Items {
		in.Namespaces = append(in.Namespaces, n.Name)
	}
	pactos, err := Cluster{Client: r.Client}.pactos(ctx)
	if err != nil {
		unread = append(unread, "the Pactos: "+err.Error())
	}
	services := map[string]bool{}
	for _, p := range pactos {
		if c := p.Status.Contract; c != nil && c.ServiceName != "" {
			services[c.ServiceName] = true
		}
	}
	in.Services = slices.Sorted(maps.Keys(services))
	in.Advertised = delivery.AdvertisedEnvironments(pactos, r.ClusterName, r.NameSource)
	for i, name := range r.Ladder {
		rung := delivery.LadderRung{Name: name, Source: ladderSource, Provenance: delivery.RungDeclared}
		if i > 0 {
			rung.Upstream = r.Ladder[i-1]
		}
		view.Declared = append(view.Declared, rung)
	}
	view.Proposal = delivery.ProposeLadder(r.Ladder, in)
	view.ProposalDigest = delivery.ProposalDigest(view.Proposal)
	if len(unread) > 0 {
		view.Completeness = fleet.CompletenessPartial
		view.CompletenessReason = "could not read " + strings.Join(unread, "; ")
	}
	return reconcile.Result{RequeueAfter: r.Resync}, r.writeView(ctx, view)
}

// writeView creates EnvironmentsConfigMap, or merge-patches its view.json
// when it exists. It reads through APIReader, so the promoter caches no
// ConfigMap and needs get on this one name only.
func (r *EnvironmentReconciler) writeView(ctx context.Context, v delivery.EnvironmentsView) error {
	b, _ := json.Marshal(v) // ponytail: plain strings, slices and ints: Marshal cannot fail
	cm := &corev1.ConfigMap{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: delivery.EnvironmentsConfigMap}, cm)
	if apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: delivery.EnvironmentsConfigMap},
			Data:       map[string]string{"view.json": string(b)}})
	}
	if err != nil {
		return err
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = map[string]string{"view.json": string(b)}
	return r.Client.Patch(ctx, cm, patch)
}
