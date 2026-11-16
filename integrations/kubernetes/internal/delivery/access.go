/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// accessTimeout bounds one SubjectAccessReview.
const accessTimeout = 5 * time.Second

// access asks the apiserver of the Stage's cluster whether id may update the
// Stage's workload: the default approver rule. The review names the user and
// groups admission bound to the approval, unfreeze or acknowledge, and no
// extra, which they do not record. A review that fails, or a kind with no
// resource, is unverified, never allowed.
// ponytail: no cache, one review per approval considered on each reconcile;
// cache by user and workload for a few seconds if reviews load the apiserver.
func (r *OperationReconciler) access(ctx context.Context, id delivery.Identity, stage *dv1.Stage) delivery.WorkloadAccess {
	a, group := delivery.AccessFor(stage.Spec, stage.Namespace)
	if a.Unverified != "" {
		return a
	}
	a.Source = delivery.AccessBySubjectReview
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
		User: id.User, Groups: id.Groups,
		ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: stage.Namespace, Verb: "update", Group: group, Resource: a.Resource, Name: a.Name},
	}}
	ctx, cancel := context.WithTimeout(ctx, accessTimeout)
	defer cancel()
	if err := r.Create(ctx, sar); err != nil {
		a.Unverified = "the SubjectAccessReview failed: " + err.Error()
		return a
	}
	a.Allowed, a.At = sar.Status.Allowed, ts(r.now())
	return a
}
