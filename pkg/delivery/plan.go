package delivery

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// IntentDigest is the hash of what was asked. Plans, approvals and records bind
// to it. The Stage spec is folded in as the policy snapshot, so a plan made
// against one policy cannot be admitted under another.
func IntentDigest(spec OperationSpec, stage StageSpec) string {
	b, _ := json.Marshal(struct {
		Type                string     `json:"type"`
		Stage               string     `json:"stage"`
		Facet               string     `json:"facet"`
		Params              Params     `json:"params"`
		Reason              string     `json:"reason"`
		OnBehalfOf          *Principal `json:"onBehalfOf"`
		StagePolicySnapshot StageSpec  `json:"stagePolicySnapshot"`
	}{spec.Type, spec.Stage, spec.Facet, spec.Params, spec.Reason, spec.OnBehalfOf, stage})
	return sha256Hex(b)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Revision is one side of a pointer move: the artifact digest and, when it could
// be read, the contract it carries.
type Revision struct {
	// Digest is the artifact manifest digest ("" for an empty Stage).
	Digest string
	// ContractRef is the digest-pinned contract reference the artifact names.
	ContractRef string
	// Bundle is the contract; nil when it could not be read.
	Bundle *contract.Bundle
	// Err says why Bundle is nil.
	Err string
	// NoContract is true when the artifact was read and names no
	// digest-pinned contract: it was not published by pacto publish.
	NoContract bool
}

// PlanInput is everything plan() reads. Gathering it is the caller's job, so the
// promoter and a client given the same cluster and registry state plan the same.
type PlanInput struct {
	Spec   OperationSpec
	Stage  StageSpec
	Status StageStatus
	From   Revision
	To     Revision
	// Snapshot is the operational graph the impact pre-gate projects onto.
	Snapshot *fleet.FleetSnapshot
	// Class is the actor class of whoever raises the operation.
	Class ActorClass
	// RequireOwnerTeam is the promoter's ClassPolicy.RequireOwnerTeam: the
	// question then says nobody may answer when no team is declared.
	RequireOwnerTeam bool
	// Upstream is the upstream Stage's status when the Stage takes its
	// candidates from one; nil when it could not be read.
	Upstream *StageStatus
	// UpstreamErr says why the records of an upstream Stage in another
	// cluster gave no Upstream.
	UpstreamErr string
	// Stages are every Stage the caller could list, in every namespace, and
	// StagesErr why they could not be listed. They place the consumers of a
	// blocked change: which Stage delivers each in this environment and what
	// its upstream Stage verified.
	Stages    []StageObject
	StagesErr error
	// Misplaced are the Pactos whose environment label no Stage declares
	// (EnvironmentPactos): they count everywhere, and while one of them is a
	// consumer of the service the impact gate cannot clear the change until
	// the label is fixed or a person accepts the unknown. One that does not
	// depend on the service holds nothing.
	Misplaced []Misplaced
	// Candidates reads what a digest on a consumer's Stage carries. It is the
	// one registry read planning makes beyond the two sides of the change,
	// only for a blocked change, and it must be bounded (ResolveRevision is).
	// Nil reads nothing: every candidate is unknown.
	Candidates func(ctx context.Context, stage StageObject, digest string) Revision
	// Gates are the promoter's gate providers, like RequireOwnerTeam
	// promoter configuration: the suites pre-gate reads which checks run on
	// an eval provider. A client plans with ClassPolicy.PlanGates, the types
	// the chart publishes; nil sees none.
	Gates map[string]Gate
}

// PlanFor predicts what the promoter will do with an operation: its intent digest,
// the pre-gate verdicts, the approval floor, the one question a human would
// be asked and, for a change a consumer does not accept, what must move first.
// It is pure apart from in.Candidates, and the promoter runs the same function
// when it admits the operation and again before it moves the pointer.
func PlanFor(ctx context.Context, in PlanInput) Plan {
	p := Plan{
		IntentDigest: IntentDigest(in.Spec, in.Stage),
		Type:         in.Spec.Type,
		From:         in.Spec.Params.From,
		To:           in.Spec.Params.To,
	}
	if in.To.Bundle != nil && in.To.Bundle.Contract != nil {
		p.ToVersion = in.To.Bundle.Contract.Service.Version
	}
	rejected := refusal(in)
	if rejected != "" {
		p.Conflicts = append(p.Conflicts, rejected)
		p.Predicted = PredictRejected
		return p
	}
	switch in.Spec.Type {
	case TypePromote:
		promoteGates(ctx, in, &p)
		if in.Stage.Approval.Always && !p.Floor.Human {
			p.Floor = Floor{Human: true, Decision: Approve}
			p.Question = "This Stage asks a person to approve every promote. Approve " + short(p.To) + "?"
		}
		levelPromote(in, &p)
		routeToServiceTeam(in, &p)
	case TypeAutonomy:
		autonomyGate(in, &p)
		routeToServiceTeam(in, &p)
	case TypeRollback:
		rollbackImpact(ctx, in, &p)
		routeToServiceTeam(in, &p)
	}
	holdForRecords(in, &p)
	p.Predicted = predict(in, &p)
	if p.Predicted == PredictRejected {
		// A refused change never reaches a person: nobody is asked anything.
		p.Floor, p.Question = Floor{}, ""
	}
	return p
}

// rollbackImpact holds a rollback a person or agent raised that a consumer
// does not accept, for that consumer's team: going back to a version a
// consumer has moved past breaks it as surely as a promote. A rollback runs
// no other gate, and nothing Pacto cannot see holds it: an emergency must not
// wait on an unreadable registry. The promoter's own rollbacks are never held.
func rollbackImpact(ctx context.Context, in PlanInput, p *Plan) {
	if in.Class == ClassController || in.Snapshot == nil || in.From.Bundle == nil || in.To.Bundle == nil {
		return
	}
	res := impact.Analyze(ctx, in.From.Bundle.Contract, in.To.Bundle.Contract, in.From.Bundle.FS, in.To.Bundle.FS, in.Snapshot,
		impact.Options{Domain: contract.OCIDomain(in.To.ContractRef), IncludeObserved: true})
	incompatible, unmet, _ := sortConsumers(res)
	if len(incompatible) == 0 && len(unmet) == 0 {
		return
	}
	names, owners := consumerNamesOwners(append(slices.Clone(incompatible), unmet...))
	if len(owners) == 0 {
		owners = serviceTeam(in)
	}
	p.Verdicts = append(p.Verdicts, Verdict{Gate: GateImpact, Phase: "pre", Result: Pass, Message: "rolling back breaks " + strings.Join(names, ", "),
		Citation: map[string]string{"classification": res.Classification, "completeness": string(res.Completeness)}})
	p.Floor = Floor{Human: true, Decision: Approve, Owners: owners, Consumers: names}
	p.Question = breaksQuestion("Rolling "+in.Stage.Service.Name+" back to "+res.NewVersion, incompatible, unmet) +
		" Approve the rollback anyway? Asked of: " + ownersOrAnyone(owners, in.RequireOwnerTeam)
}

// unmetRequired reports whether the change leaves c, a consumer that requires
// the service, with a guarantee it declares it needs and the version does not
// meet. An optional dependency's unmet guarantee holds nothing, as in pacto
// validate, where it is a warning.
func unmetRequired(c impact.AffectedConsumer) bool {
	return c.Required && len(c.Unmet) > 0
}

// breaksQuestion says which consumers the change (what) breaks: the ones
// whose range does not accept it and the ones it leaves with a required
// guarantee unmet.
func breaksQuestion(what string, incompatible, unmet []impact.AffectedConsumer) string {
	var parts []string
	if len(incompatible) > 0 {
		parts = append(parts, what+" is not accepted by "+describeRanges(incompatible)+".")
	}
	if len(unmet) > 0 {
		parts = append(parts, what+" does not meet what its consumers require: "+describeUnmet(unmet)+".")
	}
	return strings.Join(parts, " ")
}

// describeUnmet names each guarantee the consumers require that the version
// does not meet, naming consumer, provider and guarantee.
func describeUnmet(cs []impact.AffectedConsumer) string {
	var parts []string
	for _, c := range cs {
		for _, m := range c.Unmet {
			parts = append(parts, m.String())
		}
	}
	return strings.Join(parts, "; ")
}

// refusal is why an operation is rejected before any gate runs, or "".
func refusal(in PlanInput) string {
	switch {
	case !MayRaise(in.Spec.Type, in.Class, in.Spec.Witnessed):
		return fmt.Sprintf("NotPermitted: a %s actor may not raise %s", in.Class, in.Spec.Type)
	case in.Spec.Facet == FacetPointer && in.Spec.Params.From != in.Status.Pointer:
		return fmt.Sprintf("PointerMoved: from is %q but the Stage runs %q", in.Spec.Params.From, in.Status.Pointer)
	case in.Spec.Facet == FacetLevel && Level(in.Spec.Params.From) != CurrentLevel(in.Stage, in.Status):
		return fmt.Sprintf("LevelMoved: from is %q but the Stage is on %q", in.Spec.Params.From, CurrentLevel(in.Stage, in.Status))
	case in.Spec.Type == TypePromote && CurrentLevel(in.Stage, in.Status) == LevelObserve:
		return "Observing: " + in.Spec.Stage + " observes, so GitOps owns the applyRef; propose " + TypeAutonomy + " to advise first"
	case in.Spec.Type == TypePromote && slices.Contains(in.Status.FailedDigests, in.Spec.Params.To):
		return "FailedDigest: " + in.Spec.Params.To + " is one of the last 50 versions that failed on this Stage, which are never promoted again"
	}
	return AdmitAcknowledge(in.Spec, in.Stage)
}

// holdForRecords asks a person to approve a promote or rollback an agent or
// the promoter raised while a Stage that requires records cannot have them
// written. A plan that already asks a person keeps its own question.
func holdForRecords(in PlanInput, p *Plan) {
	why := RecordsUnavailable(in.Stage, in.Status)
	if why == "" || in.Class == ClassHuman || in.Class == ClassBreakGlass || p.Floor.Human ||
		in.Spec.Type != TypePromote && in.Spec.Type != TypeRollback {
		return
	}
	p.Floor.Human, p.Floor.Decision = true, Approve
	p.Question = "This Stage requires records, and " + why + ". Approve the " + in.Spec.Type + " to " + short(p.To) + " without a record yet?"
}

// ConditionRecordStoreReady is the recorder's Stage condition.
const ConditionRecordStoreReady = "RecordStoreReady"

// MaxRecordsPending is how many concluded operations may wait for a record
// before a Stage that requires records stops moving on its own.
// ponytail: a constant; make it a Stage field if one size does not fit.
const MaxRecordsPending = 5

// RecordsUnavailable says why a Stage that requires records cannot have them
// written now, or "" when it does not require them or they are being written.
func RecordsUnavailable(spec StageSpec, st StageStatus) string {
	if !spec.Records.Required {
		return ""
	}
	i := slices.IndexFunc(st.Conditions, func(c Condition) bool { return c.Type == ConditionRecordStoreReady })
	switch {
	case i < 0:
		return "the recorder has not reported on it"
	case st.Conditions[i].Status != "True":
		return "the recorder is not writing its records (" + st.Conditions[i].Reason + ": " + st.Conditions[i].Message + ")"
	case st.Stream.Seq-st.Stream.RecordedSeq > MaxRecordsPending:
		return fmt.Sprintf("%d operations wait for a record, more than %d", st.Stream.Seq-st.Stream.RecordedSeq, MaxRecordsPending)
	}
	return ""
}

// routeToServiceTeam sends a question that names no consumer's owner to the
// team of the Stage's own service, the one the contract running now declares:
// the author of the incoming version does not pick who approves it. Only a
// first version, with nothing running, is routed to its own team. It is set
// even when nothing is asked yet: a version that stays Unknown after it shipped
// is held for that same team. Only the named teams may answer (the promoter
// ignores anyone else); with no team declared anywhere, any person may, or
// nobody when the promoter requires an owner team.
func routeToServiceTeam(in PlanInput, p *Plan) {
	if len(p.Floor.Owners) == 0 {
		p.Floor.Owners = serviceTeam(in)
	}
	if p.Floor.Human && !strings.Contains(p.Question, "Asked of: ") {
		p.Question += " Asked of: " + ownersOrAnyone(p.Floor.Owners, in.RequireOwnerTeam)
	}
}

// serviceTeam is the owner team the contract running on the Stage declares;
// for a first version, with nothing running, the one it declares itself. Nil
// when that contract declares none or, with a version running, cannot be
// read: the incoming version's author never picks who answers for it, so then
// only break-glass may answer when the promoter requires an owner team.
func serviceTeam(in PlanInput) []string {
	r := in.From
	if r.Digest == "" {
		r = in.To
	}
	if r.Bundle != nil && r.Bundle.Contract != nil {
		if team := r.Bundle.Contract.Service.Owner.DisplayString(); team != "" {
			return []string{team}
		}
	}
	return nil
}

// predict turns verdicts, floor and Stage state into the expected outcome.
func predict(in PlanInput, p *Plan) Prediction {
	for _, v := range p.Verdicts {
		if v.Result == Fail {
			return PredictRejected
		}
	}
	if p.Floor.Human {
		return PredictHeld
	}
	exempt := in.Spec.Type != TypePromote
	if in.Status.Frozen && !exempt {
		p.Conflicts = append(p.Conflicts, "StageFrozen: "+in.Status.FrozenReason)
		return PredictWaits
	}
	return PredictProceeds
}

// promoteGates runs pacto.published, pacto.diff and pacto.impact and derives
// the approval floor. A version pacto publish did not publish goes no further.
func promoteGates(ctx context.Context, in PlanInput, p *Plan) {
	pub := publishedGate(in)
	p.Verdicts = append(p.Verdicts, pub)
	switch pub.Result {
	case Fail:
		return
	case Unknown:
		p.Floor = Floor{Human: true, Decision: AcceptUnknown}
		p.Question = "Pacto cannot tell whether pacto publish published " + short(p.To) + ": " + pub.Message + ". Accept the unknown?"
		return
	}
	diffV := diffGate(ctx, in)
	p.Verdicts = append(p.Verdicts, diffV)
	if diffV.Result != Pass {
		// Without both contracts there is nothing to project onto the graph.
		p.Verdicts = append(p.Verdicts, Verdict{Gate: GateImpact, Phase: "pre", Result: Unknown,
			Message: "impact needs the contracts the diff could not read"})
		if diffV.Result == Unknown {
			p.Floor = Floor{Human: true, Decision: AcceptUnknown}
			p.Question = "Pacto cannot see enough to judge " + short(p.To) + ": " + diffV.Message + ". Accept the unknown?"
		}
		return
	}
	impactGate(ctx, in, p)
	suitesGate(in, p)
	environmentGate(in, p)
	ownerGate(in, p)
}

// environmentGate holds a version whose contract does not back the Stage's
// environment (spec.environment): it declares environments but none of that
// name, or the values it declares there fail its configuration schemas. Both
// are confirmed, so, as for a consumer the change breaks, the gate reports
// what it found and the team of the service approves shipping it anyway.
// Values Pacto could not check against a schema are Unknown, for a person to
// accept. A contract that declares no environments is not judged: the gate
// passes and says so. The check is pacto validate's environments stage, run on
// the version's own bundle.
func environmentGate(in PlanInput, p *Plan) {
	c, env := in.To.Bundle.Contract, in.Stage.Environment
	v := Verdict{Gate: GateEnvironment, Phase: "pre", Result: Pass, Citation: map[string]string{"environment": env, "contract": in.To.ContractRef}}
	defer func() { p.Verdicts = append(p.Verdicts, v) }()
	if len(c.Environments) == 0 {
		v.Citation["applicable"] = "false"
		v.Message = "not applicable: the contract of " + short(p.To) + " declares no environments"
		return
	}
	var errs, unverified []string
	if i := slices.IndexFunc(c.Environments, func(e contract.Environment) bool { return e.Name == env }); i >= 0 {
		errs, unverified = environmentFindings(c, in.To.Bundle.FS, i)
	} else {
		names := make([]string, len(c.Environments))
		for j, e := range c.Environments {
			names[j] = e.Name
		}
		errs = []string{fmt.Sprintf("version %s declares no environment %s for Stage %s (it declares %s)",
			c.Service.Version, env, in.Spec.Stage, strings.Join(names, ", "))}
	}
	switch {
	case len(errs) > 0:
		v.Message = strings.Join(errs, "; ")
		holdFor(p, Approve, v.Message, v.Message+". Approve shipping "+short(p.To)+" anyway?")
		askServiceTeam(in, p)
	case len(unverified) > 0:
		v.Result, v.Message = Unknown, strings.Join(unverified, "; ")
		holdFor(p, AcceptUnknown, v.Message, "Pacto cannot verify the values of environment "+env+" in "+short(p.To)+": "+v.Message+". Accept the unknown?")
		askServiceTeam(in, p)
	default:
		v.Message = "the values of environment " + env + " satisfy the configuration schemas"
	}
}

// environmentFindings runs the environments stage on a contract and returns
// the errors, located in their values file, and the unverified warnings it
// reports for its environment i.
func environmentFindings(c *contract.Contract, bundleFS fs.FS, i int) (errs, unverified []string) {
	at := fmt.Sprintf("environments[%d]", i)
	under := func(path string) bool {
		rest, ok := strings.CutPrefix(path, at)
		return ok && (rest == "" || rest[0] == '.')
	}
	st := validation.EnvironmentStage(c, bundleFS)
	for _, e := range st.Errors {
		if under(e.Path) {
			loc := e.File
			if e.Line > 0 {
				loc += ":" + strconv.Itoa(e.Line)
			}
			errs = append(errs, loc+": "+e.Code+": "+e.Message)
		}
	}
	for _, w := range st.Warnings {
		if under(w.Path) && w.Code == string(finding.CodeEnvironmentValuesUnverified) {
			unverified = append(unverified, w.Code+": "+w.Message)
		}
	}
	return errs, unverified
}

// holdFor asks a person to decide, adding why to a question already asked:
// approve is the stronger decision, so it is never lowered to accept-unknown.
func holdFor(p *Plan, d Decision, why, question string) {
	if p.Floor.Human {
		p.Question = why + ". " + p.Question
		if d == Approve {
			p.Floor.Decision = Approve
		}
		return
	}
	p.Floor.Human, p.Floor.Decision, p.Question = true, d, question
}

// ownerGate holds, on every level and whoever raised it, a promote that
// changes service.owner: every later question about the service, autonomy
// raises included, goes to its owner, so a change of owner is approved by the
// team that owns the service now. The diff calls it non-breaking, which on
// its own would let it ship with no person.
func ownerGate(in PlanInput, p *Plan) {
	i := slices.IndexFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GateDiff })
	if i < 0 || p.Verdicts[i].Citation["owner"] == "" {
		return
	}
	msg := short(p.To) + " changes who owns " + in.Stage.Service.Name + " (" + p.Verdicts[i].Citation["owner"] +
		"), and the owner answers every later question about it, so the team that owns it now approves"
	if p.Floor.Human {
		p.Question = msg + ". " + p.Question
	} else {
		p.Question = msg + ". Approve " + short(p.To) + "?"
	}
	p.Floor.Human, p.Floor.Decision = true, Approve
	askServiceTeam(in, p)
}

// askServiceTeam adds the service's team to the owners a held plan asks, and
// to the question's "Asked of" when an earlier gate already named its owners:
// a hold about the service itself is never left to its consumers' teams.
func askServiceTeam(in PlanInput, p *Plan) {
	if len(p.Floor.Owners) == 0 {
		return // routeToServiceTeam asks the service's team
	}
	before := "Asked of: " + ownersOrAnyone(p.Floor.Owners, in.RequireOwnerTeam)
	for _, t := range serviceTeam(in) {
		if !slices.Contains(p.Floor.Owners, t) {
			p.Floor.Owners = append(p.Floor.Owners, t)
		}
	}
	p.Question = strings.Replace(p.Question, before, "Asked of: "+ownersOrAnyone(p.Floor.Owners, in.RequireOwnerTeam), 1)
}

// suitesGate holds a version whose contract does not ship a suite one of the
// Stage's checks runs: the check could never pass on it, so a person decides
// whether it ships unverified rather than the promoter failing or passing it.
// It also holds, on every level and whoever raised it, a version that changes
// the suites it ships (any checks change the diff saw): a person reviews
// the new checks before they judge the version that ships them.
func suitesGate(in PlanInput, p *Plan) {
	var missing []string
	if i := slices.IndexFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GateDiff }); i >= 0 && p.Verdicts[i].Citation["checks"] != "" {
		missing = append(missing, short(p.To)+" changes the checks it is verified by ("+p.Verdicts[i].Citation["checks"]+
			"), so a person reviews the new checks before they judge it")
	}
	checks := in.To.Bundle.Contract.Checks
	for _, c := range in.Stage.Gates.Verify.Checks {
		_, eval := in.Gates[c.Provider].(Eval)
		before := len(missing)
		switch {
		case c.Suite != "" && !slices.ContainsFunc(checks, func(k contract.Check) bool { return k.Name == c.Suite }):
			missing = append(missing, "the check "+c.Name+" runs the suite "+c.Suite+", but the contract of "+short(p.To)+" ships no check of that name")
		case c.Suite == "" && eval:
			// An eval check with no suite runs the only check the version ships.
			if _, why := pickSuite(checks, "", short(p.To)); why != "" {
				missing = append(missing, "the check "+c.Name+" names no suite and "+why)
			}
		}
		if c.SuiteDigest != "" && len(missing) == before {
			switch s, why := readSuite(in.To, c.Suite); {
			case why != "":
				missing = append(missing, "the check "+c.Name+" pins its suite, but "+why)
			case s.Digest != c.SuiteDigest:
				missing = append(missing, "the check "+c.Name+": "+pinMismatch(s, c.SuiteDigest))
			}
		}
	}
	if len(missing) == 0 {
		return
	}
	msg := strings.Join(missing, "; ")
	p.Verdicts = append(p.Verdicts, Verdict{Gate: GateSuites, Phase: "pre", Result: Unknown, Message: msg,
		Citation: map[string]string{"contract": in.To.ContractRef}})
	if p.Floor.Human {
		p.Question = msg + ". " + p.Question
		return
	}
	p.Floor = Floor{Human: true, Decision: AcceptUnknown}
	p.Question = "Pacto cannot verify " + short(p.To) + ": " + msg + ". Accept the unknown?"
}

// publishedGate admits only a version pacto publish published. From a
// candidate OCIRepository that is a digest Flux verified the signature of
// (Status.Published) whose artifact names its digest-pinned contract; from an
// upstream Stage it is a digest that Stage offers: it passed this gate there,
// with a pass rather than an accepted unknown, and verified. An upstream
// Stage that cannot be read offers nothing: the gate never asks a person to
// accept an unknown source. A Stage with no candidate source promotes nothing.
func publishedGate(in PlanInput) Verdict {
	v := Verdict{Gate: GatePublished, Phase: "pre", Result: Fail, Citation: map[string]string{}}
	c := in.Stage.Candidates
	switch {
	case c.UpstreamStage != nil || c.UpstreamRecords != nil:
		upstreamOffers(in, &v)
	case c.OCIRepositoryRef != nil:
		candidatePublished(in, &v)
	default:
		v.Message = "this Stage names no candidate source, so nothing it could promote was verified as published by pacto publish: set spec.candidates on the Stage"
	}
	return v
}

// upstreamOffers judges the published gate for a Stage that takes its
// candidates from an upstream Stage, here or in another cluster.
func upstreamOffers(in PlanInput, v *Verdict) {
	to, c := in.Spec.Params.To, in.Stage.Candidates
	v.Citation["upstream"] = upstreamName(c)
	if in.Upstream != nil && c.UpstreamRecords != nil {
		// The upstream records this verdict relied on: an auditor replays
		// the stream up to this record and gets the same answer.
		v.Citation["upstreamHead"], v.Citation["upstreamSeq"] = in.Upstream.Stream.Head, strconv.FormatInt(in.Upstream.Stream.Seq, 10)
	}
	switch {
	case in.Upstream == nil && c.UpstreamRecords != nil:
		v.Message = "the records of the upstream Stage " + v.Citation["upstream"] + " cannot be read or verified (" +
			in.UpstreamErr + "), so it offers no candidate"
	case in.Upstream == nil && in.UpstreamErr != "":
		v.Message = in.UpstreamErr + ", so it offers no candidate"
	case in.Upstream == nil:
		v.Message = "the upstream Stage " + v.Citation["upstream"] + " does not exist or cannot be read, so it offers no candidate"
	case slices.Contains(in.Upstream.Verified, to) && slices.Contains(in.Upstream.Offered, to):
		v.Result, v.Message = Pass, short(to)+" passed pacto.published and verified on the upstream Stage"
	case slices.Contains(in.Upstream.FailedDigests, to):
		v.Message = short(to) + " failed after release on the upstream Stage " + v.Citation["upstream"] + ", so it is not a candidate here: ship a fixed version through " + v.Citation["upstream"] + " first"
	default:
		v.Message = short(to) + " is not offered by the upstream Stage " + v.Citation["upstream"] +
			": it never passed pacto.published there or never verified, so it is not a candidate here. Promote it on " +
			v.Citation["upstream"] + " first, and plan again here once it verifies there"
	}
}

// candidatePublished judges the published gate for a Stage whose candidates
// come from an OCIRepository Flux verifies.
func candidatePublished(in PlanInput, v *Verdict) {
	to, ref := in.Spec.Params.To, in.Stage.Candidates.OCIRepositoryRef
	v.Citation["source"] = ref.Name
	switch {
	case !slices.Contains(in.Status.Published, to):
		v.Message = "Flux never verified a signature of " + short(to) + " on the candidate OCIRepository " + ref.Name +
			": only versions pacto publish published and signed are promoted, and the OCIRepository must set spec.verify " +
			"with a secretRef or matchOIDCIdentity naming the publisher"
	case in.To.NoContract:
		v.Message = short(to) + " names no digest-pinned " + ContractAnnotation + " annotation, so pacto publish did not publish it"
	case in.To.ContractRef == "":
		v.Result, v.Message = Unknown, "the artifact "+short(to)+" could not be read: "+in.To.Err
	default:
		v.Result, v.Message = Pass, short(to)+" is signed and names its contract"
		v.Citation["contract"] = in.To.ContractRef
	}
}

func diffGate(ctx context.Context, in PlanInput) Verdict {
	v := Verdict{Gate: GateDiff, Phase: "pre", Citation: map[string]string{}}
	to := in.To
	if to.Bundle == nil {
		v.Result, v.Message = Unknown, "the contract of "+short(to.Digest)+" could not be read: "+to.Err
		return v
	}
	v.Citation["to"] = to.ContractRef
	if name := to.Bundle.Contract.Service.Name; name != in.Stage.Service.Name {
		v.Result, v.Message = Fail, fmt.Sprintf("the contract of %s is service %q, but this Stage delivers %q", short(to.Digest), name, in.Stage.Service.Name)
		return v
	}
	from := in.From
	if from.Digest == "" {
		v.Result, v.Message = Pass, "first version on this Stage"
		v.Citation["classification"] = "INITIAL"
		return v
	}
	if from.Bundle == nil {
		v.Result, v.Message = Unknown, "the contract of the running version "+short(from.Digest)+" could not be read: "+from.Err
		return v
	}
	v.Citation["from"] = from.ContractRef
	d := diff.Compare(ctx, from.Bundle.Contract, to.Bundle.Contract, from.Bundle.FS, to.Bundle.FS)
	v.Result = Pass
	v.Citation["classification"] = d.Classification.String()
	// The suites the version ships changed: suitesGate holds it for a person.
	var suites []string
	for _, c := range d.Changes {
		if c.Path == "checks" || strings.HasPrefix(c.Path, "checks.") {
			suites = append(suites, c.Reason)
		}
	}
	if len(suites) > 0 {
		v.Citation["checks"] = strings.Join(suites, "; ")
	}
	// Who owns the service changed: ownerGate asks the team that owns it now.
	// Only the owner Pacto asks counts (Owner.DisplayString); a contacts-only
	// edit, or a DRI edit under an unchanged team, routes nothing elsewhere.
	if old, nu := from.Bundle.Contract.Service.Owner.DisplayString(), to.Bundle.Contract.Service.Owner.DisplayString(); old != nu {
		var owner []string
		for _, c := range d.Changes {
			if strings.HasPrefix(c.Path, "service.owner") {
				owner = append(owner, c.Path)
			}
		}
		v.Citation["owner"] = strings.Join(owner, ", ")
	}
	v.Message = classWords(d.Classification.String()) + ": " + countOf(len(d.Changes), "contract change")
	return v
}

// impactGate projects the change onto the graph. The floor follows the design:
// an incompatible consumer asks that consumer's owners; anything Pacto cannot
// see (partial graph, unknown confidence, an unknown range on a change that is
// not plainly non-breaking, an unverified rollback target) asks a person to
// accept the unknown; otherwise no person is needed.
func impactGate(ctx context.Context, in PlanInput, p *Plan) {
	old := in.From.Bundle
	if old == nil { // first version: compare it with itself, so only ranges speak
		old = in.To.Bundle
	}
	res := impact.Analyze(ctx, old.Contract, in.To.Bundle.Contract, old.FS, in.To.Bundle.FS, in.Snapshot,
		impact.Options{Domain: contract.OCIDomain(in.To.ContractRef), IncludeObserved: true})
	resJSON, _ := json.Marshal(res)
	p.ImpactDigest = sha256Hex(resJSON)
	v := Verdict{Gate: GateImpact, Phase: "pre", Result: Pass, Citation: map[string]string{
		"impact":         p.ImpactDigest,
		"classification": res.Classification,
		"completeness":   string(res.Completeness),
		"snapshot":       res.SnapshotID,
	}}

	incompatible, unmet, unknown := sortConsumers(res)
	citeCallers(in, res, v.Citation)
	unseenConsumers := unseenConsumersOf(in, res, unknown)
	unseen := slices.Clone(unseenConsumers)
	if len(res.Consumers) == 0 && res.Classification != diff.NonBreaking.String() && !in.Stage.Gates.Impact.DeclaredConsumersOnly {
		unseen = append(unseen, "no Pacto depends on "+in.Stage.Service.Name+", but a service with no contract can still call it")
	}
	unseen = append(unseen, versionDoubts(in, res)...)
	if in.From.Digest != "" && !slices.Contains(in.Status.Verified, in.From.Digest) {
		unseen = append(unseen, "the version a rollback would return to ("+short(in.From.Digest)+") was never verified here")
	}

	switch {
	case len(incompatible) > 0 || len(unmet) > 0:
		names, owners := consumerNamesOwners(append(slices.Clone(incompatible), unmet...))
		if len(owners) == 0 {
			owners = serviceTeam(in)
		}
		v.Message = "breaks " + strings.Join(names, ", ")
		p.Floor = Floor{Human: true, Decision: Approve, Owners: owners, Consumers: names}
		p.Question = breaksQuestion(in.Stage.Service.Name+" "+res.NewVersion, incompatible, unmet) +
			" Approve shipping it anyway? Asked of: " + ownersOrAnyone(owners, in.RequireOwnerTeam)
		if len(incompatible) > 0 {
			p.Order = orderFor(ctx, in, res, incompatible, unseenConsumers)
			p.Question += orderQuestion(p.Order)
		}
	case len(unseen) > 0:
		v.Result = Unknown
		v.Message = strings.Join(unseen, "; ")
		p.Floor = Floor{Human: true, Decision: AcceptUnknown}
		p.Question = "Pacto cannot see enough to clear " + in.Stage.Service.Name + " " + res.NewVersion + ": " + v.Message + ". Accept the unknown?"
	default:
		v.Message = countOf(len(res.Consumers), "consumer") + ", none incompatible"
	}
	p.Verdicts = append(p.Verdicts, v)
}

// sortConsumers splits the consumers the change reaches: those whose range
// does not accept it, those it leaves with a required guarantee unmet and
// those Pacto cannot judge. Like a range, an unmet guarantee is a confirmed
// contradiction wherever the consumer runs, including where Pacto cannot
// place it.
func sortConsumers(res *impact.Result) (incompatible, unmet, unknown []impact.AffectedConsumer) {
	for _, c := range res.Consumers {
		switch {
		case c.CompatibilityVerdict == impact.CompatibilityIncompatible:
			incompatible = append(incompatible, c)
		case unmetRequired(c):
			unmet = append(unmet, c)
		case unknownConsumer(res, c):
			unknown = append(unknown, c)
		}
	}
	return incompatible, unmet, unknown
}

// versionDoubts are why the candidate, against the running version, cannot be
// judged by consumer ranges: a bump smaller than its change needs, or a
// contract moved to another domain (the impact is keyed on the candidate's
// domain, which its author chooses, so consumers declared against the old one
// would drop out). None for a first version.
func versionDoubts(in PlanInput, res *impact.Result) []string {
	if in.From.Digest == "" {
		return nil
	}
	var out []string
	if why := bumpRefusal(res); why != "" {
		out = append(out, why)
	}
	if a, b := contract.OCIDomain(in.From.ContractRef), contract.OCIDomain(in.To.ContractRef); a != b {
		out = append(out, fmt.Sprintf("the contract moved from %q to %q, so consumers that depend on it in %q are not counted", a, b, a))
	}
	return out
}

// bumpRefusal says why the candidate's version cannot be trusted to speak for
// its change, or "": consumer ranges are judged against service.version,
// which the publisher chooses, and pacto publish checks the bump only on the
// publisher's machine. A breaking change published as a minor would pass every
// ^1 range, so the promoter checks the bump itself.
func bumpRefusal(res *impact.Result) string {
	if res.Classification == diff.NonBreaking.String() {
		return "" // any version, the same one included, carries a change that breaks no one
	}
	cls := diff.PotentialBreaking
	if res.Classification == diff.Breaking.String() {
		cls = diff.Breaking
	}
	need := diff.RequiredBump(cls, res.OldVersion)
	if err := diff.CheckBump(res.OldVersion, res.NewVersion, need); err != nil {
		return fmt.Sprintf("%s is %s but not a %s bump over %s, so consumer ranges cannot judge it (%v)",
			res.NewVersion, res.Classification, need, res.OldVersion, err)
	}
	return ""
}

// citeCallers cites what keeps autonomous from being proposed whatever the
// classification: runtime callers with no contract, and callers the graph
// cannot see.
func citeCallers(in PlanInput, res *impact.Result, citation map[string]string) {
	uncontracted := 0
	for _, c := range res.Consumers {
		if c.Uncontracted {
			uncontracted++
		}
	}
	if uncontracted > 0 {
		citation["uncontracted"] = strconv.Itoa(uncontracted)
	}
	if n := len(callersUnseen(in.Snapshot, fleet.NewServiceKeyDomain(contract.OCIDomain(in.To.ContractRef), in.Stage.Service.Name))); n > 0 {
		citation["callersUnseen"] = strconv.Itoa(n)
	}
}

// unknownConsumer reports whether Pacto cannot tell if the change breaks c:
// its evidence is unknown, or it is a direct consumer with no usable range of
// a change that is not plainly non-breaking.
func unknownConsumer(res *impact.Result, c impact.AffectedConsumer) bool {
	return c.Confidence == impact.ConfidenceUnknown ||
		c.Direct && c.CompatibilityVerdict == impact.CompatibilityUnknown && res.Classification != diff.NonBreaking.String()
}

// unseenConsumersOf says which consumers the impact gate cannot see: a
// partial graph, consumers with no usable range, dependencies the graph could
// not resolve and misplaced Pactos that are consumers of the service.
func unseenConsumersOf(in PlanInput, res *impact.Result, unknown []impact.AffectedConsumer) []string {
	var out []string
	if res.Completeness == fleet.CompletenessPartial {
		out = append(out, "the operational graph is partial ("+limitationCodes(res.Limitations)+")")
	}
	for _, c := range unknown {
		if c.Uncontracted {
			// ponytail: the node agent is delivery's only caller source today (design section 2.1).
			out = append(out, c.Service+" calls "+in.Stage.Service.Name+" at runtime (ebpf) but has no contract: run `"+
				InitClusterCommand(callerNamespace(c.Service))+"` for it, or declare its range")
			continue
		}
		out = append(out, c.Service+" has no usable compatibility range for this change")
	}
	if res.Classification != diff.NonBreaking.String() {
		out = append(out, callersUnseen(in.Snapshot, fleet.NewServiceKeyDomain(contract.OCIDomain(in.To.ContractRef), in.Stage.Service.Name))...)
	}
	out = append(out, unresolvedConsumers(in.Snapshot, in.Stage.Service.Name)...)
	consumers := map[string]bool{}
	for _, c := range res.Consumers {
		consumers[c.Service] = true
	}
	for _, r := range in.Snapshot.Relationships {
		if unresolvedTo(r, in.Stage.Service.Name) {
			_, name := fleet.ParseServiceKey(r.FromService) // a Misplaced names the bare service
			consumers[name] = true
		}
	}
	for _, m := range in.Misplaced {
		if m.Service == "" || consumers[m.Service] { // a Pacto with no contract cannot be ruled out
			out = append(out, m.Note)
		}
	}
	return out
}

// unresolvedConsumers names the services that declare a dependency on service
// the graph could not resolve to it: the graph resolves a dependency by name
// within the consumer's own domain, so a consumer published elsewhere (or
// declared inline) never shows up as a consumer at all. A consumer Pacto cannot
// place is a consumer it cannot rule out.
func unresolvedConsumers(snap *fleet.FleetSnapshot, service string) []string {
	var out []string
	for _, r := range snap.Relationships {
		msg := string(r.FromService) + " depends on " + service + " through " + r.RequestedRef +
			", which does not resolve to this service in its domain"
		if unresolvedTo(r, service) && !slices.Contains(out, msg) {
			out = append(out, msg)
		}
	}
	return out
}

// unresolvedTo reports whether r is a declared dependency on service the
// graph could not resolve.
func unresolvedTo(r fleet.Relationship, service string) bool {
	return r.Type == fleet.RelationshipDependency && r.Provenance == fleet.ProvenanceDeclared && !r.Resolved && !r.External && r.To == service
}

func consumerNamesOwners(cs []impact.AffectedConsumer) (names, owners []string) {
	for _, c := range cs {
		names = append(names, c.Service)
		if c.Owner != "" && !slices.Contains(owners, c.Owner) {
			owners = append(owners, c.Owner)
		}
	}
	slices.Sort(owners)
	return names, owners
}

func describeRanges(cs []impact.AffectedConsumer) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s (accepts %s)", c.Service, c.Compatibility))
	}
	return strings.Join(parts, ", ")
}

// ownersOrAnyone names who may answer, and says plainly when no team is
// declared: then any person may, or nobody but break-glass.
func ownersOrAnyone(owners []string, requireOwnerTeam bool) string {
	switch {
	case len(owners) > 0:
		return strings.Join(owners, ", ")
	case requireOwnerTeam:
		return "nobody. No team is declared and only an owner team may answer, so only break-glass can override. Declare service.owner"
	default:
		return "any person, because no team is declared. Declare service.owner to limit who may answer"
	}
}

func limitationCodes(ls []fleet.Limitation) string {
	var codes []string
	for _, l := range ls {
		if !slices.Contains(codes, l.Code) {
			codes = append(codes, l.Code)
		}
	}
	if len(codes) == 0 {
		return "a source is not available"
	}
	return strings.Join(codes, ", ")
}

// countOf is a count with its noun in the matching form: "1 consumer", "2 consumers".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// classWords names a diff classification in plain words for a message; the
// citation keeps the code.
func classWords(c string) string {
	switch c {
	case diff.NonBreaking.String():
		return "Non-breaking"
	case diff.PotentialBreaking.String():
		return "May break"
	case diff.Breaking.String():
		return "Breaking"
	}
	return c
}

// countText is countOf for a count a citation keeps as text; empty reads 0.
func countText(n, noun string) string {
	i, err := strconv.Atoi(n)
	if err != nil {
		return cmp.Or(n, "0") + " " + noun + "s"
	}
	return countOf(i, noun)
}

// short trims a digest for messages.
func short(d string) string {
	if d == "" {
		return "(none)"
	}
	if i := strings.IndexByte(d, ':'); i >= 0 && len(d) > i+13 {
		return d[:i+13]
	}
	return d
}

// StillPasses reports whether an approval given on prev still covers cur:
// every gate that passed still passes (a Pass that comes back empty is Unknown,
// and Unknown never passes), and cur asks for nothing the approval did not
// answer: it is routed to no team and names no consumer the approval was not
// asked about, and the impact gate is unknown for the reason the person saw.
func StillPasses(prev, cur Plan) bool {
	for _, v := range prev.Verdicts {
		if v.Result != Pass {
			continue
		}
		if !slices.ContainsFunc(cur.Verdicts, func(c Verdict) bool { return c.Gate == v.Gate && c.Result == Pass }) {
			return false
		}
	}
	if !cur.Floor.Human {
		return true
	}
	if !subset(cur.Floor.Owners, prev.Floor.Owners) || !subset(cur.Floor.Consumers, prev.Floor.Consumers) {
		return false
	}
	// ponytail: only the impact gate's message is compared, as it lists what
	// Pacto cannot see; other gates' Unknown messages carry error text that
	// changes from one read to the next.
	for _, v := range cur.Verdicts {
		if v.Gate == GateImpact && v.Result == Unknown &&
			!slices.ContainsFunc(prev.Verdicts, func(p Verdict) bool {
				return p.Gate == v.Gate && p.Result == v.Result && p.Message == v.Message
			}) {
			return false
		}
	}
	return cur.Floor.Decision == prev.Floor.Decision || prev.Floor.Decision == Approve
}

// subset reports whether every element of a is in b.
func subset(a, b []string) bool {
	return !slices.ContainsFunc(a, func(x string) bool { return !slices.Contains(b, x) })
}
