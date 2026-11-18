package impact

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

const catalogInfo = "apiVersion: backstage.io/v1alpha1\nkind: Component\nmetadata:\n  name: newsletter\nspec:\n  owner: group:default/team-x\n  dependsOn:\n    - component:notification-worker\n"

// catalogRev is a 2.5 newsletter revision whose record holds what
// discovery reads from its catalog-info.yaml: a dependsOn dependency and an
// owner from an owner file.
func catalogRev() fleet.RawRevision {
	sum := sha256.Sum256([]byte(catalogInfo))
	rec := (&contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "2", Sources: []contract.InferredSourceFile{
			{Path: "catalog-info.yaml", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{
			{Field: "dependencies[notification-worker]", Value: "notification-worker", Source: "catalog-info.yaml:8", Confidence: contract.ConfidencePossible},
			{Field: "service.owner", Value: "team team-x", Source: "catalog-info.yaml:6", Confidence: contract.ConfidenceLikely, Class: contract.InferredClassOwnerFile},
		},
	}).Marshal()
	c := svcContract("newsletter", "1.0.0", contract.Owner{Team: "news"})
	c.PactoVersion = "2.5"
	fsys := fstest.MapFS{"catalog-info.yaml": {Data: []byte(catalogInfo)}, contract.InferredRecordFile: {Data: rec}}
	return fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fsys}, Digest: "sha256:news"}
}

func TestDiscoveredDependencyNeverClearsGate(t *testing.T) {
	res := breakWorker(workerFleet(t, catalogRev()))
	if res.Classification != "BREAKING" || res.Gate == GateClear {
		t.Fatalf("a catalog-info.yaml dependsOn cleared the gate: %s %s (%s)", res.Classification, res.Gate, res.GateReason)
	}
	if c := consumerNamed(t, res, "newsletter"); c.CompatibilityVerdict != CompatibilityUnknown || c.Provenance != fleet.ProvenanceInferred {
		t.Errorf("newsletter = %+v", c)
	}
}

// The owner an owner file names is inferred: the affected consumer's owners
// are its declared team alone.
func TestDiscoveredOwnerIsNotTheOwner(t *testing.T) {
	res := breakWorker(workerFleet(t, catalogRev()))
	if strings.Contains(strings.Join(res.Owners, ","), "team-x") || len(res.Owners) == 0 {
		t.Errorf("owners = %v, want the declared team only", res.Owners)
	}
}
