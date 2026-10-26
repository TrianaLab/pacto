package delivery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const (
	dA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	c1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	c2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	c3 = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

var now = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func bundle(name, version string, deps ...contract.Dependency) *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: name, Version: version, Owner: contract.Owner{Team: "team-" + name}},
		Dependencies: deps,
	}}
}

func rev(digest, contractDigest string, b *contract.Bundle) Revision {
	return Revision{Digest: digest, ContractRef: "oci://reg.example/pactos/" + b.Contract.Service.Name + "@" + contractDigest, Bundle: b}
}

func checkoutPacto(rng string) PactoView {
	var p PactoView
	p.Metadata.Namespace, p.Metadata.Name = "prod", "checkout"
	p.Status.ContractStatus = "Compliant"
	p.Status.LastReconciledAt = now.Format(time.RFC3339)
	p.Status.Contract = &ContractInfoView{ServiceName: "checkout", Version: "1.0.0",
		Owner: &contract.Owner{Team: "team-checkout"}, ResolvedRef: "oci://reg.example/pactos/checkout@" + c3}
	p.Status.Dependencies = []DependencyView{{Name: "payments", Ref: "oci://reg.example/pactos/payments", Required: true, Compatibility: rng}}
	return p
}

func stageSpec() StageSpec {
	return StageSpec{Service: ServiceRef{Name: "payments"}, Environment: "prod", ApplyRef: ApplyRef{Name: "payments"},
		Candidates: Candidates{OCIRepositoryRef: &LocalRef{Name: "payments-candidates"}}}
}

func promote(from, to string) OperationSpec {
	return OperationSpec{Type: TypePromote, Stage: "payments-prod", Facet: FacetPointer, Params: Params{From: from, To: to},
		Actor: Actor{User: "system:serviceaccount:agents:bot"}}
}

func planWith(t *testing.T, spec OperationSpec, status StageStatus, from, to Revision, pactos ...PactoView) Plan {
	t.Helper()
	snap, err := Snapshot(context.Background(), pactos, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Every version these tests promote was published: Flux verified it.
	status.Published = append(status.Published, to.Digest)
	return autonomousPlan(PlanInput{Spec: spec, Stage: stageSpec(), Status: status, From: from, To: to,
		Snapshot: snap, Class: ClassAgent})
}

func verdict(t *testing.T, p Plan, gate string) Verdict {
	t.Helper()
	for _, v := range p.Verdicts {
		if v.Gate == gate {
			return v
		}
	}
	t.Fatalf("no %s verdict in %+v", gate, p.Verdicts)
	return Verdict{}
}

func TestPlanCompatibleChangeShipsWithNoPerson(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to, checkoutPacto("^1.0.0"))
	if p.Predicted != PredictProceeds || p.Floor.Human {
		t.Fatalf("want Proceeds with no person, got %+v", p)
	}
	if v := verdict(t, p, GateImpact); v.Result != Pass || v.Citation["impact"] != p.ImpactDigest || p.ImpactDigest == "" {
		t.Fatalf("impact verdict %+v (digest %q)", v, p.ImpactDigest)
	}
	if p.IntentDigest != IntentDigest(promote(dA, dB), stageSpec()) {
		t.Fatal("plan must bind to the intent digest")
	}
	if p.ToVersion != "1.1.0" {
		t.Fatalf("the plan names the contract version it moves to: %q", p.ToVersion)
	}
}

// TestPlanMisplacedConsumerAsksToAcceptTheUnknown: a Pacto labelled for an
// environment no Stage declares holds the change only while it may be a
// consumer of the service; a misplaced Pacto with nothing to do with it holds
// nothing.
func TestPlanMisplacedConsumerAsksToAcceptTheUnknown(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	inline := checkoutPacto("^1.0.0")
	inline.Status.Contract.ResolvedRef = ""
	elsewhere := checkoutPacto("^1.0.0") // published in another domain, so its key is domain-qualified
	elsewhere.Status.Contract.ResolvedRef = "oci://other.example/pactos/checkout@" + c3
	const note = `Pacto prod/h is labelled for environment "production", which no Stage declares`
	for _, c := range []struct {
		name, service string
		pacto         PactoView
		holds         bool
	}{
		{"a resolved consumer", "checkout", checkoutPacto("^1.0.0"), true},
		{"a consumer the graph cannot resolve", "checkout", inline, true},
		{"an unresolved consumer published in another domain", "checkout", elsewhere, true},
		{"a Pacto with no contract", "", checkoutPacto("^1.0.0"), true},
		{"a Pacto that does not depend on the service", "sandbox-app", checkoutPacto("^1.0.0"), false},
	} {
		snap, err := Snapshot(context.Background(), []PactoView{c.pacto}, nil, []Revision{from, to}, now)
		if err != nil {
			t.Fatal(err)
		}
		p := autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(),
			Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}}, From: from, To: to,
			Snapshot: snap, Class: ClassAgent, Misplaced: []Misplaced{{Service: c.service, Note: note}}})
		v := verdict(t, p, GateImpact)
		if held := strings.Contains(p.Question, `"production"`); held != c.holds || c.holds && (v.Result != Unknown || p.Floor.Decision != AcceptUnknown) ||
			!c.holds && v.Result != Pass {
			t.Fatalf("%s: holds=%v, got %+v", c.name, c.holds, p)
		}
	}
}

func TestPlanNoConsumersHoldsABreakingChangeUnlessTheGraphIsDeclaredComplete(t *testing.T) {
	old, breaking := bundle("payments", "1.0.0"), bundle("payments", "2.0.0")
	old.Contract.Workload, breaking.Contract.Workload = "service", "job" // a modified workload is BREAKING
	from, to := rev(dA, c1, old), rev(dB, c2, breaking)
	snap, err := Snapshot(context.Background(), nil, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	in := PlanInput{Spec: promote(dA, dB), Stage: stageSpec(), Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		From: from, To: to, Snapshot: snap, Class: ClassAgent}
	p := autonomousPlan(in)
	if v := verdict(t, p, GateImpact); v.Result != Unknown || p.Floor.Decision != AcceptUnknown || !strings.Contains(v.Message, "no contract can still call it") {
		t.Fatalf("a breaking change no Pacto depends on can still break a service with no contract: %+v", p)
	}
	in.Stage.Gates.Impact.DeclaredConsumersOnly = true
	if p := autonomousPlan(in); p.Predicted != PredictProceeds {
		t.Fatalf("a Stage that declares its graph complete ships it: %+v", p)
	}
	in.Stage.Gates.Impact.DeclaredConsumersOnly = false
	in.To = rev(dB, c2, bundle("payments", "1.0.1"))
	if p := autonomousPlan(in); p.Predicted != PredictProceeds {
		t.Fatalf("a non-breaking change ships with no consumers: %+v", p)
	}
}

func TestPlanBreakingChangeAsksOnlyTheBrokenConsumersOwners(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "2.0.0"))
	other := checkoutPacto("^1.0.0")
	other.Metadata.Name = "ledger"
	other.Status.Contract = &ContractInfoView{ServiceName: "ledger", Version: "3.0.0", Owner: &contract.Owner{Team: "team-ledger"}}
	other.Status.Dependencies = []DependencyView{{Name: "payments", Ref: "oci://reg.example/pactos/payments", Required: true, Compatibility: ">=1.0.0"}}
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to, checkoutPacto("^1.0.0"), other)
	if p.Predicted != PredictHeld || p.Floor.Decision != Approve {
		t.Fatalf("want Held for approval, got %+v", p)
	}
	if strings.Join(p.Floor.Owners, ",") != "team-checkout" || strings.Join(p.Floor.Consumers, ",") != "checkout" {
		t.Fatalf("floor must name only checkout and its owners: %+v", p.Floor)
	}
	if !strings.Contains(p.Question, "team-checkout") || !strings.Contains(p.Question, "checkout (accepts ^1.0.0)") || strings.Contains(p.Question, "ledger") {
		t.Fatalf("question %q", p.Question)
	}
}

func TestPlanRoutesEveryOtherQuestionToTheServiceTeam(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to, checkoutPacto("^1.0.0"))
	if p.Floor.Human || strings.Join(p.Floor.Owners, ",") != "team-payments" || p.Question != "" {
		t.Fatalf("with no question asked, a later verify hold still goes to the service's team: %+v", p)
	}
	unread := Revision{Digest: dB, ContractRef: "oci://reg.example/pactos/payments@" + c2, Err: "pull failed"}
	p = planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, unread)
	if strings.Join(p.Floor.Owners, ",") != "team-payments" || !strings.HasSuffix(p.Question, "Asked of: team-payments") {
		t.Fatalf("an unknown asks the running version's team: %+v", p)
	}
	// The incoming version's author does not pick who approves it.
	hijack := bundle("payments", "1.1.0")
	hijack.Contract.Service.Owner = contract.Owner{Team: "system:authenticated"}
	p = planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, rev(dB, c2, hijack))
	if strings.Join(p.Floor.Owners, ",") != "team-payments" {
		t.Fatalf("the running version's team is asked, not the one the new version names: %+v", p.Floor)
	}
	anon := bundle("payments", "1.1.0")
	anon.Contract.Service.Owner = contract.Owner{}
	p = planWith(t, promote("", dB), StageStatus{}, Revision{}, Revision{Digest: dB, ContractRef: "oci://reg.example/pactos/payments@" + c2, Bundle: anon})
	if len(p.Floor.Owners) != 0 {
		t.Fatalf("no team declared anywhere: %+v", p)
	}
}

// TestPlanSaysWhoAnswersWithNoTeam: a breaking change whose consumer declares
// no team asks the service's team, never "any person" while owners name one;
// with no team anywhere the question warns who may answer.
func TestPlanSaysWhoAnswersWithNoTeam(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "2.0.0"))
	ownerless := checkoutPacto("^1.0.0")
	ownerless.Status.Contract.Owner = nil
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to, ownerless)
	if strings.Join(p.Floor.Owners, ",") != "team-payments" || !strings.Contains(p.Question, "Asked of: team-payments") {
		t.Fatalf("an ownerless consumer's break asks the service's team: %+v", p)
	}
	anon := bundle("payments", "1.1.0")
	anon.Contract.Service.Owner = contract.Owner{}
	stage := stageSpec()
	stage.Approval.Always = true
	first := Revision{Digest: dB, ContractRef: "oci://reg.example/pactos/payments@" + c2, Bundle: anon}
	snap, err := Snapshot(context.Background(), nil, nil, []Revision{first}, now)
	if err != nil {
		t.Fatal(err)
	}
	for require, want := range map[bool]string{false: "Asked of: any person", true: "Asked of: nobody"} {
		p := autonomousPlan(PlanInput{Spec: promote("", dB), Stage: stage, Status: StageStatus{Published: []string{dB}},
			To: first, Snapshot: snap, Class: ClassAgent, RequireOwnerTeam: require})
		if !p.Floor.Human || len(p.Floor.Owners) != 0 || !strings.Contains(p.Question, want) || !strings.Contains(p.Question, "service.owner") {
			t.Fatalf("require=%v: %+v", require, p)
		}
	}
}

func TestPlanUnreadableContractWaitsForAPerson(t *testing.T) {
	from := rev(dA, c1, bundle("payments", "1.0.0"))
	to := Revision{Digest: dB, ContractRef: "oci://reg.example/pactos/payments@" + c2, Err: "pull failed"}
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to)
	if p.Predicted != PredictHeld || p.Floor.Decision != AcceptUnknown || verdict(t, p, GateDiff).Result != Unknown ||
		verdict(t, p, GateImpact).Result != Unknown || !strings.Contains(p.Question, "pull failed") {
		t.Fatalf("an unreadable contract must hold for accept-unknown: %+v", p)
	}
	from.Bundle, from.Err = nil, "gone"
	to = rev(dB, c2, bundle("payments", "1.1.0"))
	if p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA}, from, to); verdict(t, p, GateDiff).Result != Unknown {
		t.Fatalf("an unreadable running contract is Unknown: %+v", p)
	}
}

func TestPlanUnreconciledConsumerMakesTheGraphPartial(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	var raw PactoView
	raw.Metadata.Namespace, raw.Metadata.Name = "prod", "fresh"
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to, raw)
	if v := verdict(t, p, GateImpact); v.Result != Unknown || !strings.Contains(v.Message, "PACTO_NOT_RECONCILED") {
		t.Fatalf("an unreconciled Pacto must make impact Unknown: %+v", v)
	}
}

func TestPlanUnknownRangeAndUnverifiedInverseAreUnknown(t *testing.T) {
	old := bundle("payments", "1.0.0")
	old.Contract.Workload = "service"
	nu := bundle("payments", "1.1.0")
	nu.Contract.Workload = "job"
	from, to := rev(dA, c1, old), rev(dB, c2, nu)
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA}, from, to, checkoutPacto(""))
	v := verdict(t, p, GateImpact)
	if v.Result != Unknown || !strings.Contains(v.Message, "never verified") || !strings.Contains(v.Message, "checkout has no usable compatibility range") {
		t.Fatalf("want Unknown for the unverified inverse and the rangeless consumer: %+v", v)
	}
}

func TestPlanFirstVersion(t *testing.T) {
	to := rev(dB, c2, bundle("payments", "1.0.0"))
	p := planWith(t, promote("", dB), StageStatus{}, Revision{}, to, checkoutPacto("^1.0.0"))
	if p.Predicted != PredictProceeds || verdict(t, p, GateDiff).Citation["classification"] != "INITIAL" {
		t.Fatalf("first version: %+v", p)
	}
}

func TestPlanRefusals(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	cases := map[string]struct {
		spec   OperationSpec
		status StageStatus
		want   string
	}{
		"pointer moved": {promote(dC, dB), StageStatus{Pointer: dA}, "PointerMoved"},
		"failed digest": {promote(dA, dB), StageStatus{Pointer: dA, FailedDigests: []string{dB}}, "FailedDigest"},
		"agent override": {OperationSpec{Type: TypeOverride, Facet: FacetPointer, Params: Params{From: dA, To: dB}},
			StageStatus{Pointer: dA}, "NotPermitted"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := planWith(t, tc.spec, tc.status, from, to)
			if p.Predicted != PredictRejected || len(p.Conflicts) != 1 || !strings.HasPrefix(p.Conflicts[0], tc.want) {
				t.Fatalf("want %s, got %+v", tc.want, p)
			}
		})
	}
	wrong := rev(dB, c2, bundle("orders", "1.0.0"))
	if p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA}, from, wrong); p.Predicted != PredictRejected || verdict(t, p, GateDiff).Result != Fail {
		t.Fatalf("a contract for another service is a Fail: %+v", p)
	}
}

func TestPlanFrozenWaitsAndPolicyRaisesTheFloor(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}, Frozen: true, FrozenReason: "Errored"}, from, to)
	if p.Predicted != PredictWaits || !strings.Contains(strings.Join(p.Conflicts, ""), "StageFrozen: Errored") {
		t.Fatalf("frozen: %+v", p)
	}
	snap, _ := Snapshot(context.Background(), nil, nil, []Revision{from, to}, now)
	st := stageSpec()
	st.Approval.Always = true
	p = autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: st, Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		From: from, To: to, Snapshot: snap, Class: ClassAgent})
	if p.Predicted != PredictHeld || p.Floor.Decision != Approve || !strings.Contains(p.Question, "every promote") {
		t.Fatalf("approval.always: %+v", p)
	}
	rb := OperationSpec{Type: TypeRollback, Facet: FacetPointer, Params: Params{From: dA, To: dB}}
	if p := planWith(t, rb, StageStatus{Pointer: dA, Frozen: true}, from, to); p.Predicted != PredictProceeds {
		t.Fatalf("a rollback is exempt from the freeze: %+v", p)
	}
}

func TestShortAndHelpers(t *testing.T) {
	if short("") != "(none)" || short("abc") != "abc" || short(dA) != "sha256:aaaaaaaaaaaa" {
		t.Fatal("short")
	}
	if !strings.HasPrefix(ownersOrAnyone(nil, false), "any person") || !strings.HasPrefix(ownersOrAnyone(nil, true), "nobody") ||
		limitationCodes(nil) != "a source is not available" {
		t.Fatal("helpers")
	}
}

func TestSnapshotListErrorIsPartial(t *testing.T) {
	snap, err := Snapshot(context.Background(), nil, errors.New("no Pacto CRD"), nil, now)
	if err != nil || snap.Completeness != "partial" {
		t.Fatalf("an unreadable Pacto list must make the graph partial: %v %s", err, snap.Completeness)
	}
}

func TestPlanConsumerOutsideTheDomainIsUnknown(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	inline := checkoutPacto("^1.0.0")
	inline.Status.Contract.ResolvedRef = ""
	twin := inline
	twin.Metadata.Namespace = "test"
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to, inline, twin)
	v := verdict(t, p, GateImpact)
	if v.Result != Unknown || strings.Count(v.Message, "checkout depends on payments") != 1 {
		t.Fatalf("a consumer the graph cannot place must hold the change, once: %+v", v)
	}
}

// TestPublishedGate: a promote is admitted only for a version pacto publish
// published. Anything else is refused before any other gate runs.
func TestPublishedGate(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	plan := func(stage StageSpec, status StageStatus, to Revision, upstream *StageStatus) Plan {
		snap, _ := Snapshot(context.Background(), nil, nil, []Revision{from, to}, now)
		return autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stage, Status: status, From: from, To: to,
			Snapshot: snap, Class: ClassAgent, Upstream: upstream, UpstreamErr: "untrusted key"})
	}
	oci, up, none, remote := stageSpec(), stageSpec(), stageSpec(), stageSpec()
	remote.Candidates = Candidates{UpstreamRecords: &RemoteStage{Repository: "reg/streams", Stream: "c2/test/payments"}}
	up.Candidates = Candidates{UpstreamStage: &StageRef{Name: "payments", Namespace: "test"}}
	none.Candidates = Candidates{}
	published := StageStatus{Pointer: dA, Published: []string{dB}, Verified: []string{dA}}
	cases := []struct {
		name   string
		p      Plan
		result Result
		msg    string
	}{
		{"oci published", plan(oci, published, to, nil), Pass, "is signed and names its contract"},
		{"oci never verified", plan(oci, StageStatus{Pointer: dA}, to, nil), Fail, "Flux never verified a signature"},
		{"oci no contract", plan(oci, published, Revision{Digest: dB, NoContract: true, Err: "no annotation"}, nil), Fail, "names no digest-pinned"},
		{"oci unreadable", plan(oci, published, Revision{Digest: dB, Err: "registry down"}, nil), Unknown, "registry down"},
		{"upstream offered", plan(up, StageStatus{Pointer: dA}, to, &StageStatus{Verified: []string{dB}, Offered: []string{dB}}), Pass, "passed pacto.published and verified on the upstream Stage"},
		{"upstream failed", plan(up, StageStatus{Pointer: dA}, to, &StageStatus{FailedDigests: []string{dB}}), Fail, "failed after release on the upstream Stage test/payments"},
		{"upstream not verified", plan(up, StageStatus{Pointer: dA}, to, &StageStatus{Offered: []string{dB}}), Fail, "is not offered by the upstream Stage test/payments"},
		// Verified there, but on an accepted unknown or before the published
		// rule existed, or only signed there: never offered.
		{"upstream only verified", plan(up, StageStatus{Pointer: dA}, to, &StageStatus{Verified: []string{dB}}), Fail, "is not offered"},
		{"upstream only signed", plan(up, StageStatus{Pointer: dA}, to, &StageStatus{Verified: []string{dB}, Published: []string{dB}}), Fail, "is not offered"},
		// Never an unknown a person could accept.
		{"upstream unreadable", plan(up, StageStatus{Pointer: dA}, to, nil), Fail, "untrusted key, so it offers no candidate"},
		{"remote offered", plan(remote, StageStatus{Pointer: dA}, to, &StageStatus{Verified: []string{dB}, Offered: []string{dB}}), Pass, "verified on the upstream Stage"},
		{"remote unverifiable", plan(remote, StageStatus{Pointer: dA}, to, nil), Fail, "c2/test/payments (records in reg/streams) cannot be read or verified (untrusted key)"},
		{"no source", plan(none, published, to, nil), Fail, "names no candidate source"},
	}
	snap, _ := Snapshot(context.Background(), nil, nil, []Revision{from, to}, now)
	silent := autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: up, Status: StageStatus{Pointer: dA}, From: from, To: to, Snapshot: snap, Class: ClassAgent})
	cases = append(cases, struct {
		name   string
		p      Plan
		result Result
		msg    string
	}{"upstream unreadable, no reason", silent, Fail, "does not exist or cannot be read"})
	for _, c := range cases {
		v := verdict(t, c.p, GatePublished)
		if v.Result != c.result || !strings.Contains(v.Message, c.msg) {
			t.Errorf("%s: %s %q", c.name, v.Result, v.Message)
		}
		if c.result == Fail && (c.p.Predicted != PredictRejected || len(c.p.Verdicts) != 1) {
			t.Errorf("%s: a version pacto publish did not publish goes no further: %+v", c.name, c.p)
		}
		if c.result == Unknown && (c.p.Predicted != PredictHeld || c.p.Floor.Decision != AcceptUnknown || !strings.Contains(c.p.Question, "cannot tell")) {
			t.Errorf("%s: Unknown holds for a person: %+v", c.name, c.p)
		}
	}
	// A verdict on a remote upstream cites the newest record it read.
	view := &StageStatus{Verified: []string{dB}, Offered: []string{dB}, Stream: Stream{ID: "c2/test/payments", Seq: 7, Head: "sha256:r7"}}
	v := verdict(t, plan(remote, StageStatus{Pointer: dA}, to, view), GatePublished)
	if v.Citation["upstreamHead"] != "sha256:r7" || v.Citation["upstreamSeq"] != "7" {
		t.Errorf("remote citation: %+v", v.Citation)
	}
	if v := verdict(t, plan(up, StageStatus{Pointer: dA}, to, view), GatePublished); v.Citation["upstreamHead"] != "" {
		t.Errorf("a local upstream has no records to cite: %+v", v.Citation)
	}
}

// TestRecordsRequired: a Stage that requires records holds every promote or
// rollback automation raises for a person while records are not written, and
// lets people, freezes and a recorder that keeps up through.
func TestRecordsRequired(t *testing.T) {
	ready := func(status string) []Condition {
		return []Condition{{Type: ConditionRecordStoreReady, Status: status, Reason: "RegistryUnavailable", Message: "down"}}
	}
	rb := OperationSpec{Type: TypeRollback, Facet: FacetPointer, Params: Params{From: dA, To: dB}}
	freeze := OperationSpec{Type: TypeFreeze, Facet: FacetFreeze, Params: Params{From: dA}}
	st := stageSpec()
	st.Records.Required = true
	for _, c := range []struct {
		name   string
		spec   OperationSpec
		stage  StageSpec
		status StageStatus
		class  ActorClass
		held   string
	}{
		{"no recorder report", rb, st, StageStatus{Pointer: dA}, ClassController, "has not reported"},
		{"recorder down", rb, st, StageStatus{Pointer: dA, Conditions: ready("False")}, ClassAgent, "RegistryUnavailable: down"},
		{"backlog", rb, st, StageStatus{Pointer: dA, Conditions: ready("True"), Stream: Stream{Seq: 9, RecordedSeq: 3}}, ClassAgent, "6 operations wait"},
		{"keeping up", rb, st, StageStatus{Pointer: dA, Conditions: ready("True"), Stream: Stream{Seq: 8, RecordedSeq: 3}}, ClassAgent, ""},
		{"a person", rb, st, StageStatus{Pointer: dA}, ClassHuman, ""},
		{"a freeze", freeze, st, StageStatus{Pointer: dA}, ClassController, ""},
		{"not required", rb, stageSpec(), StageStatus{Pointer: dA}, ClassAgent, ""},
	} {
		p := autonomousPlan(PlanInput{Spec: c.spec, Stage: c.stage, Status: c.status, Class: c.class})
		switch {
		case c.held == "" && p.Predicted != PredictProceeds:
			t.Errorf("%s: %+v", c.name, p)
		case c.held != "" && (p.Predicted != PredictHeld || p.Floor.Decision != Approve || !strings.Contains(p.Question, c.held)):
			t.Errorf("%s: %+v", c.name, p)
		}
	}
	// A promote that already asks a person keeps its own question.
	st.Approval.Always = true
	p := autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: st, Status: StageStatus{Pointer: dA, Published: []string{dB}}, Class: ClassAgent})
	if p.Predicted == PredictHeld && strings.Contains(p.Question, "requires records") {
		t.Errorf("records replaced the promote's own question: %+v", p)
	}
}

func TestStillPasses(t *testing.T) {
	pass := Verdict{Gate: "g", Result: Pass}
	unknown := Verdict{Gate: "u", Result: Unknown}
	prev := Plan{Verdicts: []Verdict{pass, unknown}, Floor: Floor{Human: true, Decision: AcceptUnknown}}
	if !StillPasses(prev, Plan{Verdicts: []Verdict{pass}}) {
		t.Fatal("same passes, no new floor")
	}
	if StillPasses(prev, Plan{Verdicts: []Verdict{{Gate: "g", Result: Unknown}}}) {
		t.Fatal("a pass that came back Unknown")
	}
	if StillPasses(prev, Plan{Verdicts: []Verdict{pass}, Floor: Floor{Human: true, Decision: Approve}}) {
		t.Fatal("an approval-needing floor accept-unknown never answered")
	}
	// An approval routed to one team does not answer a question for another.
	approved := Plan{Verdicts: []Verdict{pass}, Floor: Floor{Human: true, Decision: Approve, Owners: []string{"team-payments"}}}
	if !StillPasses(approved, approved) {
		t.Fatal("the same question is answered")
	}
	if StillPasses(approved, Plan{Verdicts: []Verdict{pass}, Floor: Floor{Human: true, Decision: Approve, Owners: []string{"team-checkout"}, Consumers: []string{"checkout"}}}) {
		t.Fatal("a question now routed to the consumer's team needs that team")
	}
	if StillPasses(approved, Plan{Verdicts: []Verdict{pass}, Floor: Floor{Human: true, Decision: Approve, Owners: []string{"team-payments"}, Consumers: []string{"checkout"}}}) {
		t.Fatal("a consumer nobody was asked about needs a new answer")
	}
	// Accepting one unknown does not accept a different one.
	seen := Verdict{Gate: GateImpact, Result: Unknown, Message: "the graph is partial"}
	au := Plan{Verdicts: []Verdict{seen}, Floor: Floor{Human: true, Decision: AcceptUnknown}}
	if !StillPasses(au, au) {
		t.Fatal("the same unknown is accepted")
	}
	other := seen
	other.Message = "orders cannot be seen"
	if StillPasses(au, Plan{Verdicts: []Verdict{other}, Floor: au.Floor}) {
		t.Fatal("a different unknown needs a new answer")
	}
}

// autonomousPlan plans on an autonomous Stage unless the input names a rung:
// the gate and floor tests predate the ladder and judge what a fully
// automatic Stage does.
func autonomousPlan(in PlanInput) Plan {
	if in.Status.Rung == "" {
		in.Status.Rung = RungAutonomous
	}
	return PlanFor(context.Background(), in)
}

// TestPlanHoldsAVersionThatDoesNotShipTheSuite: a Stage check that runs a
// suite the version's contract does not ship holds the promote for a person,
// neither failing nor passing it; one it ships changes nothing.
func TestPlanHoldsAVersionThatDoesNotShipTheSuite(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "1.1.0"))
	to.Bundle.Contract.Checks = []contract.Check{{Name: "evals", File: "evals.yaml"}}
	snap, err := Snapshot(context.Background(), []PactoView{checkoutPacto("^1.0.0")}, nil, []Revision{from, to}, now)
	if err != nil {
		t.Fatal(err)
	}
	for suite, held := range map[string]bool{"evals": false, "refunds": true} {
		stage := stageSpec()
		stage.Gates.Verify.Checks = []GateCheck{{Name: "quality", Provider: "runner", Suite: suite}}
		p := autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stage, From: from, To: to, Snapshot: snap, Class: ClassAgent,
			Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}}})
		if !held {
			if p.Predicted != PredictProceeds || slices.ContainsFunc(p.Verdicts, func(v Verdict) bool { return v.Gate == GateSuites }) {
				t.Errorf("shipped suite: %+v", p)
			}
			continue
		}
		v := verdict(t, p, GateSuites)
		if p.Predicted != PredictHeld || v.Result != Unknown || p.Floor.Decision != AcceptUnknown ||
			!strings.Contains(p.Question, "the check quality runs the suite refunds, but the contract of") {
			t.Errorf("missing suite: %+v %+v", p, v)
		}
	}
}
