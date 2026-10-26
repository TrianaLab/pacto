package delivery

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
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

// Below is the rung one step down, and false on observe: an automatic
// step-down never goes below observe.
func (r Rung) Below() (Rung, bool) {
	if r.level() < 1 {
		return "", false
	}
	return rungOrder[r.level()-1], true
}

// Caps and defaults of the ladder's status.
const (
	// DefaultProposeAfter is how many qualifying promotions earn a proposal
	// when the Stage does not say.
	DefaultProposeAfter = 10
	// MaxShadow is how many would-have decisions a Stage keeps.
	MaxShadow = 20
	// MaxBlockers is how many blockers a proposal lists.
	MaxBlockers = 32
)

// Blocker kinds a proposal lists.
const (
	BlockerUnlabelledApplyRef   = "unlabelled-apply-ref"
	BlockerNoObservedDigest     = "no-observed-digest"
	BlockerNoRecordStore        = "no-record-store"
	BlockerNeedsVerified        = "needs-verified"
	BlockerNeedsClearedBreaking = "needs-cleared-breaking"
	BlockerIncident             = "incident"
	BlockerPartialGraph         = "partial-graph"
	BlockerUndeclaredRange      = "undeclared-range"
)

// ProposeRung is the next rung Pacto proposes for a Stage, or the blockers
// that keep it from proposing one. latest is the plan of the newest promote,
// nil when there is none. Evidence is only what the Stage earned on its
// current rung: a rung change (a raise or an automatic step-down) restarts the
// count. Observe earns advise from shadow verifies of what GitOps applied;
// the automatic rungs are earned from the Stage's own verified promotes, and
// autonomous also needs a breaking change the graph cleared and a complete
// graph.
// ponytail: status windows capped at 20 (Concluded, Shadow); fold signed
// records instead when someone needs more than 20.
func ProposeRung(spec StageSpec, st StageStatus, latest *Plan) *Proposal {
	cur := CurrentRung(spec, st)
	p := &Proposal{}
	if cur == RungAutonomous {
		return p
	}
	n := cmp.Or(spec.Autonomy.ProposeAfter, DefaultProposeAfter)
	block := func(kind, subject, msg string) {
		if len(p.Blockers) < MaxBlockers {
			p.Blockers = append(p.Blockers, Blocker{Kind: kind, Subject: subject, Message: msg})
		}
	}
	next := rungOrder[cur.level()+1]
	var evidence []string
	if cur == RungObserve {
		evidence = shadowEvidence(st, n, block)
		if !conditionIs(st, "Ready", ReasonObservingGuarded) {
			block(BlockerUnlabelledApplyRef, spec.ApplyRef.Name, "label the applyRef "+ApplyRefLabel+" and remove the pointer field from the manifest in git, so admission protects the pointer before Pacto owns it")
		}
		if st.ObservedRevision == nil {
			block(BlockerNoObservedDigest, spec.ApplyRef.Name, "GitOps' source reports no digest yet, so Pacto does not know what runs")
		}
	} else {
		var cleared bool
		evidence, cleared = promoteEvidence(st, n, block)
		if st.Frozen {
			block(BlockerIncident, spec.Service.Name, "the Stage is frozen: "+st.FrozenReason)
		}
		if !conditionIs(st, ConditionRecordStoreReady) {
			block(BlockerNoRecordStore, spec.Service.Name, "the recorder is not writing this Stage's records, and every grant of autonomy must be a signed record")
		}
		if next == RungAutonomous {
			graphReady(latest, cleared, spec.Service.Name, block)
		}
	}
	if len(p.Blockers) == 0 {
		p.Rung, p.Evidence = next, evidence
	}
	return p
}

// shadowEvidence is the candidates of the newest n shadow entries GitOps'
// source reported, when every one verified and none failed.
func shadowEvidence(st StageStatus, n int, block func(kind, subject, msg string)) []string {
	var ev []string
	for _, s := range st.Shadow {
		if s.ObservedAt == "" {
			continue
		}
		switch {
		case slices.Contains(st.FailedDigests, s.Candidate):
			block(BlockerIncident, s.Candidate, short(s.Candidate)+" failed its verify on this Stage")
		case s.Verification != Verified:
			block(BlockerNeedsVerified, s.Candidate, short(s.Candidate)+" ran here but has not verified yet")
		}
		if ev = append(ev, s.Candidate); len(ev) == n {
			return ev
		}
	}
	block(BlockerNeedsVerified, "", fmt.Sprintf("%d of %d versions GitOps applied were observed here; Pacto proposes advise after %d verified", len(ev), n, n))
	return ev
}

// promoteEvidence is the uids of the newest n promotes concluded since the
// last rung change, when every one succeeded and verified and no automatic
// rollback, freeze or failure came after that change. cleared says one of
// them was a breaking change the graph cleared.
func promoteEvidence(st StageStatus, n int, block func(kind, subject, msg string)) (ev []string, cleared bool) {
scan:
	for _, c := range slices.Backward(st.Stream.Concluded) {
		switch {
		case c.Type == TypeAutonomy && c.Outcome == Succeeded:
			break scan
		case c.Outcome == Failed, !c.Person && (c.Type == TypeRollback || c.Type == TypeFreeze):
			block(BlockerIncident, c.UID, "a "+c.Type+" ended "+string(c.Outcome)+": "+c.Reason)
		case c.Type != TypePromote || len(ev) == n:
		case c.Outcome != Succeeded || c.Verification != Verified:
			block(BlockerNeedsVerified, c.UID, "a promote ended "+string(c.Outcome)+" and "+cmp.Or(string(c.Verification), "unverified"))
		default:
			ev, cleared = append(ev, c.UID), cleared || c.ClearedBreaking
		}
	}
	if len(ev) < n {
		block(BlockerNeedsVerified, "", fmt.Sprintf("%d of %d promotes verified since the rung last changed", len(ev), n))
	}
	return ev, cleared
}

// graphReady blocks autonomous until a breaking change the graph cleared
// verified on this rung and the newest promote saw a complete graph that
// left nothing unknown.
func graphReady(latest *Plan, cleared bool, service string, block func(kind, subject, msg string)) {
	if !cleared {
		block(BlockerNeedsClearedBreaking, service, "no breaking change that every consumer accepts has verified here yet, and that is the one thing autonomous adds")
	}
	var v *Verdict
	if latest != nil {
		if i := slices.IndexFunc(latest.Verdicts, func(v Verdict) bool { return v.Gate == GateImpact }); i >= 0 {
			v = &latest.Verdicts[i]
		}
	}
	switch {
	case v == nil:
		block(BlockerPartialGraph, service, "no promote has projected a change onto the graph yet")
	case v.Citation["completeness"] != string(fleet.CompletenessComplete):
		block(BlockerPartialGraph, service, "the newest promote saw a "+cmp.Or(v.Citation["completeness"], "unknown")+" graph: "+v.Message)
	case v.Result != Pass:
		// ponytail: missing-contract waits for the observed uncontracted callers.
		block(BlockerUndeclaredRange, service, v.Message)
	}
}
