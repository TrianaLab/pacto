package impact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestImpactIgnoresRepositoryExternalEdges: the externals a consumer's
// inferred record names change no impact answer and are never named in it.
func TestImpactIgnoresRepositoryExternalEdges(t *testing.T) {
	at := time.Now()
	analyzeWith := func(record bool) *Result {
		auth := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
		gateway := svcContract("api-gateway", "1.0.0", contract.Owner{Team: "gateway-team"},
			contract.Dependency{Name: "auth-service", Ref: "ghcr.io/x/auth-service:1.0.0", Required: true, Compatibility: "^1.0.0"})
		gateway.PactoVersion = "2.5"
		fsys := fstest.MapFS{}
		if record {
			const req = "stripe==11.4.0\n"
			sum := sha256.Sum256([]byte(req))
			rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
				Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1", Sources: []contract.InferredSourceFile{{Path: "requirements.txt", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
				Fields: []contract.Inferred{{Field: "dependencies[stripe]", Value: "stripe", Source: "requirements.txt:1", Confidence: contract.ConfidencePossible,
					External: &contract.InferredExternal{Identity: "stripe", Catalog: "default", Sources: []contract.InferredExternalSource{{Kind: "sdk", Path: "requirements.txt", Line: 1}}}}}}
			fsys["requirements.txt"] = &fstest.MapFile{Data: []byte(req)}
			fsys[contract.InferredRecordFile] = &fstest.MapFile{Data: rec.Marshal()}
		}
		col := &fleet.Collection{
			Revisions: []fleet.RawRevision{{Bundle: mkBundle(auth)}, {Bundle: &contract.Bundle{Contract: gateway, FS: fsys}}},
			Targets: []fleet.RawTarget{
				{Scope: "prod", Kind: "deployment", Name: "auth", Service: "auth-service", Compliance: fleet.StatusCompliant, EvidenceAt: &at},
				{Scope: "prod", Kind: "deployment", Name: "gw", Service: "api-gateway", Compliance: fleet.StatusCompliant, EvidenceAt: &at},
			},
		}
		snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return at }}, fleet.NewMemorySource("test", "memory", col))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := snap.Externals["stripe"]; ok != record {
			t.Fatalf("record %t: externals = %+v", record, snap.Externals)
		}
		old := svcContract("auth-service", "1.0.0", contract.Owner{Team: "platform"})
		old.Interfaces = []contract.Interface{{Name: "auth-api", Type: contract.InterfaceTypeGRPC, Ref: "auth.proto"}}
		res := Analyze(context.Background(), old, svcContract("auth-service", "2.0.0", contract.Owner{Team: "platform"}), fstest.MapFS{}, fstest.MapFS{}, snap,
			Options{IncludeObserved: true})
		res.SnapshotID = ""
		return res
	}
	a, _ := json.Marshal(analyzeWith(false))
	b, _ := json.Marshal(analyzeWith(true))
	if string(a) != string(b) {
		t.Errorf("impact changed:\nwithout %s\nwith    %s", a, b)
	}
	if strings.Contains(string(b), "stripe") {
		t.Errorf("impact names the external: %s", b)
	}
}
