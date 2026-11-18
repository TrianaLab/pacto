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
	"sigs.k8s.io/controller-runtime/pkg/client"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// accessTimeout bounds one SubjectAccessReview.
const accessTimeout = 5 * time.Second

// access asks the apiserver of the Stage's cluster whether id may update the
// Stage's workload: the default approver rule. The review names the user and
// groups admission bound to the approval, unfreeze or acknowledge, and no
// extra, which they do not record. A review that fails, one the authorizers
// could not evaluate, or a kind with no resource, is unverified, never
// allowed. err is the failed request, apart from the unverified answer, so a
// caller that concludes can retry rather than judge on it.
// ponytail: no cache, one review per approval that would count on each
// reconcile; cache by user and workload for a few seconds if reviews load the
// apiserver.
func (r *OperationReconciler) access(ctx context.Context, id delivery.Identity, stage *dv1.Stage) (delivery.WorkloadAccess, error) {
	return reviewAccess(ctx, r.Client, r.now(), id, stage)
}

// reviewAccess is access asked through c, answered as of now: the recorder
// asks it too before it creates a view the promoter would not count.
func reviewAccess(ctx context.Context, c client.Client, now time.Time, id delivery.Identity, stage *dv1.Stage) (delivery.WorkloadAccess, error) {
	a, group := delivery.AccessFor(stage.Spec, stage.Namespace)
	if a.Unverified != "" {
		return a, nil
	}
	a.Source = delivery.AccessBySubjectReview
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
		User: id.User, Groups: id.Groups,
		ResourceAttributes: &authorizationv1.ResourceAttributes{Namespace: stage.Namespace, Verb: "update", Group: group, Resource: a.Resource, Name: a.Name},
	}}
	ctx, cancel := context.WithTimeout(ctx, accessTimeout)
	defer cancel()
	if err := c.Create(ctx, sar); err != nil {
		a.Unverified = "the SubjectAccessReview failed: " + err.Error()
		return a, err
	}
	// An authorizer that errored may have allowed: a refusal that carries an
	// evaluation error is no answer.
	if !sar.Status.Allowed && sar.Status.EvaluationError != "" {
		a.Unverified = "the SubjectAccessReview could not be evaluated: " + sar.Status.EvaluationError
		return a, nil
	}
	a.Allowed, a.At = sar.Status.Allowed, ts(now)
	return a, nil
}
