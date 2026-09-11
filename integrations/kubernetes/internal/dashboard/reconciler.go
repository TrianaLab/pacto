/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package dashboard

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/component"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/credentials"
)

// Reconciler manages the lifecycle of dashboard Kubernetes resources.
type Reconciler struct {
	client.Client

	// APIReader performs uncached reads straight against the API server. cleanup()
	// uses it so disabling the dashboard never starts a cluster-scoped informer
	// (ClusterRole/ClusterRoleBinding/ServiceAccount) whose list/watch the chart
	// only grants when the dashboard is enabled. A cached read would block cache
	// sync and crashloop the manager. Required.
	APIReader client.Reader

	Scheme *runtime.Scheme
	Config Config

	// tickInterval overrides the periodic reconciliation interval (default 5m).
	// Exposed for testing only.
	tickInterval time.Duration
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete

// Sync ensures dashboard resources match the desired state.
// When the feature is enabled, it creates/updates all dashboard resources.
// When disabled, it cleans up any resources it previously created.
func (r *Reconciler) Sync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("dashboard")

	if !r.Config.Enabled {
		log.V(1).Info("Dashboard feature disabled, cleaning up resources")
		return r.cleanup(ctx)
	}

	log.Info("Reconciling dashboard resources", "image", r.Config.Image, "namespace", r.Config.Namespace)

	if err := component.EnsureNamespace(ctx, r.Client, r.Config.Namespace, Labels()); err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	if err := r.reconcileServiceAccount(ctx); err != nil {
		return fmt.Errorf("service account: %w", err)
	}
	if err := r.reconcileClusterRole(ctx); err != nil {
		return fmt.Errorf("cluster role: %w", err)
	}
	if err := r.reconcileClusterRoleBinding(ctx); err != nil {
		return fmt.Errorf("cluster role binding: %w", err)
	}
	if err := r.reconcileOCICredentials(ctx); err != nil {
		return fmt.Errorf("oci credentials: %w", err)
	}
	if err := r.reconcileDeployment(ctx); err != nil {
		return fmt.Errorf("deployment: %w", err)
	}
	if err := r.reconcileService(ctx); err != nil {
		return fmt.Errorf("service: %w", err)
	}

	log.Info("Dashboard resources reconciled successfully")
	return nil
}

// Start implements manager.Runnable.
func (r *Reconciler) Start(ctx context.Context) error {
	logf.FromContext(ctx).WithName("dashboard").Info("Starting dashboard reconciler",
		"enabled", r.Config.Enabled,
		"image", r.Config.Image,
		"namespace", r.Config.Namespace,
	)
	return component.Run(ctx, "dashboard", r.Config.Enabled, r.tickInterval, r.Sync)
}

func (r *Reconciler) reconcileServiceAccount(ctx context.Context) error {
	return r.Apply(ctx, serviceAccountAC(r.Config), client.FieldOwner(FieldManager), client.ForceOwnership)
}

func (r *Reconciler) reconcileClusterRole(ctx context.Context) error {
	return r.Apply(ctx, clusterRoleAC(), client.FieldOwner(FieldManager), client.ForceOwnership)
}

func (r *Reconciler) reconcileClusterRoleBinding(ctx context.Context) error {
	return r.Apply(ctx, clusterRoleBindingAC(r.Config), client.FieldOwner(FieldManager), client.ForceOwnership)
}

// reconcileOCICredentials reads the configured OCI secrets, merges their credentials,
// and creates/updates a managed dockerconfigjson secret for the dashboard pod.
// If no OCI secrets are configured, it cleans up any previously-created managed secret.
func (r *Reconciler) reconcileOCICredentials(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("dashboard")
	secretNames := r.Config.EffectiveOCISecrets()

	if len(secretNames) == 0 {
		// Clean up managed secret if it exists
		existing := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: r.Config.Namespace, Name: ManagedSecretName}, existing)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return r.Delete(ctx, existing)
	}

	// Read all source secrets
	var sources []*corev1.Secret
	for _, name := range secretNames {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: r.Config.Namespace, Name: name}, secret); err != nil {
			log.Error(err, "Failed to read OCI secret", "secret", name)
			return fmt.Errorf("reading OCI secret %q: %w", name, err)
		}
		sources = append(sources, secret)
	}

	// Merge credentials into a single dockerconfigjson
	merged, err := credentials.MergeToDockerConfigJSON(sources)
	if err != nil {
		return fmt.Errorf("merging OCI credentials: %w", err)
	}

	return r.Apply(ctx, ociSecretAC(r.Config, merged), client.FieldOwner(FieldManager), client.ForceOwnership)
}

func (r *Reconciler) reconcileDeployment(ctx context.Context) error {
	return r.Apply(ctx, deploymentAC(r.Config), client.FieldOwner(FieldManager), client.ForceOwnership)
}

func (r *Reconciler) reconcileService(ctx context.Context) error {
	return r.Apply(ctx, serviceAC(r.Config), client.FieldOwner(FieldManager), client.ForceOwnership)
}

// cleanup deletes all dashboard resources owned by the operator, in reverse
// order of creation.
func (r *Reconciler) cleanup(ctx context.Context) error {
	ns := r.Config.Namespace
	return component.Prune(ctx, "dashboard", r.APIReader, r.Client,
		map[string]string{LabelManagedBy: ManagedByValue, LabelComponent: ComponentValue},
		[]component.Resource{
			{Kind: "Service", Obj: &corev1.Service{}, Key: client.ObjectKey{Namespace: ns, Name: Name}},
			{Kind: "Deployment", Obj: &appsv1.Deployment{}, Key: client.ObjectKey{Namespace: ns, Name: Name}},
			{Kind: "Secret", Obj: &corev1.Secret{}, Key: client.ObjectKey{Namespace: ns, Name: ManagedSecretName}},
			{Kind: "ClusterRoleBinding", Obj: &rbacv1.ClusterRoleBinding{}, Key: client.ObjectKey{Name: Name}},
			{Kind: "ClusterRole", Obj: &rbacv1.ClusterRole{}, Key: client.ObjectKey{Name: Name}},
			{Kind: "ServiceAccount", Obj: &corev1.ServiceAccount{}, Key: client.ObjectKey{Namespace: ns, Name: Name}},
		})
}
