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
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/component"
)

// Reconciler manages the lifecycle of Evidence Server Kubernetes resources.
type Reconciler struct {
	client.Client

	// APIReader performs uncached reads for cleanup, mirroring the dashboard
	// reconciler, so disabling the component never starts an informer whose RBAC
	// was never granted. Required.
	APIReader client.Reader

	Scheme *runtime.Scheme
	Config Config

	// Gate decides whether the server runs under Config.Auto; required then.
	Gate *Gate

	// tickInterval overrides the periodic reconciliation interval (default 5m).
	// Exposed for testing only.
	tickInterval time.Duration
}

// Sync ensures Evidence Server resources match the desired state. When enabled
// it applies the Deployment and internal Service; when disabled it deletes them.
// Nothing durable is deleted either way: accepted evidence lives in the contract
// registry, not in the cluster.
func (r *Reconciler) Sync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("evidence")

	if !r.Config.Enabled && r.Config.Auto {
		// The gate never fails a sync: a first-sync error would stop the operator.
		if err := r.syncAuto(ctx); err != nil {
			log.Error(err, "Reconciling the Evidence Server under profile idp failed")
		}
		return nil
	}
	if !r.Config.Enabled {
		log.V(1).Info("Evidence Server disabled, cleaning up runtime resources (evidence lives in the registry)")
		return r.cleanup(ctx)
	}

	return r.applyAll(ctx)
}

// syncAuto applies on a gate that is on, cleans up on a known off and does
// nothing when the gate cannot tell.
func (r *Reconciler) syncAuto(ctx context.Context) error {
	on, known, e := r.Gate.Check(ctx)
	log := logf.FromContext(ctx).WithName("evidence")
	switch {
	case !known:
		log.Info("Evidence gate cannot tell; keeping the current state", "reason", e.Reason)
		return nil
	case !on:
		log.V(1).Info("Evidence gate is off, cleaning up runtime resources", "why", e.Evidence)
		return r.cleanup(ctx)
	}
	return r.applyAll(ctx)
}

// applyAll applies the Deployment and the internal Service.
func (r *Reconciler) applyAll(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("evidence")
	log.Info("Reconciling Evidence Server resources", "image", r.Config.Image, "namespace", r.Config.Namespace)

	if err := r.apply(ctx, deploymentAC(r.Config)); err != nil {
		return fmt.Errorf("deployment: %w", err)
	}
	if err := r.apply(ctx, serviceAC(r.Config)); err != nil {
		return fmt.Errorf("service: %w", err)
	}

	log.Info("Evidence Server resources reconciled successfully")
	return nil
}

func (r *Reconciler) apply(ctx context.Context, ac runtime.ApplyConfiguration) error {
	return r.Apply(ctx, ac, client.FieldOwner(FieldManager), client.ForceOwnership)
}

// Start implements manager.Runnable.
func (r *Reconciler) Start(ctx context.Context) error {
	logf.FromContext(ctx).WithName("evidence").Info("Starting evidence reconciler",
		"enabled", r.Config.Enabled, "auto", r.Config.Auto, "image", r.Config.Image, "namespace", r.Config.Namespace)
	if r.Config.Auto && r.Gate.FirstPush != nil {
		// The first sync usually runs before the first push and cannot tell:
		// check again once the push lands instead of a tick later.
		go func() {
			select {
			case <-r.Gate.FirstPush:
				_ = r.Sync(ctx)
			case <-ctx.Done():
			}
		}()
	}
	return component.Run(ctx, "evidence", r.Config.Enabled || r.Config.Auto, r.tickInterval, r.Sync)
}

// cleanup deletes the managed Deployment and Service. There is nothing else to
// consider: the server is stateless, so removing it cannot lose evidence.
func (r *Reconciler) cleanup(ctx context.Context) error {
	ns := r.Config.Namespace
	return component.Prune(ctx, "evidence", r.APIReader, r.Client,
		component.Owned(ComponentValue),
		[]component.Resource{
			{Kind: "Service", Obj: &corev1.Service{}, Key: client.ObjectKey{Namespace: ns, Name: Name}},
			{Kind: "Deployment", Obj: &appsv1.Deployment{}, Key: client.ObjectKey{Namespace: ns, Name: Name}},
		})
}
