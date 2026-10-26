/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"reflect"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// shadow runs only while the Stage observes. It remembers what GitOps'
// source reports (status.observedRevision), plans what a fully automatic
// Stage would do with each new candidate and each version GitOps applied
// (status.shadow, advisory, never a record), and runs the Stage's verify
// gates on what GitOps applied once the verify window passed. Only an
// all-Pass verify counts: it marks the digest verified, and offered
// downstream when it is also published. A confirmed Fail marks it failed,
// which the proposal reads as an incident; Unknown writes nothing.
func (r *StageReconciler) shadow(ctx context.Context, stage *dv1.Stage) error {
	if delivery.CurrentRung(stage.Spec, stage.Status) != delivery.RungObserve {
		return nil
	}
	a, err := r.Cluster.applier(ctx, stage.Namespace, stage.Spec.ApplyRef)
	switch {
	case apierrors.IsNotFound(err) || apierrors.IsForbidden(err) || meta.IsNoMatchError(err):
		return nil // ready names it (applierReady)
	case err != nil:
		return err
	}
	d := delivery.RevisionDigest(a.revision)
	if _, err := delivery.ParseDigest(d); err != nil {
		return nil // nothing known runs: the proposal says no-observed-digest
	}
	st, now := stage.Status, r.Now().UTC()
	old := ""
	if st.ObservedRevision != nil {
		old = st.ObservedRevision.Digest
	}
	var add []delivery.Shadow
	for _, to := range []string{st.Candidate, d} {
		if to == "" || to == old {
			continue
		}
		// What GitOps applied moved from what ran before; a candidate would
		// move from what runs now.
		from := d
		if to == d {
			from = old
		}
		spec := r.shadowSpec(stage, from, to)
		if slices.ContainsFunc(append(add, st.Shadow...), func(s delivery.Shadow) bool { return planned(s, spec) }) {
			continue
		}
		e, err := r.wouldHave(ctx, stage, spec, now)
		if err != nil {
			return err
		}
		add = append(add, e)
	}
	if len(add) == 0 && d == old {
		return r.shadowVerify(ctx, stage, now)
	}
	set := func(s *dv1.Stage) {
		// One entry per candidate. Another reconcile of a stale read may have
		// planned it already; a new plan for a Stage spec or a running version
		// that changed replaces an entry that has not run yet.
		fresh := slices.DeleteFunc(slices.Clone(add), func(e delivery.Shadow) bool {
			return slices.ContainsFunc(s.Status.Shadow, func(x delivery.Shadow) bool {
				return x.Candidate == e.Candidate && (x.IntentDigest == e.IntentDigest || x.ObservedAt != "")
			})
		})
		kept := slices.DeleteFunc(slices.Clone(s.Status.Shadow), func(x delivery.Shadow) bool {
			return slices.ContainsFunc(fresh, func(e delivery.Shadow) bool { return e.Candidate == x.Candidate })
		})
		s.Status.Shadow = append(fresh, kept...)
		s.Status.Shadow = s.Status.Shadow[:min(len(s.Status.Shadow), delivery.MaxShadow)]
		if s.Status.ObservedRevision == nil || s.Status.ObservedRevision.Digest != d {
			s.Status.ObservedRevision = &delivery.ObservedRevision{Digest: d, Since: ts(now)}
		}
		for i := range s.Status.Shadow {
			if s.Status.Shadow[i].Candidate == d && s.Status.Shadow[i].ObservedAt == "" {
				s.Status.Shadow[i].ObservedAt = s.Status.ObservedRevision.Since
			}
		}
	}
	if err := r.updateStatus(ctx, stage, set); err != nil {
		return err
	}
	set(stage)
	return nil
}

// shadowSpec is the promote from to that the shadow plans, under the Stage
// spec as it is now.
func (r *StageReconciler) shadowSpec(stage *dv1.Stage, from, to string) delivery.OperationSpec {
	spec := delivery.OperationSpec{
		Type: delivery.TypePromote, Stage: stage.Name, Facet: delivery.FacetPointer,
		Params: delivery.Params{From: from, To: to},
		Reason: "shadow plan while the Stage observes",
		Actor:  delivery.Actor{User: r.Policy.ControllerUser},
	}
	spec.IntentDigest = delivery.IntentDigest(spec, stage.Spec)
	return spec
}

// planned says s already holds the plan for spec's candidate: planned for
// the same intent, or the version already ran here, so what it would have
// done then stays as it was.
func planned(s delivery.Shadow, spec delivery.OperationSpec) bool {
	return s.Candidate == spec.Params.To && (s.IntentDigest == spec.IntentDigest || s.ObservedAt != "")
}

// wouldHave plans the promote from to on the Stage as a fully automatic
// Stage running from would: the rung autonomous gives that verdict, and the
// pointer set to from keeps it from reading as a pointer that moved.
func (r *StageReconciler) wouldHave(ctx context.Context, stage *dv1.Stage, spec delivery.OperationSpec, now time.Time) (delivery.Shadow, error) {
	from, to := spec.Params.From, spec.Params.To
	synth := stageObject(stage)
	synth.Status.Rung, synth.Status.Pointer = delivery.RungAutonomous, from
	in, err := delivery.AssemblePlanInput(ctx, planSources{r.Cluster}, spec, synth, delivery.ClassController, now)
	if err != nil {
		return delivery.Shadow{}, err
	}
	in.RequireOwnerTeam = r.Policy.RequireOwnerTeam
	p := delivery.PlanFor(ctx, in)
	e := delivery.Shadow{Candidate: to, From: from, Predicted: p.Predicted, IntentDigest: p.IntentDigest,
		ImpactDigest: p.ImpactDigest, PlannedAt: ts(now), Question: p.Question}
	if p.Floor.Human {
		e.Decision, e.Owners = p.Floor.Decision, p.Floor.Owners[:min(len(p.Floor.Owners), 16)]
	}
	switch {
	case p.Predicted == delivery.PredictRejected:
		e.Question = planRefusal(p)
	case len(p.Conflicts) > 0:
		e.Question = p.Conflicts[0]
	}
	return e, nil
}

// shadowVerify runs the Stage's verify gates on what GitOps applied once
// the verify window passed since Pacto first saw it, at most once per
// recheck interval while it stays Unknown.
// ponytail: the last attempt is kept in memory, so a restart tries once more.
func (r *StageReconciler) shadowVerify(ctx context.Context, stage *dv1.Stage, now time.Time) error {
	st, spec := stage.Status, stage.Spec
	d := st.ObservedRevision.Digest
	since := parseOr(st.ObservedRevision.Since)
	key := stage.Namespace + "/" + stage.Name + "@" + d
	r.mu.Lock()
	last, tried := r.shadowTried[key]
	r.mu.Unlock()
	if slices.Contains(st.Verified, d) || slices.Contains(st.FailedDigests, d) || now.Before(since.Add(delivery.VerifyWindow(spec))) ||
		tried && now.Before(last.Add(delivery.RecheckInterval(spec))) {
		return nil
	}
	r.mu.Lock()
	if r.shadowTried == nil {
		r.shadowTried = map[string]time.Time{}
	}
	r.shadowTried[key] = now
	r.mu.Unlock()
	vs := delivery.RunChecks(ctx, r.Gates, verifyChecks(stage), subjectInput("", delivery.FacetPointer, d, stage, since, now))
	countVerdicts(vs)
	worst := delivery.Worst(vs)
	if worst == delivery.Unknown {
		return nil
	}
	set := func(s *dv1.Stage) {
		if s.Status.ObservedRevision == nil || s.Status.ObservedRevision.Digest != d {
			return // GitOps moved on while the gates ran
		}
		if worst == delivery.Fail {
			s.Status.FailedDigests = delivery.AddDigest(s.Status.FailedDigests, d, delivery.MaxFailedDigests)
			return
		}
		s.Status.Verified = delivery.AddDigest(s.Status.Verified, d, delivery.MaxVerified)
		if slices.Contains(s.Status.Published, d) {
			s.Status.Offered = delivery.AddDigest(s.Status.Offered, d, delivery.MaxOffered)
		}
		for i := range s.Status.Shadow {
			if s.Status.Shadow[i].Candidate == d {
				s.Status.Shadow[i].Verification = delivery.Verified
			}
		}
	}
	if err := r.updateStatus(ctx, stage, set); err != nil {
		return err
	}
	set(stage)
	return nil
}

// propose writes the rung Pacto proposes next, or why not, when it changed.
// The plan of the operation that last moved the pointer is the newest the
// graph was read for.
func (r *StageReconciler) propose(ctx context.Context, stage *dv1.Stage) error {
	var latest *delivery.Plan
	if uid := stage.Status.LastOp; uid != "" && delivery.CurrentRung(stage.Spec, stage.Status) == delivery.RungAutoNonBreaking {
		ops, err := stageOps(ctx, r.Client, stage.Namespace, stage.Name)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(ops, func(o dv1.Operation) bool { return string(o.UID) == uid }); i >= 0 {
			latest = ops[i].Status.Plan
		}
	}
	if reflect.DeepEqual(delivery.ProposeRung(stage.Spec, stage.Status, latest), stage.Status.Proposal) {
		return nil
	}
	lastOp := stage.Status.LastOp
	// The proposal is computed again from the fresh status, so a stale read
	// never writes a proposal made for an earlier rung. latest belongs to
	// lastOp: when the fresh status moved the pointer since, the next
	// reconcile proposes from that operation's plan.
	return r.updateStatus(ctx, stage, func(s *dv1.Stage) {
		if s.Status.LastOp == lastOp {
			s.Status.Proposal = delivery.ProposeRung(s.Spec, s.Status, latest)
		}
	})
}

// raiseStepDown lowers the Stage one rung after a promote that shipped with
// no person on an automatic rung (the promoter or an agent raised it and
// nobody approved it) failed on a confirmed Fail: an autonomy
// operation raised by the promoter, which the recorder signs when it is
// writing, needing no approval, never below observe. Concluding it
// restarts the evidence count (delivery.ProposeRung). Its name is derived
// from the failed operation, so raising it twice is a no-op; a second failure
// steps down again from wherever the first left the Stage, down to advise,
// where the promoter raises no promote. stage must be a fresh read: a stale
// rung would raise a step-down the swap refuses. A non-empty squat says
// someone else took that name.
func raiseStepDown(ctx context.Context, c client.Client, policy delivery.ClassPolicy, failed *dv1.Operation, stage *dv1.Stage, why string, cause *delivery.Verdict) (squat string, _ error) {
	if failed.Spec.Type != delivery.TypePromote || failed.Status.ActorClass == delivery.ClassHuman || len(failed.Status.Approvals) > 0 ||
		cause == nil || cause.Result != delivery.Fail {
		return "", nil
	}
	from := delivery.CurrentRung(stage.Spec, stage.Status)
	to, _ := from.Below()
	if !from.Automatic() {
		return "", nil // a person already lowered it: the step-down happened
	}
	op := &dv1.Operation{}
	op.Namespace, op.Name = failed.Namespace, failed.Name+"-stepdown"
	op.Labels = map[string]string{StageLabel: stage.Name}
	controllerutil.AddFinalizer(op, dv1.RecordFinalizer)
	op.Spec = delivery.OperationSpec{
		Type: delivery.TypeAutonomy, Stage: stage.Name, Facet: delivery.FacetRung,
		Params: delivery.Params{From: string(from), To: string(to)},
		Reason: "automatic step-down: " + failed.Name + ", which shipped with no person on " + string(from) + ", failed (" + why + ")",
		Actor:  delivery.Actor{User: policy.ControllerUser},
		Cause:  &delivery.Cause{Operation: string(failed.UID), Verdict: cause},
	}
	op.Spec.IntentDigest = delivery.IntentDigest(op.Spec, stage.Spec)
	return createRaised(ctx, c, op)
}
