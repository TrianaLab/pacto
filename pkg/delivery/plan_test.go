package delivery

import (
	"context"
	"errors"
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
	return StageSpec{Service: ServiceRef{Name: "payments"}, Environment: "prod", ApplyRef: LocalRef{Name: "payments"}}
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
	return PlanFor(context.Background(), PlanInput{Spec: spec, Stage: stageSpec(), Status: status, From: from, To: to,
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
}

func TestPlanNoConsumersShipsEvenWhenBreaking(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "2.0.0"))
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to)
	if p.Predicted != PredictProceeds {
		t.Fatalf("a change nothing depends on must ship: %+v", p)
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

func TestPlanUnreadableContractWaitsForAPerson(t *testing.T) {
	from := rev(dA, c1, bundle("payments", "1.0.0"))
	to := Revision{Digest: dB, Err: "no annotation"}
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to)
	if p.Predicted != PredictHeld || p.Floor.Decision != AcceptUnknown || verdict(t, p, GateDiff).Result != Unknown ||
		verdict(t, p, GateImpact).Result != Unknown || !strings.Contains(p.Question, "no annotation") {
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
	p = PlanFor(context.Background(), PlanInput{Spec: promote(dA, dB), Stage: st, Status: StageStatus{Pointer: dA, Verified: []string{dA}},
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
	if ownersOrAnyone(nil) == "" || limitationCodes(nil) != "a source is not available" {
		t.Fatal("helpers")
	}
}

func TestSnapshotListErrorIsPartial(t *testing.T) {
	snap, err := Snapshot(context.Background(), nil, errors.New("no Pacto CRD"), nil, now)
	if err != nil || snap.Completeness != "partial" {
		t.Fatalf("an unreadable Pacto list must make the graph partial: %v %s", err, snap.Completeness)
	}
}
