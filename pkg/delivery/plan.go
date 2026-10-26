package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/diff"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
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
	rejected := ""
	switch {
	case !MayRaise(in.Spec.Type, in.Class, in.Spec.Witnessed):
		rejected = fmt.Sprintf("NotPermitted: a %s actor may not raise %s", in.Class, in.Spec.Type)
	case in.Spec.Facet == FacetPointer && in.Spec.Params.From != in.Status.Pointer:
		rejected = fmt.Sprintf("PointerMoved: from is %q but the Stage runs %q", in.Spec.Params.From, in.Status.Pointer)
	case in.Spec.Facet == FacetRung && Rung(in.Spec.Params.From) != CurrentRung(in.Stage, in.Status):
		rejected = fmt.Sprintf("RungMoved: from is %q but the Stage is on %q", in.Spec.Params.From, CurrentRung(in.Stage, in.Status))
	case in.Spec.Type == TypePromote && CurrentRung(in.Stage, in.Status) == RungObserve:
		rejected = "Observing: " + in.Spec.Stage + " observes, so GitOps owns the applyRef; propose " + TypeAutonomy + " to advise first"
	case in.Spec.Type == TypePromote && slices.Contains(in.Status.FailedDigests, in.Spec.Params.To):
		rejected = "FailedDigest: " + in.Spec.Params.To + " is one of the last 50 versions that failed on this Stage, which are never promoted again"
	}
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
		rungPromote(in, &p)
		routeToServiceTeam(in, &p)
	case TypeAutonomy:
		autonomyGate(in, &p)
		routeToServiceTeam(in, &p)
	}
	holdForRecords(in, &p)
	p.Predicted = predict(in, &p)
	return p
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

// serviceTeam is the owner team the Stage's service declares, the running
// contract first; nil when neither side declares one.
func serviceTeam(in PlanInput) []string {
	for _, r := range []Revision{in.From, in.To} {
		if r.Bundle != nil && r.Bundle.Contract != nil {
			if team := r.Bundle.Contract.Service.Owner.DisplayString(); team != "" {
				return []string{team}
			}
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
		v.Message = "this Stage names no candidate source, so nothing it could promote was verified as published by pacto publish"
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
		v.Message = short(to) + " failed after release on the upstream Stage " + v.Citation["upstream"] + ", so it is not a candidate here"
	default:
		v.Message = short(to) + " is not offered by the upstream Stage " + v.Citation["upstream"] +
			": it never passed pacto.published there or never verified, so it is not a candidate here"
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
	v.Message = fmt.Sprintf("%s: %d contract changes", d.Classification, len(d.Changes))
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

	var incompatible, unknown []impact.AffectedConsumer
	for _, c := range res.Consumers {
		switch {
		case c.CompatibilityVerdict == impact.CompatibilityIncompatible:
			incompatible = append(incompatible, c)
		case unknownConsumer(res, c):
			unknown = append(unknown, c)
		}
	}
	// What keeps autonomous from being proposed whatever the classification:
	// runtime callers with no contract, and callers the graph cannot see.
	uncontracted := 0
	for _, c := range res.Consumers {
		if c.Uncontracted {
			uncontracted++
		}
	}
	if uncontracted > 0 {
		v.Citation["uncontracted"] = strconv.Itoa(uncontracted)
	}
	if n := len(callersUnseen(in.Snapshot, fleet.NewServiceKeyDomain(contract.OCIDomain(in.To.ContractRef), in.Stage.Service.Name))); n > 0 {
		v.Citation["callersUnseen"] = strconv.Itoa(n)
	}
	unseenConsumers := unseenConsumersOf(in, res, unknown)
	unseen := slices.Clone(unseenConsumers)
	if len(res.Consumers) == 0 && res.Classification != diff.NonBreaking.String() && !in.Stage.Gates.Impact.DeclaredConsumersOnly {
		unseen = append(unseen, "no Pacto depends on "+in.Stage.Service.Name+", but a service with no contract can still call it")
	}
	if in.From.Digest != "" && !slices.Contains(in.Status.Verified, in.From.Digest) {
		unseen = append(unseen, "the version a rollback would return to ("+short(in.From.Digest)+") was never verified here")
	}

	switch {
	case len(incompatible) > 0:
		names, owners := consumerNamesOwners(incompatible)
		if len(owners) == 0 {
			owners = serviceTeam(in)
		}
		v.Message = "breaks " + strings.Join(names, ", ")
		p.Floor = Floor{Human: true, Decision: Approve, Owners: owners, Consumers: names}
		p.Question = fmt.Sprintf("%s %s is not accepted by %s. Approve shipping it anyway? Asked of: %s",
			in.Stage.Service.Name, res.NewVersion, describeRanges(incompatible), ownersOrAnyone(owners, in.RequireOwnerTeam))
		p.Order = orderFor(ctx, in, res, incompatible, unseenConsumers)
		p.Question += orderQuestion(p.Order)
	case len(unseen) > 0:
		v.Result = Unknown
		v.Message = strings.Join(unseen, "; ")
		p.Floor = Floor{Human: true, Decision: AcceptUnknown}
		p.Question = "Pacto cannot see enough to clear " + in.Stage.Service.Name + " " + res.NewVersion + ": " + v.Message + ". Accept the unknown?"
	default:
		v.Message = fmt.Sprintf("%d consumers, none incompatible", len(res.Consumers))
	}
	p.Verdicts = append(p.Verdicts, v)
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
	return r.Type == fleet.RelationshipDependency && r.Provenance == fleet.ProvenanceDeclared && !r.Resolved && r.To == service
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
