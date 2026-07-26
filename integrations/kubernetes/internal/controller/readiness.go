/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package controller

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pactov1alpha1 "github.com/trianalab/pacto/integrations/kubernetes/api/v1alpha1"
	"github.com/trianalab/pacto/integrations/kubernetes/internal/metrics"
	"github.com/trianalab/pacto/v3/pkg/contract"
	"github.com/trianalab/pacto/v3/pkg/readiness"
)

// readinessClock is the clock used to derive readiness freshness. It is a
// variable so tests can pin "now" for deterministic readiness status.
var readinessClock = time.Now

// reconcileReadiness derives the readiness assessment from the contract and
// records it on the status: status.readiness, the aggregate ReadinessSatisfied
// condition (gate: score >= minScore), and transition-based events. It is a
// separate dimension from contract compliance and never mutates ContractStatus.
//
// wasUnmet reflects the prior ReadinessSatisfied condition (captured before the
// status was reset) so the warning/recovery events fire only on a gate transition
// rather than on every reconciliation.
func (r *PactoReconciler) reconcileReadiness(pacto *pactov1alpha1.Pacto, c *contract.Contract, wasUnmet bool) {
	eval := readiness.Evaluate(c.Readiness, readinessClock())
	if eval == nil {
		// No readiness declared: omit the status and the condition entirely.
		return
	}

	pacto.Status.Readiness = buildReadinessStatus(eval, c.Readiness)

	status, reason, msg := readinessCondition(eval)
	r.setCondition(pacto, pactov1alpha1.ConditionReadinessSatisfied, status, reason, msg)
	metrics.RecordReadiness(pacto.Namespace, pacto.Name, pacto.Status.Readiness, reason)

	switch {
	case status == metav1.ConditionFalse && !wasUnmet:
		r.Recorder.Event(pacto, corev1.EventTypeWarning, pactov1alpha1.EventReadinessGateUnmet, msg)
	case status == metav1.ConditionTrue && wasUnmet:
		r.Recorder.Event(pacto, corev1.EventTypeNormal, pactov1alpha1.EventReadinessRecovered, msg)
	}
}

// readinessCondition maps an evaluation to the aggregate ReadinessSatisfied
// condition. The gate is met when the assessment is current and score >= minScore;
// an expired assessment is surfaced distinctly from a plain below-threshold score.
func readinessCondition(eval *readiness.Result) (metav1.ConditionStatus, string, string) {
	if eval.Passing {
		return metav1.ConditionTrue, pactov1alpha1.ReasonReadinessSatisfied,
			fmt.Sprintf("readiness score %d meets minScore %d", eval.Score, eval.MinScore)
	}
	if eval.Expired {
		return metav1.ConditionFalse, pactov1alpha1.ReasonReadinessExpired,
			fmt.Sprintf("readiness assessment expired (expires %s); score %d below minScore %d", eval.Expires, eval.Score, eval.MinScore)
	}
	return metav1.ConditionFalse, pactov1alpha1.ReasonReadinessBelowMinScore,
		fmt.Sprintf("readiness score %d below minScore %d (%d done, %d partial, %d not-done, %d deferred)",
			eval.Score, eval.MinScore, eval.DoneCount, eval.PartialCount, eval.NotDoneCount, eval.DeferredCount)
}

// buildReadinessStatus maps the pure evaluation result to the CRD status type.
// The revision history lives on the declared contract (decl), not the evaluation.
func buildReadinessStatus(eval *readiness.Result, decl *contract.Readiness) *pactov1alpha1.ReadinessStatus {
	rs := &pactov1alpha1.ReadinessStatus{
		Score:         int32(eval.Score),
		MinScore:      int32(eval.MinScore),
		Passing:       eval.Passing,
		TotalWeight:   int32(eval.TotalWeight),
		EarnedWeight:  int32(eval.EarnedWeight),
		Expires:       eval.Expires,
		Expired:       eval.Expired,
		DoneCount:     int32(eval.DoneCount),
		PartialCount:  int32(eval.PartialCount),
		NotDoneCount:  int32(eval.NotDoneCount),
		DeferredCount: int32(eval.DeferredCount),
		Claims:        make([]pactov1alpha1.ClaimStatus, 0, len(eval.Checks)),
	}
	if eval.DaysRemaining != nil {
		d := int32(*eval.DaysRemaining)
		rs.DaysRemaining = &d
	}
	for _, ch := range eval.Checks {
		rs.Claims = append(rs.Claims, pactov1alpha1.ClaimStatus{
			ID:           ch.ID,
			Type:         ch.Type,
			Category:     ch.Category,
			Status:       ch.Status,
			Evidence:     ch.Evidence,
			Description:  ch.Description,
			Weight:       int32(ch.Weight),
			EarnedWeight: int32(ch.EarnedWeight),
			Excluded:     ch.Excluded,
		})
	}
	if decl != nil {
		for _, rev := range decl.History {
			rs.Revisions = append(rs.Revisions, pactov1alpha1.ReadinessRevisionStatus{
				Date:        rev.Date,
				Version:     rev.Version,
				Author:      rev.Author,
				Description: rev.Description,
			})
		}
	}
	return rs
}
