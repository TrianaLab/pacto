/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package component holds the lifecycle shared by the operator's optional
// sub-components (the dashboard and the Evidence Server).
//
// Each is registered with mgr.Add as a plain manager.Runnable, not as a
// controller: nothing ever calls them through reconcile.Reconciler and nothing
// reads a ctrl.Result back, so a sync either succeeds or returns an error the
// loop below logs. Both had written that loop, the ownership-checked delete and
// the namespace ensure for themselves, in copies already drifted in their log
// lines.
package component

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// DefaultInterval is the periodic re-sync period when the caller sets none.
const DefaultInterval = 5 * time.Minute

// Run is the manager.Runnable body: sync once, then re-sync on a ticker while
// the component is enabled. A failed FIRST sync stops the manager, because a
// misconfigured component must not come up looking healthy; every later failure
// is logged and retried on the next tick. A disabled component syncs exactly
// once (that sync is its cleanup) and then has nothing to poll.
func Run(ctx context.Context, name string, enabled bool, interval time.Duration, sync func(context.Context) error) error {
	log := logf.FromContext(ctx).WithName(name)
	if err := sync(ctx); err != nil {
		return fmt.Errorf("initial %s reconciliation failed: %w", name, err)
	}
	if !enabled {
		return nil
	}
	if interval == 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := sync(ctx); err != nil {
				log.Error(err, "Periodic reconciliation failed")
			}
		}
	}
}

// Resource is one object Prune considers, with a Kind used only for messages.
type Resource struct {
	Kind string
	Obj  client.Object
	Key  client.ObjectKey
}

// Prune deletes the listed resources that carry every label in owned, in the
// order given (callers list them in reverse order of creation).
//
// reader must be UNCACHED: a component that was never enabled was never granted
// list/watch on these kinds, so a cached read would start an informer that can
// never sync and would crashloop the manager. A Forbidden result is skipped for
// the same reason — no RBAC means nothing was ever created here to delete.
func Prune(ctx context.Context, name string, reader client.Reader, deleter client.Writer, owned map[string]string, resources []Resource) error {
	log := logf.FromContext(ctx).WithName(name)
	for _, res := range resources {
		if err := reader.Get(ctx, res.Key, res.Obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			if apierrors.IsForbidden(err) {
				log.V(1).Info("Skipping cleanup; permission not granted (component likely never enabled)",
					"kind", res.Kind, "name", res.Key.Name)
				continue
			}
			return fmt.Errorf("failed to get %s: %w", res.Kind, err)
		}
		if labels := res.Obj.GetLabels(); !ownedBy(labels, owned) {
			log.V(1).Info("Skipping resource not managed by us", "kind", res.Kind, "name", res.Key.Name)
			continue
		}
		if err := deleter.Delete(ctx, res.Obj, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil &&
			!apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete %s: %w", res.Kind, err)
		}
		log.Info("Deleted resource", "kind", res.Kind, "name", res.Key.Name)
	}
	return nil
}

func ownedBy(labels, owned map[string]string) bool {
	for k, v := range owned {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// EnsureNamespace creates the namespace when it is absent.
func EnsureNamespace(ctx context.Context, c client.Client, name string, labels map[string]string) error {
	err := c.Get(ctx, client.ObjectKey{Name: name}, &corev1.Namespace{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	return c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}})
}
