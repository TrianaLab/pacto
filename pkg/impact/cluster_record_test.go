package impact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// A bundle record that forges a fleet-reported range for notification-worker
// is dropped, so a BREAKING change inside that range reads unverified: a
// proposal never clears a gate.
func TestClusterProposalNeverClearsGate(t *testing.T) {
	const obj = "news/Deployment/newsletter"
	sum := sha256.Sum256([]byte(inferredManifest))
	record := (&contract.InferredRecord{Format: contract.InferredRecordFormat,
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
		t.Fatalf("the forged record must parse, so verification is what refuses it: %v", err)
	}
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
