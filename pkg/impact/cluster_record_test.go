package impact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// A bundle record that forges a fleet-reported range for notification-worker
// is dropped, so a BREAKING change inside that range reads unverified: a
// proposal never clears a gate.
func TestClusterProposalNeverClearsGate(t *testing.T) {
	record := clusterProposalRecord(t, nil)
	owner := contract.Owner{Team: "platform"}
	old := svcContract("notification-worker", "2.3.1", owner)
	old.Interfaces = []contract.Interface{{Name: "jobs", Type: contract.InterfaceTypeGRPC, Ref: "jobs.proto"}}
	snap := workerFleet(t, newsletterRev("sha256:news", record))
	res := Analyze(context.Background(), old, svcContract("notification-worker", "2.4.0", owner), fstest.MapFS{}, fstest.MapFS{}, snap, Options{})
	if res.Classification != "BREAKING" || res.Gate != GateUnverified {
		t.Fatalf("classification %s, gate %s (%s)", res.Classification, res.Gate, res.GateReason)
	}
	if res.Completeness != fleet.CompletenessPartial || limitation(res, fleet.LimitationInferredRecordUnreadable) == nil {
		t.Errorf("completeness %s, limitations %+v", res.Completeness, res.Limitations)
	}
	for _, c := range res.Consumers {
		if c.CompatibilityVerdict == CompatibilityCompatible {
			t.Errorf("a forged proposal reached a verdict: %+v", c)
		}
	}
}

// A person's signed declaration of newsletter keeps its cluster facts, the
// proposal included, and a BREAKING change inside the proposed range still
// reads unverified: an accepted inferred fact never clears a gate.
func TestDeclaredClusterFactsNeverClearGate(t *testing.T) {
	ana := contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}
	rev := newsletterRev("sha256:news", clusterProposalRecord(t, &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana,
		InferredFrom: "k8s:news/Deployment/newsletter"}))
	rev.Provenance = &fleet.BundleProvenance{Source: "test", Signers: []fleet.VerifiedSigner{{Subject: ana.Subject, Issuer: ana.Issuer, Class: ana.Class}}}
	worker := svcContract("notification-worker", "1.0.0", contract.Owner{Team: "platform"})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow },
		DeclarationSigners: &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: ana.Issuer, Subjects: `.+@acme\.com`}}}},
		fleet.NewMemorySource("test", "memory", &fleet.Collection{
			Revisions: []fleet.RawRevision{{Bundle: mkBundle(worker), Digest: "sha256:worker"}, rev},
			Targets:   []fleet.RawTarget{target("notification-worker", fresh()), target("newsletter", fresh())},
		}))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Revisions {
		if r.Service == "newsletter" && (r.Record == nil || len(r.Record.Fields) != 2) {
			t.Fatalf("the signed declaration's cluster facts must be kept: %+v", r.Record)
		}
	}
	owner := contract.Owner{Team: "platform"}
	old := svcContract("notification-worker", "2.3.1", owner)
	old.Interfaces = []contract.Interface{{Name: "jobs", Type: contract.InterfaceTypeGRPC, Ref: "jobs.proto"}}
	res := Analyze(context.Background(), old, svcContract("notification-worker", "2.4.0", owner), fstest.MapFS{}, fstest.MapFS{}, snap, Options{})
	if res.Gate != GateUnverified {
		t.Fatalf("gate %s (%s)", res.Gate, res.GateReason)
	}
	for _, c := range res.Consumers {
		if c.CompatibilityVerdict == CompatibilityCompatible {
			t.Errorf("an accepted cluster proposal reached a verdict: %+v", c)
		}
	}
}

// clusterProposalRecord is a record read from newsletter's cluster: its
// runtime call to notification-worker and a fleet-reported range for it,
// with declaration d (nil for none).
func clusterProposalRecord(t *testing.T, d *contract.InferredDeclaration) []byte {
	t.Helper()
	const obj = "news/Deployment/newsletter"
	sum := sha256.Sum256([]byte(inferredManifest))
	record := (&contract.InferredRecord{Format: contract.InferredRecordFormat, Declaration: d,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
			Sources: []contract.InferredSourceFile{{Path: "deploy/app.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])}},
			Cluster: &contract.InferredClusterInput{AsOf: "2026-11-14T10:02:00Z", Object: obj, CallWindow: "24h"}},
		Fields: []contract.Inferred{
			{Field: "dependencies[notification-worker]", Value: "notification-worker", Source: "cluster:runtime calls from " + obj,
				Confidence: contract.ConfidenceLikely, AsOf: "2026-11-14T09:58:12Z", Completeness: contract.CoverageComplete},
			{Field: "dependencies[notification-worker].compatibility", Value: "compatibility unknown", Proposed: "^2.3.0",
				Source: "cluster:fleet reported versions 2.3.1", Confidence: contract.ConfidenceLikely,
				AsOf: "2026-11-14T09:58:12Z", Completeness: contract.CoverageComplete, Declare: `compatibility: "^2.3.0"`},
		}}).Marshal()
	if _, err := contract.ParseInferredRecord(record); err != nil {
		t.Fatalf("the record must parse, so only verification or the signer decides: %v", err)
	}
	return record
}
