/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// The promoter's metrics, served on its metrics port next to
// RecordsPending. docs/delivery-setup.md lists them.
var (
	// Operations counts concluded operations. Its actor_class and
	// verification labels say how much shipped with no person and how often
	// a person accepted an unknown.
	Operations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pacto_delivery_operations_total",
		Help: "Concluded operations by type, outcome, verification and the class of who asked.",
	}, []string{"type", "outcome", "verification", "actor_class"})
	// Verdicts counts the gate verdicts of concluded operations and re-checks.
	Verdicts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pacto_delivery_verdicts_total",
		Help: "Gate verdicts of concluded operations and of re-checks after release, by gate, phase and result.",
	}, []string{"gate", "phase", "result"})
	// StageFrozen is 1 while a Stage is frozen.
	StageFrozen = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pacto_delivery_stage_frozen",
		Help: "1 while a Stage is frozen and nothing promotes it automatically.",
	}, []string{"namespace", "stage"})
	// StageWatched is 0 while the newest re-check of the running version did
	// not pass: nobody is watching it for Pacto.
	StageWatched = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pacto_delivery_stage_watched",
		Help: "1 while the newest re-check of the version a Stage runs passed, 0 while it was Unknown or failed.",
	}, []string{"namespace", "stage"})
)

func init() { metrics.Registry.MustRegister(Operations, Verdicts, StageFrozen, StageWatched) }

// countConcluded counts op's conclusion and its verdicts once its status is written.
func countConcluded(op *dv1.Operation) {
	s := op.Status
	Operations.WithLabelValues(op.Spec.Type, string(s.Outcome), string(s.Verification), string(s.ActorClass)).Inc()
	countVerdicts(s.Verdicts)
}

func countVerdicts(vs []delivery.Verdict) {
	for _, v := range vs {
		Verdicts.WithLabelValues(v.Gate, v.Phase, string(v.Result)).Inc()
	}
}

// event emits a Kubernetes Event on obj when the reconciler has a recorder.
func event(rec record.EventRecorder, obj runtime.Object, warning bool, reason, msg string) {
	if rec == nil {
		return
	}
	typ := corev1.EventTypeNormal
	if warning {
		typ = corev1.EventTypeWarning
	}
	rec.Event(obj, typ, reason, msg)
}
