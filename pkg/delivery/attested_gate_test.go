package delivery

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

const (
	gateCI     = "https://github.com/acme/payments/.github/workflows/ci.yml@refs/heads/main"
	gateIssuer = "https://token.actions.githubusercontent.com"
	gateCommit = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"
)

// withAttestableOwner makes b a 2.5 bundle with no declared owner whose
// record read team payments from CODEOWNERS at gateCommit.
func withAttestableOwner(b *contract.Bundle) *contract.Bundle {
	b.Contract.PactoVersion, b.Contract.Service.Owner = "2.5", contract.Owner{}
	rec := (&contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "4", Commit: gateCommit,
			Sources: []contract.InferredSourceFile{{Path: "repository:.github/CODEOWNERS", Digest: "sha256:" + strings.Repeat("0", 64)}}},
		Fields: []contract.Inferred{{Field: "service.owner", Value: "payments", Source: "repository:.github/CODEOWNERS:7", Confidence: contract.ConfidenceLikely,
			Class: contract.InferredClassOwnerFile, Declare: "service:\n  owner:\n    team: payments\n"}},
	}).Marshal()
	b.FS = fstest.MapFS{contract.InferredRecordFile: {Data: rec}}
	return b
}

// gateSnapshot is Snapshot over the sides, each signed by an accepted CI
// identity at gateCommit when attested.
func gateSnapshot(t *testing.T, attested bool, pactos []PactoView, sides ...Revision) *fleet.FleetSnapshot {
	t.Helper()
	col := PactoCollection(SnapshotSourceID, pactos, nil, now)
	var revs []fleet.RawRevision
	for _, side := range sides {
		repo, digest, _ := strings.Cut(contract.TrimOCIScheme(side.ContractRef), "@")
		raw := fleet.RawRevision{Bundle: side.Bundle, Domain: contract.OCIDomain(side.ContractRef), RequestedRef: side.ContractRef, ResolvedRef: repo + "@" + digest, Digest: digest}
		if attested {
			raw.Provenance = &fleet.BundleProvenance{
				Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule", Signer: gateCI, Issuer: gateIssuer,
					SignerRepository: "https://github.com/acme/payments", SignerCommit: gateCommit},
				Signers: []fleet.VerifiedSigner{{Subject: gateCI, Issuer: gateIssuer, Class: "ci"}},
			}
		}
		revs = append(revs, raw)
	}
	col.Revisions = append(revs, col.Revisions...)
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }}, fleet.NewMemorySource(SnapshotSourceID, fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// An attested owner routes reviews and never decides a delivery question: the
// plan, its owners and who may answer included, is the same with and without
// attestation (apart from the digests of the inputs), while impact names the attested team as the changed owner.
func TestAttestedOwnerNeverChangesAGate(t *testing.T) {
	from, to := rev(dA, c1, withAttestableOwner(bundle("payments", "1.0.0"))), rev(dB, c2, withAttestableOwner(bundle("payments", "2.0.0")))
	pactos := []PactoView{checkoutPacto("^1.0.0")}
	plans := map[bool]Plan{}
	owners := map[bool]string{}
	for _, attested := range []bool{false, true} {
		snap := gateSnapshot(t, attested, pactos, from, to)
		s := snap.Services[fleet.NewServiceKeyDomain(contract.OCIDomain(to.ContractRef), "payments")]
		if got := s != nil && s.OwnerProvenance == fleet.OwnerProvenanceAttested; got != attested {
			t.Fatalf("attested=%v: payments owner provenance = %+v", attested, s)
		}
		status := StageStatus{Pointer: dA, Verified: []string{dA}, Published: []string{dB}}
		plans[attested] = autonomousPlan(PlanInput{Spec: promote(dA, dB), Stage: stageSpec(), Status: status, From: from, To: to, Snapshot: snap, Class: ClassAgent})
		res := impact.Analyze(context.Background(), from.Bundle.Contract, to.Bundle.Contract, from.Bundle.FS, to.Bundle.FS, snap, impact.Options{})
		owners[attested] = res.ChangedOwnerKey
	}
	for k, p := range plans {
		// The snapshot and impact digests identify inputs that differ by the
		// signature itself; every decision beside them must not.
		p.ImpactDigest = ""
		for _, v := range p.Verdicts {
			delete(v.Citation, "impact")
			delete(v.Citation, "snapshot")
		}
		plans[k] = p
	}
	if !reflect.DeepEqual(plans[false], plans[true]) {
		t.Errorf("attestation changed the plan:\n%+v\n%+v", plans[false], plans[true])
	}
	if owners[false] != "" || owners[true] != "team:payments" {
		t.Errorf("impact changed owner = %q without, %q with attestation", owners[false], owners[true])
	}
}
