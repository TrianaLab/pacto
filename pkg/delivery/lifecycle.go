package delivery

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Login freshness. The promoter ignores an approval given more than
// AuthFreshness (or its --auth-freshness) after its author's login; the
// dashboard asks for a new login once one is older than DecisionFreshness,
// leaving a margin under it.
const (
	AuthFreshness     = 15 * time.Minute
	DecisionFreshness = 10 * time.Minute
)

// DecisionFreshnessFor is DecisionFreshness for the login freshness a Stage
// publishes in status.authFreshness: two thirds of it, so a login a person
// approves with is still fresh when the promoter counts it. An empty or
// unreadable one is AuthFreshness.
func DecisionFreshnessFor(published string) time.Duration {
	return PublishedFreshness(published) * 2 / 3
}

// PublishedFreshness is the promoter's login freshness as a Stage publishes
// it in status.authFreshness; AuthFreshness when empty or unreadable.
func PublishedFreshness(published string) time.Duration {
	d, err := time.ParseDuration(published)
	if err != nil || d <= 0 {
		return AuthFreshness
	}
	return d
}

// ParseAuthTime reads a login time as the apiserver reports it in
// userInfo.extra["delivery.pacto.trianalab.io/auth-time"]: Unix seconds (what structured
// authentication maps from auth_time) or RFC 3339.
func ParseAuthTime(v string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	secs, err := strconv.ParseInt(v, 10, 64)
	return time.Unix(secs, 0), err == nil
}

// ApprovalEligible says whether the promoter counts approval a, created at
// created, on the operation (spec, status and uid), and if not, why. The
// promoter asks it before it takes an Approval and writes the reason it
// passed one over to the operation; the CLI and the dashboard ask it before
// they create one, and refuse with the reason.
//
// It counts only on a held operation, for its uid and intent, from a person
// who neither asked for the change nor is whoever the asker acts for, in a
// team the question was routed to (floor.owners), given within freshness of
// that person's login and no earlier than the operation was last held. Its
// decision answers the question asked: once the version shipped only an
// accept or a reject, and a change that needs an approval is not cleared by
// an accepted unknown.
func ApprovalEligible(spec OperationSpec, st OperationStatus, uid string, a ApprovalSpec, created time.Time, p ClassPolicy, freshness time.Duration) (bool, string) {
	_, why := ApprovalRefusal(spec, st, uid, a, created, p, freshness)
	return why == "", why
}

// ApprovalRefusal is ApprovalEligible's answer as a code a client can show
// (OwnChange, NotYourTeam, NoOwnerTeam and so on) and the reason; both empty
// when the approval counts.
func ApprovalRefusal(spec OperationSpec, st OperationStatus, uid string, a ApprovalSpec, created time.Time, p ClassPolicy, freshness time.Duration) (code, why string) {
	if code, why = approvalAnswers(spec, st, uid, a); code == "" {
		code, why = approverMay(spec, st, a, created, p, freshness)
	}
	return code, why
}

// AnsweredTeams is approved plus every team of owners an approver in groups
// belongs to: what one approval answers of a question asked of several teams.
func AnsweredTeams(p ClassPolicy, owners, groups, approved []string) []string {
	out := slices.Clone(approved)
	for _, t := range owners {
		if p.InTeams(groups, []string{t}) && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// TeamsWaiting are the teams of owners that approved does not name: a change
// that breaks consumers of several teams waits for every one of them, so one
// team's approval never ships a change that breaks another team's consumer.
func TeamsWaiting(owners, approved []string) []string {
	return slices.DeleteFunc(slices.Clone(owners), func(t string) bool { return slices.Contains(approved, t) })
}

// approvalAnswers says why a does not answer the question op asks now.
func approvalAnswers(spec OperationSpec, st OperationStatus, uid string, a ApprovalSpec) (string, string) {
	floor := Decision("")
	if st.Plan != nil {
		floor = st.Plan.Floor.Decision
	}
	switch {
	case st.Phase != PhaseHeld:
		return "NotHeld", "the operation is not waiting for a decision"
	case a.OperationUID != uid || a.IntentDigest != spec.IntentDigest:
		return "OtherIntent", "it answers another operation or an older intent"
	case !slices.Contains([]Decision{Approve, AcceptUnknown, Reject}, a.Decision):
		return "NotADecision", fmt.Sprintf("%q is not a decision", a.Decision)
	case st.Applied && a.Decision == Approve:
		return "AlreadyShipped", "the version already shipped, so only accept-unknown or reject answers the question now"
	case !st.Applied && a.Decision == AcceptUnknown && floor == Approve:
		return "NeedsApproval", "this change needs an approval, not an accepted unknown"
	}
	return "", ""
}

// approverMay says why whoever gave a may not decide op.
func approverMay(spec OperationSpec, st OperationStatus, a ApprovalSpec, created time.Time, p ClassPolicy, freshness time.Duration) (string, string) {
	var owners []string
	if st.Plan != nil {
		owners = st.Plan.Floor.Owners
	}
	held, _ := time.Parse(time.RFC3339, st.Times.Held)
	login, ok := ParseAuthTime(a.AuthTime)
	age := created.Sub(login)
	switch {
	case a.By == spec.Actor.User || spec.OnBehalfOf != nil && a.By == spec.OnBehalfOf.User:
		return "OwnChange", a.By + " asked for this change, so someone else must decide it"
	case !p.IsHuman(Identity{User: a.By, Groups: a.Groups}):
		return "AgentsMayNotApprove", a.By + " is not a person, and only people decide"
	case len(owners) == 0 && p.RequireOwnerTeam:
		return "NoOwnerTeam", "no team is declared for this service, so nobody may answer and only break-glass can override; declare service.owner"
	case !p.InTeams(a.Groups, owners):
		return "NotYourTeam", fmt.Sprintf("%s is not in a team this question is asked of (%s)", a.By, strings.Join(owners, ", "))
	case created.Before(held):
		return "AskedAgain", "it was given before the question was last asked"
	case !ok || age < -time.Minute || age > freshness:
		return "FreshLoginRequired", fmt.Sprintf("%s's login was not within %s of the decision; sign in again", a.By, freshness)
	}
	return "", ""
}

// Caps on the Stage's digest memories.
// ponytail: bounded caches, ceiling 10 verified and 50 failed digests per Stage;
// derive both from history once records exist (M5).
const (
	MaxVerified      = 10
	MaxFailedDigests = 50
	MaxPublished     = 50
	MaxOffered       = 50
	MaxRolledBack    = 50
	// MaxConcluded is how many conclusions a Stage remembers: a retry of
	// a failed status write comes long before that many more conclude.
	MaxConcluded = 20
)

// CitedOp is what rollback admission needs to know about the operation a
// rollback undoes.
type CitedOp struct {
	UID     string
	Type    string
	Applied bool
	Pointer PointerChange
}

// AdmitRollback checks a rollback is bound to the operation it undoes: from is
// that operation's after and the Stage's pointer, to is its before and never a
// failed digest. Only a person or break-glass undoes a rollback or an override
// with one: for anyone else that is a promote. An agent rolling back a frozen
// Stage must cite a Fail verdict.
// It returns the refusal reason, or "" when the rollback is admissible.
func AdmitRollback(spec OperationSpec, status StageStatus, cited *CitedOp, class ActorClass) string {
	switch {
	case cited == nil:
		return "UnboundRollback: params.undoes names no operation on this Stage"
	case !cited.Applied:
		return "UnboundRollback: the cited operation never moved the pointer"
	case spec.Params.From != cited.Pointer.After || spec.Params.From != status.Pointer:
		return "UnboundRollback: from must be the cited operation's after and the Stage's pointer"
	case spec.Params.To != cited.Pointer.Before:
		return "UnboundRollback: to must be the cited operation's before; anything else is a promote"
	case spec.Params.To == "":
		return "UnboundRollback: the cited operation started from an empty Stage, so there is nothing to roll back to"
	case !mayUndo(cited.Type, class):
		return "UnboundRollback: undoing a rollback or override is a promote; propose it"
	case slices.Contains(status.FailedDigests, spec.Params.To):
		return "FailedDigest: a rollback may never target a failed digest"
	case status.Frozen && class == ClassAgent && (spec.Cause == nil || spec.Cause.Verdict == nil || spec.Cause.Verdict.Result != Fail):
		return "Frozen: an agent rollback on a frozen Stage needs a Fail verdict as its cause"
	}
	return ""
}

// mayUndo reports whether class may roll back an operation of type typ. A
// rollback has no pre-gates, so undoing a person's rollback or an override
// would ship again the version they removed with nobody asked: only a person
// or break-glass undoes anything but a promote.
func mayUndo(typ string, class ActorClass) bool {
	return typ == TypePromote || class == ClassHuman || class == ClassBreakGlass
}

// SetsAutoHold reports whether concluding this operation should put an
// auto-hold on the Stage: a rollback a person or agent chose, or a promote of a
// digest the Stage already verified, older or newer. Either way automation
// must not undo the choice. A person's promote of a digest the Stage has not
// verified clears it.
func SetsAutoHold(spec OperationSpec, class ActorClass, status StageStatus) bool {
	if class == ClassController {
		return false
	}
	switch spec.Type {
	case TypeRollback:
		return true
	case TypePromote:
		return slices.Contains(status.Verified, spec.Params.To)
	}
	return false
}

// AddDigest appends d to list once, keeping the newest max entries.
func AddDigest(list []string, d string, max int) []string {
	list = slices.DeleteFunc(slices.Clone(list), func(x string) bool { return x == d })
	list = append(list, d)
	if len(list) > max {
		list = list[len(list)-max:]
	}
	return list
}

// RemoveDigest removes d from list.
func RemoveDigest(list []string, d string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(x string) bool { return x == d })
}

// NewestCandidate picks the version auto-promotion would ship. From an
// OCIRepository it is the digest of its current artifact revision, unless this
// Stage already verified it (a tag moved back to an old digest is not a new
// version, and automation never ships an old one) or it is not in Published;
// from an upstream Stage it is
// the newest digest that Stage offers (it passed pacto.published there and
// verified) that failed neither there nor here. It returns ""
// when there is nothing new: the Stage already runs it or it failed here.
func NewestCandidate(status StageStatus, ociRevision string, upstream *StageStatus) string {
	cand := ""
	if upstream != nil {
		for _, d := range slices.Backward(upstream.Verified) {
			if slices.Contains(upstream.Offered, d) && !slices.Contains(upstream.FailedDigests, d) && !slices.Contains(status.FailedDigests, d) {
				cand = d
				break
			}
		}
	} else if cand = RevisionDigest(ociRevision); slices.Contains(status.Verified, cand) || !slices.Contains(status.Published, cand) {
		return "" // already shipped, or never verified as published
	}
	if cand == status.Pointer || slices.Contains(status.FailedDigests, cand) {
		return ""
	}
	return cand
}

// VerifyWindow is the Stage's verify window, defaulting to DefaultVerifyWindow.
func VerifyWindow(spec StageSpec) time.Duration {
	return durationOr(spec.Gates.Verify.Window, DefaultVerifyWindow)
}

// RecheckInterval is how often the Stage's verify gates run again on a shipped
// version, defaulting to DefaultRecheck.
func RecheckInterval(spec StageSpec) time.Duration {
	return durationOr(spec.Gates.Verify.Recheck, DefaultRecheck)
}

// SetCondition puts c in conds in place of the condition of its type, keeping
// that one's transition time while the status is the same, and says whether
// anything changed. It never edits conds.
func SetCondition(conds []Condition, c Condition, now time.Time) ([]Condition, bool) {
	c.LastTransitionTime = now.UTC().Format(time.RFC3339)
	for i, x := range conds {
		if x.Type != c.Type {
			continue
		}
		if x.Status == c.Status {
			c.LastTransitionTime = x.LastTransitionTime
		}
		if x == c {
			return conds, false
		}
		out := slices.Clone(conds)
		out[i] = c
		return out, true
	}
	return append(slices.Clone(conds), c), true
}

// passedPublished reports whether pacto.published passed: a version a person
// accepted on an unknown published verdict is never offered downstream.
func passedPublished(vs []Verdict) bool {
	return slices.ContainsFunc(vs, func(v Verdict) bool {
		return v.Gate == GatePublished && v.Result == Pass
	})
}

// Conclusion is how an operation ended, as ApplyEffects needs it.
type Conclusion struct {
	Outcome      Outcome
	Verification Verification
	// Reason is "Code: message".
	Reason string
	// Freeze says the conclusion itself freezes the Stage, for Reason.
	Freeze bool
}

// ApplyEffects updates a Stage's memories for the operation name (uid, spec,
// status) that concluded as c: what verified, failed, is offered downstream,
// auto-hold and freezes. The promoter applies it in the compare-and-swap that
// concludes, and the WASM demo applies the same rules.
func ApplyEffects(s *StageStatus, name, uid string, spec OperationSpec, st OperationStatus, c Conclusion) {
	to := spec.Params.To
	if st.Applied {
		switch {
		case SetsAutoHold(spec, st.ActorClass, *s):
			s.AutoHold = true
		case spec.Type == TypePromote && st.ActorClass == ClassHuman:
			s.AutoHold = false
		}
	}
	switch c.Outcome {
	case Succeeded:
		succeeded(s, name, spec, st, c)
	case Rejected:
		// A refused automatic rollback must not leave the failed version
		// running unwatched: a person decides.
		if spec.Type == TypeRollback && st.ActorClass == ClassController {
			c.Freeze, c.Reason = true, "the automatic rollback was refused: "+c.Reason
		}
	case Failed:
		s.FailedDigests = AddDigest(s.FailedDigests, to, MaxFailedDigests)
		s.Verified = RemoveDigest(s.Verified, to)
		c = failed(s, uid, spec, st, c)
	}
	if c.Freeze {
		s.Frozen, s.FrozenReason = true, name+": "+c.Reason
	}
}

func succeeded(s *StageStatus, name string, spec OperationSpec, st OperationStatus, c Conclusion) {
	if c.Verification == Verified {
		s.Verified = AddDigest(s.Verified, spec.Params.To, MaxVerified)
		if spec.Type == TypePromote && passedPublished(st.Verdicts) {
			s.Offered = AddDigest(s.Offered, spec.Params.To, MaxOffered)
		}
	}
	switch {
	case spec.Type == TypeAutonomy:
		changeRung(s, spec)
	case spec.Type == TypeUnfreeze:
		s.Frozen, s.FrozenReason, s.AutoHold = false, "", false
	case spec.Type == TypeFreeze && !s.Frozen:
		// A freeze a failed re-check raised finds the Stage frozen already,
		// with that check's reason, which it keeps.
		s.Frozen, s.FrozenReason = true, name+": frozen: "+spec.Reason
	}
}

// failed is how a failed operation ends on the Stage: its one automatic
// rollback counted, or a freeze when nothing can be rolled back to.
func failed(s *StageStatus, uid string, spec OperationSpec, st OperationStatus, c Conclusion) Conclusion {
	switch {
	case spec.Type != TypePromote:
		c.Freeze, c.Reason = true, "a "+spec.Type+" failed: "+c.Reason
	case st.Pointer.Before == "":
		c.Freeze, c.Reason = true, "the first version failed, so there is nothing to roll back to"
	case slices.Contains(s.FailedDigests, st.Pointer.Before):
		c.Freeze, c.Reason = true, "the version before it failed here too, so there is nothing safe to roll back to"
	default:
		s.RolledBack = AddDigest(s.RolledBack, uid, MaxRolledBack)
	}
	return c
}

// changeRung moves the Stage to the rung an autonomy operation names. Leaving
// observe adopts what GitOps' source reports as the pointer: Pacto must know
// what runs before it owns the pointer, and a promote from an empty pointer
// would compare the new version with itself.
func changeRung(s *StageStatus, spec OperationSpec) {
	if Rung(spec.Params.From) == RungObserve && s.ObservedRevision != nil {
		s.Pointer = s.ObservedRevision.Digest
	}
	// Evidence is earned on a rung: a change restarts the count. Concluded
	// restarts at this operation's entry (ProposeRung); the shadow
	// window restarts when the Stage observes again.
	s.Rung, s.Proposal = Rung(spec.Params.To), nil
	if s.Rung == RungObserve {
		// GitOps owns the pointer again, so the old one is cleared: kept, it
		// would read as an edit outside Pacto once the applyRef is labelled
		// to leave observe (witness).
		s.Shadow, s.Pointer = nil, ""
	}
}

// RungRefusal says why an autonomy operation may not move the Stage now, or
// "": the rung moved since it was planned, or it leaves observe with no
// observed digest to adopt or while an operation holds the pointer. The promoter checks it in the compare-and-swap
// that concludes the operation.
func RungRefusal(spec OperationSpec, stage StageSpec, st StageStatus) string {
	switch cur := CurrentRung(stage, st); {
	case Rung(spec.Params.From) != cur:
		return "RungMoved: from is " + spec.Params.From + " but the Stage is on " + string(cur)
	case cur == RungObserve &&
		(st.ObservedRevision == nil || !isDigest(st.ObservedRevision.Digest)):
		return "NoObservedDigest: GitOps' source reports no digest, so there is no pointer to adopt"
	case cur == RungObserve && st.Locks[FacetPointer] != "":
		return "PointerBusy: operation " + st.Locks[FacetPointer] + " holds the pointer, so it cannot be adopted now"
	}
	return ""
}
