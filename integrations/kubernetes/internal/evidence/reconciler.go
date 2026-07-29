/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package evidence

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Reconciler manages the lifecycle of Evidence Server Kubernetes resources.
type Reconciler struct {
	client.Client

	// APIReader performs uncached reads for cleanup, mirroring the dashboard
	// reconciler, so disabling the component never starts an informer whose RBAC
	// was never granted. Falls back to the cached client when unset.
	APIReader client.Reader

	Scheme *runtime.Scheme
	Config Config

	// tickInterval overrides the periodic reconciliation interval (default 5m).
	// Exposed for testing only.
	tickInterval time.Duration
}

func (r *Reconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// Reconcile ensures Evidence Server resources match the desired state. When
// enabled it applies the PVC (for a file:// bucket), Deployment and internal
// Service; when disabled it deletes the Deployment and Service but PRESERVES the
// PVC so persistent evidence is retained.
func (r *Reconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithName("evidence")

	if !r.Config.Enabled {
		log.V(1).Info("Evidence Server disabled, cleaning up runtime resources (evidence PVC retained)")
		if err := r.cleanup(ctx); err != nil {
			log.Error(err, "Failed to clean up evidence resources")
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling Evidence Server resources", "image", r.Config.Image, "namespace", r.Config.Namespace)

	if err := r.ensureNamespace(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("namespace: %w", err)
	}
	if r.needsPVC() {
		if err := r.apply(ctx, pvcAC(r.Config)); err != nil {
			return ctrl.Result{}, fmt.Errorf("pvc: %w", err)
		}
	}
	if err := r.apply(ctx, deploymentAC(r.Config)); err != nil {
		return ctrl.Result{}, fmt.Errorf("deployment: %w", err)
	}
	if err := r.apply(ctx, serviceAC(r.Config)); err != nil {
		return ctrl.Result{}, fmt.Errorf("service: %w", err)
	}

	log.Info("Evidence Server resources reconciled successfully")
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// needsPVC reports whether the operator should provision a PVC: only for a
// file:// bucket with persistence enabled and no externally-managed claim.
func (r *Reconciler) needsPVC() bool {
	return isFileBucket(r.Config.BucketURL) &&
		r.Config.Persistence.Enabled &&
		r.Config.Persistence.ExistingClaim == ""
}

func (r *Reconciler) apply(ctx context.Context, ac runtime.ApplyConfiguration) error {
	return r.Apply(ctx, ac, client.FieldOwner(FieldManager), client.ForceOwnership)
}

// Start runs the initial reconciliation and, when enabled, a periodic loop. It
// implements manager.Runnable.
func (r *Reconciler) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("evidence")
	log.Info("Starting evidence reconciler", "enabled", r.Config.Enabled, "image", r.Config.Image, "namespace", r.Config.Namespace)

	if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
		return fmt.Errorf("initial evidence reconciliation failed: %w", err)
	}

	if r.Config.Enabled {
		interval := r.tickInterval
		if interval == 0 {
			interval = 5 * time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if _, err := r.Reconcile(ctx, ctrl.Request{}); err != nil {
					log.Error(err, "Periodic evidence reconciliation failed")
				}
			}
		}
	}
	return nil
}

func (r *Reconciler) ensureNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{}
	err := r.Get(ctx, client.ObjectKey{Name: r.Config.Namespace}, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: r.Config.Namespace, Labels: Labels()}}
	return r.Create(ctx, ns)
}

// cleanup deletes the managed Deployment and Service. It deliberately never
// touches the PVC: accepted evidence is retained across disablement and
// uninstall.
func (r *Reconciler) cleanup(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("evidence")

	resources := []struct {
		name string
		obj  client.Object
		key  client.ObjectKey
	}{
		{"Service", &corev1.Service{}, client.ObjectKey{Namespace: r.Config.Namespace, Name: Name}},
		{"Deployment", &appsv1.Deployment{}, client.ObjectKey{Namespace: r.Config.Namespace, Name: Name}},
	}

	for _, res := range resources {
		if err := r.reader().Get(ctx, res.key, res.obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			if apierrors.IsForbidden(err) {
				log.V(1).Info("Skipping cleanup; permission not granted (evidence likely never enabled)", "kind", res.name)
				continue
			}
			return fmt.Errorf("failed to get %s: %w", res.name, err)
		}
		labels := res.obj.GetLabels()
		if labels[LabelManagedBy] != ManagedByValue || labels[LabelComponent] != ComponentValue {
			log.V(1).Info("Skipping resource not managed by us", "kind", res.name)
			continue
		}
		if err := r.Delete(ctx, res.obj, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete %s: %w", res.name, err)
		}
		log.Info("Deleted evidence resource", "kind", res.name)
	}
	return nil
}
