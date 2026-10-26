// Package story is "the payments story": the one scenario every Pacto delivery
// demo plays (browser, Compose and Kubernetes). It holds real contracts for
// every service and version in the story, a plain service that is not
// pactified yet, and the Beats table: each beat's input and the outcome
// Pacto's own planning code gives for it. story_test.go proves every outcome
// with that code, so a demo only ever shows what the test proved.
//
// It imports only wasm-safe packages (no registry client, no Kubernetes), so
// the browser demo can import it.
package story

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/diff"
)

// Files holds contracts/<service>/<version>/ (real pactoVersion 2.1 bundles
// with a publish section) and plain/fraud/ (a service with a Dockerfile and
// Kubernetes manifests and no pacto.yaml: the starting point of the pactify act).
//
//go:embed all:contracts all:plain
var Files embed.FS

// Domain is the registry scope every story contract is published under. A
// demo that runs its own registry rewrites only this host.
const Domain = "registry.example/story"

// The two environments, in promotion order. Each service has one Stage per
// environment, named after the service in a namespace named after the
// environment; prod takes its versions from the test Stage.
const (
	Test = "test"
	Prod = "prod"
)

// Now is the story's fixed clock.
var Now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

// Persona is someone who acts in the story.
type Persona struct {
	Name   string
	User   string
	Groups []string
}

// The personas. pay-agent is an agent working for a developer; alice is on
// team payments and is never asked; carol is on team orders and is asked;
// promoter is the delivery controller itself.
var (
	PayAgent = Persona{Name: "pay-agent", User: "system:serviceaccount:agents:pay-agent"}
	Alice    = Persona{Name: "alice", User: "alice@example.com", Groups: []string{"team-payments"}}
	Carol    = Persona{Name: "carol", User: "carol@example.com", Groups: []string{"team-orders"}}
	Promoter = Persona{Name: "promoter", User: "system:serviceaccount:pacto-system:pacto-operator-delivery"}
)

// Policy maps the personas to actor classes and contract teams to groups:
// a member of team-orders belongs to team orders.
var Policy = delivery.ClassPolicy{ControllerUser: Promoter.User, TeamGroupPrefix: "team-"}

// Class is the actor class Policy gives p.
func (p Persona) Class() delivery.ActorClass {
	return Policy.Classify(delivery.Identity{User: p.User, Groups: p.Groups})
}

// Beat is one step of the story: what is asked and what Pacto answers.
type Beat struct {
	ID string
	// Moment is the step of the five-moment guided path this beat is (1
	// pactify, 2 a safe change ships on its own, 3 a breaking change asks only
	// the affected team, 4 a bad release is caught and rolled back, 5 every
	// step is a signed record), or 0 for "explore more". Demos play beats in
	// table order, guided or not, because each beat starts where the last one
	// left off; so the guided path shows moment 4 (B2) before moment 3 (B5).
	Moment int
	// Says is the one sentence a demo shows; Why says why it works.
	Says, Why string

	// Service and Environment name the Stage the operation is for.
	Service, Environment string
	// Type is the operation type; empty means promote.
	Type string
	// Actor raises the operation.
	Actor Persona
	// From and To are versions of Service; From empty is an empty Stage, To
	// empty means the beat plans nothing (its outcome is a verify verdict).
	From, To string
	// Base is the last published version the version rule checks To and
	// Tried against; empty means From.
	Base string
	// Tried is a version someone tried to publish instead of To.
	Tried string
	// Running is what else runs in Environment: service to version.
	Running map[string]string
	// Verified is what each consumer's test Stage verified, oldest first:
	// where a version that can move first comes from.
	Verified map[string][]string
	// Failed marks To as already failed on this Stage; Unpublished makes To a
	// copy pushed with plain oras, so pacto publish never published it;
	// Frozen freezes the Stage.
	Failed, Unpublished, Frozen bool

	Expect Outcome
}

// Outcome is what Pacto answers for a beat.
type Outcome struct {
	// Classification is the contract diff from Base (or From) to To.
	Classification string
	// RequiredBump is the smallest bump that classification needs.
	RequiredBump diff.Bump
	// Refused is Tried when the version rule refuses it.
	Refused string
	// Predicted is what the plan expects the promoter to do.
	Predicted delivery.Prediction
	// Refusal names what rejected the operation: a conflict code or a gate.
	Refusal string
	// Decision is what a person is asked for, empty when nobody is asked.
	Decision delivery.Decision
	// Asked are the teams asked, the only ones whose members may answer.
	Asked []string
	// MoveFirst are the consumer versions that can ship first ("checkout 1.9.0").
	MoveFirst []string
	// Verify is the verify gate's verdict after the version ships, when the
	// beat is about one.
	Verify delivery.Result
	// Concluded is "outcome/verification" for an operation the controller
	// concludes without planning it (an unfreeze), so Predicted stays empty:
	// the operator plans no freeze-facet operation
	// (TestFrozenPromoteWaitsAndUnfreeze).
	Concluded string
}

var (
	before3 = map[string]string{"checkout": "1.8.0", "orders": "1.4.0"}
	after6  = map[string]string{"checkout": "1.9.0", "orders": "1.4.0"}
	inTest  = map[string][]string{"checkout": {"1.8.0", "1.9.0"}, "orders": {"1.4.0"}}
)

// Beats is the story, in the order every demo plays it.
var Beats = []Beat{
	// The pactify input, contracts/fraud/1.0.0, is what pacto init writes in
	// plain/fraud (TestPactifyWritesTheFraudContract), and a first version
	// ships with nobody.
	{
		ID: "pactify", Moment: 1,
		Says:    "pacto init reads fraud's Dockerfile and manifests and writes its contract, and its first version ships to test with nobody involved.",
		Why:     "Nothing depends on fraud yet, so its first version breaks no one.",
		Service: "fraud", Environment: Test, Actor: PayAgent, To: "1.0.0",
		Expect: Outcome{Predicted: delivery.PredictProceeds},
	},
	{
		ID: "B1", Moment: 2,
		Says:    "payments 2.4.0 adds an endpoint and ships to prod with nobody involved.",
		Why:     "checkout and orders declared they accept payments ^2, and 2.4.0 is inside that range.",
		Service: "payments", Environment: Prod, Actor: PayAgent, From: "2.3.0", To: "2.4.0", Running: before3,
		Expect: Outcome{Classification: "NON_BREAKING", RequiredBump: diff.BumpPatch, Predicted: delivery.PredictProceeds},
	},
	// B2: the story proves the rollback is bound to the promote it undoes and
	// that a digest already in FailedDigests is refused (the test sets Failed
	// by hand). That a failed health check puts 2.4.0 there and raises the
	// rollback is proven by the operator (TestRecheckAfterRelease and
	// TestFailedVerifyRollsBackOnceAndNeverRetries).
	{
		ID: "B2", Moment: 4,
		Says:    "The prod error ratio rises after 2.4.0, the health check fails and payments rolls back to 2.3.0 on its own.",
		Why:     "The check reads the service's own traces and cites the query, and 2.4.0 is never promoted on that Stage again.",
		Service: "payments", Environment: Prod, Type: delivery.TypeRollback, Actor: Promoter, From: "2.4.0", To: "2.3.0",
		Expect: Outcome{Predicted: delivery.PredictProceeds, Verify: delivery.Fail},
	},
	// B3: the story proves that a failed 2.4.2 is refused on test again and
	// that prod refuses a version its test Stage never offered. That the
	// claim gate fails 2.4.2 and records it as failed is proven by the
	// operator (TestRuntimeClaimGate and
	// TestFailedVerifyRollsBackOnceAndNeverRetries).
	{
		ID:      "B3",
		Says:    "payments 2.4.2 calls fraud, which its contract never declares, so it fails in test and never reaches prod.",
		Why:     "The node agent saw a call the contract does not name, and a seen contradiction is a failure.",
		Service: "payments", Environment: Test, Actor: PayAgent, From: "2.4.0", To: "2.4.2",
		Expect: Outcome{Classification: "NON_BREAKING", RequiredBump: diff.BumpPatch, Predicted: delivery.PredictProceeds, Verify: delivery.Fail},
	},
	{
		ID:      "B4",
		Says:    "The fix removes a field, so publishing it as 2.4.3 is refused; 3.0.0 is published signed and a copy pushed with plain oras is never offered.",
		Why:     "Removing a field is breaking, a breaking change needs a major bump, and only what pacto publish signed is a candidate.",
		Service: "payments", Environment: Test, Actor: PayAgent, From: "2.4.0", To: "3.0.0", Base: "2.4.2", Tried: "2.4.3", Unpublished: true,
		Expect: Outcome{Classification: "BREAKING", RequiredBump: diff.BumpMajor, Refused: "2.4.3",
			Predicted: delivery.PredictRejected, Refusal: delivery.GatePublished},
	},
	{
		ID: "B5", Moment: 3,
		Says:    "payments 3.0.0 is held for prod: only checkout and orders are asked, and checkout 1.9 can ship first.",
		Why:     "checkout 1.8 and orders 1.4 accept only payments ^2, and checkout 1.9, already verified in test, accepts ^2 || ^3.",
		Service: "payments", Environment: Prod, Actor: PayAgent, From: "2.3.0", To: "3.0.0", Running: before3, Verified: inTest,
		Expect: Outcome{Classification: "BREAKING", RequiredBump: diff.BumpMajor, Predicted: delivery.PredictHeld,
			Decision: delivery.Approve, Asked: []string{"checkout", "orders"}, MoveFirst: []string{"checkout 1.9.0"}},
	},
	// B6 plans 3.0.0 again once checkout 1.9 runs in prod. On Kubernetes the
	// operation held in B5 keeps the owners it was admitted with until the
	// delivery logic re-asks only the teams still affected (ledger C); until
	// then the demo cancels and proposes 3.0.0 again. This outcome is the plan,
	// which is what both paths ask.
	{
		ID:      "B6",
		Says:    "checkout 1.9 ships with nobody involved, and planning 3.0.0 again now asks only orders.",
		Why:     "checkout 1.9 accepts payments ^2 || ^3, so the only consumer left that does not accept 3.0.0 is orders 1.4.",
		Service: "payments", Environment: Prod, Actor: PayAgent, From: "2.3.0", To: "3.0.0", Running: after6, Verified: inTest,
		Expect: Outcome{Classification: "BREAKING", RequiredBump: diff.BumpMajor, Predicted: delivery.PredictHeld,
			Decision: delivery.Approve, Asked: []string{"orders"}},
	},
	// B7: the story proves only who may decide. That carol's approval counts
	// only with a fresh sign-in is proven by the operator
	// (TestBreakingChangeWaitsForTheConsumersTeam, its stale-login approval).
	{
		ID:      "B7",
		Says:    "pay-agent cannot approve, alice cannot either, and carol signs in again and approves 3.0.0.",
		Why:     "Only a person on the asked team may decide, and an agent is never a person.",
		Service: "payments", Environment: Prod, Actor: PayAgent, From: "2.3.0", To: "3.0.0", Running: after6, Verified: inTest,
		Expect: Outcome{Classification: "BREAKING", RequiredBump: diff.BumpMajor, Predicted: delivery.PredictHeld,
			Decision: delivery.Approve, Asked: []string{"orders"}},
	},
	// B8: the story proves only that Mira says Unknown on too little
	// telemetry. That the Stage waits and an accepted unknown is recorded as
	// accepted is proven by the operator (TestVerifyUnknownHoldsAfterApply)
	// and pkg/delivery's record tests.
	{
		ID:      "B8",
		Says:    "Too little telemetry arrives in the verify window, so the check says Unknown and the Stage waits until carol accepts the unknown.",
		Why:     "I don't know is never yes: an accepted unknown is recorded as accepted, never as a pass.",
		Service: "payments", Environment: Prod,
		Expect: Outcome{Verify: delivery.Unknown},
	},
	{
		ID: "B9", Moment: 5,
		Says:    "carol rolls 3.0.0 back, automation holds off, and every step so far reads back from signed records.",
		Why:     "A person's rollback sets an auto-hold so automation never undoes the choice, and each decision is a signed record.",
		Service: "payments", Environment: Prod, Type: delivery.TypeRollback, Actor: Carol, From: "3.0.0", To: "2.3.0",
		Expect: Outcome{Predicted: delivery.PredictProceeds},
	},
	// B10: the story starts from a Stage already frozen and proves that an
	// agent cannot unfreeze it, a person can and a frozen Stage ships nothing.
	// That a stream whose newest record went missing freezes the Stage is
	// proven by the operator (TestStreamConflictFreezesTheStage).
	{
		ID:      "B10",
		Says:    "A missing record freezes payments in prod until alice unfreezes it.",
		Why:     "A frozen Stage ships nothing automatically, and only a person may unfreeze it.",
		Service: "payments", Environment: Prod, Type: delivery.TypeUnfreeze, Actor: Alice, Frozen: true,
		Expect: Outcome{Concluded: "Succeeded/Unverified"},
	},
	// agent: pay-agent is a service too. Its contract declares its tools, its
	// model and its eval suite, and its test Stage checks it with an eval
	// runner. 1.1.0 swaps the model, which needs at least a minor version.
	{
		ID:      "agent",
		Says:    "pay-agent 1.1.0 swaps its model, passes only 1 of its 2 evals in test, and rolls back without reaching prod.",
		Why:     "An agent ships like any other service, and its eval suite is the check after release.",
		Service: "pay-agent", Environment: Test, Actor: PayAgent, From: "1.0.0", To: "1.1.0",
		Expect: Outcome{Classification: "POTENTIAL_BREAKING", RequiredBump: diff.BumpMinor, Predicted: delivery.PredictProceeds, Verify: delivery.Fail},
	},
}

// Bundle reads the story contract of service at version.
func Bundle(service, version string) (*contract.Bundle, error) {
	sub, err := fs.Sub(Files, path.Join("contracts", service, version))
	if err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(sub, "pacto.yaml")
	if err != nil {
		return nil, fmt.Errorf("%s %s is not in the story: %w", service, version, err)
	}
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", service, version, err)
	}
	return &contract.Bundle{Contract: c, RawYAML: raw, FS: sub}, nil
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ArtifactDigest stands in for the digest of the deploy artifact pacto
// publish pushes for service at version. A real registry gives other digests;
// the story compares versions and teams, never digests.
func ArtifactDigest(service, version string) string {
	if version == "" {
		return ""
	}
	return sha("manifests " + service + "@" + version)
}

// orasCopy is the digest of a copy of service at version pushed with plain oras.
func orasCopy(service, version string) string { return sha("oras copy " + service + "@" + version) }

// Revision is service at version as a Stage sees it: its artifact digest and
// the contract it names, pinned by the digest of its pacto.yaml.
func Revision(service, version string) (delivery.Revision, error) {
	if version == "" {
		return delivery.Revision{}, nil
	}
	b, err := Bundle(service, version)
	if err != nil {
		return delivery.Revision{}, err
	}
	return delivery.Revision{Digest: ArtifactDigest(service, version),
		ContractRef: "oci://" + Domain + "/" + service + "@" + sha(string(b.RawYAML)), Bundle: b}, nil
}

// pacto is the Pacto object that checks service at version in env, as the
// operator reports it once reconciled.
func pacto(env string, r delivery.Revision) delivery.PactoView {
	c := r.Bundle.Contract
	var p delivery.PactoView
	p.Metadata.Namespace, p.Metadata.Name = env, c.Service.Name
	p.Status.ContractStatus = "Compliant"
	p.Status.LastReconciledAt = Now.Format(time.RFC3339)
	owner := c.Service.Owner
	p.Status.Contract = &delivery.ContractInfoView{ServiceName: c.Service.Name, Version: c.Service.Version,
		Owner: &owner, ResolvedRef: r.ContractRef}
	p.Status.EvaluatedRevision = &delivery.EvaluatedView{ContractDigest: delivery.ContractDigest(r.ContractRef)}
	for _, d := range c.Dependencies {
		p.Status.Dependencies = append(p.Status.Dependencies, delivery.DependencyView(d))
	}
	return p
}

func stage(service, env string) delivery.StageSpec {
	s := delivery.StageSpec{Service: delivery.ServiceRef{Name: service}, Environment: env, ApplyRef: delivery.ApplyRef{Name: service}}
	s.Gates.Impact.DeclaredConsumersOnly = true // every service in the story has a contract
	if env == Test {
		s.Candidates.OCIRepositoryRef = &delivery.LocalRef{Name: service + "-candidates"}
	} else {
		s.Candidates.UpstreamStage = &delivery.StageRef{Name: service, Namespace: Test}
	}
	return s
}

// PlanInput is everything delivery.PlanFor reads for the beat, built from the
// story contracts. It errors for a beat that plans nothing.
func (b Beat) PlanInput(ctx context.Context) (delivery.PlanInput, error) {
	if b.To == "" && b.Type != delivery.TypeUnfreeze {
		return delivery.PlanInput{}, fmt.Errorf("beat %s plans nothing", b.ID)
	}
	from, err := Revision(b.Service, b.From)
	if err != nil {
		return delivery.PlanInput{}, err
	}
	to, err := Revision(b.Service, b.To)
	if err != nil {
		return delivery.PlanInput{}, err
	}
	if b.Unpublished {
		to.Digest = orasCopy(b.Service, b.To)
	}
	in := delivery.PlanInput{
		Spec: delivery.OperationSpec{Type: b.Type, Stage: b.Service, Facet: delivery.FacetPointer,
			Params: delivery.Params{From: from.Digest, To: to.Digest}, Actor: delivery.Actor{User: b.Actor.User}},
		Stage: stage(b.Service, b.Environment), From: from, To: to, Class: b.Actor.Class(),
		Status: delivery.StageStatus{Pointer: from.Digest, Frozen: b.Frozen},
	}
	switch b.Type {
	case "":
		in.Spec.Type = delivery.TypePromote
	case delivery.TypeUnfreeze:
		in.Spec.Facet, in.Spec.Params = delivery.FacetFreeze, delivery.Params{}
	}
	if b.Frozen {
		in.Status.FrozenReason = "a record of this Stage does not verify"
	}
	if from.Digest != "" {
		in.Status.Verified = []string{from.Digest}
	}
	if b.Failed {
		in.Status.FailedDigests = []string{to.Digest}
	}
	published := []string{ArtifactDigest(b.Service, b.To)} // what pacto publish signed
	if b.Environment == Test {
		in.Status.Published = published
	} else {
		in.Upstream = &delivery.StageStatus{Verified: published, Offered: published}
	}

	pactos, known, err := others(b, &in)
	if err != nil {
		return delivery.PlanInput{}, err
	}
	in.Candidates = func(_ context.Context, _ delivery.StageObject, d string) delivery.Revision {
		if r, ok := known[d]; ok {
			return r
		}
		return delivery.Revision{Digest: d, Err: "not a story version"}
	}
	in.Snapshot, err = delivery.Snapshot(ctx, pactos, nil, []delivery.Revision{from, to}, Now)
	return in, err
}

// others places the beat's other services: the Pacto of each one running in
// the beat's environment, its Stages in in.Stages and, by digest, the versions
// its test Stage verified.
func others(b Beat, in *delivery.PlanInput) ([]delivery.PactoView, map[string]delivery.Revision, error) {
	var pactos []delivery.PactoView
	known := map[string]delivery.Revision{}
	for _, svc := range services(b) {
		test := delivery.StageStatus{}
		for _, v := range b.Verified[svc] {
			r, err := Revision(svc, v)
			if err != nil {
				return nil, nil, err
			}
			known[r.Digest] = r
			test.Verified, test.Offered, test.Pointer = append(test.Verified, r.Digest), append(test.Offered, r.Digest), r.Digest
		}
		here := delivery.StageStatus{}
		if v := b.Running[svc]; v != "" {
			r, err := Revision(svc, v)
			if err != nil {
				return nil, nil, err
			}
			pactos = append(pactos, pacto(b.Environment, r))
			here.Pointer, here.Verified = r.Digest, []string{r.Digest}
		}
		if b.Environment == Test {
			test = here
		}
		in.Stages = append(in.Stages,
			delivery.StageObject{Metadata: delivery.ObjectMeta{Name: svc, Namespace: Test}, Spec: stage(svc, Test), Status: test})
		if b.Environment == Prod {
			in.Stages = append(in.Stages,
				delivery.StageObject{Metadata: delivery.ObjectMeta{Name: svc, Namespace: Prod}, Spec: stage(svc, Prod), Status: here})
		}
	}
	return pactos, known, nil
}

// services are the other services of the beat, sorted.
func services(b Beat) []string {
	var out []string
	for s := range b.Running {
		out = append(out, s)
	}
	for s := range b.Verified {
		out = append(out, s)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
