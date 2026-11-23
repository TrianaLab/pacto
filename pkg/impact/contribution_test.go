package impact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const ctbRoot = "reg.example/acme"

// contributedLedgerSync is acme-idp's contribution of ledger-sync, whose
// record names ledger, made asOf.
func contributedLedgerSync(asOf time.Time) fleet.RawRevision {
	export := []byte(`{"ledger-sync":["ledger"]}`)
	sum := sha256.Sum256(export)
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{{Path: "export.json", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[ledger]", Value: "ledger", Source: "export.json:1", Confidence: contract.ConfidenceLikely}}}
	raw := "pactoVersion: \"2.5\"\nservice:\n  name: ledger-sync\n  version: 0.1.0\n"
	return fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "ledger-sync", Version: "0.1.0"}},
			RawYAML: []byte(raw), FS: fstest.MapFS{"export.json": {Data: export}, contract.InferredRecordFile: {Data: rec.Marshal()}}},
		ResolvedRef: ctbRoot + "/pacto-inferred/acme-idp/services@sha256:ls", Digest: "sha256:ls",
		Contributor: &fleet.Contributor{Producer: "acme-idp", ReadAt: evidenceNow, AsOf: asOf},
	}
}

// ledgerFleet holds ledger (running) and the given declared consumers at
// ctbRoot, plus the contributions.
func ledgerFleet(t *testing.T, consumers []*contract.Contract, contributed ...fleet.RawRevision) *fleet.FleetSnapshot {
	t.Helper()
	declared := &fleet.Collection{}
	for _, c := range append([]*contract.Contract{svcContract("ledger", "1.0.0", contract.Owner{Team: "ledger"})}, consumers...) {
		declared.Revisions = append(declared.Revisions, fleet.RawRevision{Bundle: mkBundle(c), Domain: ctbRoot, Digest: "sha256:" + c.Service.Name})
		tgt := target(c.Service.Name, fresh())
		tgt.Domain = ctbRoot
		declared.Targets = append(declared.Targets, tgt)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("test", "memory", declared),
		fleet.NewMemorySource("evidence", fleet.SourceKindEvidenceHTTP, &fleet.Collection{Revisions: contributed}))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// breakCtbLedger analyzes a BREAKING change of ledger 1.0.0 to 2.0.0.
func breakCtbLedger(snap *fleet.FleetSnapshot) *Result {
	old := svcContract("ledger", "1.0.0", contract.Owner{Team: "ledger"})
	old.Interfaces = []contract.Interface{{Name: "api", Type: contract.InterfaceTypeGRPC, Ref: "api.proto"}}
	return Analyze(context.Background(), old, svcContract("ledger", "2.0.0", contract.Owner{Team: "ledger"}),
		fstest.MapFS{}, fstest.MapFS{}, snap, Options{Domain: ctbRoot})
}

func TestContributedServiceNeverClearsGate(t *testing.T) {
	res := breakCtbLedger(ledgerFleet(t, nil, contributedLedgerSync(evidenceNow)))
	if res.Classification != "BREAKING" || res.Gate == GateClear {
		t.Fatalf("classification %s, gate %s (%s)", res.Classification, res.Gate, res.GateReason)
	}
	c := consumerNamed(t, res, "ledger-sync")
	wantInferredOnly := c.Provenance == fleet.ProvenanceInferred && c.CompatibilityVerdict == CompatibilityUnknown
	if !wantInferredOnly || len(c.Evidence) == 0 || strings.Contains(c.Evidence[len(c.Evidence)-1].Message, "stale") {
		t.Errorf("ledger-sync = %+v", c)
	}
	// Beside a declared consumer whose range accepts 2.0.0 the verdict is
	// the one it has alone, never clearer.
	billing := svcContract("billing", "1.0.0", contract.Owner{Team: "billing"}, contract.Dependency{Name: "ledger", Compatibility: "^2.0.0", Required: true})
	alone := breakCtbLedger(ledgerFleet(t, []*contract.Contract{billing}))
	if both := breakCtbLedger(ledgerFleet(t, []*contract.Contract{billing}, contributedLedgerSync(evidenceNow))); both.Gate != alone.Gate || both.Gate == GateClear {
		t.Errorf("with the contribution: gate %s, alone %s", both.Gate, alone.Gate)
	}
	// A record older than the window reads stale on the consumer.
	stale := consumerNamed(t, breakCtbLedger(ledgerFleet(t, nil, contributedLedgerSync(evidenceNow.Add(-40*24*time.Hour)))), "ledger-sync")
	if !strings.HasSuffix(stale.Evidence[len(stale.Evidence)-1].Message, ", contributed by acme-idp and stale") {
		t.Errorf("stale evidence = %+v", stale.Evidence)
	}
}
