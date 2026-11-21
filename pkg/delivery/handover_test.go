package delivery

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// handoverSnap is the plan's snapshot with the legacy workload, which calls
// payments at runtime, handed over to invoices by ana's accepted
// declaration: invoices declares no dependency on payments.
func handoverSnap(t *testing.T, from, to Revision) *fleet.FleetSnapshot {
	t.Helper()
	ana := contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat,
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana, InferredFrom: "k8s:prod/Deployment/legacy"}}
	inv := bundle("invoices", "0.1.0").Contract
	inv.PactoVersion = "2.5"
	col := PactoCollection(SnapshotSourceID, []PactoView{paymentsPacto(complete(), legacy)}, nil, now)
	for _, side := range []Revision{from, to} {
		repo, digest, _ := strings.Cut(contract.TrimOCIScheme(side.ContractRef), "@")
		col.Revisions = append(col.Revisions, fleet.RawRevision{Bundle: side.Bundle, Domain: contract.OCIDomain(side.ContractRef),
			RequestedRef: side.ContractRef, ResolvedRef: repo + "@" + digest, Digest: digest})
	}
	col.Revisions = append(col.Revisions, fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: inv, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}}},
		Domain: "reg.example/pactos", ResolvedRef: "reg.example/pactos/invoices@" + c3, Digest: c3,
		Provenance: &fleet.BundleProvenance{Source: "oci", Signers: []fleet.VerifiedSigner{{Subject: ana.Subject, Issuer: ana.Issuer, Class: ana.Class}}},
	})
	workloads := fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{
		InferredServices: []fleet.RawInferredService{{Namespace: "prod", Kind: "Deployment", Name: "legacy"}}})
	policy := &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: ana.Issuer, Subjects: `[^@]+@acme\.com`}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }, DeclarationSigners: policy},
		fleet.NewMemorySource(SnapshotSourceID, fleet.SourceKindKubernetes, col), workloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Replacements) != 1 {
		t.Fatalf("the fixture must hand legacy over: %+v %+v", snap.Replacements, snap.Limitations)
	}
	return snap
}

// TestHandoverObservedConsumerAsksPerson: after the hand-over the declared
// invoices is an observed consumer of payments with no range. A breaking
// change to payments asks a person; a non-breaking one does not.
func TestHandoverObservedConsumerAsksPerson(t *testing.T) {
	for name, tc := range map[string]struct {
		sides func() (Revision, Revision)
		asks  bool
	}{
		"breaking":     {breakingSides, true},
		"non-breaking": {minorSides, false},
	} {
		t.Run(name, func(t *testing.T) {
			from, to := tc.sides()
			in := planIn(t, from, to)
			in.Snapshot = handoverSnap(t, from, to)
			res := impact.Analyze(context.Background(), from.Bundle.Contract, to.Bundle.Contract, nil, nil, in.Snapshot,
				impact.Options{Domain: contract.OCIDomain(to.ContractRef), IncludeObserved: true})
			c := consumerOf(res, "invoices")
			if c == nil || !c.Direct || c.Uncontracted || c.CompatibilityVerdict != impact.CompatibilityUnknown || c.Confidence != impact.ConfidenceObserved {
				t.Fatalf("invoices is no observed consumer: %+v", res.Consumers)
			}
			p := PlanFor(context.Background(), in)
			v := verdict(t, p, GateImpact)
			asked := v.Result == Unknown && p.Floor.Human && strings.Contains(v.Message, "invoices")
			if asked != tc.asks {
				t.Errorf("asked %v: %+v", asked, p)
			}
		})
	}
}

func consumerOf(res *impact.Result, name string) *impact.AffectedConsumer {
	for i, c := range res.Consumers {
		if strings.HasSuffix(c.Service, name) {
			return &res.Consumers[i]
		}
	}
	return nil
}
