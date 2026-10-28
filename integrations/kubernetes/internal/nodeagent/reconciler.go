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
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"
)

// The operator grants the agent's Role, so it must hold every verb in it, in the
// agent namespace; list, watch and delete are its own (the kernel merge reads the
// observations and Leases, the prunes remove them). Everything it creates lives
// in that one namespace, so the lifecycle verbs are a namespaced Role there; the
// namespace itself is read and relabelled by name. Nodes get is the orphan
// prune. The legacy rules are the teardown of what earlier releases ran in the
// operator namespace ("system" is kustomize's placeholder for it).
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;patch,resourceNames=pacto-node-agent
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;create;patch;delete,namespace=pacto-node-agent
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;create;patch;delete,namespace=pacto-node-agent
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;create;update;patch;delete,namespace=pacto-node-agent
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=noderuntimeobservations,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=noderuntimeobservations,verbs=create;patch,namespace=pacto-node-agent
// +kubebuilder:rbac:groups=pacto.trianalab.io,resources=noderuntimeobservations/status,verbs=get;create;patch,namespace=pacto-node-agent
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=create;patch;update;delete,namespace=pacto-node-agent
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;delete,namespace=system,resourceNames=pacto-node-agent
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;delete,namespace=system,resourceNames=pacto-node-agent
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;delete,namespace=system,resourceNames=pacto-node-agent

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
// labels the agent namespace, applies the ServiceAccount, Role, RoleBinding and
// DaemonSet there and prunes the observations and Leases of removed nodes; when
// disabled it deletes them and every NodeRuntimeObservation and Lease the agents
// published. Either way it removes what an earlier release ran in the operator
// namespace.
func (r *Reconciler) Sync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("node-agent")

	if !r.Config.Enabled {
		log.V(1).Info("Node agent disabled, cleaning up its resources and observations")
		if err := r.cleanup(ctx, r.Config.Namespace); err != nil {
			return err
		}
		r.pruneLegacy(ctx)
		return nil
	}

	log.Info("Reconciling node agent resources", "image", r.Config.Image, "namespace", r.Config.Namespace)

	for _, step := range []struct {
		kind string
		ac   runtime.ApplyConfiguration
	}{
		{"namespace", namespaceAC(r.Config)},
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
	r.pruneLegacy(ctx)

	log.Info("Node agent resources reconciled successfully")
	return nil
}

// pruneLegacy removes what an earlier release ran in the operator namespace,
// where the agent lived before it had its own. Best effort, like the orphan
// prune: a leftover there is logged, never a Sync failure.
func (r *Reconciler) pruneLegacy(ctx context.Context) {
	if r.Config.OperatorNamespace == "" || r.Config.OperatorNamespace == r.Config.Namespace {
		return
	}
	if err := r.cleanup(ctx, r.Config.OperatorNamespace); err != nil {
		logf.FromContext(ctx).WithName("node-agent").Error(err, "Could not remove the node agent from the operator namespace",
			"namespace", r.Config.OperatorNamespace)
	}
}

// pruneOrphans deletes the observations and Leases of nodes that no longer
// exist: the agent of a removed node cannot delete its own objects, which would
// otherwise linger as stale evidence. It is best effort, logged and never a Sync
// failure: a failed first sync stops the whole manager.
func (r *Reconciler) pruneOrphans(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("node-agent")
	for _, kind := range transportKinds() {
		if err := r.APIReader.List(ctx, kind.list, client.InNamespace(r.Config.Namespace), client.MatchingLabels(Labels())); err != nil {
			log.Error(err, "Could not list node agent objects to prune", "kind", kind.name)
			continue
		}
		items, _ := meta.ExtractList(kind.list)
		for _, item := range items {
			obj := item.(client.Object)
			err := r.APIReader.Get(ctx, client.ObjectKey{Name: obj.GetName()}, &corev1.Node{})
			if !apierrors.IsNotFound(err) {
				if err != nil {
					log.Error(err, "Could not read the node of an observation", "node", obj.GetName())
				}
				continue
			}
			if err := r.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
				log.Error(err, "Could not delete the object of a removed node", "kind", kind.name, "node", obj.GetName())
				continue
			}
			log.Info("Deleted the object of a removed node", "kind", kind.name, "name", obj.GetName())
		}
	}
}

// transportKind is one kind the agents write, one object per node.
type transportKind struct {
	name string
	list client.ObjectList
}

// transportKinds returns fresh lists of the observations and the heartbeat Leases.
func transportKinds() []transportKind {
	return []transportKind{
		{"NodeRuntimeObservation", &pactov1alpha1.NodeRuntimeObservationList{}},
		{"Lease", &coordinationv1.LeaseList{}},
	}
}

// Start implements manager.Runnable.
func (r *Reconciler) Start(ctx context.Context) error {
	logf.FromContext(ctx).WithName("node-agent").Info("Starting node agent reconciler",
		"enabled", r.Config.Enabled, "image", r.Config.Image, "namespace", r.Config.Namespace)
	return component.Run(ctx, "node-agent", r.Config.Enabled, r.tickInterval, r.Sync)
}

// cleanup deletes the workload in ns first, then the RBAC that let it write,
// then the observations and Leases it wrote: revoking the Role before the sweep
// means a terminating agent cannot publish its objects again after they were
// deleted. The namespace itself is the chart's and is left alone.
func (r *Reconciler) cleanup(ctx context.Context, ns string) error {
	owned := component.Owned(ComponentValue)
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

// pruneObservations deletes every NodeRuntimeObservation and Lease carrying the
// agent's labels. The objects are named after nodes, so they are found by label
// rather than by name; one the agent did not write is left alone.
func (r *Reconciler) pruneObservations(ctx context.Context, ns string, owned map[string]string) error {
	log := logf.FromContext(ctx).WithName("node-agent")
	for _, kind := range transportKinds() {
		if err := r.APIReader.List(ctx, kind.list, client.InNamespace(ns), client.MatchingLabels(owned)); err != nil {
			// No list RBAC or no CRD means nothing of ours can exist to delete.
			if apierrors.IsForbidden(err) || meta.IsNoMatchError(err) {
				log.V(1).Info("Skipping cleanup; not readable", "kind", kind.name, "reason", err.Error())
				continue
			}
			return fmt.Errorf("failed to list %ss: %w", kind.name, err)
		}
		items, _ := meta.ExtractList(kind.list)
		for _, item := range items {
			obj := item.(client.Object)
			err := r.Delete(ctx, obj)
			switch {
			case apierrors.IsForbidden(err):
				// Readable but not deletable: the chart already removed the
				// namespace these live in, with everything in it.
				log.V(1).Info("Skipping cleanup; not deletable", "kind", kind.name, "name", obj.GetName())
				continue
			case err != nil && !apierrors.IsNotFound(err):
				return fmt.Errorf("failed to delete %s %s: %w", kind.name, obj.GetName(), err)
			}
			log.Info("Deleted resource", "kind", kind.name, "name", obj.GetName())
		}
	}
	return nil
}
