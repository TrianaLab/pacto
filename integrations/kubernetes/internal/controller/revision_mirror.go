/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	corev1 "k8s.io/api/core/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/v5/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/v5/internal/component"
)

// RevisionMirror keeps a PactoRevision for every registry tag of every Pacto whose
// contract ref is tag-shaped, and raises TagOverwritten when a tag's digest moves.
//
// It is a periodic manager.Runnable, not a step of Reconcile. Reconcile is
// re-entered on every write to every watched Service, Deployment, StatefulSet,
// ReplicaSet, Job and CronJob, and one mirror pass costs a ListTags plus a Load per
// tag, so running it inline made a single rolling update an unbounded registry
// fan-out. Nothing in the reconcile result ever depended on it: the mirror writes
// PactoRevision objects and never touches the Pacto status.
type RevisionMirror struct {
	// Reconciler supplies the client, loader, scheme and recorder. The mirror is a
	// different SCHEDULE for work the reconciler already owns, not a second owner
	// of it, so it borrows rather than re-declares them.
	Reconciler *PactoReconciler

	// Interval overrides the mirror period. Zero means component.DefaultInterval.
	Interval time.Duration
}

// Start implements manager.Runnable.
func (m *RevisionMirror) Start(ctx context.Context) error {
	return component.Run(ctx, "revision-mirror", true, m.Interval, m.Sync)
}

// Sync mirrors every Pacto once. Only a failure to enumerate the Pactos is returned:
// component.Run stops the manager on a failed FIRST sync, and a registry that is down
// at startup must not take the operator with it. Per-Pacto failures become events.
func (m *RevisionMirror) Sync(ctx context.Context) error {
	list := &pactov1alpha1.PactoList{}
	if err := m.Reconciler.List(ctx, list); err != nil {
		return fmt.Errorf("failed to list pactos: %w", err)
	}
	for i := range list.Items {
		m.mirror(ctx, &list.Items[i])
	}
	return nil
}

// mirror runs one Pacto's tag sync. A digest-pinned or inline ref has no tag set to
// walk, so it is skipped before any registry call.
func (m *RevisionMirror) mirror(ctx context.Context, pacto *pactov1alpha1.Pacto) {
	ociRef := pacto.Spec.ContractRef.OCI
	if ociRef == "" || strings.Contains(ociRef, "@") {
		return
	}

	r := m.Reconciler
	var ociAuth *authn.AuthConfig
	if secretName := pacto.Spec.ContractRef.PullSecretRef; secretName != "" {
		auth, err := r.resolveOCIAuth(ctx, pacto.Namespace, secretName, ociRef)
		if err != nil {
			r.recordMirrorFailure(pacto, fmt.Errorf("pull secret %q: %w", secretName, err))
			return
		}
		ociAuth = auth
	}

	if err := r.syncAllRevisions(ctx, pacto, ociRef, ociAuth); err != nil {
		r.recordMirrorFailure(pacto, err)
	}
}

// recordMirrorFailure gives a mirror failure the vocabulary the main load path uses
// -- classifyLoadError picks ContractUnavailable for a transient obtain-failure and
// ContractInvalid for everything else -- instead of the V(1) log line that hid every
// one of them. One event per Pacto per pass; the API server collapses repeats of the
// same reason and message into a single event with a count, so a tag that stays
// broken does not accumulate objects.
func (r *PactoReconciler) recordMirrorFailure(pacto *pactov1alpha1.Pacto, err error) {
	reason := pactov1alpha1.EventContractInvalid
	if classifyLoadError(err) == pactov1alpha1.ContractStatusUnknown {
		reason = pactov1alpha1.EventContractUnavailable
	}
	r.Recorder.Eventf(pacto, corev1.EventTypeWarning, reason,
		"Revision mirror failed for %s: %v", pacto.Spec.ContractRef.OCI, err)
}
