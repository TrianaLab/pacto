package delivery

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/diff"
)

// Rung is how much a Stage lets Pacto do without a person.
type Rung string

// The rungs, lowest first.
const (
	// RungObserve plans every change and moves nothing: GitOps owns the applyRef.
	RungObserve Rung = "observe"
	// RungAdvise ships only what a person proposed or approved.
	RungAdvise Rung = "advise"
	// RungAutoNonBreaking auto-promotes NON_BREAKING changes only.
	RungAutoNonBreaking Rung = "auto-nonbreaking"
	// RungAutonomous auto-promotes any plan that proceeds.
	RungAutonomous Rung = "autonomous"
)

var rungOrder = []Rung{RungObserve, RungAdvise, RungAutoNonBreaking, RungAutonomous}

// level is r's place on the ladder; -1 for a name that is not a rung.
func (r Rung) level() int { return slices.Index(rungOrder, r) }

// Automatic reports whether Pacto promotes on r without anyone asking.
func (r Rung) Automatic() bool { return r.level() >= RungAutoNonBreaking.level() }

// CurrentRung is the Stage's rung: status.rung, or before the stage
// reconciler seeds it, spec.startRung, or observe. An unseeded Stage never
// reads as an automatic one.
func CurrentRung(spec StageSpec, st StageStatus) Rung {
	switch {
	case st.Rung != "":
		return st.Rung
	case spec.StartRung == RungAdvise:
		return RungAdvise
	}
	return RungObserve
}

// ApplyRefLabel marks the object a Stage applies through; admission then
// lets only the promoter and break-glass write its pointer field.
const ApplyRefLabel = "delivery.pacto.trianalab.io/apply-ref"

// Ready reasons on a Stage that observes. Only ObservingGuarded, the applyRef
// labelled so admission protects the pointer, lets the Stage leave observe.
const (
	ReasonObserving        = "Observing"
	ReasonObservingGuarded = "ObservingGuarded"
)

// conditionIs reports whether the Stage's condition typ is status True with
// one of reasons (any reason when none is given).
func conditionIs(st StageStatus, typ string, reasons ...string) bool {
	i := slices.IndexFunc(st.Conditions, func(c Condition) bool { return c.Type == typ })
	return i >= 0 && st.Conditions[i].Status == "True" && (len(reasons) == 0 || slices.Contains(reasons, st.Conditions[i].Reason))
}

// rungPromote applies the Stage's rung to a promote that no person raised:
// on advise a person approves it; on auto-nonbreaking a person approves any
// change that is not NON_BREAKING. A floor already set stays. Observe
// rejects every promote before the gates run (PlanFor).
func rungPromote(in PlanInput, p *Plan) {
	if in.Class == ClassHuman || p.Floor.Human {
		return
	}
	switch CurrentRung(in.Stage, in.Status) {
	case RungAdvise:
		p.Floor = Floor{Human: true, Decision: Approve}
		p.Question = "This Stage advises: a person approves every promote a person did not raise. Approve " + short(p.To) + "?"
	case RungAutoNonBreaking:
		if c := classification(p); c != diff.NonBreaking.String() {
			p.Floor = Floor{Human: true, Decision: Approve}
			p.Question = "This Stage auto-promotes only non-breaking changes, and " + short(p.To) + " is " + cmp.Or(c, "not classified") + ". Approve it?"
		}
	}
}

// classification is the impact verdict's classification, "" without one.
func classification(p *Plan) string {
	i := slices.IndexFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GateImpact })
	if i < 0 {
		return ""
	}
	return p.Verdicts[i].Citation["classification"]
}

// ClearedBreaking reports whether p is a promote of a change that is not
// NON_BREAKING whose impact verdict passed: the case autonomous adds over
// auto-nonbreaking.
func ClearedBreaking(p *Plan) bool {
	if p == nil || p.Type != TypePromote {
		return false
	}
	i := slices.IndexFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GateImpact })
	return i >= 0 && p.Verdicts[i].Result == Pass && classification(p) != diff.NonBreaking.String()
}

// autonomyGate judges a change of rung: one rung at a time, lowering freely
// (an agent never to observe), raising only by a person's approval from the
// running service's team, never by the promoter. Leaving observe needs the
// applyRef labelled and an observed digest to adopt as the pointer; an
// automatic rung needs the recorder, so every grant of autonomy is a signed
// record. A raise above what Pacto proposes is Unknown.
func autonomyGate(in PlanInput, p *Plan) {
	from, to := Rung(in.Spec.Params.From), Rung(in.Spec.Params.To)
	v := Verdict{Gate: GateAutonomy, Phase: "pre", Result: Fail, Citation: map[string]string{"from": string(from), "to": string(to)}}
	defer func() { p.Verdicts = append(p.Verdicts, v) }()
	stage := in.Spec.Stage
	switch {
	case to.level() < 0:
		v.Message = fmt.Sprintf("%q is not a rung: use observe, advise, auto-nonbreaking or autonomous", to)
		return
	case to == from:
		v.Message = stage + " is already on " + string(to)
		return
	case to.level() < from.level() && in.Class == ClassAgent && to == RungObserve:
		v.Message = "an agent may not lower a Stage to observe: like a freeze, it stops a Stage moving"
		return
	case to.level() < from.level():
		v.Result, v.Message = Pass, "lowering "+stage+" from "+string(from)+" to "+string(to)+" needs no approval"
		return
	case in.Class == ClassController:
		v.Message = "the promoter never raises a rung: a person confirms every raise"
		return
	case to.level() > from.level()+1:
		v.Message = "one rung at a time: propose " + string(rungOrder[from.level()+1]) + " first"
		return
	case from == RungObserve && !conditionIs(in.Status, "Ready", ReasonObservingGuarded):
		v.Message = "unlabelled-apply-ref: label the applyRef " + ApplyRefLabel + " and remove the pointer field from the manifest in git, so admission protects the pointer before Pacto owns it"
		return
	case from == RungObserve && (in.Status.ObservedRevision == nil || !isDigest(in.Status.ObservedRevision.Digest)):
		v.Message = "no-observed-digest: GitOps' source reports no digest yet, so Pacto does not know what runs"
		return
	case to.Automatic() && !conditionIs(in.Status, ConditionRecordStoreReady):
		v.Message = "no-record-store: the recorder is not writing this Stage's records, and every grant of autonomy must be a signed record"
		return
	}
	p.Floor = Floor{Human: true, Decision: Approve, Owners: serviceTeam(in)}
	p.Question = "Raise " + stage + " from " + string(from) + " to " + string(to) + "?"
	prop := in.Status.Proposal
	switch {
	case in.From.Bundle == nil || in.From.Bundle.Contract == nil:
		v.Result = Unknown
		v.Message = "the contract running on " + stage + " (" + short(in.From.Digest) + ") cannot be read, so Pacto cannot tell whose team decides: " + cmp.Or(in.From.Err, "nothing is known to run")
	case prop != nil && to.level() <= prop.Rung.level():
		v.Result, v.Message = Pass, "Pacto proposes "+string(prop.Rung)+": "+strings.Join(prop.Evidence, ", ")
		return
	default:
		v.Result = Unknown
		v.Message = "Pacto has not proposed " + string(to) + " for " + stage + blockerText(prop)
	}
	p.Floor.Decision = AcceptUnknown
	p.Question = v.Message + ". Accept the unknown and raise it anyway?"
}

// blockerText lists a proposal's blockers for a message.
func blockerText(prop *Proposal) string {
	if prop == nil || len(prop.Blockers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(prop.Blockers))
	for _, b := range prop.Blockers {
		parts = append(parts, b.Kind+": "+b.Message)
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

func isDigest(d string) bool {
	_, err := ParseDigest(d)
	return err == nil
}
