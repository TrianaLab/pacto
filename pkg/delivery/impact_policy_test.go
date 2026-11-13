package delivery

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

// TestImpactGateHoldsIncompatiblePolicyReferrer: payments publishes a policy
// whose new schema checkout's contract fails, so promoting it breaks checkout
// even though checkout declares no dependency on it.
func TestImpactGateHoldsIncompatiblePolicyReferrer(t *testing.T) {
	const policyRef, domain = "oci://reg.example/pactos/payments", "reg.example/pactos"
	schema := func(s string) fstest.MapFS { return fstest.MapFS{"policy/schema.json": {Data: []byte(s)}} }
	from, to := bundle("payments", "1.0.0"), bundle("payments", "2.0.0")
	from.FS, to.FS = schema(`{"type":"object"}`), schema(`{"type":"object","required":["metadata"]}`)

	referrer := bundle("checkout", "1.0.0")
	referrer.Contract.Policies = []contract.Policy{{Name: "http", Ref: policyRef}}
	referrer.FS = fstest.MapFS{"pacto.yaml": {Data: []byte("pactoVersion: \"2.0\"\nservice:\n  name: checkout\n  version: 1.0.0\n")}}
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{
			{Bundle: from, Digest: c1, Domain: domain, RequestedRef: policyRef + "@" + c1},
			{Bundle: referrer, Digest: c3, Domain: domain, RequestedRef: "oci://reg.example/pactos/checkout@" + c3,
				Lock: &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
					Kind: contract.ReferenceKindPolicy, Name: "http", Source: "oci", Ref: policyRef, Digest: c1,
				}}}},
		},
		Targets: []fleet.RawTarget{{Scope: "prod", Kind: "deployment", Name: "checkout", Service: "checkout", Domain: domain, Compliance: fleet.StatusCompliant}},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	p := autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(),
		Status: StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}},
		From:   rev(dA, c1, from), To: rev(dB, c2, to), Snapshot: snap, Class: ClassAgent})
	v := verdict(t, p, GateImpact)
	if v.Message != "breaks checkout" || !p.Floor.Human || len(p.Floor.Consumers) != 1 || p.Floor.Consumers[0] != "checkout" ||
		!strings.Contains(p.Question, "checkout") {
		t.Fatalf("impact verdict %+v, floor %+v, question %q", v, p.Floor, p.Question)
	}
}

// TestImpactGateOwnersStayDisplay: impact answers name owners by canonical key,
// but the question a breaking change asks still goes to the team's display
// name, which is what admission compares with team membership.
func TestImpactGateOwnersStayDisplay(t *testing.T) {
	from, to := rev(dA, c1, bundle("payments", "1.0.0")), rev(dB, c2, bundle("payments", "2.0.0"))
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, from, to, checkoutPacto("^1.0.0"))
	if strings.Join(p.Floor.Owners, ",") != "team-checkout" {
		t.Fatalf("floor owners = %v, want the display name team-checkout", p.Floor.Owners)
	}
}
