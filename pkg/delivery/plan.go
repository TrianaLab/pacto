package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
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
	// Upstream is the upstream Stage's status when the Stage takes its
	// candidates from one; nil when it could not be read.
	Upstream *StageStatus
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
	rejected := ""
	switch {
	case !MayRaise(in.Spec.Type, in.Class, in.Spec.Witnessed):
		rejected = fmt.Sprintf("NotPermitted: a %s actor may not raise %s", in.Class, in.Spec.Type)
	case in.Spec.Facet == FacetPointer && in.Spec.Params.From != in.Status.Pointer:
		rejected = fmt.Sprintf("PointerMoved: from is %q but the Stage runs %q", in.Spec.Params.From, in.Status.Pointer)
	case in.Spec.Type == TypePromote && slices.Contains(in.Status.FailedDigests, in.Spec.Params.To):
		rejected = "FailedDigest: " + in.Spec.Params.To + " already failed on this Stage and is never promoted again"
	}
	if rejected != "" {
		p.Conflicts = append(p.Conflicts, rejected)
		p.Predicted = PredictRejected
		return p
	}
	if in.Spec.Type == TypePromote {
		promoteGates(ctx, in, &p)
		if in.Stage.Approval.Always && !p.Floor.Human {
			p.Floor = Floor{Human: true, Decision: Approve}
			p.Question = "This Stage asks a person to approve every promote. Approve " + short(p.To) + "?"
		}
		routeToServiceTeam(in, &p)
	}
	p.Predicted = predict(in, &p)
	return p
}

// routeToServiceTeam sends a question that names no consumer's owner to the
// team of the Stage's own service, the one its contract declares. It is set
// even when nothing is asked yet: a version that stays Unknown after it shipped
// is held for that same team. Only the named teams may answer (the promoter
// ignores anyone else); with no team declared anywhere, any person may.
func routeToServiceTeam(in PlanInput, p *Plan) {
	if len(p.Floor.Owners) > 0 {
		return
	}
	for _, r := range []Revision{in.To, in.From} {
		if r.Bundle != nil && r.Bundle.Contract != nil {
			if team := r.Bundle.Contract.Service.Owner.DisplayString(); team != "" {
				p.Floor.Owners = []string{team}
				break
			}
		}
	}
	if p.Floor.Human && !strings.Contains(p.Question, "Asked of: ") {
		p.Question += " Asked of: " + ownersOrAnyone(p.Floor.Owners)
	}
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
	to, c := in.Spec.Params.To, in.Stage.Candidates
	switch {
	case c.UpstreamStage != nil:
		v.Citation["upstream"] = c.UpstreamStage.Namespace + "/" + c.UpstreamStage.Name
		switch {
		case in.Upstream == nil:
			v.Message = "the upstream Stage " + v.Citation["upstream"] + " does not exist or cannot be read, so it offers no candidate"
		case slices.Contains(in.Upstream.Verified, to) && slices.Contains(in.Upstream.Offered, to):
			v.Result, v.Message = Pass, short(to)+" passed pacto.published and verified on the upstream Stage"
		default:
			v.Message = short(to) + " is not offered by the upstream Stage " + v.Citation["upstream"] +
				": it never passed pacto.published there or never verified, so it is not a candidate here"
		}
	case c.OCIRepositoryRef != nil:
		v.Citation["source"] = c.OCIRepositoryRef.Name
		switch {
		case !slices.Contains(in.Status.Published, to):
			v.Message = "Flux never verified a signature of " + short(to) + " on the candidate OCIRepository " + c.OCIRepositoryRef.Name +
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
	default:
		v.Message = "this Stage names no candidate source, so nothing it could promote was verified as published by pacto publish"
	}
	return v
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
		impact.Options{Domain: contract.OCIDomain(in.To.ContractRef)})
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
		case c.Confidence == impact.ConfidenceUnknown,
			c.Direct && c.CompatibilityVerdict == impact.CompatibilityUnknown && res.Classification != diff.NonBreaking.String():
			unknown = append(unknown, c)
		}
	}
	var unseenConsumers []string
	if res.Completeness == fleet.CompletenessPartial {
		unseenConsumers = append(unseenConsumers, "the operational graph is partial ("+limitationCodes(res.Limitations)+")")
	}
	for _, c := range unknown {
		unseenConsumers = append(unseenConsumers, c.Service+" has no usable compatibility range for this change")
	}
	unseenConsumers = append(unseenConsumers, unresolvedConsumers(in.Snapshot, in.Stage.Service.Name)...)
	consumers := map[string]bool{}
	for _, c := range res.Consumers {
		consumers[c.Service] = true
	}
	for _, r := range in.Snapshot.Relationships {
		if unresolvedTo(r, in.Stage.Service.Name) {
			consumers[string(r.FromService)] = true
		}
	}
	for _, m := range in.Misplaced {
		if m.Service == "" || consumers[m.Service] { // a Pacto with no contract cannot be ruled out
			unseenConsumers = append(unseenConsumers, m.Note)
		}
	}
	unseen := slices.Clone(unseenConsumers)
	if in.From.Digest != "" && !slices.Contains(in.Status.Verified, in.From.Digest) {
		unseen = append(unseen, "the version a rollback would return to ("+short(in.From.Digest)+") was never verified here")
	}

	switch {
	case len(incompatible) > 0:
		names, owners := consumerNamesOwners(incompatible)
		v.Message = "breaks " + strings.Join(names, ", ")
		p.Floor = Floor{Human: true, Decision: Approve, Owners: owners, Consumers: names}
		p.Question = fmt.Sprintf("%s %s is not accepted by %s. Approve shipping it anyway? Asked of: %s",
			in.Stage.Service.Name, res.NewVersion, describeRanges(incompatible), ownersOrAnyone(owners))
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

func ownersOrAnyone(owners []string) string {
	if len(owners) == 0 {
		return "any person (no team is declared)"
	}
	return strings.Join(owners, ", ")
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
