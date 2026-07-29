package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v3/pkg/contract"
)

// TestSnapshotJSONDeterministic asserts a snapshot marshals byte-identically
// across repeated marshals (map string keys sort, slices are pre-sorted) and
// that map-keyed fields serialize.
func TestSnapshotJSONDeterministic(t *testing.T) {
	web := &contract.Contract{
		PactoVersion: "2.0",
		Service:      contract.Service{Name: "web", Version: "1.0.0", Owner: contract.Owner{Team: "web"}},
		Dependencies: []contract.Dependency{{Name: "leaf-svc", Ref: "oci://x/leaf", Required: true, Compatibility: "^1.0.0"}},
	}
	col := &Collection{
		Revisions: []RawRevision{
			{Bundle: &contract.Bundle{Contract: web, FS: fstest.MapFS{}}, Digest: "sha256:web"},
			{Bundle: validLeafBundle(t), Digest: "sha256:leaf"},
		},
		Targets: []RawTarget{
			{Scope: "prod", Kind: "k8s", Name: "web-app", Service: "web", Digest: "sha256:web", Compliance: StatusCompliant, EvidenceAt: ptrTime(fixedNow())},
		},
	}
	snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, NewMemorySource("local", "local", col))
	if err != nil {
		t.Fatal(err)
	}

	first, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		next, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("snapshot JSON not deterministic on marshal %d", i)
		}
	}

	// Map-keyed fields (services/revisions/targets) serialize.
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"schemaVersion", "snapshotId", "services", "revisions", "targets", "relationships", "sources", "completeness"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("serialized snapshot missing %q", field)
		}
	}
}
