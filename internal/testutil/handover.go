package testutil

import (
	"context"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// handoverNow is the as-of time of HandoverQuery.
var handoverNow = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

// HandoverQuery is one cluster where ana@acme.com declared invoices 0.1.0
// from the inferred invoices Deployment of billing, accepting its ledger
// range under the right to set its owner, so the fleet hands the workload over to invoices.
func HandoverQuery(t *testing.T) *fleet.Query {
	t.Helper()
	ana := contract.InferredSigner{Subject: "ana@acme.com", Issuer: "https://accounts.google.com", Class: contract.SignerClassHuman}
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1"},
		Declaration: &contract.InferredDeclaration{State: contract.DeclarationDeclared, Asker: ana, InferredFrom: "k8s:billing/Deployment/invoices",
			Accepted: []contract.InferredAcceptance{{Field: "dependencies[ledger].compatibility", Range: "^2.3.0"}}}}
	c := &contract.Contract{PactoVersion: "2.5", Service: contract.Service{Name: "invoices", Version: "0.1.0", Owner: contract.Owner{Team: "billing"}},
		Dependencies: []contract.Dependency{{Name: "ledger", Ref: "oci://ghcr.io/acme/ledger", Compatibility: "^2.3.0"}}}
	rev := fleet.RawRevision{Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{contract.InferredRecordFile: {Data: rec.Marshal()}}},
		Digest: "sha256:invoices-0.1.0", FetchedAt: new(handoverNow),
		Provenance: &fleet.BundleProvenance{Signature: fleet.BundleSignature{Status: fleet.SignatureUnverified}, Source: "oci",
			Signers:    []fleet.VerifiedSigner{{Subject: ana.Subject, Issuer: ana.Issuer, Class: contract.SignerClassHuman}},
			OwnerRight: &fleet.OwnerRightCheck{Allowed: true}}}
	cl := &fleet.Cluster{ID: "9f1c4e2a-0000-4000-8000-00000000000a", Name: "eu", NameSource: fleet.ClusterNameFromContext}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return handoverNow },
		DeclarationSigners: &fleet.DeclarationSignerPolicy{Signers: []fleet.PolicySigner{{Issuer: ana.Issuer, Subjects: `[^@]+@acme\.com`}}}},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{rev}}),
		fleet.NewMemorySource("k8s-workloads", fleet.SourceKindKubernetesWorkloads, &fleet.Collection{Cluster: cl,
			InferredServices: []fleet.RawInferredService{{Namespace: "billing", Kind: "Deployment", Name: "invoices",
				Image: &fleet.InferredValue{Value: "ghcr.io/acme/invoices:0.9.0", From: "spec.template.spec.containers[name=invoices].image"}}}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Replacements) != 1 {
		t.Fatalf("the fixture must hand over: %+v %+v", snap.Replacements, snap.Limitations)
	}
	return fleet.NewQuery(snap)
}
