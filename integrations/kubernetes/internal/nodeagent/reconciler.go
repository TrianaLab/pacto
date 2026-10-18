/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package nodeagent

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/component"
)

// The operator grants the agent's Role, so it must hold every verb in it; list,
// watch and delete are its own (the kernel merge reads the observations, the
// prune removes them). Namespaces get is the Pod Security check, nodes get the
// orphan prune.
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;create;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;create;patch;delete
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;create;update;patch;delete
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=noderuntimeobservations,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=noderuntimeobservations/status,verbs=get;create;patch

// Reconciler manages the lifecycle of the node agent's Kubernetes resources.
type Reconciler struct {
	client.Client

	// APIReader performs uncached reads (the Pod Security check and cleanup), so
	// neither ever starts an informer whose RBAC was never granted. Required.
	APIReader client.Reader

	Scheme *runtime.Scheme
	Config Config

	// tickInterval overrides the periodic reconciliation interval (default 5m).
	// Exposed for testing only.
	tickInterval time.Duration
}

// Sync ensures the node agent resources match the desired state. When enabled it
// applies the ServiceAccount, Role, RoleBinding and DaemonSet and prunes the
// observations of removed nodes; when disabled it deletes them and every
// NodeRuntimeObservation the agents published.
func (r *Reconciler) Sync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("node-agent")

	if !r.Config.Enabled {
		log.V(1).Info("Node agent disabled, cleaning up its resources and observations")
		return r.cleanup(ctx)
	}

	log.Info("Reconciling node agent resources", "image", r.Config.Image, "namespace", r.Config.Namespace)

	r.checkPodSecurity(ctx)

	for _, step := range []struct {
		kind string
		ac   runtime.ApplyConfiguration
	}{
		{"serviceaccount", serviceAccountAC(r.Config)},
		{"role", roleAC(r.Config)},
		{"rolebinding", roleBindingAC(r.Config)},
		{"daemonset", daemonSetAC(r.Config)},
	} {
		if err := r.Apply(ctx, step.ac, client.FieldOwner(FieldManager), client.ForceOwnership); err != nil {
			return fmt.Errorf("%s: %w", step.kind, err)
		}
	}

	r.pruneOrphans(ctx)

	log.Info("Node agent resources reconciled successfully")
	return nil
}

// pruneOrphans deletes the observations of nodes that no longer exist: the agent
// of a removed node cannot delete its own object, which would otherwise linger
// as stale evidence. It is best effort, logged and never a Sync failure, for the
// same reason as the Pod Security check.
func (r *Reconciler) pruneOrphans(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("node-agent")
	list := &pactov1alpha1.NodeRuntimeObservationList{}
	if err := r.APIReader.List(ctx, list, client.InNamespace(r.Config.Namespace), client.MatchingLabels(Labels())); err != nil {
		log.Error(err, "Could not list node observations to prune")
		return
	}
	for i := range list.Items {
		obj := &list.Items[i]
		err := r.APIReader.Get(ctx, client.ObjectKey{Name: obj.Name}, &corev1.Node{})
		if !apierrors.IsNotFound(err) {
			if err != nil {
				log.Error(err, "Could not read the node of an observation", "node", obj.Name)
			}
			continue
		}
		if err := r.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Could not delete the observation of a removed node", "node", obj.Name)
			continue
		}
		log.Info("Deleted the observation of a removed node", "kind", "NodeRuntimeObservation", "name", obj.Name)
	}
}

// checkPodSecurity logs an error when the namespace's enforced Pod Security level
// would reject the agent pod. It is a logged condition, never a Sync failure: a
// failed first sync stops the whole manager, and an operator that crashloops
// over a namespace label helps nobody. The DaemonSet is still applied, so the
// agents start as soon as the label is relaxed. The namespace is never
// relabelled: what a namespace admits is its owner's decision.
func (r *Reconciler) checkPodSecurity(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("node-agent")
	ns := &corev1.Namespace{}
	if err := r.APIReader.Get(ctx, client.ObjectKey{Name: r.Config.Namespace}, ns); err != nil {
		log.Error(err, "Could not read the namespace to check its Pod Security level", "namespace", r.Config.Namespace)
		return
	}
	switch level := ns.Labels[PodSecurityEnforceLabel]; level {
	case "baseline", "restricted":
		log.Error(nil, "Namespace Pod Security level rejects the node agent pods; label it privileged",
			"namespace", r.Config.Namespace, "label", PodSecurityEnforceLabel, "level", level)
	}
}

// Start implements manager.Runnable.
func (r *Reconciler) Start(ctx context.Context) error {
	logf.FromContext(ctx).WithName("node-agent").Info("Starting node agent reconciler",
		"enabled", r.Config.Enabled, "image", r.Config.Image, "namespace", r.Config.Namespace)
	return component.Run(ctx, "node-agent", r.Config.Enabled, r.tickInterval, r.Sync)
}

// cleanup deletes the workload first, then the RBAC that let it write, then the
// observations it wrote: revoking the Role before the sweep means a terminating
// agent cannot publish its object again after it was deleted.
func (r *Reconciler) cleanup(ctx context.Context) error {
	ns := r.Config.Namespace
	owned := map[string]string{LabelManagedBy: ManagedByValue, LabelComponent: ComponentValue}
	key := client.ObjectKey{Namespace: ns, Name: Name}
	if err := component.Prune(ctx, "node-agent", r.APIReader, r.Client, owned, []component.Resource{
		{Kind: "DaemonSet", Obj: &appsv1.DaemonSet{}, Key: key},
		{Kind: "RoleBinding", Obj: &rbacv1.RoleBinding{}, Key: key},
		{Kind: "Role", Obj: &rbacv1.Role{}, Key: key},
		{Kind: "ServiceAccount", Obj: &corev1.ServiceAccount{}, Key: key},
	}); err != nil {
		return err
	}
	return r.pruneObservations(ctx, ns, owned)
}

// pruneObservations deletes every NodeRuntimeObservation carrying the agent's
// labels. The objects are named after nodes, so they are found by label rather
// than by name; one the agent did not write is left alone.
func (r *Reconciler) pruneObservations(ctx context.Context, ns string, owned map[string]string) error {
	log := logf.FromContext(ctx).WithName("node-agent")
	list := &pactov1alpha1.NodeRuntimeObservationList{}
	if err := r.APIReader.List(ctx, list, client.InNamespace(ns), client.MatchingLabels(owned)); err != nil {
		// No list RBAC or no CRD means nothing of ours can exist to delete.
		if apierrors.IsForbidden(err) || meta.IsNoMatchError(err) {
			log.V(1).Info("Skipping observation cleanup; not readable", "reason", err.Error())
			return nil
		}
		return fmt.Errorf("failed to list NodeRuntimeObservations: %w", err)
	}
	for i := range list.Items {
		obj := &list.Items[i]
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete NodeRuntimeObservation %s: %w", obj.Name, err)
		}
		log.Info("Deleted resource", "kind", "NodeRuntimeObservation", "name", obj.Name)
	}
	return nil
}
