package delivery

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Login freshness. The promoter ignores an approval given more than
// AuthFreshness after its author's login; the dashboard asks for a new login
// once one is older than DecisionFreshness, leaving a margin under it.
const (
	AuthFreshness     = 15 * time.Minute
	DecisionFreshness = 10 * time.Minute
)

// ParseAuthTime reads a login time as the apiserver reports it in
// userInfo.extra["pacto.dev/auth-time"]: Unix seconds (what structured
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
	why := approvalAnswers(spec, st, uid, a)
	if why == "" {
		why = approverMay(spec, st, a, created, p, freshness)
	}
	return why == "", why
}

// approvalAnswers says why a does not answer the question op asks now.
func approvalAnswers(spec OperationSpec, st OperationStatus, uid string, a ApprovalSpec) string {
	floor := Decision("")
	if st.Plan != nil {
		floor = st.Plan.Floor.Decision
	}
	switch {
	case st.Phase != PhaseHeld:
		return "the operation is not waiting for a decision"
	case a.OperationUID != uid || a.IntentDigest != spec.IntentDigest:
		return "it answers another operation or an older intent"
	case !slices.Contains([]Decision{Approve, AcceptUnknown, Reject}, a.Decision):
		return fmt.Sprintf("%q is not a decision", a.Decision)
	case st.Applied && a.Decision == Approve:
		return "the version already shipped, so only accept-unknown or reject answers the question now"
	case !st.Applied && a.Decision == AcceptUnknown && floor == Approve:
		return "this change needs an approval, not an accepted unknown"
	}
	return ""
}

// approverMay says why whoever gave a may not decide op.
func approverMay(spec OperationSpec, st OperationStatus, a ApprovalSpec, created time.Time, p ClassPolicy, freshness time.Duration) string {
	var owners []string
	if st.Plan != nil {
		owners = st.Plan.Floor.Owners
	}
	held, _ := time.Parse(time.RFC3339, st.Times.Held)
	login, ok := ParseAuthTime(a.AuthTime)
	age := created.Sub(login)
	switch {
	case a.By == spec.Actor.User || spec.OnBehalfOf != nil && a.By == spec.OnBehalfOf.User:
		return a.By + " asked for this change, so someone else must decide it"
	case !p.IsHuman(Identity{User: a.By, Groups: a.Groups}):
		return a.By + " is not a person, and only people decide"
	case !p.InTeams(a.Groups, owners):
		return fmt.Sprintf("%s is not in a team this question is asked of (%s)", a.By, strings.Join(owners, ", "))
	case created.Before(held):
		return "it was given before the question was last asked"
	case !ok || age < -time.Minute || age > freshness:
		return fmt.Sprintf("%s's login was not within %s of the decision; sign in again", a.By, freshness)
	}
	return ""
}

// Caps on the Stage's digest memories.
// ponytail: bounded caches, ceiling 10 verified and 50 failed digests per Stage;
// derive both from history once records exist (M5).
const (
	MaxVerified      = 10
	MaxFailedDigests = 50
	MaxPublished     = 50
	MaxOffered       = 50
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
// failed digest. An agent rolling back a frozen Stage must cite a Fail verdict.
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
	case slices.Contains(status.FailedDigests, spec.Params.To):
		return "FailedDigest: a rollback may never target a failed digest"
	case status.Frozen && class == ClassAgent && (spec.Cause == nil || spec.Cause.Verdict == nil || spec.Cause.Verdict.Result != Fail):
		return "Frozen: an agent rollback on a frozen Stage needs a Fail verdict as its cause"
	}
	return ""
}

// SetsAutoHold reports whether concluding this operation should put an
// auto-hold on the Stage: a rollback a person or agent chose, or a promote of a
// digest the Stage already ran (an older version). Either way automation must
// not undo the choice.
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
