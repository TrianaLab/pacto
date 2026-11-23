package delivery

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const contributionRepo = "reg.example/acme/pacto-inferred/acme-idp/services"

// contributedLedgerSync is acme-idp's record of ledger-sync at version.
func contributedLedgerSync(version string) *contract.Bundle {
	return &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "ledger-sync", Version: version}},
		RawYAML: []byte("pactoVersion: \"2.5\"\nservice:\n  name: ledger-sync\n  version: " + version + "\n"), FS: fstest.MapFS{}}
}

// pinContribution is a dependency on ledger-sync's contribution with rng.
func pinContribution(rng string) contract.Dependency {
	return contract.Dependency{Name: "ledger-sync", Ref: "oci://" + contributionRepo + ":ledger-sync_0.1.0", Compatibility: rng, Required: true}
}

// TestContributedServiceNeverPassesEnvironmentGate: a range on a producer's
// contributed record is never met: the fleet reads it unknown, the move-first
// check an unknown error and the environment gate Unknown.
func TestContributedServiceNeverPassesEnvironmentGate(t *testing.T) {
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Revisions: []fleet.RawRevision{
			{Bundle: bundle("checkout", "1.0.0", pinContribution("^0.1")), Digest: "sha256:checkout"}}}),
		fleet.NewMemorySource("evidence", fleet.SourceKindEvidenceHTTP, &fleet.Collection{Revisions: []fleet.RawRevision{
			{Bundle: contributedLedgerSync("0.1.0"), ResolvedRef: contributionRepo + "@sha256:ls", Digest: "sha256:ls",
				Contributor: &fleet.Contributor{Producer: "acme-idp", AsOf: now}}}}))
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, rel := range snap.Relationships {
		if rel.FromService != "checkout" {
			continue
		}
		seen++
		if rel.RangeStatus != fleet.RangeUnknown || rel.RangeReason != fleet.RangeReasonInferredProvider || rel.ToService != "" {
			t.Errorf("edge = %+v", rel)
		}
	}
	if seen != 1 {
		t.Errorf("%d edges from checkout", seen)
	}
	why, err := acceptsWhatRunsHere(snap, bundle("checkout", "1.1.0", pinContribution("^0.1")).Contract, "payments")
	if why != "" || err == nil || !strings.Contains(err.Error(), "Pacto never checks a range against it") {
		t.Errorf("acceptsWhatRunsHere = %q %v", why, err)
	}
	b := envBundle(map[string]string{"prod": "prod.yaml"}, map[string]string{"prod.yaml": "app:\n  replicas: 3\n"}, false)
	b.Contract.Configurations[0].Ref = "oci://" + contributionRepo + ":ledger-sync_0.1.0"
	to := ResolveRevision(t.Context(), mapRegistry{"r/consumer@" + c1: b, contributionRepo + ":ledger-sync_0.1.0": contributedLedgerSync("0.1.0")}, "r/a", dB)
	to.ContractRef = rev(dB, c2, b).ContractRef
	p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, rev(dA, c1, bundle("payments", "1.0.0")), to)
	if v := verdict(t, p, GateEnvironment); v.Result != Unknown || p.Floor.Decision != AcceptUnknown {
		t.Errorf("environment = %+v, floor %+v", v, p.Floor)
	}
}
