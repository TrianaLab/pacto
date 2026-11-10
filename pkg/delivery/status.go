package delivery

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
)

// Listing is how complete a list of delivery objects is: what could not be
// listed is left out and named.
type Listing struct {
	AsOf         string   `json:"asOf"`
	Completeness string   `json:"completeness" enum:"complete,partial"`
	Unreadable   []string `json:"unreadable,omitempty" doc:"What could not be listed, each naming its namespace and why, or * when the namespaces could not be known"`
}

// StageRow is one Stage: one service in one environment, as every surface
// shows it (the dashboard, pacto delivery status and MCP).
type StageRow struct {
	Namespace        string   `json:"namespace"`
	Stage            string   `json:"stage"`
	Service          string   `json:"service"`
	Domain           string   `json:"domain,omitempty" doc:"The registry domain the Stage names for its service; empty when it names none"`
	Environment      string   `json:"environment"`
	Pointer          string   `json:"pointer,omitempty" doc:"The digest the environment runs"`
	Version          string   `json:"version,omitempty" doc:"The version its contract declares, when the artifact could be read"`
	Verified         bool     `json:"verified" doc:"The running digest passed its verify gates here"`
	Failed           bool     `json:"failed" doc:"The running digest failed here"`
	Frozen           bool     `json:"frozen"`
	FrozenReason     string   `json:"frozenReason,omitempty"`
	AutoHold         bool     `json:"autoHold"`
	Held             int      `json:"held" doc:"Operations on this Stage waiting for a person"`
	Candidate        string   `json:"candidate,omitempty" doc:"The newest eligible candidate"`
	CandidateVersion string   `json:"candidateVersion,omitempty" doc:"The version the candidate's contract declares, when the artifact could be read"`
	Ready            string   `json:"ready,omitempty" doc:"The Ready condition: True, or False with the reason the Stage cannot promote"`
	ReadyMessage     string   `json:"readyMessage,omitempty" doc:"Why the Stage cannot promote, in words, when Ready is not True"`
	Actions          []string `json:"actions" doc:"What the caller's class may ask for here: promote, unfreeze"`
	Level            string   `json:"level,omitempty" doc:"observe, advise, auto-nonbreaking or autonomous; empty when the Stage does not report one"`

	State       string        `json:"state" enum:"failed,frozen,autohold,waiting,empty,unverified,verified,unknown" doc:"The one state the Stage is counted in, as the board's state filter and counts.byEnvironment count it. unknown appears only on status rows whose operations could not be read; the board never emits it"`
	ContractRef string        `json:"contractRef,omitempty" doc:"The digest-pinned contract the running artifact names (delivery.pacto.trianalab.io/contract), when its artifact could be read"`
	ReadyFix    string        `json:"readyFix,omitempty" doc:"What to change so the Stage can promote, when Ready is False with a known reason"`
	Watched     *Watch        `json:"watched,omitempty" doc:"Whether the newest re-check of the running version passed: the Watched condition when lastCheck is about the running digest and the operation that shipped it, else Unknown with reason NotRechecked"`
	LastCheck   *CheckSummary `json:"lastCheck,omitempty" doc:"The newest re-check of the running version after it shipped (status.lastCheck)"`
	HeldUnknown bool          `json:"heldUnknown,omitempty" doc:"The Stage's operations could not be read, so held is not known"`
	Offers      []Offer       `json:"offers" doc:"Each action with whether the caller may ask for it here and why. Freeze and undo are offered only when the request names a service. Offers only describe: proposing goes through this surface's propose path, when it has one, with the caller's own identity, and admission decides"`
}

// Watch is the Watched condition as a row shows it.
type Watch struct {
	Status  string `json:"status" enum:"True,False,Unknown"`
	Reason  string `json:"reason,omitempty" doc:"Rechecked, RecheckFailed or RecheckUnknown from the Watched condition, or NotRechecked when the running version has not been re-checked since it shipped"`
	Message string `json:"message,omitempty"`
	Since   string `json:"since,omitempty" doc:"When the condition last changed (RFC 3339)"`
}

// CheckSummary is the newest re-check of a Stage's running version.
type CheckSummary struct {
	Digest   string    `json:"digest"`
	At       string    `json:"at" doc:"When the gates ran (RFC 3339)"`
	Result   Result    `json:"result" enum:"Pass,Fail,Unknown" doc:"The worst verdict"`
	Current  bool      `json:"current" doc:"The check is about the digest the Stage runs now and the operation that shipped it"`
	Verdicts []Verdict `json:"verdicts"`
}

// Offer is one action a caller may or may not ask for on a Stage, and why.
type Offer struct {
	Action    string `json:"action" enum:"promote,unfreeze,freeze,undo"`
	Available bool   `json:"available"`
	Reason    string `json:"reason" doc:"Why it is offered, or why not"`
	To        string `json:"to,omitempty" doc:"promote: the candidate digest; undo: the digest it goes back to"`
	Operation string `json:"operation,omitempty" doc:"undo: the operation it undoes; open GET /api/delivery/undo or pacto delivery undo for the plan, which says rollback, promote back or nothing"`
}

// Described is what an artifact digest carries: its contract's version and
// owner team, the contract it names, and Err when it could not be read.
type Described struct{ Version, Team, Contract, Err string }

// RowInput is what RowOf reads beside the Stage: its held operations (or
// that they could not be read), the caller, what each digest carries, and
// whether the request names a service (freeze is offered only then).
type RowInput struct {
	Held        int
	HeldUnknown bool
	Caller      Caller
	Read        map[string]Described
	WithService bool
}

// RowOf is one Stage as every surface shows it. It only describes: nothing
// it offers is asked for here.
func RowOf(st StageObject, in RowInput) StageRow {
	ss := st.Status
	row := StageRow{Namespace: st.Metadata.Namespace, Stage: st.Metadata.Name, Service: st.Spec.Service.Name, Domain: st.Spec.Service.Domain,
		Environment: st.Spec.Environment, Pointer: ss.Pointer, Verified: slices.Contains(ss.Verified, ss.Pointer),
		Failed: slices.Contains(ss.FailedDigests, ss.Pointer), Frozen: ss.Frozen, FrozenReason: ss.FrozenReason, AutoHold: ss.AutoHold,
		Held: in.Held, HeldUnknown: in.HeldUnknown, Candidate: ss.Candidate, Level: string(ss.Level), Actions: []string{},
		State: StageState(st, in.Held, in.HeldUnknown)}
	if ss.Pointer != "" {
		row.Version, row.ContractRef = in.Read[ss.Pointer].Version, in.Read[ss.Pointer].Contract
	}
	if ss.Candidate != "" {
		row.CandidateVersion = in.Read[ss.Candidate].Version
	}
	if c := readyOf(ss); c != nil {
		row.Ready = strings.TrimSuffix(c.Status+": "+c.Reason, ": ")
		if c.Status != "True" {
			row.ReadyMessage = c.Message
		}
		if c.Status == "False" {
			row.ReadyFix = ReadyFix(c.Reason)
		}
	}
	row.LastCheck = checkOf(ss)
	row.Watched = watchOf(ss, row.LastCheck)
	row.Offers = offersOf(st, in, &row)
	for _, o := range row.Offers {
		if o.Available && (o.Action == "promote" || o.Action == "unfreeze") {
			row.Actions = append(row.Actions, o.Action)
		}
	}
	return row
}

// checkOf is the Stage's newest re-check, nil when there is none.
func checkOf(ss StageStatus) *CheckSummary {
	lc := ss.LastCheck
	if lc == nil {
		return nil
	}
	return &CheckSummary{Digest: lc.Digest, At: lc.At, Result: Worst(lc.Verdicts), Verdicts: append([]Verdict{}, lc.Verdicts...),
		Current: lc.Digest == ss.Pointer && lc.Operation == ss.LastOp}
}

// watchOf reads the Watched condition only while lastCheck is about the
// running version: the promoter writes both on a re-check and never resets
// them on a promote, so after one the condition still speaks of the previous
// digest.
func watchOf(ss StageStatus, lc *CheckSummary) *Watch {
	if ss.Pointer == "" {
		return nil
	}
	if i := slices.IndexFunc(ss.Conditions, func(c Condition) bool { return c.Type == ConditionWatched }); i >= 0 && lc != nil && lc.Current {
		c := ss.Conditions[i]
		return &Watch{Status: c.Status, Reason: c.Reason, Message: c.Message, Since: c.LastTransitionTime}
	}
	return &Watch{Status: "Unknown", Reason: "NotRechecked", Message: "the running version has not been re-checked since it shipped"}
}

// offersOf is promote, then unfreeze while frozen or on auto-hold, then with
// a service freeze while a version runs unfrozen. With the identity policy
// unread every offer is unavailable: the caller's class may be wrong.
func offersOf(st StageObject, in RowInput, row *StageRow) []Offer {
	ss := st.Status
	offers := []Offer{promoteOffer(st, in.Caller.Class, row)}
	if ss.Frozen || ss.AutoHold {
		offers = append(offers, unfreezeOffer(in.Caller, in.Read[ss.Pointer]))
	}
	if in.WithService && !ss.Frozen && ss.Pointer != "" {
		offers = append(offers, freezeOffer(in.Caller.Class, ss.Pointer, row.Version))
	}
	if in.Caller.PolicyErr != "" {
		for i := range offers {
			offers[i].Available, offers[i].Reason = false, "Pacto could not read the delivery identity policy ("+in.Caller.PolicyErr+
				"), so it cannot tell what you may ask for; admission decides when you propose"
		}
	}
	return offers
}

func promoteOffer(st StageObject, class ActorClass, row *StageRow) Offer {
	ss := st.Status
	o := Offer{Action: "promote", To: ss.Candidate}
	ready := readyOf(ss)
	notReady := ready != nil && ready.Status == "False"
	switch {
	case CurrentLevel(st.Spec, ss) == LevelObserve:
		o.Reason = "the Stage observes: GitOps owns the applyRef; propose an autonomy change to advise first"
	case notReady && ready.Reason == ReadyReasonUnguarded:
		o.Reason = "the promoter admits no operation until the delivery admission policies are installed: " + ReadyFix(ReadyReasonUnguarded)
	case ss.Candidate == "":
		o.Reason = "no eligible candidate: " + cmp.Or(row.ReadyFix, "the candidate source offers nothing newer that has not failed here")
	case ss.Candidate == ss.Pointer:
		o.Reason = "the Stage runs its newest candidate"
	case !MayRaise(TypePromote, class, false):
		o.Reason = classPhrase(class) + " may not raise a promote"
	default:
		o.Available, o.Reason = true, "promotes "+cmp.Or(row.CandidateVersion, short(ss.Candidate))
		if ss.Frozen {
			o.Reason += "; the Stage is frozen, so the promote waits until a person unfreezes it"
		}
		if notReady {
			o.Reason += "; the Stage is not ready (" + ready.Reason + "): " + cmp.Or(row.ReadyFix, ready.Message)
		}
	}
	return o
}

// unfreezeOffer mirrors the promoter's admission rule for an unfreeze
// (unfreezeRefusal), so no surface offers one it would refuse.
func unfreezeOffer(caller Caller, running Described) Offer {
	o := Offer{Action: "unfreeze"}
	switch {
	case !MayRaise(TypeUnfreeze, caller.Class, false):
		o.Reason = classPhrase(caller.Class) + " may not raise an unfreeze"
	case mayUnfreeze(caller, running.Team):
		o.Available, o.Reason = true, "unfreezes the Stage"
	case running.Team != "":
		o.Reason = "only a person of team " + running.Team + " may unfreeze it; that team or break-glass can"
	default:
		why := ""
		if running.Err != "" {
			why = " (" + running.Err + ")"
		}
		o.Reason = "the running contract declares no team or cannot be read" + why + ", and only an owner team may unfreeze it; break-glass can"
	}
	return o
}

func freezeOffer(class ActorClass, pointer, version string) Offer {
	if !MayRaise(TypeFreeze, class, false) {
		return Offer{Action: "freeze", Reason: classPhrase(class) + " may not raise a freeze"}
	}
	return Offer{Action: "freeze", Available: true, Reason: "freezes the Stage on " + cmp.Or(version, short(pointer))}
}

// mayUnfreeze is the promoter's admission rule for an unfreeze: a person
// unfreezes only a Stage whose running contract names their team
// (Policy.InTeams); with no team named or readable, anyone, or nobody with
// RequireOwnerTeam. Every other class the type lets raise one may.
func mayUnfreeze(caller Caller, team string) bool {
	if caller.Class != ClassHuman {
		return true
	}
	var teams []string
	if team != "" {
		teams = []string{team}
	}
	return caller.Policy.InTeams(caller.Groups, teams)
}

// classPhrase names a class in a sentence.
func classPhrase(c ActorClass) string {
	switch c {
	case ClassHuman:
		return "a person"
	case ClassAgent:
		return "an agent"
	case ClassBreakGlass:
		return "a break-glass caller"
	case ClassController:
		return "a controller"
	}
	return "a " + string(c) + " caller"
}

// StageState is the one state st is counted in: failed, frozen, autohold,
// then unknown when its operations could not be read, then waiting (held
// operations), empty, unverified or verified.
func StageState(st StageObject, held int, heldUnknown bool) string {
	ss := st.Status
	switch {
	case slices.Contains(ss.FailedDigests, ss.Pointer):
		return "failed"
	case ss.Frozen:
		return "frozen"
	case ss.AutoHold:
		return "autohold"
	case heldUnknown:
		return "unknown"
	case held > 0:
		return "waiting"
	case ss.Pointer == "":
		return "empty"
	case !slices.Contains(ss.Verified, ss.Pointer):
		return "unverified"
	}
	return "verified"
}

// describeParallel is how many digests DescribeAll reads at once: each read
// goes to a registry.
const describeParallel = 8

// DescribeAll describes n digests, describeParallel at once; at(i) names the
// Stage to read the i-th from and its digest. out[i] answers at(i).
func DescribeAll(ctx context.Context, n int, at func(int) (*StageObject, string), describe func(context.Context, *StageObject, string) Revision) []Revision {
	out := make([]Revision, n)
	sem := make(chan struct{}, describeParallel)
	var wg sync.WaitGroup
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			st, d := at(i)
			out[i] = describe(ctx, st, d)
		})
	}
	wg.Wait()
	return out
}
